package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/operations"
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
	_, code = dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 400 {
		t.Fatalf("rotate_root_creds against MinIO: want 400, got %d", code)
	}
}

func TestM7RotateRootCredsValidation(t *testing.T) {
	h := newM7Harness(t)
	cases := []struct {
		name     string
		password string
	}{
		{name: "too short", password: "short"},
		{name: "too long", password: strings.Repeat("a", 41)},
		{name: "contains space", password: "bad pass123"},
		{name: "contains newline", password: "bad\npass123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
				"newPassword": tc.password,
			})
			if code != 400 {
				t.Fatalf("rotate_root_creds validation: want 400, got %d", code)
			}
		})
	}
}

func TestM7RotateRootCredsUsesAdminRestartWhenConfigEnvExists(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio":    "MINIO_CONFIG_ENV_FILE=\"/etc/minio/config.env\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
		"/etc/minio/config.env": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\n",
	}
	var adminRestarts int
	var systemctlRestarts int
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "systemctl restart") {
			systemctlRestarts++
			return "", "", 0, true
		}
		return "", "", 0, false
	})
	originalRestart := h.calls.serviceRestart.Load()
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("rotate_root_creds: %s (%s)", row.Status, row.FailureNote)
	}
	adminRestarts = int(h.calls.serviceRestart.Load() - originalRestart)
	if adminRestarts != 1 {
		t.Fatalf("expected 1 admin restart, got %d", adminRestarts)
	}
	if systemctlRestarts != 0 {
		t.Fatalf("expected 0 systemctl restarts, got %d", systemctlRestarts)
	}
	if files["/etc/default/minio"] != "MINIO_CONFIG_ENV_FILE=\"/etc/minio/config.env\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n" {
		t.Fatalf("expected primary env file unchanged, got %q", files["/etc/default/minio"])
	}
	if got := files["/etc/minio/config.env"]; !strings.Contains(got, `MINIO_ROOT_USER="ak"`) || !strings.Contains(got, `MINIO_ROOT_PASSWORD="newpassword123"`) {
		t.Fatalf("expected updated root creds in config env, got %q", got)
	}
	creds, err := h.admin.Get(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKey != "ak" || creds.SecretKey != "newpassword123" {
		t.Fatalf("expected persisted new password, got %+v", creds)
	}
}

func TestM7RotateRootCredsMigratesPrimaryEnvAndUsesSystemctl(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
	}
	var systemctlRestarts int
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "systemctl restart") {
			systemctlRestarts++
			return "", "", 0, true
		}
		return "", "", 0, false
	})
	originalRestart := h.calls.serviceRestart.Load()
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("rotate_root_creds: %s (%s)", row.Status, row.FailureNote)
	}
	if int(h.calls.serviceRestart.Load()-originalRestart) != 1 {
		t.Fatalf("expected 1 admin restart after normalization, got %d", h.calls.serviceRestart.Load()-originalRestart)
	}
	if systemctlRestarts != 2 {
		t.Fatalf("expected 2 rolling systemctl restarts, got %d", systemctlRestarts)
	}
	gotPrimary := files["/etc/default/minio"]
	if !strings.Contains(gotPrimary, `MINIO_CONFIG_ENV_FILE="/etc/minio/config.env"`) {
		t.Fatalf("expected MINIO_CONFIG_ENV_FILE in primary env, got %q", gotPrimary)
	}
	if strings.Contains(gotPrimary, "MINIO_ROOT_PASSWORD") || strings.Contains(gotPrimary, "MINIO_ROOT_USER") {
		t.Fatalf("expected primary env root creds removed, got %q", gotPrimary)
	}
	gotSecondary := files["/etc/minio/config.env"]
	if !strings.Contains(gotSecondary, `MINIO_ROOT_USER="ak"`) || !strings.Contains(gotSecondary, `MINIO_ROOT_PASSWORD="newpassword123"`) {
		t.Fatalf("expected migrated creds in secondary env, got %q", gotSecondary)
	}
}

func TestM7RotateRootCredsLegacyNodesIgnoreStaleSecondaryFile(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio":    "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
		"/etc/minio/config.env": "MINIO_ROOT_USER=\"stale\"\nMINIO_ROOT_PASSWORD=\"stalepass123\"\n",
	}
	h.sshSrv.CmdOverride = envRotationOverride(files, nil)
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("rotate_root_creds: %s (%s)", row.Status, row.FailureNote)
	}
	gotSecondary := files["/etc/minio/config.env"]
	if !strings.Contains(gotSecondary, `MINIO_ROOT_USER="ak"`) || !strings.Contains(gotSecondary, `MINIO_ROOT_PASSWORD="newpassword123"`) {
		t.Fatalf("expected primary env creds to win over stale secondary file, got %q", gotSecondary)
	}
}

func TestM7RotateRootCredsPersistsNewPasswordAfterRestartAcceptedWhenHealthFails(t *testing.T) {
	h := newM7Harness(t)
	h.unhealthyAfterRestart = true
	restoreWait := operations.SetRotateRootCredsPostRestartWaitForTest(operations.WaitOptions{
		Timeout: 50 * time.Millisecond,
		Tick:    5 * time.Millisecond,
	})
	defer restoreWait()
	files := map[string]string{
		"/etc/default/minio":    "MINIO_CONFIG_ENV_FILE=\"/etc/minio/config.env\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
		"/etc/minio/config.env": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\n",
	}
	h.sshSrv.CmdOverride = envRotationOverride(files, nil)
	originalRestart := h.calls.serviceRestart.Load()
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds post-restart health: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "post-rotation health") {
		t.Fatalf("expected post-rotation health failure, got %q", row.FailureNote)
	}
	if !strings.Contains(row.FailureNote, "Restart cluster") || !strings.Contains(row.FailureNote, "Rolling restart") {
		t.Fatalf("expected recovery guidance in failure note, got %q", row.FailureNote)
	}
	if int(h.calls.serviceRestart.Load()-originalRestart) != 1 {
		t.Fatalf("expected 1 admin restart, got %d", h.calls.serviceRestart.Load()-originalRestart)
	}
	creds, err := h.admin.Get(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if creds.SecretKey != "newpassword123" {
		t.Fatalf("expected new password persisted after restart acceptance, got %+v", creds)
	}
}

func TestM7RotateRootCredsServiceRestartFailureAdvisesManualRestart(t *testing.T) {
	h := newM7Harness(t)
	h.failServiceRestart = true
	restoreTimeout := operations.SetRotateRootCredsRestartRequestTimeoutForTest(50 * time.Millisecond)
	defer restoreTimeout()
	files := map[string]string{
		"/etc/default/minio":    "MINIO_CONFIG_ENV_FILE=\"/etc/minio/config.env\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
		"/etc/minio/config.env": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\n",
	}
	h.sshSrv.CmdOverride = envRotationOverride(files, nil)
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds restart failure: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "admin restart") {
		t.Fatalf("expected admin restart failure, got %q", row.FailureNote)
	}
	if !strings.Contains(row.FailureNote, "Restart cluster") || !strings.Contains(row.FailureNote, "Rolling restart") {
		t.Fatalf("expected recovery guidance in failure note, got %q", row.FailureNote)
	}
	creds, err := h.admin.Get(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if creds.SecretKey != "sk" {
		t.Fatalf("expected stored password unchanged when restart request fails, got %+v", creds)
	}
}

func TestM7RotateRootCredsRejectsUnhealthyCluster(t *testing.T) {
	h := newM7Harness(t)
	h.info.Servers[1].State = "offline"
	files := map[string]string{
		"/etc/default/minio":    "MINIO_CONFIG_ENV_FILE=\"/etc/minio/config.env\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
		"/etc/minio/config.env": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\n",
	}
	var writeCount int
	var systemctlRestarts int
	originalRestart := h.calls.serviceRestart.Load()
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "systemctl restart") {
			systemctlRestarts++
			return "", "", 0, true
		}
		if strings.Contains(inner, "&& tee ") || strings.HasPrefix(inner, "tee ") {
			writeCount++
		}
		return "", "", 0, false
	})
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds unhealthy preflight: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "cluster must be healthy before rotating root password") {
		t.Fatalf("expected unhealthy-cluster failure note, got %q", row.FailureNote)
	}
	if writeCount != 0 {
		t.Fatalf("expected no env file writes on unhealthy cluster, got %d", writeCount)
	}
	if systemctlRestarts != 0 {
		t.Fatalf("expected no systemctl restarts on unhealthy cluster, got %d", systemctlRestarts)
	}
	if int(h.calls.serviceRestart.Load()-originalRestart) != 0 {
		t.Fatalf("expected no admin restart on unhealthy cluster, got %d", h.calls.serviceRestart.Load()-originalRestart)
	}
	if got := files["/etc/minio/config.env"]; !strings.Contains(got, `MINIO_ROOT_PASSWORD="sk"`) {
		t.Fatalf("expected env files unchanged on unhealthy cluster, got %q", got)
	}
}

func TestM7RotateRootCredsRollsBackWriteFailure(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
	}
	originalPrimary := files["/etc/default/minio"]
	_, hadOriginalSecondary := files["/etc/minio/config.env"]
	var teeWrites int
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "&& tee ") || strings.HasPrefix(inner, "tee ") {
			teeWrites++
			if teeWrites == 3 {
				return "", "disk full", 1, true
			}
		}
		return "", "", 0, false
	})
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds rollback: want failed, got %s", row.Status)
	}
	if files["/etc/default/minio"] != originalPrimary {
		t.Fatalf("expected primary env rollback, got %q", files["/etc/default/minio"])
	}
	if _, ok := files["/etc/minio/config.env"]; ok != hadOriginalSecondary {
		t.Fatalf("expected secondary env rollback, got %q", files["/etc/minio/config.env"])
	}
	creds, err := h.admin.Get(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if creds.SecretKey != "sk" {
		t.Fatalf("expected persisted creds unchanged on failure, got %+v", creds)
	}
}

func TestM7RotateRootCredsPass2RollbackPreservesNormalizedBaseline(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
	}
	var teeWrites int
	var systemctlRestarts int
	originalRestart := h.calls.serviceRestart.Load()
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "systemctl restart") {
			systemctlRestarts++
			return "", "", 0, true
		}
		if strings.Contains(inner, "&& tee ") || strings.HasPrefix(inner, "tee ") {
			teeWrites++
			if teeWrites == 5 {
				return "", "disk full", 1, true
			}
		}
		return "", "", 0, false
	})
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds pass2 rollback: want failed, got %s", row.Status)
	}
	if row.Result == nil || len(row.Result.HostStatuses) != 2 {
		t.Fatalf("expected 2 host statuses, got %+v", row.Result)
	}
	for _, hs := range row.Result.HostStatuses {
		if hs.State == tasks.HostPending {
			t.Fatalf("did not expect pending host status after terminal failure: %+v", hs)
		}
	}
	if systemctlRestarts != 2 {
		t.Fatalf("expected pass1 rolling restarts before pass2 failure, got %d", systemctlRestarts)
	}
	if int(h.calls.serviceRestart.Load()-originalRestart) != 0 {
		t.Fatalf("did not expect admin restart when pass2 write fails, got %d", h.calls.serviceRestart.Load()-originalRestart)
	}
	gotPrimary := files["/etc/default/minio"]
	if !strings.Contains(gotPrimary, `MINIO_CONFIG_ENV_FILE="/etc/minio/config.env"`) {
		t.Fatalf("expected normalized primary env after rollback, got %q", gotPrimary)
	}
	if strings.Contains(gotPrimary, "MINIO_ROOT_PASSWORD") || strings.Contains(gotPrimary, "MINIO_ROOT_USER") {
		t.Fatalf("expected inline creds to stay removed after pass2 rollback, got %q", gotPrimary)
	}
	gotSecondary := files["/etc/minio/config.env"]
	if !strings.Contains(gotSecondary, `MINIO_ROOT_USER="ak"`) || !strings.Contains(gotSecondary, `MINIO_ROOT_PASSWORD="sk"`) {
		t.Fatalf("expected rollback to restore normalized old password, got %q", gotSecondary)
	}
	creds, err := h.admin.Get(context.Background(), "test-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if creds.SecretKey != "sk" {
		t.Fatalf("expected persisted creds unchanged on pass2 failure, got %+v", creds)
	}
}

func TestM7RotateRootCredsPass1RestartFailureRollsBackAndRestarts(t *testing.T) {
	h := newM7Harness(t)
	files := map[string]string{
		"/etc/default/minio": "MINIO_ROOT_USER=\"ak\"\nMINIO_ROOT_PASSWORD=\"sk\"\nMINIO_VOLUMES=\"https://node{1...2}/data\"\n",
	}
	originalPrimary := files["/etc/default/minio"]
	var systemctlRestarts int
	h.sshSrv.CmdOverride = envRotationOverride(files, func(inner string) (string, string, int, bool) {
		if strings.Contains(inner, "systemctl restart") {
			systemctlRestarts++
			if systemctlRestarts == 2 {
				return "", "boom", 1, true
			}
			return "", "", 0, true
		}
		return "", "", 0, false
	})
	id, code := dispatch(t, h, tasks.OpRotateRootCreds, nil, map[string]any{
		"newPassword": "newpassword123",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 10*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("rotate_root_creds pass1 restart rollback: want failed, got %s", row.Status)
	}
	if systemctlRestarts != 4 {
		t.Fatalf("expected rollback restart after pass1 failure, got %d restarts", systemctlRestarts)
	}
	if files["/etc/default/minio"] != originalPrimary {
		t.Fatalf("expected primary env restored after pass1 restart failure, got %q", files["/etc/default/minio"])
	}
	if _, ok := files["/etc/minio/config.env"]; ok {
		t.Fatalf("expected secondary env removed after pass1 restart rollback, got %q", files["/etc/minio/config.env"])
	}
}

func envRotationOverride(
	files map[string]string,
	extra func(inner string) (stdout, stderr string, exit int, ok bool),
) func(string) (string, string, int, bool) {
	return func(cmd string) (string, string, int, bool) {
		inner := unwrapSudoBash(cmd)
		if extra != nil {
			if stdout, stderr, exit, ok := extra(inner); ok {
				return stdout, stderr, exit, true
			}
		}
		switch {
		case strings.HasPrefix(inner, "if [ -f "):
			p := parseProbePath(inner)
			if p == "" {
				return "", "bad file probe", 1, true
			}
			if v, ok := files[p]; ok {
				return "__BM_EXISTS__\n" + v, "", 0, true
			}
			return "__BM_MISSING__\n", "", 0, true
		case strings.HasPrefix(inner, "cp "):
			src, dst, ok := parseCopy(inner)
			if !ok {
				return "", "bad cp command", 1, true
			}
			v, exists := files[src]
			if !exists {
				return "", fmt.Sprintf("missing %s", src), 1, true
			}
			files[dst] = v
			return "", "", 0, true
		case strings.HasPrefix(inner, "rm -f "):
			p := strings.TrimSpace(strings.TrimPrefix(inner, "rm -f "))
			files[unquoteShell(p)] = ""
			delete(files, unquoteShell(p))
			return "", "", 0, true
		case strings.HasPrefix(inner, "systemctl show "):
			return "User=buckit\nGroup=buckit\n", "", 0, true
		case strings.HasPrefix(inner, "chown ") || strings.HasPrefix(inner, "chmod "):
			return "", "", 0, true
		case strings.Contains(inner, "&& tee ") || strings.HasPrefix(inner, "tee "):
			p, body, ok := parseTeeWrite(inner)
			if !ok {
				return "", "bad tee command", 1, true
			}
			files[p] = body
			return "", "", 0, true
		case strings.HasPrefix(inner, "curl -fsS"):
			return "", "", 0, true
		default:
			return "", "", 0, false
		}
	}
}

func unwrapSudoBash(cmd string) string {
	const prefix = "sudo -n bash -c '"
	if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, "'") {
		return cmd
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(cmd, prefix), "'")
	return strings.ReplaceAll(inner, `'\''`, `'`)
}

func parseProbePath(cmd string) string {
	const prefix = "if [ -f "
	start := strings.TrimPrefix(cmd, prefix)
	idx := strings.Index(start, " ]; then")
	if idx < 0 {
		return ""
	}
	return unquoteShell(strings.TrimSpace(start[:idx]))
}

func parseCopy(cmd string) (string, string, bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(cmd, "cp "))
	parts := strings.Fields(rest)
	if len(parts) != 2 {
		return "", "", false
	}
	return unquoteShell(parts[0]), unquoteShell(parts[1]), true
}

func parseTeeWrite(cmd string) (string, string, bool) {
	const marker = " > /dev/null <<'BMCFG'\n"
	idx := strings.Index(cmd, marker)
	if idx < 0 {
		return "", "", false
	}
	head := cmd[:idx]
	teeIdx := strings.LastIndex(head, "tee ")
	if teeIdx < 0 {
		return "", "", false
	}
	p := unquoteShell(strings.TrimSpace(head[teeIdx+len("tee "):]))
	rest := cmd[idx+len(marker):]
	end := strings.Index(rest, "\nBMCFG\n")
	if end < 0 {
		return "", "", false
	}
	return p, rest[:end] + "\n", true
}

func unquoteShell(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		return strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
	}
	return v
}

func TestM7ClusterUpgradeBySystemctlUsesArm64RPM(t *testing.T) {
	h := newM7Harness(t)
	h.restartVersion = "RELEASE.2026-06-01T00-00-00Z"
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
		case strings.Contains(cmd, "rpm -q --qf") && strings.Contains(cmd, "rpm -qp --qf"):
			return "installed=0:20260501000000.0.0-1.aarch64\ncandidate=0:20260601000000.0.0-1.aarch64\ncmp=-1\n", "", 0, true
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

func TestM7ClusterUpgradeBySystemctlFailsIfVersionUnchanged(t *testing.T) {
	restoreVersionWait := operations.SetClusterUpgradePostRestartWaitForTest(operations.WaitOptions{
		Timeout: 100 * time.Millisecond,
		Tick:    time.Millisecond,
	})
	defer restoreVersionWait()

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
		Tag:         "RELEASE.2026-06-01T00-00-00Z",
		Label:       "RELEASE.2026-06-01T00-00-00Z",
		RpmURL:      artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLAmd64: artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLArm64: artifactSrv.URL + "/buckit-arm64.rpm",
		SHA256URL:   artifactSrv.URL + "/buckit.sha256",
	}})
	defer restoreVersions()

	h.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		switch {
		case cmd == "uname -m":
			return "aarch64\n", "", 0, true
		case strings.HasPrefix(cmd, "curl -fSL -o /tmp/buckit.rpm "):
			return "", "", 0, true
		case strings.Contains(cmd, "sha256sum -c -") || strings.Contains(cmd, "shasum -a 256 -c -"):
			return "", "", 0, true
		case strings.Contains(cmd, "rpm -q --qf") && strings.Contains(cmd, "rpm -qp --qf"):
			return "installed=0:20260501000000.0.0-1.aarch64\ncandidate=0:20260601000000.0.0-1.aarch64\ncmp=-1\n", "", 0, true
		default:
			return "", "", 0, false
		}
	}

	id, code := dispatch(t, h, tasks.OpClusterUpgradeBySystemctl, nil, map[string]any{
		"version": "RELEASE.2026-06-01T00-00-00Z",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateFailed {
		t.Fatalf("cluster_upgrade_by_systemctl: want failed, got %s", row.Status)
	}
	if !strings.Contains(row.FailureNote, "still reports") {
		t.Fatalf("expected unchanged-version failure note, got %q", row.FailureNote)
	}
}

func TestM7ClusterUpgradeBySystemctlReinstallsSameInstalledRPM(t *testing.T) {
	h := newM7Harness(t)
	h.restartVersion = "RELEASE.2026-05-22T02-20-39Z"
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
		Tag:         "RELEASE.2026-05-22T02-20-39Z",
		Label:       "RELEASE.2026-05-22T02-20-39Z",
		RpmURL:      artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLAmd64: artifactSrv.URL + "/buckit-amd64.rpm",
		RpmURLArm64: artifactSrv.URL + "/buckit-arm64.rpm",
		SHA256URL:   artifactSrv.URL + "/buckit.sha256",
	}})
	defer restoreVersions()

	var usedUpgrade bool
	var usedReinstall bool
	h.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		switch {
		case cmd == "uname -m":
			return "aarch64\n", "", 0, true
		case strings.HasPrefix(cmd, "curl -fSL -o /tmp/buckit.rpm "):
			return "", "", 0, true
		case strings.Contains(cmd, "sha256sum -c -") || strings.Contains(cmd, "shasum -a 256 -c -"):
			return "", "", 0, true
		case strings.Contains(cmd, "rpm -q --qf") && strings.Contains(cmd, "rpm -qp --qf"):
			return "installed=0:20260522022039.0.0-1.aarch64\ncandidate=0:20260522022039.0.0-1.aarch64\ncmp=0\n", "", 0, true
		case strings.Contains(cmd, "dnf upgrade -y /tmp/buckit.rpm"):
			usedUpgrade = true
			return "", "", 0, true
		case strings.Contains(cmd, "dnf reinstall -y /tmp/buckit.rpm"):
			usedReinstall = true
			return "", "", 0, true
		default:
			return "", "", 0, false
		}
	}

	id, code := dispatch(t, h, tasks.OpClusterUpgradeBySystemctl, nil, map[string]any{
		"version": "RELEASE.2026-05-22T02-20-39Z",
	})
	if code != 202 {
		t.Fatalf("dispatch: %d", code)
	}
	row := waitTermM7(t, h, id, 30*time.Second)
	if row.Status != tasks.StateSucceeded {
		t.Fatalf("cluster_upgrade_by_systemctl: %s (%s)", row.Status, row.FailureNote)
	}
	if usedUpgrade {
		t.Fatal("did not expect dnf upgrade for same installed rpm identity")
	}
	if !usedReinstall {
		t.Fatal("expected dnf reinstall for same installed rpm identity")
	}
}

// TestM7ClusterUpgradeBySystemctlOnDeb mirrors the RPM upgrade flow on
// a Debian/Ubuntu host. The probe returns apt-get only, the inspect
// script uses dpkg-query / dpkg-deb / dpkg --compare-versions, and the
// install verb is apt-get install -y /tmp/buckit.deb.
func TestM7ClusterUpgradeBySystemctlOnDeb(t *testing.T) {
	h := newM7Harness(t)
	h.restartVersion = "RELEASE.2026-06-01T00-00-00Z"
	artifactLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	artifactSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/buckit.sha256":
			_, _ = w.Write([]byte("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  buckit-arm64.deb\n"))
		default:
			_, _ = w.Write([]byte("deb"))
		}
	}))
	artifactSrv.Listener = artifactLn
	artifactSrv.Start()
	defer artifactSrv.Close()
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "v1.0.0",
		Label: "v1.0.0",
		Artifacts: []domain.BuckitArtifact{
			{Kind: "deb", OS: "linux", Arch: "arm64", URL: artifactSrv.URL + "/buckit-arm64.deb", SHA256URL: artifactSrv.URL + "/buckit.sha256"},
		},
		DebURL: artifactSrv.URL + "/buckit-arm64.deb",
	}})
	defer restoreVersions()

	var downloaded []string
	var aptInstalled bool
	h.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		switch {
		case cmd == "uname -m":
			return "aarch64\n", "", 0, true
		case strings.Contains(cmd, "command -v dnf") && strings.Contains(cmd, "command -v apt-get"):
			// Combined probe: only apt-get is on this host.
			return "\n\n/usr/bin/apt-get\n", "", 0, true
		case strings.HasPrefix(cmd, "curl -fSL -o /tmp/buckit.deb "):
			downloaded = append(downloaded, strings.TrimPrefix(cmd, "curl -fSL -o /tmp/buckit.deb "))
			return "", "", 0, true
		case strings.Contains(cmd, "sha256sum -c -") || strings.Contains(cmd, "shasum -a 256 -c -"):
			return "", "", 0, true
		case strings.Contains(cmd, "dpkg-query -W") && strings.Contains(cmd, "dpkg-deb -f"):
			return "installed=0.1.0-1\ncandidate=0.2.0-1\ncmp=-1\n", "", 0, true
		case strings.Contains(cmd, "apt-get install -y /tmp/buckit.deb"):
			aptInstalled = true
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
		t.Fatalf("cluster_upgrade_by_systemctl on deb: %s (%s)", row.Status, row.FailureNote)
	}
	if len(downloaded) == 0 {
		t.Fatal("expected at least one .deb download URL")
	}
	for _, url := range downloaded {
		if strings.Trim(url, "'") != artifactSrv.URL+"/buckit-arm64.deb" {
			t.Fatalf("want arm64 deb URL, got %q", url)
		}
	}
	if !aptInstalled {
		t.Fatal("expected apt-get install -y /tmp/buckit.deb to run")
	}
	if h.calls.serviceRestart.Load() != 1 {
		t.Fatalf("expected 1 admin restart call, got %d", h.calls.serviceRestart.Load())
	}
}

// TestM7SkipsHostsMissingUnit asserts the per-host skip path: if
// `systemctl show -p LoadState` reports anything but "loaded" for every
// host, the three cluster-wide executors fail with a clean message
// instead of attempting any `systemctl restart`. The three ops share
// one harness so we don't multiply test-fixture cost (which otherwise
// pushes the wider package over wait timeouts under -race).
func TestM7SkipsHostsMissingUnit(t *testing.T) {
	h := newM7Harness(t)
	h.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		if strings.Contains(cmd, "systemctl show -p LoadState") {
			return "not-found", "", 0, true
		}
		return "", "", 0, false
	}

	cases := []struct {
		name   string
		kind   tasks.OpKind
		params any
	}{
		{"rolling_restart", tasks.OpRollingRestart, nil},
		{"start_cluster", tasks.OpStartCluster, nil},
		{"rotate_root_creds", tasks.OpRotateRootCreds, map[string]any{"newPassword": "newpassword123"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The per-cluster lock can linger for a few ms after the
			// previous subtest's task is marked terminal in history.
			// Retry briefly on 409 so a stale lock from the prior
			// subtest doesn't fail this one.
			var id string
			var code int
			deadline := time.Now().Add(2 * time.Second)
			for {
				id, code = dispatch(t, h, c.kind, nil, c.params)
				if code != http.StatusConflict || time.Now().After(deadline) {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if code != 202 {
				t.Fatalf("dispatch: %d", code)
			}
			row := waitTermM7(t, h, id, 30*time.Second)
			if row.Status != tasks.StateFailed {
				t.Fatalf("expected failed, got %s (note=%q)", row.Status, row.FailureNote)
			}
			if !strings.Contains(row.FailureNote, "no hosts have buckit.service installed") {
				t.Fatalf("expected friendly precondition error, got %q", row.FailureNote)
			}
			if row.Result == nil || len(row.Result.HostStatuses) != 2 {
				t.Fatalf("expected 2 host statuses, got %+v", row.Result)
			}
			for _, hs := range row.Result.HostStatuses {
				if !strings.Contains(hs.Detail, "skipped") {
					t.Fatalf("expected per-host detail to mention skip, got %q", hs.Detail)
				}
			}
		})
	}
}
