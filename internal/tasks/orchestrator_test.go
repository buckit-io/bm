package tasks

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/store"
)

func newManagerFixture(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m := NewManager(s)
	RegisterNoop()
	return m
}

func TestDispatchUnknownKind(t *testing.T) {
	m := newManagerFixture(t)
	_, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1",
		Kind:      OpKind("does_not_exist"),
	})
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("want ErrUnknownKind, got %v", err)
	}
}

func TestDispatchNoopSuccess(t *testing.T) {
	m := newManagerFixture(t)
	params, _ := json.Marshal(NoopParams{Events: 2, IntervalMs: 10})
	taskID, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1",
		Kind:      OpNoop,
		Params:    params,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	waitFor(t, func() bool {
		entry, err := m.hist.Get(context.Background(), taskID)
		return err == nil && entry.Status.IsTerminal()
	})

	entry, err := m.hist.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != StateSucceeded {
		t.Fatalf("want succeeded, got %s", entry.Status)
	}
	if entry.Result == nil {
		t.Fatal("missing result snapshot")
	}
	if entry.DurationSec == nil || *entry.DurationSec <= 0 {
		t.Fatalf("bad duration: %v", entry.DurationSec)
	}
}

func TestDispatchNoopFailure(t *testing.T) {
	m := newManagerFixture(t)
	params, _ := json.Marshal(NoopParams{
		Events:      5,
		IntervalMs:  10,
		FailAfter:   2,
		FailureNote: "synthetic",
	})
	taskID, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1",
		Kind:      OpNoop,
		Params:    params,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		entry, err := m.hist.Get(context.Background(), taskID)
		return err == nil && entry.Status.IsTerminal()
	})
	entry, _ := m.hist.Get(context.Background(), taskID)
	if entry.Status != StateFailed {
		t.Fatalf("want failed, got %s", entry.Status)
	}
	if !strings.Contains(entry.FailureNote, "synthetic") {
		t.Fatalf("missing failureNote: %q", entry.FailureNote)
	}
}

func TestDispatchClusterBusy(t *testing.T) {
	m := newManagerFixture(t)
	params, _ := json.Marshal(NoopParams{Events: 50, IntervalMs: 30})
	_, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1", Kind: OpNoop, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1", Kind: OpNoop,
	})
	if !errors.Is(err, ErrClusterBusy) {
		t.Fatalf("want ErrClusterBusy, got %v", err)
	}
	// Different cluster should proceed.
	if _, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c2", Kind: OpNoop, Params: mustJSON(NoopParams{Events: 1, IntervalMs: 10}),
	}); err != nil {
		t.Fatalf("different-cluster dispatch failed: %v", err)
	}
}

func TestDispatchCancel(t *testing.T) {
	m := newManagerFixture(t)
	params, _ := json.Marshal(NoopParams{Events: 50, IntervalMs: 50})
	taskID, err := m.Dispatch(context.Background(), DispatchRequest{
		ClusterID: "c1", Kind: OpNoop, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := m.Cancel(taskID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		entry, err := m.hist.Get(context.Background(), taskID)
		return err == nil && entry.Status.IsTerminal()
	})
	entry, _ := m.hist.Get(context.Background(), taskID)
	if entry.Status != StateCanceled {
		t.Fatalf("want canceled, got %s", entry.Status)
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
