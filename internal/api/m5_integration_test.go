package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/preflight"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m5Harness struct {
	server *httptest.Server
	store  *store.Store
	sshSrv *sshtest.Server
}

func newM5Harness(t *testing.T) *m5Harness {
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

	// Wire preflight resolver to the deploy catalog (production wiring).
	preflight.SetVersionResolver(func(tag string) string {
		if v := deploy.VersionByTag(tag); v != nil {
			return v.RpmURL
		}
		return ""
	})
	t.Cleanup(func() { preflight.SetVersionResolver(nil) })

	// Cleanup is LIFO: register sshSrv.Stop FIRST so it runs LAST — the pool
	// must release its cached clients before the server waits for goroutines.
	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sshSrv.Stop)

	sshPool := bmssh.NewPool(nil)
	t.Cleanup(sshPool.Close)

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		SSHConfig:    sshcfgRepo,
		SSHPool:      sshPool,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AliasPath:    filepath.Join(dir, "config.json"),
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &m5Harness{server: ts, store: st, sshSrv: sshSrv}
}

func TestM5VersionsEndpoint(t *testing.T) {
	h := newM5Harness(t)
	resp := do(t, h.server, http.MethodGet, "/api/v1/artifacts/versions", nil)
	if resp.code != 200 {
		t.Fatalf("versions: %d %s", resp.code, resp.body)
	}
	var got []domain.BuckitVersion
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Tag == "" {
		t.Fatalf("unexpected versions: %+v", got)
	}
}

func TestM5ValidateArtifact(t *testing.T) {
	h := newM5Harness(t)
	// Tiny upstream that returns a small RPM-shaped body + a sha256 sidecar.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  buckit.rpm\n"))
		default:
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(200)
		}
	}))
	defer up.Close()
	body, _ := json.Marshal(map[string]string{"url": up.URL + "/buckit.rpm"})
	resp := do(t, h.server, http.MethodPost, "/api/v1/artifacts/validate", body)
	if resp.code != 200 {
		t.Fatalf("validate: %d %s", resp.code, resp.body)
	}
	var got domain.CustomUrlCheck
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != domain.CustomUrlValid {
		t.Fatalf("want valid state, got %s (%s)", got.State, got.Message)
	}
	if len(got.SHA256) != 64 {
		t.Fatalf("expected 64-char sha256, got %q", got.SHA256)
	}
}

func TestM5NewClusterDiscover(t *testing.T) {
	h := newM5Harness(t)
	host, port := h.sshSrv.HostPort()
	body, _ := json.Marshal(map[string]any{
		"hosts": []domain.HostRow{{ID: "h1", Hostname: host, Port: port, Probe: domain.HostProbeReachable}},
		"ssh":   domain.SshCreds{AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password()},
	})
	resp := do(t, h.server, http.MethodPost, "/api/v1/clusters/new/discover", body)
	if resp.code != 200 {
		t.Fatalf("discover: %d %s", resp.code, resp.body)
	}
	var got map[string]domain.WizardDiscoveryResult
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatal(err)
	}
	r := got["h1"]
	if r.State != domain.WizardDiscoveryDone {
		t.Fatalf("state: want done, got %s (%s)", r.State, r.Error)
	}
	if r.Arch != "amd64" || r.Cores == nil || *r.Cores != 8 || len(r.Drives) != 3 {
		t.Fatalf("unexpected discovery: %+v", r)
	}
}

func TestM5NewClusterPreflight(t *testing.T) {
	h := newM5Harness(t)
	host, port := h.sshSrv.HostPort()
	a := 8
	r := 16
	hostRow := domain.HostRow{ID: "h1", Hostname: host, Port: port, Probe: domain.HostProbeReachable}
	drives := []domain.DiscoveredDrive{
		{Mount: "/data/disk1", FsType: "xfs", SizeBytes: 16 << 40},
		{Mount: "/data/disk2", FsType: "xfs", SizeBytes: 16 << 40},
	}
	draft := domain.NewClusterDraft{
		Name:    "test",
		Version: "v1.0.0",
		API:     domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:  "us-east-1",
		Hosts:   []domain.HostRow{hostRow},
		SSH:     domain.SshCreds{AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password()},
		Topology: domain.Topology{SetSize: 4, Parity: 2, SelectedMounts: []string{"/data/disk1", "/data/disk2"}},
		Discovery: map[string]domain.WizardDiscoveryResult{
			"h1": {State: domain.WizardDiscoveryDone, Arch: "amd64", OS: "test 1", Cores: &a, RamGiB: &r, Drives: drives},
		},
	}
	body, _ := json.Marshal(draft)
	resp := do(t, h.server, http.MethodPost, "/api/v1/clusters/new/preflight", body)
	if resp.code != 200 {
		t.Fatalf("preflight: %d %s", resp.code, resp.body)
	}
	var results []domain.PreflightResult
	if err := json.Unmarshal(resp.body, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) < 10 {
		t.Fatalf("want >=10 results, got %d", len(results))
	}
	// SSH + sudo + pkg_mgr should pass since the fake server answers all three.
	for _, id := range []string{"ssh", "sudo", "pkg_mgr"} {
		got := findResultByID(results, id)
		if got.Result != domain.PreflightPass {
			t.Errorf("check %s: want pass, got %s (%s)", id, got.Result, got.Detail)
		}
	}
}

func findResultByID(results []domain.PreflightResult, id string) domain.PreflightResult {
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	return domain.PreflightResult{}
}

// ---- helpers ----

type genericResp struct {
	code int
	body []byte
}

func do(t *testing.T, srv *httptest.Server, method, path string, body []byte) genericResp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return genericResp{code: resp.StatusCode, body: b}
}
