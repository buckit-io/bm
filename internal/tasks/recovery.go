package tasks

import (
	"context"
	"time"
)

// recoverNote is stamped on history rows that were running when bm web exited.
const recoverNote = "bm process restarted mid-op"

// RecoverInFlight scans the history bucket and transitions any row still in
// `running` to `failed`. Run once at process startup before the API begins
// serving requests so the History page never shows a stale running row from
// a previous run.
func RecoverInFlight(ctx context.Context, m *Manager) (int, error) {
	rows, err := m.ListHistory(ctx, HistoryFilter{Status: StateRunning})
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, r := range rows {
		duration := now.Sub(r.At).Seconds()
		err := m.hist.Update(ctx, r.ID, func(e *HistoryEntry) {
			e.Status = StateFailed
			e.FailureNote = recoverNote
			d := duration
			e.DurationSec = &d
			e.Result = &OperationResult{
				State:       StateFailed,
				FailureNote: recoverNote,
			}
		})
		if err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}
