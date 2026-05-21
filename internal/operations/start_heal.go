package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/tasks"
)

const healCoalesceWindow = 100

// healParams is the optional params shape — both fields are optional.
type healParams struct {
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Recursive *bool  `json:"recursive,omitempty"`
}

type startHealExecutor struct{ deps Deps }

func (e *startHealExecutor) Validate(req tasks.DispatchRequest) error {
	if len(req.Params) > 0 {
		var p healParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("start_heal: invalid params: %w", err)
		}
	}
	return nil
}

func (e *startHealExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	var p healParams
	if len(run.Params) > 0 {
		_ = json.Unmarshal(run.Params, &p)
	}
	recursive := true
	if p.Recursive != nil {
		recursive = *p.Recursive
	}

	run.LogInfo("starting heal: bucket=%q prefix=%q recursive=%v", p.Bucket, p.Prefix, recursive)
	token, started, err := rc.admin.HealStart(ctx, p.Bucket, p.Prefix, recursive)
	if err != nil {
		return fmt.Errorf("heal start: %w", err)
	}
	run.LogOK("heal task started (token=%s, started=%s)", token[:min(len(token), 8)], started.Format(time.RFC3339))

	var (
		totalObjects int
		totalErrors  int
		lastSummary  string
		coalesce     int
	)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			// Best-effort cancel of the underlying heal so the cluster
			// doesn't keep working after the operator gave up.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = rc.admin.HealStop(cancelCtx, p.Bucket, p.Prefix, token)
			cancel()
			return ctx.Err()
		case <-tick.C:
		}

		status, err := rc.admin.HealStatus(ctx, p.Bucket, p.Prefix, token)
		if err != nil {
			return fmt.Errorf("heal status: %w", err)
		}
		for _, it := range status.Items {
			totalObjects++
			if it.Detail != "" && strings.Contains(strings.ToLower(it.Detail), "error") {
				totalErrors++
			}
			coalesce++
		}
		if coalesce >= healCoalesceWindow {
			run.LogInfo("healed %d objects (%d errors)", totalObjects, totalErrors)
			coalesce = 0
		}
		if status.Summary != lastSummary {
			run.MutateState(func(s *tasks.OperationProgress) {
				s.Detail = fmt.Sprintf("heal %s: %d objects, %d errors", status.Summary, totalObjects, totalErrors)
			})
			lastSummary = status.Summary
		}
		if status.IsTerminal() {
			run.LogOK("heal finished: %d objects, %d errors", totalObjects, totalErrors)
			run.MutateState(func(s *tasks.OperationProgress) {
				s.Detail = fmt.Sprintf("Heal finished — %d objects", totalObjects)
				s.Summary = []tasks.SummaryItem{
					{Label: "Objects", Value: fmt.Sprintf("%d", totalObjects)},
					{Label: "Errors", Value: fmt.Sprintf("%d", totalErrors)},
					{Label: "Duration", Value: formatDuration(time.Since(started))},
				}
			})
			refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
			if status.FailureDetail != "" {
				return fmt.Errorf("heal stopped: %s", status.FailureDetail)
			}
			return nil
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
