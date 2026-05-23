package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/buckit-io/bm/internal/tasks"
)

type restartClusterExecutor struct{ deps Deps }

func (e *restartClusterExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *restartClusterExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	start := time.Now()
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Requesting in-place restart"
	})
	run.LogInfo("calling admin ServiceRestart")
	run.LogInfo("waiting for restart request to complete; the admin connection may drop while buckit re-execs")
	if err := rc.admin.ServiceRestart(ctx); err != nil {
		return fmt.Errorf("admin restart: %w", err)
	}
	run.LogOK("Restart request accepted, polling for healthy")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Polling cluster health"
	})

	if err := waitClusterHealthy(ctx, rc.admin, WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}); err != nil {
		run.LogError("cluster did not become healthy within 2m")
		return fmt.Errorf("post-restart health: %w", err)
	}
	run.LogOK("cluster healthy after in-place restart")

	duration := time.Since(start)
	online := 0
	if info, err := rc.admin.ServerInfo(ctx); err == nil {
		for _, s := range info.Servers {
			if s.State == "online" {
				online++
			}
		}
	}
	run.LogOK("Cluster healthy after %s", formatDuration(duration))
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = fmt.Sprintf("Cluster restarted in %s", formatDuration(duration))
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Restart time", Value: formatDuration(duration)},
			{Label: "Online after", Value: fmt.Sprintf("%d/%d", online, len(rc.nodes))},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}
