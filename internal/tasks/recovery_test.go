package tasks

import (
	"context"
	"testing"
	"time"
)

func TestRecoverInFlightMarksRunningAsFailed(t *testing.T) {
	m := newManagerFixture(t)
	ctx := context.Background()
	id := "01"
	if err := m.hist.Insert(ctx, HistoryEntry{
		ID:        id,
		At:        time.Now().UTC().Add(-time.Hour),
		OpKind:    OpNoop,
		ClusterID: "c",
		Status:    StateRunning,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := RecoverInFlight(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 recovered, got %d", n)
	}
	got, err := m.hist.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StateFailed || got.FailureNote != recoverNote {
		t.Fatalf("unexpected post-recovery state: %+v", got)
	}
	if got.Result == nil || got.Result.State != StateFailed {
		t.Fatalf("result snapshot not stamped: %+v", got.Result)
	}
}
