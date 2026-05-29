package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/migration"
	"github.com/buckit-io/bm/internal/tasks"
)

// overridesFromHosts collects each HostRow's non-nil SSHOverride into a
// map keyed by host ID. Returns an empty map when no host has a custom
// override — the wire shape requires the map to be present even when
// empty.
func overridesFromHosts(hosts []domain.HostRow) map[string]domain.SshOverrides {
	out := make(map[string]domain.SshOverrides, len(hosts))
	for _, h := range hosts {
		if h.SSHOverride != nil {
			out[h.ID] = *h.SSHOverride
		}
	}
	return out
}

// migrateCutover serves POST /clusters/:id/migrate/cutover. Dispatches a
// migrate_cutover op via the M2 orchestrator and returns the taskId.
// Progress streams via the existing /operations/:taskId/events SSE endpoint.
func migrateCutover(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Tasks == nil || opts.Clusters == nil || opts.ClusterAdmin == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "migration not wired")
			return
		}
		clusterID := chi.URLParam(r, "id")

		// Confirm the cluster exists and is currently MinIO. Cutover on a
		// Buckit cluster is meaningless and rejecting at dispatch is a
		// nicer UX than failing partway.
		cluster, err := opts.Clusters.Get(r.Context(), clusterID)
		if errors.Is(err, clusters.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}

		// Decode the wire body. The wizard's MigrationDraft is a superset of
		// what the executor needs; FromMigrationBody picks the relevant fields.
		var body migration.MigrationBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		// The URL is the source of truth for source cluster id; ignore any
		// mismatching value in the body.
		body.SourceClusterID = clusterID

		params := migration.FromMigrationBody(body)
		if err := params.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}

		// Confirm admin creds exist; the executor will reload them, but a
		// dispatch-time 404 is friendlier than a mid-task failure.
		if _, err := opts.ClusterAdmin.Get(r.Context(), clusterID); err != nil {
			if errors.Is(err, clusteradmin.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no admin creds for cluster")
				return
			}
			writeError(w, http.StatusInternalServerError, "creds_failed", err.Error())
			return
		}

		// Persist SSH credentials to the cluster_ssh bucket if the wizard
		// asked for it. Done at dispatch (not commit) so the credentials
		// land before the executor even spawns — post-migration operations
		// can read them immediately and rollback can still find them if
		// the cutover fails mid-flight.
		if body.PersistSsh && opts.SSHConfig != nil {
			cfg := domain.ClusterSshConfig{
				SSH:       body.SSH,
				Overrides: overridesFromHosts(body.Hosts),
			}
			if err := opts.SSHConfig.Put(r.Context(), clusterID, cfg); err != nil {
				writeError(w, http.StatusInternalServerError, "ssh_persist_failed", err.Error())
				return
			}
		}

		raw, err := json.Marshal(body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal_failed", err.Error())
			return
		}

		req := tasks.DispatchRequest{
			ClusterID:   clusterID,
			ClusterName: cluster.Name,
			Kind:        tasks.OpMigrateCutover,
			OpLabel:     "Migrate cluster to Buckit",
			Params:      raw,
		}
		taskID, err := opts.Tasks.Dispatch(r.Context(), req)
		switch {
		case err == nil:
			logAction(r, "migrate.cutover", "result", "queued", "cluster_id", clusterID, "hosts", len(params.Hosts), "task_id", taskID)
			writeJSON(w, http.StatusAccepted, tasks.DispatchResponse{TaskID: taskID})
		case errors.Is(err, tasks.ErrClusterBusy):
			logAction(r, "migrate.cutover", "result", "cluster_busy", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusConflict, "cluster_busy", err.Error())
		case errors.Is(err, tasks.ErrUnknownKind):
			logAction(r, "migrate.cutover", "result", "unknown_kind", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "unknown_kind", err.Error())
		default:
			logAction(r, "migrate.cutover", "result", "dispatch_failed", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "dispatch_failed", err.Error())
		}
	}
}

// migrateRollback serves POST /clusters/:id/migrate/rollback. Body shape is
// the same as cutover but the snapshot path is optional — rollback only
// disables buckit.service, removes bm's drop-in, and re-enables
// minio.service. Env files are untouched throughout (the Buckit package
// doesn't ship or modify them), so there's nothing to restore.
func migrateRollback(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Tasks == nil || opts.Clusters == nil || opts.ClusterAdmin == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "migration not wired")
			return
		}
		clusterID := chi.URLParam(r, "id")
		cluster, err := opts.Clusters.Get(r.Context(), clusterID)
		if errors.Is(err, clusters.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}

		var body migration.MigrationBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		body.SourceClusterID = clusterID

		// Rollback validation is a subset of cutover validation — re-running
		// FromMigrationBody + a manual guard keeps the contract clear.
		params := migration.FromMigrationBody(body)
		if len(params.Hosts) == 0 {
			writeError(w, http.StatusBadRequest, "validation_failed", "rollback: at least one host required")
			return
		}
		if params.SSH.User == "" {
			writeError(w, http.StatusBadRequest, "validation_failed", "rollback: ssh user required")
			return
		}

		raw, err := json.Marshal(body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal_failed", err.Error())
			return
		}

		req := tasks.DispatchRequest{
			ClusterID:   clusterID,
			ClusterName: cluster.Name,
			Kind:        tasks.OpMigrateRollback,
			OpLabel:     "Rollback migration to MinIO",
			Params:      raw,
		}
		taskID, err := opts.Tasks.Dispatch(r.Context(), req)
		switch {
		case err == nil:
			logAction(r, "migrate.rollback", "result", "queued", "cluster_id", clusterID, "hosts", len(params.Hosts), "task_id", taskID)
			writeJSON(w, http.StatusAccepted, tasks.DispatchResponse{TaskID: taskID})
		case errors.Is(err, tasks.ErrClusterBusy):
			logAction(r, "migrate.rollback", "result", "cluster_busy", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusConflict, "cluster_busy", err.Error())
		case errors.Is(err, tasks.ErrUnknownKind):
			logAction(r, "migrate.rollback", "result", "unknown_kind", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "unknown_kind", err.Error())
		default:
			logAction(r, "migrate.rollback", "result", "dispatch_failed", "cluster_id", clusterID, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "dispatch_failed", err.Error())
		}
	}
}
