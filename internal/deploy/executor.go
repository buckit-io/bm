package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/tasks"
)

// Executor implements tasks.Executor for OpClusterDeploy.
type Executor struct {
	Installer    *Installer
	Clusters     *clusters.Repo
	Nodes        *nodes.Repo
	ClusterAdmin *clusteradmin.Repo
	SSHConfig    *sshconfig.Repo
	Verify       func(context.Context, DeployParams) (VerifyResult, error)
	// AfterCommit is called once the cluster commit succeeds. Production wires
	// this to alias.Sync so bm-cli verbs see the new cluster.
	AfterCommit func(ctx context.Context, clusterID string)
}

// Register wires the executor into the tasks registry. Idempotent — repeat
// calls overwrite, matching the RegisterNoop / RegisterSshProbe pattern.
func Register(e *Executor) {
	tasks.OverwriteRegister(tasks.OpClusterDeploy, e)
}

// Validate decodes the params and runs invariant checks. Called synchronously
// during Dispatch so the operator gets a 400 for bad input.
func (e *Executor) Validate(req tasks.DispatchRequest) error {
	if e.Clusters == nil || e.Nodes == nil || e.ClusterAdmin == nil || e.SSHConfig == nil {
		return errors.New("deploy executor: repos not wired")
	}
	if len(req.Params) == 0 {
		return errors.New("deploy: params (NewClusterDraft) required")
	}
	var draft domain.NewClusterDraft
	if err := json.Unmarshal(req.Params, &draft); err != nil {
		return fmt.Errorf("deploy: decode params: %w", err)
	}
	params := FromDraft(draft)
	return params.Validate()
}

// Execute runs the deploy. Returns nil on success (cluster committed), error
// otherwise (no commit).
func (e *Executor) Execute(ctx context.Context, run *tasks.Run) error {
	var draft domain.NewClusterDraft
	if err := json.Unmarshal(run.Params, &draft); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	params := FromDraft(draft)
	if err := params.Validate(); err != nil {
		return err
	}

	// Seed per-host states.
	statuses := make([]tasks.HostOpStatus, len(params.Hosts))
	for i, h := range params.Hosts {
		statuses[i] = tasks.HostOpStatus{HostID: h.ID, Hostname: h.Hostname, State: tasks.HostPending}
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = statuses
		total := len(params.Hosts)
		zero := 0
		s.Total = &total
		s.Current = &zero
	})

	concurrency := deployConcurrency()
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(params.Hosts) {
		concurrency = len(params.Hosts)
	}

	hostIdx := map[string]int{}
	for i, h := range params.Hosts {
		hostIdx[h.ID] = i
	}

	var (
		mu       sync.Mutex
		firstErr error
		done     int
	)

	process := func(h domain.HostRow) error {
		stepCh := make(chan StepEvent, 32)
		go func() {
			for ev := range stepCh {
				stage := ev.Stage
				stageDetail := ev.Detail
				idx := hostIdx[ev.HostID]
				run.MutateState(func(s *tasks.OperationProgress) {
					if idx < len(s.HostStatuses) {
						s.HostStatuses[idx].State = stageToHostState(stage)
						s.HostStatuses[idx].Detail = stageDetail
					}
				})
				if ev.Err != nil {
					run.LogError("%s: %s", ev.Hostname, ev.Err.Error())
				} else {
					run.LogInfo("%s: %s — %s", ev.Hostname, stage, stageDetail)
				}
			}
		}()
		emit := func(ev StepEvent) { stepCh <- ev }
		err := e.Installer.Install(ctx, h, params, emit)
		close(stepCh)
		mu.Lock()
		done++
		cur := done
		mu.Unlock()
		run.MutateState(func(s *tasks.OperationProgress) { s.Current = &cur })
		return err
	}

	if concurrency == 1 {
		// Sequential — default. Halts on first failure so the operator can
		// inspect the partial state without further hosts getting touched.
		for _, h := range params.Hosts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := process(h); err != nil {
				firstErr = err
				break
			}
		}
	} else {
		// Parallel — operator opt-in via BM_DEPLOY_CONCURRENCY.
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, h := range params.Hosts {
			wg.Add(1)
			go func(h domain.HostRow) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := process(h); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}(h)
		}
		wg.Wait()
	}

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	run.LogInfo("verify: checking cluster status")
	verifyFn := e.Verify
	if verifyFn == nil {
		verifyFn = verifyCluster
	}
	verify, err := verifyFn(ctx, params)
	if err != nil {
		run.LogError("verify: %v", err)
		return err
	}
	run.LogOK("verify: %d/%d nodes healthy", verify.NodesHealthy, verify.NodesTotal)
	run.LogOK("verify: %d/%d pool online", verify.PoolsOnline, verify.PoolsOnline)
	run.LogOK("verify: read/write smoke test passed")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Summary = append(s.Summary,
			tasks.SummaryItem{Label: "Console URL", Value: verify.ConsoleURL},
			tasks.SummaryItem{Label: "Nodes healthy", Value: fmt.Sprintf("%d/%d", verify.NodesHealthy, verify.NodesTotal)},
			tasks.SummaryItem{Label: "Pools online", Value: fmt.Sprintf("%d/%d", verify.PoolsOnline, verify.PoolsOnline)},
			tasks.SummaryItem{Label: "Read/write smoke test", Value: "passed"},
		)
	})

	clusterID, err := e.commit(ctx, params, verify)
	if err != nil {
		return fmt.Errorf("commit cluster: %w", err)
	}
	if e.AfterCommit != nil {
		e.AfterCommit(ctx, clusterID)
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster committed: " + clusterID
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Cluster ID", Value: clusterID})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Hosts", Value: strconv.Itoa(len(params.Hosts))})
	})
	return nil
}

// commit persists cluster + node rows + admin creds + SSH config. Mirrors M4
// import-commit with the fields the wizard populates.
func (e *Executor) commit(ctx context.Context, p DeployParams, verify VerifyResult) (string, error) {
	clusterID := slugify(p.Name)
	exists, err := e.Clusters.Exists(ctx, clusterID)
	if err != nil {
		return "", err
	}
	if exists {
		for i := 1; i < 1000; i++ {
			candidate := fmt.Sprintf("%s-%d", clusterID, i)
			has, err := e.Clusters.Exists(ctx, candidate)
			if err != nil {
				return "", err
			}
			if !has {
				clusterID = candidate
				break
			}
		}
	}

	nodeList := make([]domain.Node, 0, len(p.Hosts))
	for i, h := range p.Hosts {
		nodeID := fmt.Sprintf("%s-n%d", clusterID, i+1)
		nodeList = append(nodeList, domain.Node{
			ID:        nodeID,
			ClusterID: clusterID,
			Hostname:  h.Hostname,
			SSHPort:   h.Port,
			State:     domain.NodeOnline,
			Pool:      1,
			Pingable:  true,
			Sshable:   true,
		})
	}

	c := deployedCluster(clusterID, p, verify)
	if err := e.Clusters.Put(ctx, c); err != nil {
		return "", err
	}
	for _, n := range nodeList {
		if err := e.Nodes.Put(ctx, n); err != nil {
			return "", err
		}
	}

	// bm always dials a real node for management calls — p.ServerURL is for
	// MinIO's own MINIO_SERVER_URL (presigned URLs, etc.) and may point at a
	// load balancer that bm can't or shouldn't depend on.
	var url string
	if len(p.Hosts) > 0 {
		url = adminURLForHost(p, p.Hosts[0])
	}
	if err := e.ClusterAdmin.Put(ctx, clusterID, domain.AdminCreds{
		URL:       url,
		AccessKey: p.Credentials.RootUser,
		SecretKey: p.Credentials.RootPassword,
		// BYO certs aren't in the operator's system trust store. Skip
		// verification rather than maintaining a separate cert pool — this
		// matches the personal-tool trust model (see CLAUDE.md).
		Insecure: p.TLS.Enabled(),
	}); err != nil {
		return "", err
	}
	if err := e.SSHConfig.Put(ctx, clusterID, sshConfigFromParams(clusterID, p)); err != nil {
		return "", err
	}
	return clusterID, nil
}

func sshConfigFromParams(clusterID string, p DeployParams) domain.ClusterSshConfig {
	overrides := make(map[string]domain.SshOverrides)
	for i, h := range p.Hosts {
		if h.SSHOverride == nil {
			continue
		}
		hostID := fmt.Sprintf("%s-n%d", clusterID, i+1)
		overrides[hostID] = *h.SSHOverride
	}
	return domain.ClusterSshConfig{
		SSH:       p.SSH,
		Overrides: overrides,
	}
}

func stageToHostState(s Stage) tasks.HostOpState {
	switch s {
	case StagePending:
		return tasks.HostPending
	case StageHealthy:
		return tasks.HostSucceeded
	case StageFailed:
		return tasks.HostFailed
	default:
		return tasks.HostRunning
	}
}

func deployConcurrency() int {
	v := os.Getenv("BM_DEPLOY_CONCURRENCY")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// SlugifyName is the exported helper API handlers use to derive cluster ids
// from operator-supplied names.
func SlugifyName(name string) string { return slugify(name) }

// slugify is a small kebab-case slugger used by commit for the cluster id.
// Mirrors the rule the M4 import-commit handler uses via discovery.SlugifyHost.
func slugify(name string) string {
	out := make([]byte, 0, len(name))
	prevDash := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
			prevDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
			prevDash = false
		default:
			if !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	s := string(out)
	for len(s) > 0 && s[len(s)-1] == '-' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return "deployed"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
