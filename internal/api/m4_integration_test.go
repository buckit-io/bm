package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m4Harness struct {
	server     *httptest.Server
	store      *store.Store
	mgr        *tasks.Manager
	adminSrv   *httptest.Server
	admin      *clusteradmin.Repo
	aliasCalls int
}

func newM4Harness(t *testing.T) *m4Harness {
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

	adminSrv := fakeAdminHTTPServer()
	t.Cleanup(adminSrv.Close)

	h := &m4Harness{store: st, mgr: mgr, adminSrv: adminSrv, admin: clusterAdminRepo}

	handler := New(Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
		AliasPath:    filepath.Join(dir, "config.json"),
		AliasSync: func(ctx context.Context, _ *store.Store, _ string) error {
			h.aliasCalls++
			return nil
		},
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	h.server = ts
	return h
}

func fakeAdminHTTPServer() *httptest.Server {
	infoBody := madmin.InfoMessage{
		Mode: "online",
		Servers: []madmin.ServerProperties{
			{
				Endpoint:   "node1:9000",
				State:      "online",
				Version:    "RELEASE.2026-06-01T00-00-00Z", // post-cutoff -> Buckit
				PoolNumber: 1,
				Uptime:     42,
				Disks: []madmin.Disk{
					{Endpoint: "/dev/sda", DrivePath: "/", State: "ok", TotalSpace: 256 << 30, UsedSpace: 50 << 30, RootDisk: true},
					{Endpoint: "/dev/sdb", DrivePath: "/data/disk1", State: "ok", TotalSpace: 16 << 40, UsedSpace: 1 << 40},
				},
			},
			{
				Endpoint:   "node2:9000",
				State:      "online",
				Version:    "RELEASE.2026-06-01T00-00-00Z",
				PoolNumber: 1,
				Disks: []madmin.Disk{
					{Endpoint: "/dev/sdb", DrivePath: "/data/disk1", State: "ok", TotalSpace: 16 << 40, UsedSpace: 1 << 40},
				},
			},
		},
		Backend: madmin.ErasureBackend{Type: "Erasure", StandardSCParity: 2},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/admin/v3/info") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(infoBody)
	}))
}

func TestM4FullImportFlow(t *testing.T) {
	h := newM4Harness(t)

	// Discover: SSE stream — parse the final `event: result` frame.
	body, _ := json.Marshal(map[string]any{
		"url":      h.adminSrv.URL,
		"username": "ak",
		"password": "sk",
		"insecure": true,
	})
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/import/discover", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	candidate := parseDiscoverResult(t, resp.Body)
	if candidate.Engine != domain.EngineBuckit {
		t.Fatalf("want Buckit, got %s", candidate.Engine)
	}
	if len(candidate.Nodes) != 2 {
		t.Fatalf("want 2 nodes in candidate, got %d", len(candidate.Nodes))
	}

	// Commit
	commitBody, _ := json.Marshal(map[string]any{
		"candidate":  candidate,
		"chosenName": "prod-east",
		"insecure":   true,
	})
	resp2 := doRequest(t, h, http.MethodPost, "/api/v1/clusters/import/commit", commitBody)
	if resp2.code != 200 {
		t.Fatalf("commit: want 200, got %d %s", resp2.code, resp2.body)
	}
	var commitOut struct {
		ClusterID string `json:"clusterId"`
	}
	_ = json.Unmarshal(resp2.body, &commitOut)
	if commitOut.ClusterID != "prod-east" {
		t.Fatalf("want clusterId prod-east, got %s", commitOut.ClusterID)
	}

	// List
	list := doRequest(t, h, http.MethodGet, "/api/v1/clusters", nil)
	if list.code != 200 {
		t.Fatalf("list: %d %s", list.code, list.body)
	}
	var rows []domain.Cluster
	_ = json.Unmarshal(list.body, &rows)
	if len(rows) != 1 || rows[0].ID != "prod-east" {
		t.Fatalf("list unexpected: %+v", rows)
	}

	// Get
	one := doRequest(t, h, http.MethodGet, "/api/v1/clusters/prod-east", nil)
	if one.code != 200 {
		t.Fatalf("get: %d", one.code)
	}

	// Nodes seeded by commit
	ns := doRequest(t, h, http.MethodGet, "/api/v1/clusters/prod-east/nodes", nil)
	if ns.code != 200 {
		t.Fatalf("nodes: %d", ns.code)
	}
	var nodeRows []domain.Node
	_ = json.Unmarshal(ns.body, &nodeRows)
	if len(nodeRows) != 2 {
		t.Fatalf("want 2 node rows, got %d", len(nodeRows))
	}

	// Admin creds are configurable without exposing the stored secret.
	adminGet := doRequest(t, h, http.MethodGet, "/api/v1/clusters/prod-east/admin-creds", nil)
	if adminGet.code != 200 {
		t.Fatalf("get admin creds: %d %s", adminGet.code, adminGet.body)
	}
	if strings.Contains(string(adminGet.body), `"secretKey"`) || strings.Contains(string(adminGet.body), `"sk"`) {
		t.Fatalf("admin creds response exposed secret: %s", adminGet.body)
	}
	adminBody, _ := json.Marshal(map[string]any{
		"url":       h.adminSrv.URL,
		"accessKey": "ak",
		"secretKey": "correct-password",
		"insecure":  true,
	})
	adminPut := doRequest(t, h, http.MethodPut, "/api/v1/clusters/prod-east/admin-creds", adminBody)
	if adminPut.code != 204 {
		t.Fatalf("put admin creds: %d %s", adminPut.code, adminPut.body)
	}
	storedAdmin, err := h.admin.Get(context.Background(), "prod-east")
	if err != nil {
		t.Fatal(err)
	}
	if storedAdmin.SecretKey != "correct-password" {
		t.Fatalf("admin creds not updated: %+v", storedAdmin)
	}

	// Refresh single
	rf := doRequest(t, h, http.MethodPost, "/api/v1/clusters/prod-east/refresh", nil)
	if rf.code != 200 {
		t.Fatalf("refresh single: %d %s", rf.code, rf.body)
	}

	// Refresh all
	rfa := doRequest(t, h, http.MethodPost, "/api/v1/clusters/refresh", nil)
	if rfa.code != 200 {
		t.Fatalf("refresh all: %d %s", rfa.code, rfa.body)
	}

	// Delete cascade
	del := doRequest(t, h, http.MethodDelete, "/api/v1/clusters/prod-east", nil)
	if del.code != 204 {
		t.Fatalf("delete: %d", del.code)
	}
	listAfter := doRequest(t, h, http.MethodGet, "/api/v1/clusters", nil)
	if !strings.Contains(string(listAfter.body), "[]") {
		t.Fatalf("expected empty list after delete, got %s", listAfter.body)
	}

	if h.aliasCalls < 2 {
		t.Fatalf("alias sync should run on commit + delete, got %d calls", h.aliasCalls)
	}
}

func TestM4DiscoverInvalidURL(t *testing.T) {
	h := newM4Harness(t)
	body, _ := json.Marshal(map[string]any{"url": "", "username": "ak", "password": "sk"})
	resp, err := http.Post(h.server.URL+"/api/v1/clusters/import/discover", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	result := parseDiscoverFinal(t, resp.Body)
	if okVal, _ := result["ok"].(bool); okVal {
		t.Fatalf("expected ok:false, got %+v", result)
	}
	errObj := result["error"].(map[string]any)
	if errObj["kind"] != string(domain.ImportErrInvalidURL) {
		t.Fatalf("want invalid_url, got %v", errObj)
	}
}

func TestMergeAdminNodeFactsRefreshesNodeRows(t *testing.T) {
	existing := []domain.Node{{
		ID:        "n1",
		ClusterID: "c1",
		Hostname:  "node1",
		SSHPort:   22,
		State:     domain.NodeOffline,
		Version:   "old",
		Pool:      9,
		Drives:    []domain.Drive{{Mount: "/old", Device: "/dev/old", State: domain.DriveFailed}},
		Pingable:  true,
	}}
	info := &domain.ServerInfo{
		Servers: []domain.ServerInfoServer{{
			Endpoint:   "node1:9000",
			State:      domain.NodeOnline,
			PoolNumber: 1,
			Version:    "new",
			Drives: []domain.ServerInfoDrive{
				{Mount: "/data/drive1", Device: "/dev/sdb", State: domain.DriveReady},
			},
		}},
	}

	got := mergeAdminNodeFacts(existing, info)
	if len(got) != 1 {
		t.Fatalf("want 1 node, got %d", len(got))
	}
	if got[0].State != domain.NodeOnline || got[0].Version != "new" || got[0].Pool != 1 {
		t.Fatalf("node facts not refreshed: %+v", got[0])
	}
	if len(got[0].Drives) != 1 || got[0].Drives[0].Mount != "/data/drive1" {
		t.Fatalf("drive facts not refreshed: %+v", got[0].Drives)
	}
	if !got[0].Pingable {
		t.Fatal("existing probe fields should be preserved until async refresh")
	}
}

func TestRefreshNodeConnectivityUpdatesProbeFlags(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	nodesRepo := nodes.New(st)
	clusterID := "c1"
	node := domain.Node{
		ID:                "n1",
		ClusterID:         clusterID,
		Hostname:          "node1",
		SSHPort:           2222,
		Pingable:          false,
		Sshable:           false,
		APIAccessible:     false,
		ConsoleAccessible: false,
	}
	if err := nodesRepo.Put(context.Background(), node); err != nil {
		t.Fatal(err)
	}

	prevTCP := refreshTCPProbe.Load()
	prevHTTP := refreshHTTPProbe.Load()
	prevICMP := refreshICMPProbe.Load()
	t.Cleanup(func() {
		refreshTCPProbe.Store(prevTCP)
		refreshHTTPProbe.Store(prevHTTP)
		refreshICMPProbe.Store(prevICMP)
	})

	tcp := tcpProbeFn(func(_ context.Context, address string) bool {
		return strings.HasSuffix(address, ":2222") || strings.HasSuffix(address, ":9000")
	})
	refreshTCPProbe.Store(&tcp)
	httpProbe := httpProbeFn(func(_ context.Context, _ *http.Client, rawURL string) bool {
		return strings.Contains(rawURL, ":9000/")
	})
	refreshHTTPProbe.Store(&httpProbe)
	icmp := icmpProbeFn(func(_ context.Context, _ string) bool { return false })
	refreshICMPProbe.Store(&icmp)

	err = refreshNodeConnectivity(context.Background(), Options{
		Nodes: nodesRepo,
	}, clusterID, []domain.Node{node}, domain.AdminCreds{URL: "http://node1:9000"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := nodesRepo.Get(context.Background(), clusterID, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pingable || !got.Sshable || !got.APIAccessible || got.ConsoleAccessible {
		t.Fatalf("unexpected probe flags: %+v", got)
	}
}

func TestFallbackConsoleURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://node1:9000", "http://node1:9001"},
		{"https://vip.example.com:9443", "https://vip.example.com:9001"},
		{"node1:9000", "https://node1:9001"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := fallbackConsoleURL(tc.in); got != tc.want {
			t.Fatalf("fallbackConsoleURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestM4CommitSlugCollision(t *testing.T) {
	h := newM4Harness(t)
	// First commit with a synthetic candidate to seed "prod".
	candidate := domain.ImportCandidate{
		URL:      h.adminSrv.URL,
		Username: "ak", Password: "sk",
		Engine: domain.EngineBuckit, Version: "v1",
		Nodes: []domain.Node{{Hostname: "n1", SSHPort: 22, State: domain.NodeOnline, Pool: 1}},
	}
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(map[string]any{"candidate": candidate, "chosenName": "prod"})
		resp := doRequest(t, h, http.MethodPost, "/api/v1/clusters/import/commit", body)
		if resp.code != 200 {
			t.Fatalf("commit %d: %d %s", i, resp.code, resp.body)
		}
	}
	list := doRequest(t, h, http.MethodGet, "/api/v1/clusters", nil)
	var rows []domain.Cluster
	_ = json.Unmarshal(list.body, &rows)
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if !ids["prod"] || !ids["prod-1"] {
		t.Fatalf("want both prod and prod-1, got %+v", ids)
	}
}

// ---- helpers ----

type respBody struct {
	code int
	body []byte
}

func doRequest(t *testing.T, h *m4Harness, method, path string, body []byte) respBody {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, h.server.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return respBody{code: resp.StatusCode, body: b}
}

// parseDiscoverResult walks an SSE response and returns the candidate from the
// final `event: result` frame. Fails the test if the result is an error.
func parseDiscoverResult(t *testing.T, body io.Reader) domain.ImportCandidate {
	t.Helper()
	final := parseDiscoverFinal(t, body)
	if ok, _ := final["ok"].(bool); !ok {
		t.Fatalf("discover failed: %+v", final)
	}
	candRaw, _ := json.Marshal(final["candidate"])
	var c domain.ImportCandidate
	if err := json.Unmarshal(candRaw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// parseDiscoverFinal walks the SSE response and returns the JSON body of the
// final `event: result` frame as a generic map.
func parseDiscoverFinal(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1024*64), 1024*1024)
	var lastEvent, lastData string
	var final map[string]any
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			lastEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			lastData = strings.TrimPrefix(line, "data: ")
		case line == "":
			if lastEvent == "result" && lastData != "" {
				if err := json.Unmarshal([]byte(lastData), &final); err != nil {
					t.Fatalf("parse final result: %v", err)
				}
			}
			lastEvent, lastData = "", ""
		}
	}
	if final == nil {
		t.Fatal("no result event in SSE stream")
	}
	return final
}
