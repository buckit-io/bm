package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// Host-scoped service ops: systemctl restart/stop/start on a target subset
// of hosts. Sequential. Restart + start wait for health between hosts; stop
// does not (cluster is intentionally degrading).

type hostSystemctlExecutor struct {
	deps        Deps
	verb        string // "restart" | "stop" | "start"
	waitHealthy bool   // true for restart/start, false for stop
}

func (e *hostSystemctlExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *hostSystemctlExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	hosts, err := targetHosts(tasks.DispatchRequest{TargetHostIDs: run.Targets}, rc.nodes)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("%s: no target hosts", e.verb)
	}
	unit := unitName(rc.cluster.Engine)
	start := time.Now()
	seedHostStatuses(run, hosts)

	var failed int
	for i, n := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "systemctl "+e.verb)
		cmd := sudoSystemctl(rc.sshCreds.User, e.verb+" "+unit)
		if _, err := runHostStep(ctx, e.deps, rc, n, cmd); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			failed++
			run.LogError("%s: %v", n.Hostname, err)
			continue
		}
		if e.waitHealthy {
			if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
				setHostState(run, i, n, tasks.HostFailed, err.Error())
				failed++
				continue
			}
		}
		setHostState(run, i, n, tasks.HostSucceeded, "done")
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = fmt.Sprintf("systemctl %s complete", e.verb)
		s.Summary = []tasks.SummaryItem{
			{Label: "Targeted", Value: fmt.Sprintf("%d", len(hosts))},
			{Label: "Succeeded", Value: fmt.Sprintf("%d", len(hosts)-failed)},
			{Label: "Failed", Value: fmt.Sprintf("%d", failed)},
			{Label: "Duration", Value: formatDuration(duration)},
		}
	})
	// Don't refresh the cluster row after a stop — admin API is probably
	// down on stopped nodes.
	if e.verb != "stop" {
		refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d hosts failed", failed, len(hosts))
	}
	return nil
}

// Host-scoped reboot ops.

type rebootHostExecutor struct{ deps Deps }

func (e *rebootHostExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *rebootHostExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	hosts, err := targetHosts(tasks.DispatchRequest{TargetHostIDs: run.Targets}, rc.nodes)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("reboot_host: no target hosts")
	}
	seedHostStatuses(run, hosts)
	for i, n := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "issuing reboot")
		run.LogInfo("%s: reboot", n.Hostname)
		// The reboot command kills the SSH session — we expect the error.
		// Treat any failure as "the command went out" and move to the wait loop.
		_, _ = runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, "shutdown -r now"))

		setHostState(run, i, n, tasks.HostRunning, "waiting for SSH return")
		if err := waitSSHReturn(ctx, e.deps, rc, n, WaitOptions{Timeout: 5 * time.Minute}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "waiting for service")
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 90 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "back online")
		run.LogOK("%s: rebooted and healthy", n.Hostname)
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Reboot complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Targeted", Value: fmt.Sprintf("%d", len(hosts))},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

type shutdownHostExecutor struct{ deps Deps }

func (e *shutdownHostExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *shutdownHostExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	hosts, err := targetHosts(tasks.DispatchRequest{TargetHostIDs: run.Targets}, rc.nodes)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("shutdown_host: no target hosts")
	}
	seedHostStatuses(run, hosts)
	for i, n := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "issuing shutdown")
		run.LogInfo("%s: shutdown -h now", n.Hostname)
		// Like reboot, the command kills the session.
		_, _ = runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, "shutdown -h now"))
		setHostState(run, i, n, tasks.HostSucceeded, "shutdown issued")
		// Drop the cached client; it's broken.
		e.deps.SSHPool.Drop(rc.cluster.ID, n.ID)
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Shutdown commands issued"
		s.Summary = []tasks.SummaryItem{
			{Label: "Targeted", Value: fmt.Sprintf("%d", len(hosts))},
		}
	})
	return nil
}

// Suppress unused-warning for domain (imported via Node parameter type).
var _ = domain.Node{}
