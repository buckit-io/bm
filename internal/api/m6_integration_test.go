package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m6Harness struct {
	server   *httptest.Server
	store    *store.Store
	mgr      *tasks.Manager
	clusters *clusters.Repo
	nodes    *nodes.Repo
	sshSrv   *sshtest.Server
}

func newM6Harness(t *testing.T) *m6Harness {
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
	clusterAdminRepo := clusteradmin.New(st)
	nodesRepo := nodes.New(st)
	sshcfgRepo := sshconfig.New(st)
	mgr := tasks.NewManager(st)
	t.Cleanup(mgr.Shutdown)

	// Register deploy executor.
	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sshSrv.Stop)

	sshPool := bmssh.NewPool(nil)
	t.Cleanup(sshPool.Close)

	deploy.Register(&deploy.Executor{
		Installer:    deploy.NewInstaller(sshPool),
		Clusters:     clustersRepo,
		Nodes:        nodesRepo,
		ClusterAdmin: clusterAdminRepo,
	})

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		SSHConfig:    sshcfgRepo,
		SSHPool:      sshPool,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &m6Harness{server: ts, store: st, mgr: mgr, clusters: clustersRepo, nodes: nodesRepo, sshSrv: sshSrv}
}

func TestM6DeployHappyPath(t *testing.T) {
	// Speed up the healthy-probe loop.
	prev := deploy.NewInstaller // capture reference so we hold onto the package
	_ = prev

	h := newM6Harness(t)
	host, port := h.sshSrv.HostPort()

	draft := domain.NewClusterDraft{
		Name:    "test-deploy",
		Version: "v1.0.0",
		API:     domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:  "us-east-1",
		Credentials: domain.Credentials{
			RootUser: "rootuser", RootPassword: "supersecret",
		},
		Hosts: []domain.HostRow{
			{ID: "h1", Hostname: host, Port: port, Probe: domain.HostProbeReachable},
			{ID: "h2", Hostname: host, Port: port, Probe: domain.HostProbeReachable},
		},
		SSH:      domain.SshCreds{AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password()},
		Topology: domain.Topology{SetSize: 4, Parity: 2, SelectedMounts: []string{"/data/disk1", "/data/disk2"}},
	}
	body, _ := json.Marshal(draft)

	resp := do(t, h.server, http.MethodPost, "/api/v1/clusters/new/deploy", body)
	if resp.code != 202 {
		t.Fatalf("dispatch: %d %s", resp.code, resp.body)
	}
	var disp tasks.DispatchResponse
	_ = json.Unmarshal(resp.body, &disp)
	if disp.TaskID == "" {
		t.Fatal("no taskId returned")
	}

	// Wait for terminal — poll history.
	if !waitTerminal(t, h.server, disp.TaskID, 5*time.Second) {
		t.Fatalf("deploy did not terminate in time")
	}

	// Cluster should now be committed.
	list := do(t, h.server, http.MethodGet, "/api/v1/clusters", nil)
	var got []domain.Cluster
	_ = json.Unmarshal(list.body, &got)
	if len(got) != 1 || got[0].ID != "test-deploy" {
		t.Fatalf("expected cluster test-deploy, got %+v", got)
	}
	if got[0].Engine != domain.EngineBuckit {
		t.Fatalf("expected engine buckit, got %s", got[0].Engine)
	}

	// Nodes should be persisted.
	ns := do(t, h.server, http.MethodGet, "/api/v1/clusters/test-deploy/nodes", nil)
	var nodeRows []domain.Node
	_ = json.Unmarshal(ns.body, &nodeRows)
	if len(nodeRows) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodeRows))
	}
}

func TestM6DeploySlugCollision(t *testing.T) {
	h := newM6Harness(t)
	// Pre-seed a cluster with the slug the wizard would pick.
	if err := h.clusters.Put(context.Background(), domain.Cluster{ID: "test", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	draft := domain.NewClusterDraft{
		Name:        "test",
		Version:     "v1.0.0",
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Credentials: domain.Credentials{RootUser: "u", RootPassword: "p"},
		Hosts:       []domain.HostRow{{ID: "h1", Hostname: "node1", Port: 22, Probe: domain.HostProbeReachable}},
		SSH:         domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"},
		Topology:    domain.Topology{SetSize: 4, Parity: 2, SelectedMounts: []string{"/data/disk1"}},
	}
	body, _ := json.Marshal(draft)
	resp := do(t, h.server, http.MethodPost, "/api/v1/clusters/new/deploy", body)
	if resp.code != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", resp.code, resp.body)
	}
}

func TestM6DeployValidationFailures(t *testing.T) {
	h := newM6Harness(t)
	cases := []struct {
		name  string
		draft domain.NewClusterDraft
	}{
		{"no name", domain.NewClusterDraft{Version: "v1.0.0", Credentials: domain.Credentials{RootUser: "u", RootPassword: "p"}, Hosts: []domain.HostRow{{ID: "h1", Hostname: "x"}}, SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"}, Topology: domain.Topology{SelectedMounts: []string{"/data/d1"}}}},
		{"no creds", domain.NewClusterDraft{Name: "x", Version: "v1.0.0", Hosts: []domain.HostRow{{ID: "h1", Hostname: "x"}}, SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"}, Topology: domain.Topology{SelectedMounts: []string{"/data/d1"}}}},
		{"no hosts", domain.NewClusterDraft{Name: "x", Version: "v1.0.0", Credentials: domain.Credentials{RootUser: "u", RootPassword: "p"}, SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"}, Topology: domain.Topology{SelectedMounts: []string{"/data/d1"}}}},
		{"no mounts", domain.NewClusterDraft{Name: "x", Version: "v1.0.0", Credentials: domain.Credentials{RootUser: "u", RootPassword: "p"}, Hosts: []domain.HostRow{{ID: "h1", Hostname: "x"}}, SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"}}},
		{"bad version", domain.NewClusterDraft{Name: "x", Version: "nope", Credentials: domain.Credentials{RootUser: "u", RootPassword: "p"}, Hosts: []domain.HostRow{{ID: "h1", Hostname: "x"}}, SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"}, Topology: domain.Topology{SelectedMounts: []string{"/data/d1"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.draft)
			resp := do(t, h.server, http.MethodPost, "/api/v1/clusters/new/deploy", body)
			if resp.code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d %s", resp.code, resp.body)
			}
		})
	}
}

// waitTerminal polls /history for the given task id until its status is terminal.
func waitTerminal(t *testing.T, srv *httptest.Server, taskID string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp := do(t, srv, http.MethodGet, "/api/v1/history?limit=50", nil)
		var rows []tasks.HistoryEntry
		_ = json.Unmarshal(resp.body, &rows)
		for _, r := range rows {
			if r.ID == taskID && r.Status.IsTerminal() {
				if r.Status != tasks.StateSucceeded {
					t.Logf("task ended in %s: %s", r.Status, r.FailureNote)
					return false
				}
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// _ keeps these imports referenced so the test compiles even when one of the
// helpers is later trimmed.
var (
	_ = bufio.NewReader
	_ = io.EOF
	_ = strings.TrimSpace
)
