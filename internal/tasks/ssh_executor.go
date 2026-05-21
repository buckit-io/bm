package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
)

// SshProbeParams is the params payload for the ssh_probe executor. Either
// supply Hosts inline (handy for the /ssh/test flow and tests), or rely on
// TargetHostIDs at the dispatch level so the executor looks the nodes up in
// the `nodes` bucket. For M3 inline is the typical path because the nodes
// bucket isn't populated until M4 discovery lands.
type SshProbeParams struct {
	Hosts []domain.HostRef `json:"hosts,omitempty"`
}

type sshProbeExecutor struct {
	nodes  *nodes.Repo
	sshcfg *sshconfig.Repo
}

// RegisterSshProbe wires the ssh_probe executor with the dependencies it needs.
// Call once at startup after the repos exist.
func RegisterSshProbe(nodesRepo *nodes.Repo, sshcfgRepo *sshconfig.Repo) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[OpSshProbe] = &sshProbeExecutor{nodes: nodesRepo, sshcfg: sshcfgRepo}
}

func (e *sshProbeExecutor) Validate(req DispatchRequest) error {
	if e.sshcfg == nil {
		return errors.New("ssh_probe: sshconfig repo not configured")
	}
	if len(req.Params) == 0 && len(req.TargetHostIDs) == 0 {
		return errors.New("ssh_probe: provide params.hosts or targetHostIds")
	}
	if len(req.Params) > 0 {
		var p SshProbeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("ssh_probe: invalid params: %w", err)
		}
	}
	return nil
}

func (e *sshProbeExecutor) Execute(ctx context.Context, run *Run) error {
	cfg, err := e.sshcfg.Get(ctx, run.ClusterID)
	if err != nil {
		return fmt.Errorf("load ssh config for %s: %w", run.ClusterID, err)
	}

	hosts, err := e.resolveHosts(ctx, run)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return errors.New("ssh_probe: no hosts to probe")
	}

	statuses := make([]HostOpStatus, len(hosts))
	for i, h := range hosts {
		statuses[i] = HostOpStatus{HostID: h.ID, Hostname: h.Hostname, State: HostPending}
	}
	run.MutateState(func(s *OperationProgress) {
		s.HostStatuses = statuses
		s.Total = intPtr(len(hosts))
		s.Current = intPtr(0)
	})

	results := make([]bmssh.ProbeResult, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	completed := 0

	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host domain.HostRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}
			run.MutateState(func(s *OperationProgress) {
				if i < len(s.HostStatuses) {
					s.HostStatuses[i].State = HostRunning
				}
			})
			run.LogInfo("probing %s", host.Hostname)

			ov := cfg.Overrides[host.ID]
			var ovPtr *domain.SshOverrides
			if _, has := cfg.Overrides[host.ID]; has {
				ovPtr = &ov
			}
			res := bmssh.Probe(ctx, host, bmssh.Merge(cfg.SSH, ovPtr))

			mu.Lock()
			results[i] = res
			completed++
			done := completed
			mu.Unlock()

			run.MutateState(func(s *OperationProgress) {
				if i < len(s.HostStatuses) {
					if res.OK {
						s.HostStatuses[i].State = HostSucceeded
						s.HostStatuses[i].Detail = res.Kernel
					} else {
						s.HostStatuses[i].State = HostFailed
						s.HostStatuses[i].Detail = res.Error
					}
				}
				cp := done
				s.Current = &cp
			})
			if res.OK {
				run.LogOK("%s: %s (%dms)", host.Hostname, res.Kernel, res.RttMs)
			} else {
				run.LogError("%s: %s", host.Hostname, res.Error)
			}
		}(i, host)
	}
	wg.Wait()

	failures := 0
	summaryItems := make([]SummaryItem, 0, len(results))
	for i, r := range results {
		if !r.OK {
			failures++
			continue
		}
		summaryItems = append(summaryItems, SummaryItem{
			Label: hosts[i].Hostname,
			Value: fmt.Sprintf("%s · %s · %dms", r.Kernel, r.OS, r.RttMs),
		})
	}
	run.MutateState(func(s *OperationProgress) {
		s.Summary = summaryItems
		if failures > 0 {
			s.FailureNote = fmt.Sprintf("%d/%d hosts unreachable", failures, len(results))
		}
	})

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failures > 0 {
		return fmt.Errorf("%d/%d hosts unreachable", failures, len(results))
	}
	return nil
}

func (e *sshProbeExecutor) resolveHosts(ctx context.Context, run *Run) ([]domain.HostRef, error) {
	if len(run.Params) > 0 {
		var p SshProbeParams
		if err := json.Unmarshal(run.Params, &p); err == nil && len(p.Hosts) > 0 {
			return p.Hosts, nil
		}
	}
	// Fall back to nodes bucket lookup once the bucket is populated (M4+).
	if e.nodes == nil || len(run.Targets) == 0 {
		return nil, errors.New("ssh_probe: no inline hosts and no nodes repo configured")
	}
	out := make([]domain.HostRef, 0, len(run.Targets))
	for _, id := range run.Targets {
		n, err := e.nodes.Get(ctx, run.ClusterID, id)
		if err != nil {
			return nil, fmt.Errorf("lookup node %s: %w", id, err)
		}
		out = append(out, domain.HostRef{ID: n.ID, Hostname: n.Hostname, Port: n.SSHPort})
	}
	return out, nil
}
