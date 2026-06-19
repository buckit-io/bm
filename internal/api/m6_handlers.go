package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// newClusterDeploy serves POST /clusters/new/deploy. Dispatches a
// cluster_deploy op via the M2 orchestrator and returns the taskId. Progress
// streams via the existing /operations/:taskId/events SSE endpoint.
func newClusterDeploy(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Tasks == nil || opts.Clusters == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "deploy not wired")
			return
		}
		var draft domain.NewClusterDraft
		if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		params := deploy.FromDraft(draft)
		if err := params.Validate(); err != nil {
			logAction(r, "new_cluster.deploy", "result", "validation_failed", "cluster", draft.Name, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		slug := deploy.SlugifyName(params.Name)
		raw, err := json.Marshal(draft)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal_failed", err.Error())
			return
		}
		req := tasks.DispatchRequest{
			ClusterID:   "deploy-" + slug, // synthetic — real id assigned on commit
			ClusterName: params.Name,
			Kind:        tasks.OpClusterDeploy,
			OpLabel:     "Deploy new cluster",
			Params:      raw,
		}
		taskID, err := opts.Tasks.Dispatch(r.Context(), req)
		switch {
		case err == nil:
			logAction(r, "new_cluster.deploy", "result", "queued", "cluster", params.Name, "slug", slug, "hosts", len(params.Hosts), "mounts", len(params.Topology.SelectedMounts), "task_id", taskID)
			writeJSON(w, http.StatusAccepted, tasks.DispatchResponse{TaskID: taskID})
		case errors.Is(err, tasks.ErrClusterBusy):
			logAction(r, "new_cluster.deploy", "result", "cluster_busy", "cluster", params.Name, "slug", slug, "reason", err.Error())
			writeError(w, http.StatusConflict, "cluster_busy", err.Error())
		case errors.Is(err, tasks.ErrUnknownKind):
			logAction(r, "new_cluster.deploy", "result", "unknown_kind", "cluster", params.Name, "slug", slug, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "unknown_kind", err.Error())
		default:
			logAction(r, "new_cluster.deploy", "result", "dispatch_failed", "cluster", params.Name, "slug", slug, "reason", err.Error())
			writeError(w, http.StatusBadRequest, "dispatch_failed", err.Error())
		}
	}
}

// _ keeps the clusters package imported even when nothing else in this file
// references its types directly.
var _ = clusters.ErrNotFound
