package tasks

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/store"
)

func newHistoryFixture(t *testing.T) *historyStore {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return newHistoryStore(s)
}

func TestHistoryInsertListNewestFirst(t *testing.T) {
	h := newHistoryFixture(t)
	ctx := context.Background()
	base := time.Now().UTC()

	for i := 0; i < 5; i++ {
		// ULIDs are lex-sortable: zero-pad an integer to emulate them in tests.
		id := fmt.Sprintf("%026d", i)
		if err := h.Insert(ctx, HistoryEntry{
			ID:        id,
			At:        base.Add(time.Duration(i) * time.Minute),
			OpKind:    OpNoop,
			ClusterID: "c1",
			Status:    StateSucceeded,
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	got, err := h.List(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5, got %d", len(got))
	}
	// Reverse order: 4,3,2,1,0
	for idx, want := range []int{4, 3, 2, 1, 0} {
		expected := fmt.Sprintf("%026d", want)
		if got[idx].ID != expected {
			t.Fatalf("idx %d: want %s, got %s", idx, expected, got[idx].ID)
		}
	}
}

func TestHistoryFilters(t *testing.T) {
	h := newHistoryFixture(t)
	ctx := context.Background()
	base := time.Now().UTC()

	seed := []HistoryEntry{
		{ID: "01", At: base, OpKind: OpNoop, ClusterID: "a", Status: StateSucceeded},
		{ID: "02", At: base.Add(time.Minute), OpKind: OpNoop, ClusterID: "b", Status: StateFailed},
		{ID: "03", At: base.Add(2 * time.Minute), OpKind: OpNoop, ClusterID: "a", Status: StateRunning},
	}
	for _, e := range seed {
		if err := h.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name   string
		filter HistoryFilter
		want   int
	}{
		{"status", HistoryFilter{Status: StateFailed}, 1},
		{"cluster", HistoryFilter{ClusterID: "a"}, 2},
		{"since", HistoryFilter{Since: base.Add(time.Minute)}, 2},
		{"limit", HistoryFilter{Limit: 1}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.List(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("want %d, got %d", tc.want, len(got))
			}
		})
	}
}

func TestHistorySweep(t *testing.T) {
	h := newHistoryFixture(t)
	ctx := context.Background()
	for i := 0; i < historyRowCap+50; i++ {
		_ = h.Insert(ctx, HistoryEntry{
			ID:        fmt.Sprintf("%026d", i),
			At:        time.Now().UTC(),
			OpKind:    OpNoop,
			ClusterID: "c",
			Status:    StateSucceeded,
		})
	}
	deleted, err := h.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 50 {
		t.Fatalf("want 50 deletions, got %d", deleted)
	}
	got, err := h.List(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != historyRowCap {
		t.Fatalf("want %d remaining, got %d", historyRowCap, len(got))
	}
}

func TestHistoryUpdate(t *testing.T) {
	h := newHistoryFixture(t)
	ctx := context.Background()
	id := "01"
	_ = h.Insert(ctx, HistoryEntry{
		ID:        id,
		At:        time.Now().UTC(),
		OpKind:    OpNoop,
		ClusterID: "c",
		Status:    StateRunning,
	})

	if err := h.Update(ctx, id, func(e *HistoryEntry) {
		e.Status = StateSucceeded
		d := 1.5
		e.DurationSec = &d
	}); err != nil {
		t.Fatal(err)
	}

	got, err := h.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StateSucceeded || got.DurationSec == nil || *got.DurationSec != 1.5 {
		t.Fatalf("update did not stick: %+v", got)
	}
}
