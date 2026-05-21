package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
)

// listNodes serves GET /clusters/:id/nodes.
func listNodes(repo *nodes.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeJSON(w, http.StatusOK, []domain.Node{})
			return
		}
		clusterID := chi.URLParam(r, "id")
		got, err := repo.List(r.Context(), clusterID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// getNode serves GET /clusters/:id/nodes/:nodeId.
func getNode(repo *nodes.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusNotFound, "not_found", "no nodes")
			return
		}
		got, err := repo.Get(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeId"))
		if errors.Is(err, nodes.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "node not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// getSSHConfig serves GET /clusters/:id/ssh.
func getSSHConfig(repo *sshconfig.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusNotFound, "not_found", "no config")
			return
		}
		cfg, err := repo.Get(r.Context(), chi.URLParam(r, "id"))
		if errors.Is(err, sshconfig.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "ssh config not set")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

// putSSHConfig serves PUT /clusters/:id/ssh.
func putSSHConfig(repo *sshconfig.Repo, pool *bmssh.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "ssh config repo not configured")
			return
		}
		clusterID := chi.URLParam(r, "id")
		var cfg domain.ClusterSshConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if cfg.Overrides == nil {
			cfg.Overrides = map[string]domain.SshOverrides{}
		}
		if err := repo.Put(r.Context(), clusterID, cfg); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		// Drop any cached SSH clients for this cluster — the creds may have changed.
		if pool != nil {
			// We can't enumerate hosts here, so caller is expected to call /ssh/test
			// which re-dials anyway. A future helper could iterate overrides.
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// testSSHRequest is the body of POST /clusters/:id/ssh/test.
type testSSHRequest struct {
	SSH       domain.SshCreds                `json:"ssh"`
	Overrides map[string]domain.SshOverrides `json:"overrides"`
	Hosts     []domain.HostRef               `json:"hosts"`
}

const sshTestConcurrency = 8

// testSSHConfig serves POST /clusters/:id/ssh/test.
func testSSHConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req testSSHRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if len(req.Hosts) == 0 {
			writeJSON(w, http.StatusOK, []bmssh.ProbeResult{})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		results := make([]bmssh.ProbeResult, len(req.Hosts))
		sem := make(chan struct{}, sshTestConcurrency)
		var wg sync.WaitGroup
		for i, host := range req.Hosts {
			wg.Add(1)
			go func(i int, host domain.HostRef) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				ov := req.Overrides[host.ID]
				var overridePtr *domain.SshOverrides
				if _, has := req.Overrides[host.ID]; has {
					overridePtr = &ov
				}
				results[i] = bmssh.Probe(ctx, host, bmssh.Merge(req.SSH, overridePtr))
			}(i, host)
		}
		wg.Wait()
		writeJSON(w, http.StatusOK, results)
	}
}
