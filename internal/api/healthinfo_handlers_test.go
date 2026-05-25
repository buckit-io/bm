package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"
	"github.com/shirou/gopsutil/v3/host"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

// fakeHealthInfoServer answers /info with a minimal InfoMessage and
// /healthinfo with a single-frame madmin.HealthInfo JSON document.
func fakeHealthInfoServer() *httptest.Server {
	healthBody := madmin.HealthInfo{
		Version: madmin.HealthInfoVersion,
		Sys: madmin.SysInfo{
			OSInfo: []madmin.OSInfo{{
				NodeCommon: madmin.NodeCommon{Addr: "node1:9000"},
				Info:       host.InfoStat{KernelVersion: "6.6.10", Platform: "ubuntu", PlatformVersion: "22.04"},
			}},
			MemInfo: []madmin.MemInfo{{
				NodeCommon: madmin.NodeCommon{Addr: "node1:9000"}, Total: 64 << 30,
			}},
			CPUInfo: []madmin.CPUs{{
				NodeCommon: madmin.NodeCommon{Addr: "node1:9000"},
				CPUs: []madmin.CPU{{ModelName: "Xeon Gold 6342", Mhz: 2800, Cores: 24, PhysicalID: "0"}},
			}},
			NetInfo: []madmin.NetInfo{{
				NodeCommon: madmin.NodeCommon{Addr: "node1:9000"}, Interface: "eno1", Driver: "ixgbe",
			}},
		},
	}
	infoBody := madmin.InfoMessage{Mode: "online", Servers: []madmin.ServerProperties{{Endpoint: "node1:9000", State: "online"}}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/admin/v3/healthinfo"):
			// Mirror the upstream wire format: emit a version-only preamble
			// (drained by madmin.ServerHealthInfo), flush, then the data
			// frame. The flush + sleep gap is what prevents madmin's internal
			// bufio decoder from over-reading the second frame and silently
			// discarding it when its decoder goes out of scope.
			flusher, _ := w.(http.Flusher)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"version": madmin.HealthInfoVersion})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(healthBody)
			if flusher != nil {
				flusher.Flush()
			}
		case strings.Contains(r.URL.Path, "/admin/v3/info"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(infoBody)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newHealthInfoHarness(t *testing.T) (*httptest.Server, *clusters.Repo, *clusteradmin.Repo, *httptest.Server) {
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
	adminPool := admin.NewPool()
	mgr := tasks.NewManager(st)
	t.Cleanup(mgr.Shutdown)

	upstream := fakeHealthInfoServer()
	t.Cleanup(upstream.Close)

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, clustersRepo, clusterAdminRepo, upstream
}

func TestHealthInfo_NotFound(t *testing.T) {
	ts, _, _, _ := newHealthInfoHarness(t)
	resp, err := http.Get(ts.URL + "/api/v1/clusters/does-not-exist/healthinfo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestHealthInfo_MissingCreds(t *testing.T) {
	ts, clustersRepo, _, _ := newHealthInfoHarness(t)
	_ = clustersRepo.Put(context.Background(), domain.Cluster{ID: "c1", Name: "c1"})

	resp, err := http.Get(ts.URL + "/api/v1/clusters/c1/healthinfo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("want 424, got %d", resp.StatusCode)
	}
}

func TestHealthInfo_HappyPath(t *testing.T) {
	ts, clustersRepo, adminRepo, upstream := newHealthInfoHarness(t)
	_ = clustersRepo.Put(context.Background(), domain.Cluster{ID: "c1", Name: "c1"})
	_ = adminRepo.Put(context.Background(), "c1", domain.AdminCreds{
		URL: upstream.URL, AccessKey: "ak", SecretKey: "sk", Insecure: true,
	})

	resp, err := http.Get(ts.URL + "/api/v1/clusters/c1/healthinfo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got domain.HealthInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hosts) != 1 {
		t.Fatalf("want 1 host, got %d (%+v)", len(got.Hosts), got)
	}
	h := got.Hosts[0]
	if h.Addr != "node1:9000" {
		t.Fatalf("wrong addr: %s", h.Addr)
	}
	if h.OS == nil || h.OS.KernelVersion != "6.6.10" {
		t.Fatalf("OS not mapped: %+v", h.OS)
	}
	if h.Mem == nil || h.Mem.Total != 64<<30 {
		t.Fatalf("Mem not mapped: %+v", h.Mem)
	}
	if len(h.CPUs) != 1 || h.CPUs[0].ModelName != "Xeon Gold 6342" || h.CPUs[0].Cores != 24 {
		t.Fatalf("CPUs not mapped: %+v", h.CPUs)
	}
	if len(h.NICs) != 1 || h.NICs[0].Interface != "eno1" {
		t.Fatalf("NICs not mapped: %+v", h.NICs)
	}
}
