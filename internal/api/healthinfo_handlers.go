package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
)

// healthInfoTimeout caps the whole endpoint round-trip. Slightly larger than
// admin.healthInfoDeadline so the upstream's collection budget can fire on
// its own boundary before this wrapper cancels.
const healthInfoTimeout = 35 * time.Second

func getClusterHealthInfo(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil || opts.ClusterAdmin == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repo not configured")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := opts.Clusters.Get(r.Context(), id); err != nil {
			if errors.Is(err, clusters.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "cluster not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		creds, err := opts.ClusterAdmin.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, clusteradmin.ErrNotFound) {
				writeError(w, http.StatusFailedDependency, "admin_creds_missing", "admin credentials not set for cluster")
				return
			}
			writeError(w, http.StatusInternalServerError, "creds_failed", err.Error())
			return
		}
		var client *admin.Client
		if opts.AdminPool != nil {
			client, err = opts.AdminPool.Get(id, creds)
		} else {
			client, err = admin.New(creds)
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "admin_client_failed", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), healthInfoTimeout)
		defer cancel()
		info, err := client.HealthInfo(ctx)
		if err != nil {
			status, code := healthInfoStatus(err)
			writeError(w, status, code, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

// healthInfoStatus picks an HTTP status + error code for an admin.Error.
// Mirrors the classification used elsewhere so the UI can switch on `error`.
func healthInfoStatus(err error) (int, string) {
	var aerr *admin.Error
	if errors.As(err, &aerr) {
		switch aerr.Kind {
		case admin.ErrUnreachable:
			return http.StatusBadGateway, "unreachable"
		case admin.ErrAuth:
			return http.StatusUnauthorized, "auth"
		}
	}
	return http.StatusBadGateway, "admin_failed"
}
