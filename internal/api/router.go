// Package api builds the chi router that serves /api/v1 and the embedded /
// (web/dist) static assets.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	bmassets "github.com/buckit-io/bm"
	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/alias"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/preflight"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
	"github.com/buckit-io/bm/internal/update"
	"github.com/buckit-io/bm/internal/version"
)

// Options configures the router.
type Options struct {
	Store        *store.Store
	Tasks        *tasks.Manager
	Nodes        *nodes.Repo
	SSHConfig    *sshconfig.Repo
	SSHPool      *bmssh.Pool
	Clusters     *clusters.Repo
	ClusterAdmin *clusteradmin.Repo
	AdminPool    *admin.Pool
	// AliasPath is the on-disk path the bm-cli alias bridge writes to. When
	// non-empty, commit + delete handlers re-sync after mutating state.
	AliasPath string
	// AliasSync is the function that flushes cluster state to AliasPath.
	// Defaults to alias.Sync when nil so production wiring stays terse.
	AliasSync func(ctx context.Context, s *store.Store, path string) error
	// WebDist optionally overrides the embedded UI assets with files from disk.
	// Used for local development against a freshly built web/dist tree.
	WebDist string
	Updater *update.Service
	// Shutdown requests bm web process shutdown. It is nil when the router is
	// mounted outside the real bm web lifecycle, such as tests.
	Shutdown func()
}

// resolveAliasSync returns opts.AliasSync or the package default.
func resolveAliasSync(opts Options) func(ctx context.Context, s *store.Store, path string) error {
	if opts.AliasSync != nil {
		return opts.AliasSync
	}
	return alias.Sync
}

// New returns the chi router. It is safe to mount as the http.Handler of any
// http.Server; nothing else is wired in.
func New(opts Options) http.Handler {
	preflight.SetVersionResolver(func(tag string) string {
		v := deploy.VersionByTag(tag)
		if v == nil {
			return ""
		}
		return v.RpmURL
	})

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(requestLogger())
	r.Use(jsonRecoverer())
	r.Use(jsonContentType())

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/healthz", healthz)

		// Read paths the UI calls on first paint.
		r.Get("/clusters", listClusters(opts.Clusters))
		r.Get("/clusters/{id}", getCluster(opts.Clusters))
		r.Get("/settings", currentSettings)
		r.Get("/manager/update", getManagerUpdate(opts.Updater))
		r.Post("/manager/update/apply", postManagerUpdateApply(opts.Updater))
		r.Post("/manager/shutdown", postManagerShutdown(opts.Shutdown))
		r.Get("/sessions/me", loopbackMe)

		// M4: discovery + cluster lifecycle.
		r.Post("/clusters/import/discover", postDiscover())
		r.Post("/clusters/import/commit", postCommit(opts))
		r.Delete("/clusters/{id}", deleteCluster(opts))
		r.Post("/clusters/refresh", refreshAllClusters(opts))
		r.Post("/clusters/{id}/refresh", refreshOneCluster(opts))
		r.Get("/clusters/{id}/healthinfo", getClusterHealthInfo(opts))

		// M5: new-cluster + migration preflight + artifact helpers.
		r.Get("/artifacts/versions", listVersions())
		r.Post("/artifacts/validate", validateArtifact())
		r.Post("/clusters/new/discover", newClusterDiscover(opts.SSHPool))
		r.Post("/clusters/new/preflight", newClusterPreflight(opts.SSHPool))
		r.Post("/local-deployments/preview", previewLocalDeployment())
		r.Post("/local-deployments/prepare", prepareLocalDeployment())
		r.Post("/clusters/{id}/migrate/snapshot", migrateSnapshot(opts))
		r.Post("/clusters/{id}/migrate/preflight", migratePreflight(opts))
		r.Post("/clusters/{id}/migrate/cutover", migrateCutover(opts))
		r.Post("/clusters/{id}/migrate/rollback", migrateRollback(opts))

		// M6: new-cluster deploy.
		r.Post("/clusters/new/deploy", newClusterDeploy(opts))

		// M2: operations + history (real handlers).
		r.Post("/operations", postOperation(opts.Tasks))
		r.Get("/operations/{taskId}", getOperation(opts.Tasks))
		r.Post("/operations/{taskId}/cancel", cancelOperation(opts.Tasks))
		r.Get("/operations/{taskId}/events", operationEvents(opts.Tasks))
		r.Get("/history", listHistory(opts.Tasks))
		r.Delete("/history", clearHistory(opts.Tasks))

		// M3: SSH layer + node CRUD.
		r.Get("/clusters/{id}/nodes", listNodes(opts.Nodes))
		r.Get("/clusters/{id}/nodes/{nodeId}", getNode(opts.Nodes))
		r.Get("/clusters/{id}/nodes/{nodeId}/logs", getNodeLogs(opts))
		r.Get("/clusters/{id}/admin-creds", getAdminCreds(opts.ClusterAdmin))
		r.Put("/clusters/{id}/admin-creds", putAdminCreds(opts.ClusterAdmin, opts.AdminPool))
		r.Get("/clusters/{id}/ssh", getSSHConfig(opts.SSHConfig))
		r.Put("/clusters/{id}/ssh", putSSHConfig(opts.SSHConfig, opts.Nodes, opts.SSHPool))
		r.Post("/clusters/{id}/ssh/test", testSSHConfig())

		// Everything else: shape-correct 501 with the milestone tag so the
		// caller can route around features that haven't landed yet.
		r.HandleFunc("/*", stubNotImplemented)
	})

	r.Get("/*", staticHandler(opts.WebDist))
	return r
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

const managerShutdownDelay = 100 * time.Millisecond

func postManagerShutdown(shutdown func()) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if shutdown == nil {
			writeError(w, http.StatusServiceUnavailable, "shutdown_unavailable", "shutdown is not wired")
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{"status": "shutting_down"})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		go func() {
			time.Sleep(managerShutdownDelay)
			shutdown()
		}()
	}
}

func postOperation(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusInternalServerError, "no_manager", "task manager not configured")
			return
		}
		var req tasks.DispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		taskID, err := mgr.Dispatch(r.Context(), req)
		switch {
		case err == nil:
			logAction(r, "ops.dispatch",
				"result", "queued",
				"op_kind", string(req.Kind),
				"cluster_id", req.ClusterID,
				"task_id", taskID,
				"hosts", len(req.TargetHostIDs),
			)
			writeJSON(w, http.StatusAccepted, tasks.DispatchResponse{TaskID: taskID})
		case errors.Is(err, tasks.ErrUnknownKind):
			logAction(r, "ops.dispatch",
				"result", "unknown_kind",
				"op_kind", string(req.Kind),
				"cluster_id", req.ClusterID,
				"reason", err.Error(),
			)
			writeError(w, http.StatusBadRequest, "unknown_kind", err.Error())
		case errors.Is(err, tasks.ErrClusterBusy):
			logAction(r, "ops.dispatch",
				"result", "cluster_busy",
				"op_kind", string(req.Kind),
				"cluster_id", req.ClusterID,
				"reason", err.Error(),
			)
			writeError(w, http.StatusConflict, "cluster_busy", err.Error())
		default:
			logAction(r, "ops.dispatch",
				"result", "dispatch_failed",
				"op_kind", string(req.Kind),
				"cluster_id", req.ClusterID,
				"reason", err.Error(),
			)
			writeError(w, http.StatusBadRequest, "dispatch_failed", err.Error())
		}
	}
}

func getOperation(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusInternalServerError, "no_manager", "task manager not configured")
			return
		}
		id := chi.URLParam(r, "taskId")
		progress, err := mgr.Snapshot(id)
		if errors.Is(err, tasks.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "task not found")
			return
		}
		writeJSON(w, http.StatusOK, progress)
	}
}

func cancelOperation(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusInternalServerError, "no_manager", "task manager not configured")
			return
		}
		id := chi.URLParam(r, "taskId")
		err := mgr.Cancel(id)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, tasks.ErrTaskNotFound):
			writeError(w, http.StatusNotFound, "not_found", "task not found")
		case errors.Is(err, tasks.ErrNotRunning):
			writeError(w, http.StatusConflict, "not_running", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "cancel_failed", err.Error())
		}
	}
}

func operationEvents(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeError(w, http.StatusInternalServerError, "no_manager", "task manager not configured")
			return
		}
		id := chi.URLParam(r, "taskId")
		streamOperation(w, r, mgr, id)
	}
}

func listHistory(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			writeJSON(w, http.StatusOK, []any{})
			return
		}
		q := r.URL.Query()
		filter := tasks.HistoryFilter{
			Status:    tasks.OperationState(q.Get("status")),
			ClusterID: q.Get("clusterId"),
		}
		if s := q.Get("since"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				filter.Since = t
			}
		}
		if s := q.Get("until"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				filter.Until = t
			}
		}
		if s := q.Get("limit"); s != "" {
			var n int
			if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
				filter.Limit = n
			}
		}
		rows, err := mgr.ListHistory(r.Context(), filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "history_list_failed", err.Error())
			return
		}
		if rows == nil {
			rows = []tasks.HistoryEntry{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func clearHistory(mgr *tasks.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mgr == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var before time.Time
		if s := r.URL.Query().Get("before"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				before = t
			}
		}
		n, err := mgr.ClearHistory(r.Context(), before)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "history_clear_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
	}
}

func loopbackMe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"username": "admin", "goos": runtime.GOOS})
}

func currentSettings(w http.ResponseWriter, _ *http.Request) {
	// Default settings struct until /PATCH-settings persistence lands in a
	// later milestone.
	writeJSON(w, http.StatusOK, map[string]any{
		"remoteAccess": map[string]any{"enabled": false},
		"versionPin":   nil,
		"version":      version.Version,
	})
}

func stubNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error":     "not_implemented",
		"milestone": milestoneFor(r.Method, r.URL.Path),
		"path":      r.URL.Path,
		"method":    r.Method,
	})
}

// milestoneFor returns the milestone tag for an endpoint that hasn't landed
// yet. Keeps the milestone table close to the routes so it's easy to keep in
// sync with backend-design.md § REST API.
func milestoneFor(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/operations"):
		// /operations + /operations/:id + /events + /cancel are M2;
		// real-cluster executors land in M3/M7 but the dispatch path itself
		// answers now. If we hit this branch it's an unrouted variant.
		return "M3-M7"
	case strings.HasPrefix(path, "/api/v1/sessions/"):
		return "M-remote-access"
	case method == "PATCH" && path == "/api/v1/settings":
		return "M-remote-access"
	}
	return "Mn"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}

func jsonContentType() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func jsonRecoverer() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Log the stack server-side, but only send a generic JSON body to the client.
					fmt.Fprintf(os.Stderr, "panic %v: %s\n%s\n", r.URL.Path, rec, debug.Stack())
					writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func requestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			// Skip noisy static asset hits.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				fmt.Fprintf(os.Stderr, "%s %s %d %s\n", r.Method, r.URL.Path, ww.Status(), time.Since(start))
			}
		})
	}
}

// staticHandler serves the React app from disk when a valid webDist override
// exists, otherwise it falls back to the embedded release bundle.
func staticHandler(webDist string) http.HandlerFunc {
	if handler, ok := diskStaticHandler(webDist); ok {
		return handler
	}
	if handler, ok := embeddedStaticHandler(); ok {
		return handler
	}
	return missingDistHandler(webDist)
}

func diskStaticHandler(webDist string) (http.HandlerFunc, bool) {
	if strings.TrimSpace(webDist) == "" {
		return nil, false
	}
	info, err := os.Stat(webDist)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	indexPath := filepath.Join(webDist, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return nil, false
	}
	fileServer := http.FileServer(http.Dir(webDist))
	return func(w http.ResponseWriter, r *http.Request) {
		// Serve the file when it exists on disk; otherwise fall through to
		// index.html so the SPA router can render the path client-side.
		clean := filepath.Clean(r.URL.Path)
		target := filepath.Join(webDist, clean)
		if _, err := os.Stat(target); err == nil && clean != "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, indexPath)
	}, true
}

func embeddedStaticHandler() (http.HandlerFunc, bool) {
	uiFS, err := bmassets.WebDist()
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(uiFS, "index.html"); err != nil {
		return nil, false
	}
	fileServer := http.FileServer(http.FS(uiFS))
	return func(w http.ResponseWriter, r *http.Request) {
		// URL paths always use forward slashes. filepath.Clean converts them
		// to backslashes on Windows, which embedded fs.FS rejects and would
		// make every asset fall through to index.html.
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean != "" && clean != "." {
			if _, err := fs.Stat(uiFS, clean); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(uiFS, "index.html")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "ui_read_failed", err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}, true
}

func missingDistHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			// API path bled through to the static handler — shouldn't happen
			// in practice, but guard against it returning HTML.
			writeError(w, http.StatusNotFound, "not_found", "no route")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, "bm web: no UI assets available (disk override: %s)\nrun `cd web && npm run build` before building bm.\n", path)
	}
}

// ErrNonLoopbackBind is returned by AssertLoopback for any non-loopback host.
var ErrNonLoopbackBind = errors.New("non-loopback bind requires remote-access mode (not yet implemented)")

// AssertLoopback rejects addresses that bind beyond the local interface. M1
// only supports loopback; the remote-access milestone lifts this.
func AssertLoopback(addr string) error {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "127.0.0.1", "::1", "localhost":
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrNonLoopbackBind, addr)
	}
}
