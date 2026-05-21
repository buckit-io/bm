package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// ---- start_cluster: parallel systemctl start across all hosts ----

type startClusterExecutor struct{ deps Deps }

func (e *startClusterExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *startClusterExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	start := time.Now()
	unit := unitName(rc.cluster.Engine)
	seedHostStatuses(run, rc.nodes)

	var (
		mu      sync.Mutex
		failed  int
		failErr error
		wg      sync.WaitGroup
	)
	for i, n := range rc.nodes {
		wg.Add(1)
		go func(i int, n domain.Node) {
			defer wg.Done()
			setHostState(run, i, n, tasks.HostRunning, "systemctl start")
			cmd := sudoSystemctl(rc.sshCreds.User, "start "+unit)
			if _, err := runHostStep(ctx, e.deps, rc, n, cmd); err != nil {
				setHostState(run, i, n, tasks.HostFailed, err.Error())
				run.LogError("%s: %v", n.Hostname, err)
				mu.Lock()
				failed++
				if failErr == nil {
					failErr = err
				}
				mu.Unlock()
				return
			}
			setHostState(run, i, n, tasks.HostSucceeded, "started")
			run.LogOK("%s: started", n.Hostname)
		}(i, n)
	}
	wg.Wait()

	if failed > 0 {
		return fmt.Errorf("%d/%d hosts failed to start: %w", failed, len(rc.nodes), failErr)
	}
	// Cluster-wide health wait — admin API only works once quorum is up.
	run.LogInfo("waiting for cluster healthy")
	if err := waitClusterHealthy(ctx, rc.admin, WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}); err != nil {
		return fmt.Errorf("post-start health: %w", err)
	}
	duration := time.Since(start)
	run.LogOK("Cluster healthy after %s", formatDuration(duration))
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster started"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Healthy after", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- rolling_restart: sequential systemctl restart with health-wait between hosts ----

type rollingRestartExecutor struct{ deps Deps }

func (e *rollingRestartExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *rollingRestartExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	start := time.Now()
	unit := unitName(rc.cluster.Engine)
	seedHostStatuses(run, rc.nodes)

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "systemctl restart")
		run.LogInfo("%s: systemctl restart %s", n.Hostname, unit)
		cmd := sudoSystemctl(rc.sshCreds.User, "restart "+unit)
		if _, err := runHostStep(ctx, e.deps, rc, n, cmd); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		// Wait for THIS host's service before moving on.
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "healthy")
		run.LogOK("%s: healthy", n.Hostname)
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Rolling restart complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Sequential time", Value: formatDuration(duration)},
			{Label: "Failed", Value: "0"},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- rolling_upgrade: Buckit-only. download new RPM + dnf upgrade + restart per host ----

type rollingUpgradeParams struct {
	Version   string `json:"version,omitempty"`
	CustomURL string `json:"customUrl,omitempty"`
}

type rollingUpgradeExecutor struct{ deps Deps }

func (e *rollingUpgradeExecutor) Validate(req tasks.DispatchRequest) error {
	if len(req.Params) > 0 {
		var p rollingUpgradeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("rolling_upgrade: invalid params: %w", err)
		}
	}
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpRollingUpgrade)
}

func (e *rollingUpgradeExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpKind("rolling_upgrade")); err != nil {
		return err
	}
	var p rollingUpgradeParams
	if len(run.Params) > 0 {
		_ = json.Unmarshal(run.Params, &p)
	}
	url, err := resolveRpmURL(p.Version, p.CustomURL)
	if err != nil {
		return err
	}
	fromVersion := rc.cluster.Version
	toVersion := p.Version
	if toVersion == "" || toVersion == "custom" {
		toVersion = "custom URL"
	}

	start := time.Now()
	seedHostStatuses(run, rc.nodes)

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "downloading")
		run.LogInfo("%s: downloading %s", n.Hostname, url)
		if _, err := runHostStep(ctx, e.deps, rc, n, fmt.Sprintf("curl -fSL -o /tmp/buckit.rpm %s", shellQuote(url))); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s download: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "dnf upgrade")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, "dnf upgrade -y /tmp/buckit.rpm")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s upgrade: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "systemctl restart")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "restart buckit.service")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s restart: %w", n.Hostname, err)
		}
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "upgraded")
		run.LogOK("%s: upgraded", n.Hostname)
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Rolling upgrade complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "From", Value: fromVersion},
			{Label: "To", Value: toVersion},
			{Label: "Duration", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- redeploy_software: Buckit-only. stop -> reinstall -> start per host ----

type redeployExecutor struct{ deps Deps }

func (e *redeployExecutor) Validate(req tasks.DispatchRequest) error {
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpRedeploySoftware)
}

func (e *redeployExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpKind("redeploy_software")); err != nil {
		return err
	}
	url, err := resolveRpmURL(rc.cluster.Version, "")
	if err != nil {
		return err
	}
	start := time.Now()
	seedHostStatuses(run, rc.nodes)

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "stop")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "stop buckit.service")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s stop: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "reinstalling")
		if _, err := runHostStep(ctx, e.deps, rc, n, fmt.Sprintf("curl -fSL -o /tmp/buckit.rpm %s", shellQuote(url))); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s download: %w", n.Hostname, err)
		}
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, "dnf reinstall -y /tmp/buckit.rpm")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s reinstall: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "start")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "start buckit.service")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s start: %w", n.Hostname, err)
		}
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "redeployed")
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Redeploy complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Duration", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- shared helpers for the orchestrated flavor ----

func seedHostStatuses(run *tasks.Run, ns []domain.Node) {
	statuses := make([]tasks.HostOpStatus, len(ns))
	for i, n := range ns {
		statuses[i] = tasks.HostOpStatus{HostID: n.ID, Hostname: n.Hostname, State: tasks.HostPending}
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = statuses
		total := len(ns)
		zero := 0
		s.Total = &total
		s.Current = &zero
	})
}

func setHostState(run *tasks.Run, i int, _ domain.Node, state tasks.HostOpState, detail string) {
	run.MutateState(func(s *tasks.OperationProgress) {
		if i < len(s.HostStatuses) {
			s.HostStatuses[i].State = state
			s.HostStatuses[i].Detail = detail
		}
		if state == tasks.HostSucceeded || state == tasks.HostFailed {
			cur := 0
			if s.Current != nil {
				cur = *s.Current
			}
			cur++
			s.Current = &cur
		}
	})
}

func sudoSystemctl(user, args string) string {
	cmd := "systemctl " + args + " && systemctl daemon-reload"
	if user == "root" {
		return cmd
	}
	return "sudo -n bash -c " + shellQuote(cmd)
}

func sudoBash(user, cmd string) string {
	if user == "root" {
		return cmd
	}
	return "sudo -n bash -c " + shellQuote(cmd)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + replaceSingleQuotes(s) + "'"
}

func replaceSingleQuotes(s string) string {
	out := ""
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
		} else {
			out += string(r)
		}
	}
	return out
}

// resolveRpmURL returns the URL for a version tag, or the custom URL when
// version="custom".
func resolveRpmURL(version, customURL string) (string, error) {
	if version == "custom" {
		if customURL == "" {
			return "", errors.New("rolling_upgrade: customUrl required when version=custom")
		}
		return customURL, nil
	}
	v := deploy.VersionByTag(version)
	if v == nil {
		return "", fmt.Errorf("rolling_upgrade: unsupported version %q", version)
	}
	if v.RpmURL == "" {
		return "", fmt.Errorf("rolling_upgrade: no rpm URL for %q", version)
	}
	return v.RpmURL, nil
}
