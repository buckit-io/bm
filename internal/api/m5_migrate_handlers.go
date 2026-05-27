package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/migration"
	"github.com/buckit-io/bm/internal/preflight"
	"github.com/buckit-io/bm/internal/tasks"
)

const migratePreflightOpKind tasks.OpKind = "migrate_preflight"

// migrateSnapshot serves POST /clusters/:id/migrate/snapshot.
func migrateSnapshot(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil || opts.ClusterAdmin == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repos not configured")
			return
		}
		clusterID := chi.URLParam(r, "id")
		creds, err := opts.ClusterAdmin.Get(r.Context(), clusterID)
		if errors.Is(err, clusteradmin.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no admin creds for cluster")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "creds_failed", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		dir := snapshotsDir(opts)
		snap, path, err := migration.Snapshot(ctx, dir, clusterID, creds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "snapshot_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"snapshot": snap,
			"summary":  migration.Summarize(snap),
			"path":     path,
		})
	}
}

// migratePreflight serves POST /clusters/:id/migrate/preflight. Writes a
// history row — unlike new-cluster preflight, this is a one-shot pre-cutover
// audit signal.
func migratePreflight(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil || opts.ClusterAdmin == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repos not configured")
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
		creds, err := opts.ClusterAdmin.Get(r.Context(), clusterID)
		if errors.Is(err, clusteradmin.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no admin creds for cluster")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "creds_failed", err.Error())
			return
		}

		// Body may carry a draft override (typically the wizard-side migrate
		// step pre-populates discovery); otherwise we run with an empty draft
		// and the checks fall back to "skipped" where they need discovery.
		var draft domain.NewClusterDraft
		if r.ContentLength > 0 {
			_ = json.NewDecoder(r.Body).Decode(&draft)
		}
		// Prefer the body's hosts when the client provided them — that's
		// where wizard-level edits like per-host SSH port and credential
		// overrides live. Fall back to the persisted cluster nodes only
		// when the body is empty (e.g. direct API callers).
		if len(draft.Hosts) == 0 {
			draft.Hosts = hostsFromClusterNodes(opts, r.Context(), clusterID)
		}
		// Hydrate draft.Discovery from cluster node rows so the arch/os
		// uniformity checks have data to read. The migrate flow has no
		// host-SSH discovery step (the cluster was imported via the admin
		// API), so without this the OS/Arch checks would always fail with
		// "re-run discovery" — there's nothing for the operator to re-run.
		hydrateDiscoveryFromNodes(opts, r.Context(), clusterID, &draft)

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		conn := &poolHostConn{pool: opts.SSHPool, ssh: draft.SSH, httpClient: &http.Client{Timeout: 10 * time.Second}}
		results := preflight.RunCatalog(ctx, conn, draft, migration.Catalog(creds))

		// Write a history row so the migration audit shows when preflight last ran.
		if opts.Tasks != nil {
			_ = recordMigratePreflightHistory(ctx, opts, cluster, results)
		}

		writeJSON(w, http.StatusOK, results)
	}
}

// hydrateDiscoveryFromNodes fills draft.Discovery with WizardDiscoveryResult
// entries derived from the cluster's node rows (OS, Arch). Skips nodes that
// already have an entry in the body, so a future migrate flow that does run
// host-SSH discovery wouldn't be overwritten.
func hydrateDiscoveryFromNodes(opts Options, ctx context.Context, clusterID string, draft *domain.NewClusterDraft) {
	if opts.Nodes == nil {
		return
	}
	ns, err := opts.Nodes.List(ctx, clusterID)
	if err != nil {
		return
	}
	if draft.Discovery == nil {
		draft.Discovery = make(map[string]domain.WizardDiscoveryResult, len(ns))
	}
	for _, n := range ns {
		if _, ok := draft.Discovery[n.ID]; ok {
			continue
		}
		draft.Discovery[n.ID] = domain.WizardDiscoveryResult{
			State: domain.WizardDiscoveryDone,
			OS:    n.OS,
			Arch:  n.Arch,
		}
	}
}

func hostsFromClusterNodes(opts Options, ctx context.Context, clusterID string) []domain.HostRow {
	if opts.Nodes == nil {
		return nil
	}
	ns, err := opts.Nodes.List(ctx, clusterID)
	if err != nil {
		return nil
	}
	out := make([]domain.HostRow, 0, len(ns))
	for _, n := range ns {
		port := n.SSHPort
		if port == 0 {
			port = 22
		}
		out = append(out, domain.HostRow{
			ID:       n.ID,
			Hostname: n.Hostname,
			Port:     port,
			Probe:    domain.HostProbeReachable,
		})
	}
	return out
}

func snapshotsDir(opts Options) string {
	if opts.AliasPath != "" {
		// AliasPath is `<configDir>/config.json` — sibling snapshots dir.
		return filepath.Join(filepath.Dir(opts.AliasPath), "snapshots")
	}
	return "snapshots"
}

func recordMigratePreflightHistory(ctx context.Context, opts Options, cluster domain.Cluster, results []domain.PreflightResult) error {
	// Dispatch a synthetic, immediately-terminal history row so the History
	// page surfaces the preflight run. We don't go through the orchestrator
	// because there's no executor body to run — the work already happened.
	failures := 0
	warnings := 0
	for _, r := range results {
		switch r.Result {
		case domain.PreflightFail:
			failures++
		case domain.PreflightWarn:
			warnings++
		}
	}
	status := tasks.StateSucceeded
	note := ""
	if failures > 0 {
		status = tasks.StateFailed
		note = "migration preflight surfaced blocking failures"
	}
	// History records are typically written by the orchestrator; we emit a
	// stub row directly via tasks.Manager so this audit trail is visible
	// without inventing a new code path.
	return opts.Tasks.RecordImmediate(ctx, tasks.HistoryEntry{
		OpKind:      migratePreflightOpKind,
		OpLabel:     "Migration preflight",
		ClusterID:   cluster.ID,
		ClusterName: cluster.Name,
		Status:      status,
		FailureNote: note,
		Result: &tasks.OperationResult{
			State:       status,
			Detail:      preflightSummary(failures, warnings, len(results)),
			FailureNote: note,
		},
	})
}

func preflightSummary(failures, warnings, total int) string {
	if failures == 0 && warnings == 0 {
		return "All preflight checks passed."
	}
	out := ""
	if failures > 0 {
		out += commaJoin(out, plural(failures, "failure"))
	}
	if warnings > 0 {
		out += commaJoin(out, plural(warnings, "warning"))
	}
	out += " of " + plural(total, "check")
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return shortInt(n) + " " + word + "s"
}

func shortInt(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(digits[n%10]) + out
		n /= 10
	}
	return out
}

func commaJoin(soFar, next string) string {
	if soFar == "" {
		return next
	}
	return soFar + ", " + next
}
