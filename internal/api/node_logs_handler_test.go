package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

func newNodeLogsHarness(t *testing.T) (*httptest.Server, *clusters.Repo, *nodes.Repo, *sshconfig.Repo) {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	clustersRepo := clusters.New(st)
	nodesRepo := nodes.New(st)
	sshRepo := sshconfig.New(st)
	clusterAdminRepo := clusteradmin.New(st)
	adminPool := admin.NewPool()
	mgr := tasks.NewManager(st)
	t.Cleanup(mgr.Shutdown)

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		Clusters:     clustersRepo,
		SSHConfig:    sshRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
		// SSHPool intentionally left nil — these tests exercise validation
		// paths that short-circuit before a dial would be attempted.
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, clustersRepo, nodesRepo, sshRepo
}

func TestNodeLogs_NotFound(t *testing.T) {
	ts, _, _, _ := newNodeLogsHarness(t)
	resp, err := http.Get(ts.URL + "/api/v1/clusters/missing/nodes/n1/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for missing cluster, got %d", resp.StatusCode)
	}
}

func TestNodeLogs_BadSince(t *testing.T) {
	ts, clustersRepo, nodesRepo, _ := newNodeLogsHarness(t)
	ctx := context.Background()
	_ = clustersRepo.Put(ctx, domain.Cluster{ID: "c1", Name: "c1", Engine: domain.EngineBuckit})
	_ = nodesRepo.Put(ctx, domain.Node{ID: "n1", ClusterID: "c1", Hostname: "node1", SSHPort: 22})

	resp, err := http.Get(ts.URL + "/api/v1/clusters/c1/nodes/n1/logs?since=99x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown since, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "bad_request" {
		t.Fatalf("want bad_request error, got %v", body)
	}
}

func TestNodeLogs_BadUnit(t *testing.T) {
	ts, clustersRepo, nodesRepo, _ := newNodeLogsHarness(t)
	ctx := context.Background()
	_ = clustersRepo.Put(ctx, domain.Cluster{ID: "c1", Name: "c1", Engine: domain.EngineBuckit})
	_ = nodesRepo.Put(ctx, domain.Node{ID: "n1", ClusterID: "c1", Hostname: "node1", SSHPort: 22})

	resp, err := http.Get(ts.URL + "/api/v1/clusters/c1/nodes/n1/logs?unit=sshd.service")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for disallowed unit, got %d", resp.StatusCode)
	}
}

func TestNodeLogs_NoSshConfig(t *testing.T) {
	ts, clustersRepo, nodesRepo, _ := newNodeLogsHarness(t)
	ctx := context.Background()
	_ = clustersRepo.Put(ctx, domain.Cluster{ID: "c1", Name: "c1", Engine: domain.EngineBuckit})
	_ = nodesRepo.Put(ctx, domain.Node{ID: "n1", ClusterID: "c1", Hostname: "node1", SSHPort: 22})

	resp, err := http.Get(ts.URL + "/api/v1/clusters/c1/nodes/n1/logs?since=1h")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("want 424 when ssh isn't configured, got %d", resp.StatusCode)
	}
}

func TestBuildJournalctlCmd(t *testing.T) {
	// Non-root user, sudo disabled → no sudo prefix.
	cmd := buildJournalctlCmd("buckit.service", 1, 2000, bmssh.Resolved{User: "ops"})
	if !contains(cmd, "journalctl -u buckit.service") || !contains(cmd, "--lines=2000") {
		t.Fatalf("unexpected cmd: %s", cmd)
	}
	if contains(cmd, "sudo") {
		t.Fatalf("unexpected sudo prefix when sudo=false: %s", cmd)
	}

	// Root user — never sudo, even if Sudo flag is true.
	rootCmd := buildJournalctlCmd("buckit.service", 1, 100, bmssh.Resolved{User: "root", Sudo: true})
	if contains(rootCmd, "sudo") {
		t.Fatalf("unexpected sudo prefix for root user: %s", rootCmd)
	}

	// Non-root + sudo enabled → wrapped via SudoWrap.
	sudoCmd := buildJournalctlCmd("buckit.service", 1, 100, bmssh.Resolved{User: "ops", Sudo: true})
	if !startsWith(sudoCmd, "sudo -n bash -c") {
		t.Fatalf("missing sudo wrap for non-root sudo creds: %s", sudoCmd)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || hasInside(s, sub)))
}

func hasInside(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
