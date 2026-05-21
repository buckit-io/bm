package operations

import (
	"context"
	"fmt"

	"github.com/buckit-io/bm/internal/tasks"
)

// signal executors share a tiny template: load → adminCall → side effects.

type freezeExecutor struct{ deps Deps }

func (e *freezeExecutor) Validate(req tasks.DispatchRequest) error { return nil }
func (e *freezeExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	run.LogInfo("calling admin ServiceFreeze")
	if err := rc.admin.ServiceFreeze(ctx); err != nil {
		return fmt.Errorf("admin freeze: %w", err)
	}
	run.LogOK("S3 API frozen")
	setAPIFrozen(ctx, e.deps, run.ClusterID, true)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "S3 API frozen"
		s.Summary = []tasks.SummaryItem{{Label: "Result", Value: "frozen"}}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

type unfreezeExecutor struct{ deps Deps }

func (e *unfreezeExecutor) Validate(req tasks.DispatchRequest) error { return nil }
func (e *unfreezeExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	run.LogInfo("calling admin ServiceUnfreeze")
	if err := rc.admin.ServiceUnfreeze(ctx); err != nil {
		return fmt.Errorf("admin unfreeze: %w", err)
	}
	run.LogOK("S3 API unfrozen")
	setAPIFrozen(ctx, e.deps, run.ClusterID, false)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "S3 API unfrozen"
		s.Summary = []tasks.SummaryItem{{Label: "Result", Value: "unfrozen"}}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

type stopClusterExecutor struct{ deps Deps }

func (e *stopClusterExecutor) Validate(req tasks.DispatchRequest) error { return nil }
func (e *stopClusterExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	run.LogInfo("calling admin ServiceStop")
	if err := rc.admin.ServiceStop(ctx); err != nil {
		return fmt.Errorf("admin stop: %w", err)
	}
	run.LogOK("Cluster stopped")
	// Admin API is now unreachable by design. Don't try to refresh — just
	// mark the cluster row as unreachable so the UI shows the right state.
	markUnreachable(ctx, e.deps, run.ClusterID)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster stopped"
		s.Summary = []tasks.SummaryItem{
			{Label: "Result", Value: "stopped"},
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
		}
	})
	return nil
}
