package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/migration"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m8Harness struct {
	server       *httptest.Server
	store        *store.Store
	mgr          *tasks.Manager
	clusters     *clusters.Repo
	clusterAdmin *clusteradmin.Repo
	sshSrv       *sshtest.Server
	adminSrv     *httptest.Server
	clusterID    string
	snapshotPath string
	hosts        []domain.HostRow
	sshPool      *bmssh.Pool
}

func newM8Harness(t *testing.T) *m8Harness {
	t.Helper()
	artifactLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	artifactSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/buckit.rpm.sha256":
			_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  buckit.rpm\n"))
		default:
			_, _ = w.Write([]byte("rpm"))
		}
	}))
	artifactSrv.Listener = artifactLn
	artifactSrv.Start()
	t.Cleanup(artifactSrv.Close)
	restoreVersions := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:       "v1.0.0",
		Label:     "v1.0.0",
		RpmURL:    artifactSrv.URL + "/buckit.rpm",
		SHA256URL: artifactSrv.URL + "/buckit.rpm.sha256",
	}})
	t.Cleanup(restoreVersions)
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

	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sshSrv.Stop)

	sshPool := bmssh.NewPool(nil)
	t.Cleanup(sshPool.Close)
	adminPool := admin.NewPool()

	infoBody := madmin.InfoMessage{
		Mode: "online",
		Servers: []madmin.ServerProperties{
			{Endpoint: "node1:9000", State: "online", Version: "RELEASE.2026-01-01T00-00-00Z"},
		},
	}
	adminSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/admin/v3/info-account"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(madmin.AccountInfo{AccountName: "admin"})
		case strings.Contains(r.URL.Path, "/admin/v3/info"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(infoBody)
		case strings.Contains(r.URL.Path, "/admin/v3/accountinfo"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(madmin.AccountInfo{AccountName: "admin"})
		case strings.Contains(r.URL.Path, "/admin/v3/groups"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]string{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(adminSrv.Close)

	clusterID := "test-cluster"
	now := time.Now().UTC()
	if err := clustersRepo.Put(context.Background(), domain.Cluster{
		ID:             clusterID,
		Name:           "test",
		Engine:         domain.EngineMinio,
		Version:        "RELEASE.2026-01-01T00-00-00Z",
		NodeCount:      1,
		PoolCount:      1,
		LastActivityAt: now,
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := clusterAdminRepo.Put(context.Background(), clusterID, domain.AdminCreds{
		URL:       adminSrv.URL,
		AccessKey: "ak",
		SecretKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	host, port := sshSrv.HostPort()
	hosts := []domain.HostRow{
		{ID: "h1", Hostname: host, Port: port, Probe: domain.HostProbeReachable},
	}
	_ = nodesRepo.Put(context.Background(), domain.Node{
		ID: "h1", ClusterID: clusterID, Hostname: host, SSHPort: port,
		State: domain.NodeOnline, Pool: 1,
	})

	// Pre-write a snapshot file so the cutover dispatch's Validate passes.
	snapDir := filepath.Join(dir, "snapshots")
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snap := &domain.MinioSnapshot{
		CapturedAt: now,
		ClusterID:  clusterID,
		Version:    "RELEASE.2026-01-01T00-00-00Z",
		Buckets:    []domain.BucketSnapshot{{Name: "alpha"}},
	}
	body, _ := json.MarshalIndent(snap, "", "  ")
	snapshotPath := filepath.Join(snapDir, clusterID+"-test.json")
	if err := os.WriteFile(snapshotPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	// Wire the migration executors against the live tasks manager.
	migration.Register(migration.Deps{
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
		SSHPool:      sshPool,
	})

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		SSHConfig:    sshcfgRepo,
		SSHPool:      sshPool,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return &m8Harness{
		server:       ts,
		store:        st,
		mgr:          mgr,
		clusters:     clustersRepo,
		clusterAdmin: clusterAdminRepo,
		sshSrv:       sshSrv,
		adminSrv:     adminSrv,
		clusterID:    clusterID,
		snapshotPath: snapshotPath,
		hosts:        hosts,
		sshPool:      sshPool,
	}
}

func (h *m8Harness) cutoverBody() migration.MigrationBody {
	return migration.MigrationBody{
		SourceClusterID: h.clusterID,
		Name:            "test",
		TargetVersion:   "v1.0.0",
		API:             domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:          "us-east-1",
		Hosts:           h.hosts,
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthPassword,
			User:       h.sshSrv.User(),
			Password:   h.sshSrv.Password(),
			Sudo:       true,
		},
		SnapshotPath: h.snapshotPath,
	}
}

// TestM8CutoverDispatch hits POST /clusters/:id/migrate/cutover, polls the
// task to terminal, and asserts the cluster row's Engine flipped.
func TestM8CutoverDispatch(t *testing.T) {
	h := newM8Harness(t)

	body, _ := json.Marshal(h.cutoverBody())
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/"+h.clusterID+"/migrate/cutover", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: want 202, got %d", resp.StatusCode)
	}
	var dispatch tasks.DispatchResponse
	_ = json.NewDecoder(resp.Body).Decode(&dispatch)
	if dispatch.TaskID == "" {
		t.Fatal("empty taskId")
	}

	if !waitForTerminalStatus(t, h, dispatch.TaskID, 15*time.Second) {
		t.Fatal("cutover did not reach terminal in time")
	}

	c, err := h.clusters.Get(context.Background(), h.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine != domain.EngineBuckit {
		t.Fatalf("Engine: want buckit, got %s", c.Engine)
	}
	if c.MigratedFrom == nil {
		t.Fatal("MigratedFrom: nil")
	}
}

// TestM8RollbackDispatch runs cutover, then dispatches a rollback and
// asserts the cluster row flips back to MinIO.
func TestM8RollbackDispatch(t *testing.T) {
	h := newM8Harness(t)

	// 1. Cutover.
	body, _ := json.Marshal(h.cutoverBody())
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/"+h.clusterID+"/migrate/cutover", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var dispatch tasks.DispatchResponse
	_ = json.NewDecoder(resp.Body).Decode(&dispatch)
	if !waitForTerminalStatus(t, h, dispatch.TaskID, 15*time.Second) {
		t.Fatal("cutover did not reach terminal")
	}

	// 2. Rollback. The orchestrator briefly holds the per-cluster lock
	//    after the hub state goes terminal (history update + sweep), so a
	//    same-tick dispatch can race with the lock release. Retry the
	//    dispatch a handful of times before giving up.
	rb, _ := json.Marshal(h.cutoverBody())
	var dispatch2 tasks.DispatchResponse
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp2, err := http.Post(h.server.URL+"/api/v1/clusters/"+h.clusterID+"/migrate/rollback", "application/json", bytes.NewReader(rb))
		if err != nil {
			t.Fatal(err)
		}
		if resp2.StatusCode == http.StatusAccepted {
			_ = json.NewDecoder(resp2.Body).Decode(&dispatch2)
			resp2.Body.Close()
			break
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusConflict {
			t.Fatalf("rollback dispatch: want 202 or 409, got %d", resp2.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dispatch2.TaskID == "" {
		t.Fatal("rollback dispatch never accepted")
	}
	if !waitForTerminalStatus(t, h, dispatch2.TaskID, 15*time.Second) {
		t.Fatal("rollback did not reach terminal")
	}

	c, err := h.clusters.Get(context.Background(), h.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine != domain.EngineMinio {
		t.Fatalf("Engine: want minio after rollback, got %s", c.Engine)
	}
}

// TestM8CutoverRejectsMissingCluster confirms a 404 + clean error when the
// referenced cluster doesn't exist.
func TestM8CutoverRejectsMissingCluster(t *testing.T) {
	h := newM8Harness(t)
	body, _ := json.Marshal(h.cutoverBody())
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/nope/migrate/cutover", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// TestM8CutoverRejectsBadValidation confirms a 400 when the wire body is
// missing required fields.
func TestM8CutoverRejectsBadValidation(t *testing.T) {
	h := newM8Harness(t)
	body, _ := json.Marshal(map[string]any{
		"sourceClusterId": h.clusterID,
		// missing targetVersion + hosts + snapshotPath
	})
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/"+h.clusterID+"/migrate/cutover", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// waitForTerminalStatus polls /operations/:taskId until the status is
// terminal, or returns false on timeout.
func waitForTerminalStatus(t *testing.T, h *m8Harness, taskID string, deadline time.Duration) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		resp, err := http.Get(h.server.URL + "/api/v1/operations/" + taskID)
		if err == nil {
			var prog tasks.OperationProgress
			_ = json.NewDecoder(resp.Body).Decode(&prog)
			resp.Body.Close()
			if prog.State.IsTerminal() {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
