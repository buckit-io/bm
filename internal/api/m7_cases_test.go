package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/deploy"
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

func TestM7ClusterUpgradeByAdminUpdate(t *testing.T) {
	h := newM7Harness(t)
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "RELEASE.2026-06-01T00-00-00Z",
		Label: "RELEASE.2026-06-01T00-00-00Z",
		Artifacts: []domain.BuckitArtifact{
			{Kind: "binary", OS: "linux", Arch: "amd64", URL: "https://example.test/buckit-linux-amd64"},
			{Kind: "binary", OS: "linux", Arch: "arm64", URL: "https://example.test/buckit-linux-arm64"},
		},
	}})
	defer restoreVersions()
	id, code := dispatch(t, h, tasks.OpClusterUpgradeByAdminUpdate, nil, map[string]any{
		"version": "RELEASE.2026-06-01T00-00-00Z",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("cluster_upgrade_by_admin_update: %s (%s)", row.Status, row.FailureNote)
	}
	if h.calls.serverUpdate.Load() != 1 {
		t.Fatalf("expected 1 server update call, got %d", h.calls.serverUpdate.Load())
	}
	if got, _ := h.calls.serverUpdateURL.Load().(string); got != "https://example.test/buckit-linux-amd64" {
		t.Fatalf("expected amd64 binary update URL, got %q", got)
	}
	if h.calls.serviceRestart.Load() != 0 {
		t.Fatalf("did not expect explicit service restart call, got %d", h.calls.serviceRestart.Load())
	}
}

func TestM7ClusterUpgradeByAdminUpdateRejectsNonNewerVersion(t *testing.T) {
	h := newM7Harness(t)
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "RELEASE.2026-05-01T00-00-00Z",
		Label: "RELEASE.2026-05-01T00-00-00Z",
		Artifacts: []domain.BuckitArtifact{
			{Kind: "binary", OS: "linux", Arch: "amd64", URL: "https://example.test/buckit-linux-amd64"},
		},
	}})
	defer restoreVersions()
	id, code := dispatch(t, h, tasks.OpClusterUpgradeByAdminUpdate, nil, map[string]any{
		"version": "RELEASE.2026-05-01T00-00-00Z",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("cluster_upgrade_by_admin_update: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "not newer") {
		t.Fatalf("expected non-newer failure note, got %q", row.FailureNote)
	}
	if h.calls.serverUpdate.Load() != 0 {
		t.Fatalf("did not expect server update call, got %d", h.calls.serverUpdate.Load())
	}
}

func TestM7ClusterUpgradeByAdminUpdateFailsOnServerReportedError(t *testing.T) {
	h := newM7Harness(t)
	h.update = madmin.ServerUpdateStatusV2{
		Results: []madmin.ServerPeerUpdateStatus{
			{Host: "node1", CurrentVersion: "2026-05-01T00:00:00Z", Err: "permission denied"},
			{Host: "node2", CurrentVersion: "2026-05-01T00:00:00Z", UpdatedVersion: "2026-06-01T00:00:00Z"},
		},
	}
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "RELEASE.2026-06-01T00-00-00Z",
		Label: "RELEASE.2026-06-01T00-00-00Z",
		Artifacts: []domain.BuckitArtifact{
			{Kind: "binary", OS: "linux", Arch: "amd64", URL: "https://example.test/buckit-linux-amd64"},
		},
	}})
	defer restoreVersions()
	id, code := dispatch(t, h, tasks.OpClusterUpgradeByAdminUpdate, nil, map[string]any{
		"version": "RELEASE.2026-06-01T00-00-00Z",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("cluster_upgrade_by_admin_update: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "permission denied") {
		t.Fatalf("expected server-reported error, got %q", row.FailureNote)
	}
	if row.Result == nil || len(row.Result.HostStatuses) != 2 {
		t.Fatalf("expected 2 host statuses, got %+v", row.Result)
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
	_, code := dispatch(t, h, tasks.OpClusterUpgradeBySystemctl, nil, nil)
	if code != 400 {
		t.Fatalf("cluster_upgrade_by_systemctl against MinIO: want 400, got %d", code)
	}
	_, code = dispatch(t, h, tasks.OpClusterUpgradeByAdminUpdate, nil, nil)
	if code != 400 {
		t.Fatalf("cluster_upgrade_by_admin_update against MinIO: want 400, got %d", code)
	}
}

func TestM7RotateRootCredsStub(t *testing.T) {
	h := newM7Harness(t)
	_, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, nil)
	if code != 400 {
		t.Fatalf("rotate_root_creds stub: want 400, got %d", code)
	}
}

func TestM7ClusterUpgradeBySystemctlUsesArm64RPM(t *testing.T) {
	h := newM7Harness(t)
	artifactLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	artifactSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/buckit.sha256":
			_, _ = w.Write([]byte(strings.Join([]string{
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  buckit-amd64.rpm",
				"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  buckit-arm64.rpm",
			}, "\n")))
		default:
			_, _ = w.Write([]byte("rpm"))
		}
	}))
	artifactSrv.Listener = artifactLn
	artifactSrv.Start()
	defer artifactSrv.Close()
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:         "v1.0.0",
		Label:       "v1.0.0",
		RpmURL:      artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLAmd64: artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLArm64: artifactSrv.URL + "/buckit-arm64.rpm",
		SHA256URL:   artifactSrv.URL + "/buckit.sha256",
	}})
	defer restoreVersions()

	var downloaded []string
	var verified bool
	var restartedBySystemctl bool
	h.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		switch {
		case cmd == "uname -m":
			return "aarch64\n", "", 0, true
		case strings.HasPrefix(cmd, "curl -fSL -o /tmp/buckit.rpm "):
			downloaded = append(downloaded, strings.TrimPrefix(cmd, "curl -fSL -o /tmp/buckit.rpm "))
			return "", "", 0, true
		case strings.Contains(cmd, "sha256sum -c -") || strings.Contains(cmd, "shasum -a 256 -c -"):
			verified = true
			return "", "", 0, true
		case strings.Contains(cmd, "systemctl restart"):
			restartedBySystemctl = true
			return "", "", 0, true
		default:
			return "", "", 0, false
		}
	}

	id, code := dispatch(t, h, tasks.OpClusterUpgradeBySystemctl, nil, map[string]any{
		"version": "v1.0.0",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("cluster_upgrade_by_systemctl: %s (%s)", row.Status, row.FailureNote)
	}
	if len(downloaded) == 0 {
		t.Fatal("expected at least one download URL")
	}
	for _, url := range downloaded {
		if strings.Trim(url, "'") != artifactSrv.URL+"/buckit-arm64.rpm" {
			t.Fatalf("want arm64 rpm URL, got %q", url)
		}
	}
	if !verified {
		t.Fatal("expected checksum verification command")
	}
	if restartedBySystemctl {
		t.Fatal("did not expect per-host systemctl restart during cluster upgrade")
	}
	if h.calls.serviceRestart.Load() != 1 {
		t.Fatalf("expected 1 admin restart call, got %d", h.calls.serviceRestart.Load())
	}
}
