package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/operations"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/sshtest"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
)

type m7Harness struct {
	server                *httptest.Server
	store                 *store.Store
	clusters              *clusters.Repo
	nodes                 *nodes.Repo
	admin                 *clusteradmin.Repo
	sshSrv                *sshtest.Server
	adminSrv              *httptest.Server
	info                  madmin.InfoMessage
	update                madmin.ServerUpdateStatusV2
	restartVersion        string
	unhealthyAfterRestart bool
	failServiceRestart    bool

	// counters so tests can assert which admin verb was hit.
	calls struct {
		serverUpdate    atomic.Int32
		serverUpdateURL atomic.Value
		serviceRestart  atomic.Int32
		serviceStop     atomic.Int32
		serviceFreeze   atomic.Int32
		serviceUnfreeze atomic.Int32
		healStart       atomic.Int32
	}
}

func newM7Harness(t *testing.T) *m7Harness {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	h := &m7Harness{store: st}
	h.info = canonicalInfo()
	h.update = madmin.ServerUpdateStatusV2{
		Results: []madmin.ServerPeerUpdateStatus{
			{Host: "node1", CurrentVersion: "2026-05-01T00:00:00Z", UpdatedVersion: "2026-06-01T00:00:00Z"},
			{Host: "node2", CurrentVersion: "2026-05-01T00:00:00Z", UpdatedVersion: "2026-06-01T00:00:00Z"},
		},
	}

	clustersRepo := clusters.New(st)
	clusterAdminRepo := clusteradmin.New(st)
	nodesRepo := nodes.New(st)
	sshcfgRepo := sshconfig.New(st)
	mgr := tasks.NewManager(st)
	t.Cleanup(mgr.Shutdown)

	h.clusters = clustersRepo
	h.nodes = nodesRepo
	h.admin = clusterAdminRepo

	// SSH test server first (cleanup order: pool closes BEFORE server stops).
	sshSrv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sshSrv.Stop)
	h.sshSrv = sshSrv

	sshPool := bmssh.NewPool(nil)
	t.Cleanup(sshPool.Close)
	adminPool := admin.NewPool()

	// Fake admin httptest server — counts admin verbs + returns canned ServerInfo.
	h.adminSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/admin/v3/info-account"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(madmin.AccountInfo{
				AccountName: "tester",
			})
		case strings.Contains(path, "/admin/v3/service"):
			// V2 service endpoint returns a ServiceActionResult JSON body;
			// madmin tries to decode the body and trips on an empty response.
			q := r.URL.Query()
			action := q.Get("action")
			switch action {
			case "restart":
				h.calls.serviceRestart.Add(1)
				if h.failServiceRestart {
					writeError(w, http.StatusServiceUnavailable, "restart_failed", "restart unavailable")
					return
				}
				if strings.TrimSpace(h.restartVersion) != "" {
					for i := range h.info.Servers {
						h.info.Servers[i].Version = h.restartVersion
					}
				}
				if h.unhealthyAfterRestart && len(h.info.Servers) > 0 {
					h.info.Servers[len(h.info.Servers)-1].State = "offline"
				}
			case "stop":
				h.calls.serviceStop.Add(1)
			case "freeze":
				h.calls.serviceFreeze.Add(1)
			case "unfreeze":
				h.calls.serviceUnfreeze.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(madmin.ServiceActionResult{
				Action: madmin.ServiceAction(action),
			})
		case strings.Contains(path, "/admin/v3/update"):
			h.calls.serverUpdate.Add(1)
			h.calls.serverUpdateURL.Store(r.URL.Query().Get("updateURL"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(h.update)
			if len(h.update.Results) > 0 {
				for i := range h.info.Servers {
					if i < len(h.update.Results) && strings.TrimSpace(h.update.Results[i].Err) == "" && strings.TrimSpace(h.update.Results[i].UpdatedVersion) != "" {
						h.info.Servers[i].Version = h.update.Results[i].UpdatedVersion
					}
				}
			}
		case strings.Contains(path, "/admin/v3/heal"):
			h.calls.healStart.Add(1)
			w.Header().Set("Content-Type", "application/json")
			// Two response shapes: client sends clientToken="" to start a heal
			// (response = HealStartSuccess) and clientToken="<id>" to poll for
			// status (response = HealTaskStatus).
			if r.URL.Query().Get("clientToken") == "" {
				_ = json.NewEncoder(w).Encode(madmin.HealStartSuccess{
					ClientToken: "heal-token-1",
					StartTime:   time.Now().UTC(),
				})
			} else {
				_ = json.NewEncoder(w).Encode(madmin.HealTaskStatus{
					Summary:   "finished",
					StartTime: time.Now().UTC(),
					Items:     nil,
				})
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(h.info)
		}
	}))
	t.Cleanup(h.adminSrv.Close)

	// Seed cluster + node + admin creds so the operations have something to load.
	host, port := sshSrv.HostPort()
	cluster := domain.Cluster{
		ID:        "test-cluster",
		Name:      "test",
		Engine:    domain.EngineBuckit,
		Version:   "v1.0.0",
		Parity:    2,
		NodeCount: 2,
	}
	if err := clustersRepo.Put(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_ = nodesRepo.Put(context.Background(), domain.Node{
			ID:        cluster.ID + "-n" + string(rune('1'+i)),
			ClusterID: cluster.ID,
			Hostname:  host,
			SSHPort:   port,
			State:     domain.NodeOnline,
			Pool:      1,
		})
	}
	_ = clusterAdminRepo.Put(context.Background(), cluster.ID, domain.AdminCreds{
		URL:       h.adminSrv.URL,
		AccessKey: "ak",
		SecretKey: "sk",
		Insecure:  true,
	})
	_ = sshcfgRepo.Put(context.Background(), cluster.ID, domain.ClusterSshConfig{
		SSH:       domain.SshCreds{AuthMethod: domain.AuthPassword, User: sshSrv.User(), Password: sshSrv.Password(), Sudo: false},
		Overrides: map[string]domain.SshOverrides{},
	})

	operations.RegisterAll(operations.Deps{
		Clusters:     clustersRepo,
		Nodes:        nodesRepo,
		ClusterAdmin: clusterAdminRepo,
		SSHConfig:    sshcfgRepo,
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
	h.server = ts
	return h
}

func canonicalInfo() madmin.InfoMessage {
	return madmin.InfoMessage{
		Mode: "online",
		Servers: []madmin.ServerProperties{
			{Endpoint: "node1:9000", State: "online", Version: "2026-05-01T00:00:00Z", PoolNumber: 1, OS: "linux", Arch: "amd64"},
			{Endpoint: "node2:9000", State: "online", Version: "2026-05-01T00:00:00Z", PoolNumber: 1, OS: "linux", Arch: "amd64"},
		},
		Backend: madmin.ErasureBackend{Type: "Erasure", StandardSCParity: 2},
	}
}
