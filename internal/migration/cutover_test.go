package migration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
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
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

// TestCutoverExecutorOneHost exercises CutoverExecutor.Execute against the
// in-memory SSH test server. Confirms:
//
//  1. Per-host install pipeline runs to StageDone.
//  2. The persisted Cluster row's Engine flips minio → buckit.
//  3. Version is updated and MigratedFrom is stamped.
//
// The single-host case skips waitClusterHealthy entirely (it's only invoked
// between hosts), so we don't need an admin server that speaks ServerInfo.
// The Verify pass at the end DOES need one — we wire a stub.
func TestCutoverExecutorOneHost(t *testing.T) {
	fix := newCutoverFixture(t, 1)
	defer fix.cleanup()

	exec := &CutoverExecutor{
		Installer:    NewInstaller(fix.sshPool),
		Clusters:     fix.clusters,
		ClusterAdmin: fix.clusterAdmin,
		AdminPool:    fix.adminPool,
	}

	body := fix.body()
	raw, _ := json.Marshal(body)
	req := tasks.DispatchRequest{
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateCutover,
		Params:    raw,
	}
	if err := exec.Validate(req); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	hub := tasks.NewHub("test-task")
	run := &tasks.Run{
		TaskID:    "test-task",
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateCutover,
		Params:    raw,
		Hub:       hub,
		Store:     fix.store,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.Execute(ctx, run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Cluster row should be flipped to Buckit.
	c, err := fix.clusters.Get(context.Background(), fix.clusterID)
	if err != nil {
		t.Fatalf("Get cluster: %v", err)
	}
	if c.Engine != domain.EngineBuckit {
		t.Fatalf("Engine: want buckit, got %s", c.Engine)
	}
	if c.Version != "v1.0.0" {
		t.Fatalf("Version: %s", c.Version)
	}
	if c.MigratedFrom == nil {
		t.Fatal("MigratedFrom: nil")
	}
	if c.MigratedFrom.Product != "minio" {
		t.Fatalf("MigratedFrom.Product: %s", c.MigratedFrom.Product)
	}

	// Hub state should reflect succeeded host with summary entries.
	state := hub.Snapshot()
	if len(state.HostStatuses) != 1 || state.HostStatuses[0].State != tasks.HostSucceeded {
		t.Fatalf("HostStatuses: %+v", state.HostStatuses)
	}
	foundHosts := false
	for _, item := range state.Summary {
		if item.Label == "Hosts migrated" && item.Value == "1" {
			foundHosts = true
		}
	}
	if !foundHosts {
		t.Fatalf("missing 'Hosts migrated' summary item: %+v", state.Summary)
	}
}

// TestCutoverExecutorValidatesSnapshot rejects a dispatch when the snapshot
// path doesn't exist. Reading it should fail before any host is touched.
func TestCutoverExecutorValidatesSnapshot(t *testing.T) {
	fix := newCutoverFixture(t, 1)
	defer fix.cleanup()

	exec := &CutoverExecutor{
		Installer:    NewInstaller(fix.sshPool),
		Clusters:     fix.clusters,
		ClusterAdmin: fix.clusterAdmin,
		AdminPool:    fix.adminPool,
	}
	body := fix.body()
	body.SnapshotPath = filepath.Join(t.TempDir(), "missing.json")
	raw, _ := json.Marshal(body)
	err := exec.Validate(tasks.DispatchRequest{
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateCutover,
		Params:    raw,
	})
	if err == nil {
		t.Fatal("Validate: want error for missing snapshot")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("err: %v", err)
	}
}

// TestCutoverExecutorMultiHostHealthWait runs cutover across two hosts and
// confirms waitClusterHealthy is exercised between them. The fake admin
// server returns an InfoMessage with all servers online so the wait
// resolves immediately on the first poll.
func TestCutoverExecutorMultiHostHealthWait(t *testing.T) {
	fix := newCutoverFixture(t, 2)
	defer fix.cleanup()

	exec := &CutoverExecutor{
		Installer:    NewInstaller(fix.sshPool),
		Clusters:     fix.clusters,
		ClusterAdmin: fix.clusterAdmin,
		AdminPool:    fix.adminPool,
	}
	body := fix.body()
	raw, _ := json.Marshal(body)

	hub := tasks.NewHub("multi-host")
	run := &tasks.Run{
		TaskID:    "multi-host",
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateCutover,
		Params:    raw,
		Hub:       hub,
		Store:     fix.store,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.Execute(ctx, run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	state := hub.Snapshot()
	if len(state.HostStatuses) != 2 {
		t.Fatalf("HostStatuses: %+v", state.HostStatuses)
	}
	for _, hs := range state.HostStatuses {
		if hs.State != tasks.HostSucceeded {
			t.Fatalf("host %s: state %s detail %s", hs.Hostname, hs.State, hs.Detail)
		}
	}

	c, err := fix.clusters.Get(context.Background(), fix.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine != domain.EngineBuckit {
		t.Fatalf("Engine: %s", c.Engine)
	}
}

// cutoverFixture wires up a bbolt store, repos, an SSH test server, an
// admin httptest server (returning canned ServerInfo so Verify works),
// and a snapshot file on disk.
type cutoverFixture struct {
	store        *store.Store
	clusters     *clusters.Repo
	clusterAdmin *clusteradmin.Repo
	sshSrv       *sshtest.Server
	sshPool      *bmssh.Pool
	adminPool    *admin.Pool
	adminSrv     *httptest.Server
	clusterID    string
	snapshotPath string
	hosts        []domain.HostRow
}

func (f *cutoverFixture) cleanup() {
	f.sshPool.Close()
	f.sshSrv.Stop()
	f.adminSrv.Close()
	_ = f.store.Close()
}

func (f *cutoverFixture) body() MigrationBody {
	return MigrationBody{
		SourceClusterID: f.clusterID,
		Name:            "test",
		TargetVersion:   "v1.0.0",
		API:             domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:          "us-east-1",
		Hosts:           f.hosts,
		SSH:             domain.SshCreds{AuthMethod: domain.AuthPassword, User: f.sshSrv.User(), Password: f.sshSrv.Password(), Sudo: true},
		SnapshotPath:    f.snapshotPath,
	}
}

func newCutoverFixture(t *testing.T, hostCount int) *cutoverFixture {
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

	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	host, port := sshSrv.HostPort()

	hosts := make([]domain.HostRow, 0, hostCount)
	for i := 0; i < hostCount; i++ {
		hosts = append(hosts, domain.HostRow{
			ID:       "h" + string(rune('1'+i)),
			Hostname: host,
			Port:     port,
			Probe:    domain.HostProbeReachable,
		})
	}

	clustersRepo := clusters.New(st)
	clusterAdminRepo := clusteradmin.New(st)
	clusterID := "test-cluster"

	now := time.Now().UTC()
	if err := clustersRepo.Put(context.Background(), domain.Cluster{
		ID:             clusterID,
		Name:           "test",
		Engine:         domain.EngineMinio,
		Version:        "RELEASE.2026-01-01T00-00-00Z",
		NodeCount:      hostCount,
		PoolCount:      1,
		LastActivityAt: now,
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}

	// Admin server that returns canned ServerInfo + AccountInfo so the
	// post-cutover Verify pass produces sensible counts. The endpoints
	// match the SSH-test host so the new partitionHosts/verify path
	// considers every test host online.
	servers := make([]madmin.ServerProperties, 0, hostCount)
	for i := 0; i < hostCount; i++ {
		servers = append(servers, madmin.ServerProperties{
			Endpoint: fmt.Sprintf("%s:9000", host),
			State:    "online",
			Version:  "RELEASE.2026-01-01T00-00-00Z",
		})
	}
	infoBody := madmin.InfoMessage{
		Mode:    "online",
		Servers: servers,
	}
	adminSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
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

	if err := clusterAdminRepo.Put(context.Background(), clusterID, domain.AdminCreds{
		URL:       adminSrv.URL,
		AccessKey: "ak",
		SecretKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	// Write a snapshot file the executor can read.
	snapshotsDir := filepath.Join(dir, "snapshots")
	if err := os.MkdirAll(snapshotsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snap := &domain.MinioSnapshot{
		CapturedAt: now,
		ClusterID:  clusterID,
		Version:    "RELEASE.2026-01-01T00-00-00Z",
		Buckets:    []domain.BucketSnapshot{{Name: "alpha"}},
	}
	snapshotPath, err := writeSnapshot(snapshotsDir, clusterID, snap)
	if err != nil {
		t.Fatal(err)
	}

	return &cutoverFixture{
		store:        st,
		clusters:     clustersRepo,
		clusterAdmin: clusterAdminRepo,
		sshSrv:       sshSrv,
		sshPool:      bmssh.NewPool(nil),
		adminPool:    admin.NewPool(),
		adminSrv:     adminSrv,
		clusterID:    clusterID,
		snapshotPath: snapshotPath,
		hosts:        hosts,
	}
}
