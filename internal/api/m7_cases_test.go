package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// dispatch is a tiny helper for these tests.
func dispatch(t *testing.T, h *m7Harness, kind tasks.OpKind, targets []string, params any) (string, int) {
	t.Helper()
	body := map[string]any{
		"clusterId":     "test-cluster",
		"clusterName":   "test",
		"kind":          string(kind),
		"targetHostIds": targets,
	}
	if params != nil {
		raw, _ := json.Marshal(params)
		body["params"] = json.RawMessage(raw)
	}
	rawBody, _ := json.Marshal(body)
	resp := do(t, h.server, http.MethodPost, "/api/v1/operations", rawBody)
	if resp.code != 202 {
		return "", resp.code
	}
	var d tasks.DispatchResponse
	_ = json.Unmarshal(resp.body, &d)
	return d.TaskID, resp.code
}

func waitTermM7(t *testing.T, h *m7Harness, taskID string, within time.Duration) tasks.HistoryEntry {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := do(t, h.server, http.MethodGet, "/api/v1/history?limit=50", nil)
		var rows []tasks.HistoryEntry
		_ = json.Unmarshal(resp.body, &rows)
		for _, r := range rows {
			if r.ID == taskID && r.Status.IsTerminal() {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s did not terminate within %s", taskID, within)
	return tasks.HistoryEntry{}
}

func TestM7FreezeUnfreeze(t *testing.T) {
	h := newM7Harness(t)
	id, code := dispatch(t, h, tasks.OpFreezeAPI, nil, nil)
	if code != 202 {
		t.Fatalf("freeze dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 5*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("freeze: want succeeded, got %s (%s)", row.Status, row.FailureNote)
	}
	if h.calls.serviceFreeze.Load() != 1 {
		t.Fatalf("expected 1 freeze call, got %d", h.calls.serviceFreeze.Load())
	}
	c, _ := h.clusters.Get(context.Background(), "test-cluster")
	if !c.APIFrozen {
		t.Fatal("APIFrozen should be true after freeze")
	}

	id2, _ := dispatch(t, h, tasks.OpUnfreezeAPI, nil, nil)
	row2 := waitTermM7(t, h, id2, 5*time.Second)
	if row2.Status != tasks.StateSucceeded {
		t.Fatalf("unfreeze: %s (%s)", row2.Status, row2.FailureNote)
	}
	c, _ = h.clusters.Get(context.Background(), "test-cluster")
	if c.APIFrozen {
		t.Fatal("APIFrozen should be false after unfreeze")
	}
}

func TestM7RestartCluster(t *testing.T) {
	h := newM7Harness(t)
	id, code := dispatch(t, h, tasks.OpRestartCluster, nil, nil)
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("restart: %s (%s)", row.Status, row.FailureNote)
	}
	if h.calls.serviceRestart.Load() != 1 {
		t.Fatalf("expected 1 restart call, got %d", h.calls.serviceRestart.Load())
	}
}

func TestM7RollingRestart(t *testing.T) {
	h := newM7Harness(t)
	id, code := dispatch(t, h, tasks.OpRollingRestart, nil, nil)
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("rolling_restart: %s (%s)", row.Status, row.FailureNote)
	}
	if row.Result == nil || len(row.Result.HostStatuses) != 2 {
		t.Fatalf("expected 2 host statuses, got %+v", row.Result)
	}
}

func TestM7StartCluster(t *testing.T) {
	h := newM7Harness(t)
	id, code := dispatch(t, h, tasks.OpStartCluster, nil, nil)
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("start_cluster: %s (%s)", row.Status, row.FailureNote)
	}
}

func TestM7HostScopedSystemctl(t *testing.T) {
	h := newM7Harness(t)
	// Target only one of the two hosts.
	id, code := dispatch(t, h, tasks.OpSystemctlRestart, []string{"test-cluster-n1"}, nil)
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("systemctl_restart: %s (%s)", row.Status, row.FailureNote)
	}
	if row.Result == nil || len(row.Result.HostStatuses) != 1 {
		t.Fatalf("expected 1 host status (targeted), got %+v", row.Result)
	}
}

func TestM7StartHeal(t *testing.T) {
	h := newM7Harness(t)
	id, code := dispatch(t, h, tasks.OpStartHeal, nil, nil)
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("start_heal: %s (%s)", row.Status, row.FailureNote)
	}
	if h.calls.healStart.Load() < 1 {
		t.Fatalf("expected at least 1 heal call, got %d", h.calls.healStart.Load())
	}
}

func TestM7EngineMismatch(t *testing.T) {
	h := newM7Harness(t)
	// Flip the cluster to MinIO.
	c, _ := h.clusters.Get(context.Background(), "test-cluster")
	c.Engine = domain.EngineMinio
	_ = h.clusters.Put(context.Background(), c)

	// Buckit-only op against a MinIO cluster → 400.
	_, code := dispatch(t, h, tasks.OpRollingUpgrade, nil, nil)
	if code != 400 {
		t.Fatalf("rolling_upgrade against MinIO: want 400, got %d", code)
	}
}

func TestM7RotateRootCredsStub(t *testing.T) {
	h := newM7Harness(t)
	_, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, nil)
	if code != 400 {
		t.Fatalf("rotate_root_creds stub: want 400, got %d", code)
	}
}
