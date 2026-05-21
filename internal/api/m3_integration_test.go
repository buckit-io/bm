package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m3Harness struct {
	server *httptest.Server
	store  *store.Store
	mgr    *tasks.Manager
	sshSrv *sshtest.Server
}

func newM3Harness(t *testing.T) *m3Harness {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	nodesRepo := nodes.New(st)
	sshcfgRepo := sshconfig.New(st)
	pool := bmssh.NewPool(nil)
	t.Cleanup(pool.Close)
	mgr := tasks.NewManager(st)
	tasks.RegisterSshProbe(nodesRepo, sshcfgRepo)
	t.Cleanup(mgr.Shutdown)

	handler := New(Options{
		Store:     st,
		Tasks:     mgr,
		Nodes:     nodesRepo,
		SSHConfig: sshcfgRepo,
		SSHPool:   pool,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sshSrv.Stop)

	return &m3Harness{server: ts, store: st, mgr: mgr, sshSrv: sshSrv}
}

func TestM3SshConfigPutGet(t *testing.T) {
	h := newM3Harness(t)
	body, _ := json.Marshal(domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password(),
		},
		Overrides: map[string]domain.SshOverrides{},
	})
	resp := httpDo(t, h, http.MethodPut, "/api/v1/clusters/c1/ssh", body)
	if resp.code != 204 {
		t.Fatalf("PUT ssh: want 204, got %d: %s", resp.code, resp.body)
	}
	resp = httpDo(t, h, http.MethodGet, "/api/v1/clusters/c1/ssh", nil)
	if resp.code != 200 {
		t.Fatalf("GET ssh: want 200, got %d: %s", resp.code, resp.body)
	}
	var got domain.ClusterSshConfig
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.SSH.User != h.sshSrv.User() {
		t.Fatalf("user round-trip: %+v", got)
	}
}

func TestM3SshTestProbesHost(t *testing.T) {
	h := newM3Harness(t)
	host, port := h.sshSrv.HostPort()
	body, _ := json.Marshal(map[string]any{
		"ssh": domain.SshCreds{
			AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password(),
		},
		"hosts": []domain.HostRef{{ID: "h1", Hostname: host, Port: port}},
	})
	resp := httpDo(t, h, http.MethodPost, "/api/v1/clusters/c1/ssh/test", body)
	if resp.code != 200 {
		t.Fatalf("POST ssh/test: want 200, got %d: %s", resp.code, resp.body)
	}
	var got []bmssh.ProbeResult
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("unexpected probe results: %+v", got)
	}
	if !strings.Contains(got[0].Kernel, "Linux") {
		t.Fatalf("missing kernel: %q", got[0].Kernel)
	}
}

func TestM3SshProbeDispatchE2E(t *testing.T) {
	h := newM3Harness(t)
	// Save creds first so the executor can load them.
	host, port := h.sshSrv.HostPort()
	cfg, _ := json.Marshal(domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthPassword, User: h.sshSrv.User(), Password: h.sshSrv.Password(),
		},
		Overrides: map[string]domain.SshOverrides{},
	})
	resp := httpDo(t, h, http.MethodPut, "/api/v1/clusters/c1/ssh", cfg)
	if resp.code != 204 {
		t.Fatalf("setup PUT ssh: %d %s", resp.code, resp.body)
	}

	body, _ := json.Marshal(map[string]any{
		"clusterId":   "c1",
		"clusterName": "test",
		"kind":        "ssh_probe",
		"params": map[string]any{
			"hosts": []domain.HostRef{{ID: "h1", Hostname: host, Port: port}},
		},
	})
	resp = httpDo(t, h, http.MethodPost, "/api/v1/operations", body)
	if resp.code != 202 {
		t.Fatalf("dispatch: want 202, got %d: %s", resp.code, resp.body)
	}
	var dispatch tasks.DispatchResponse
	if err := json.Unmarshal(resp.body, &dispatch); err != nil {
		t.Fatal(err)
	}

	// Wait for terminal.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp := httpDo(t, h, http.MethodGet, "/api/v1/history?limit=1", nil)
		if resp.code == 200 {
			var rows []tasks.HistoryEntry
			_ = json.Unmarshal(resp.body, &rows)
			if len(rows) > 0 && rows[0].Status.IsTerminal() {
				if rows[0].Status != tasks.StateSucceeded {
					t.Fatalf("probe op failed: %+v", rows[0])
				}
				if rows[0].Result == nil || len(rows[0].Result.Summary) == 0 {
					t.Fatalf("missing summary: %+v", rows[0].Result)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("probe op did not terminate within deadline")
}

func TestM3NodeEndpoints(t *testing.T) {
	h := newM3Harness(t)
	// Initially empty.
	resp := httpDo(t, h, http.MethodGet, "/api/v1/clusters/c1/nodes", nil)
	if resp.code != 200 || strings.TrimSpace(string(resp.body)) != "[]" {
		t.Fatalf("GET nodes empty: %d %s", resp.code, resp.body)
	}
	// 404 on missing.
	resp = httpDo(t, h, http.MethodGet, "/api/v1/clusters/c1/nodes/ghost", nil)
	if resp.code != 404 {
		t.Fatalf("GET missing: want 404, got %d", resp.code)
	}
}

type httpResp struct {
	code int
	body []byte
}

func httpDo(t *testing.T, h *m3Harness, method, path string, body []byte) httpResp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, rdr)
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
	if testing.Verbose() {
		fmt.Printf("[%s %s] %d %s\n", method, path, resp.StatusCode, b)
	}
	return httpResp{code: resp.StatusCode, body: b}
}
