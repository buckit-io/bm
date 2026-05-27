package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// CutoverExecutor implements tasks.Executor for OpMigrateCutover.
//
// Four-phase parallel pipeline (Buckit's distributed binary-checksum
// guard rejects mixed-binary clusters, so a rolling host-by-host
// cutover is fundamentally impossible — every healthy node has to
// flip together):
//
//  1. Pre-stage (cluster healthy, NO downtime): probe minio.service →
//     download Buckit pkg → verify SHA256 → install pkg → write drop-in.
//     Runs concurrently across all attempted hosts. Any failure here
//     aborts the cutover with zero impact on the running cluster.
//  2. Cutover (downtime window): systemctl stop minio.service then
//     enable --now buckit.service, concurrently across all attempted
//     hosts. Downtime is bounded by the slowest single host's stop +
//     enable, not the sum.
//  3. Verify: poll admin ServerInfo until every attempted host reports
//     online or params.ClusterHealthyTimeout elapses.
//  4. On success: commit engine flip. On failure: auto-rollback every
//     attempted host (stop buckit, enable minio).
//
// Hosts that were already offline at cutover start are filtered out
// before phase 1 — they stay on MinIO; the operator re-runs migration
// on each once it's back online.
type CutoverExecutor struct {
	Installer    *Installer
	Clusters     *clusters.Repo
	ClusterAdmin *clusteradmin.Repo
	// AdminPool resolves a cached *admin.Client per cluster. The verify
	// phase uses this to poll ServerInfo.
	AdminPool *admin.Pool
}

// Validate decodes the params and runs invariant checks. Called synchronously
// during Dispatch so the operator gets a 400 for bad input.
func (e *CutoverExecutor) Validate(req tasks.DispatchRequest) error {
	if e.Clusters == nil || e.ClusterAdmin == nil {
		return errors.New("cutover: repos not wired")
	}
	if len(req.Params) == 0 {
		return errors.New("cutover: params required")
	}
	var body MigrationBody
	if err := json.Unmarshal(req.Params, &body); err != nil {
		return fmt.Errorf("cutover: decode params: %w", err)
	}
	params := FromMigrationBody(body)
	if err := params.Validate(); err != nil {
		return err
	}
	// Snapshot must exist on disk — otherwise rollback has nothing to fall
	// back to. Loaded once here so we fail fast.
	if _, err := ReadSnapshot(params.SnapshotPath); err != nil {
		return fmt.Errorf("cutover: %w", err)
	}
	return nil
}

// Execute runs the cutover. Returns nil when every attempted host
// reaches StageDone AND the engine flip persisted; non-nil otherwise
// (the orchestrator marks the history row as failed).
func (e *CutoverExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	var body MigrationBody
	if err := json.Unmarshal(run.Params, &body); err != nil {
		return fmt.Errorf("cutover: decode params: %w", err)
	}
	params := FromMigrationBody(body)
	if err := params.Validate(); err != nil {
		return err
	}

	creds, err := e.ClusterAdmin.Get(ctx, params.SourceClusterID)
	if err != nil {
		return fmt.Errorf("cutover: load admin creds: %w", err)
	}
	params.AdminCreds = creds

	// ----- Partition hosts into attempted vs skipped based on a fresh
	// ServerInfo probe. Skipped hosts are offline at cutover-start;
	// we leave them on MinIO and don't count them toward verify.
	attempted, skippedIDs, err := e.partitionHosts(ctx, params)
	if err != nil {
		return fmt.Errorf("probe cluster state: %w", err)
	}
	if len(attempted) == 0 {
		return errors.New("no hosts online to migrate")
	}

	// Seed per-host status: attempted → pending, skipped → skipped.
	statuses := make([]tasks.HostOpStatus, len(params.Hosts))
	hostIdx := map[string]int{}
	for i, h := range params.Hosts {
		hostIdx[h.ID] = i
		st := tasks.HostOpStatus{HostID: h.ID, Hostname: h.Hostname, State: tasks.HostPending}
		if skippedIDs[h.ID] {
			st.State = tasks.HostSkipped
			st.Detail = "Offline at cutover start — stays on MinIO. Re-run migration on this host once it's back online."
		}
		statuses[i] = st
	}
	total := len(attempted)
	zero := 0
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = statuses
		s.Total = &total
		s.Current = &zero
		s.CurrentStep = "starting cutover"
	})
	if len(skippedIDs) > 0 {
		var skipNames []string
		for _, h := range params.Hosts {
			if skippedIDs[h.ID] {
				skipNames = append(skipNames, h.Hostname)
			}
		}
		run.LogWarn("skipping %d offline host(s): %s", len(skippedIDs), strings.Join(skipNames, ", "))
	}

	emitFor := e.makeEmitter(run, hostIdx)

	// ----- Phase 1: pre-stage all attempted hosts in parallel.
	run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "pre-staging" })
	run.LogInfo("phase 1: pre-staging %d host(s)", len(attempted))
	if err := e.fanOut(ctx, attempted, params, emitFor, e.Installer.PreStage); err != nil {
		// Pre-stage failed — no downtime, no rollback needed.
		return e.failPreStage(run, hostIdx, err)
	}

	// ----- Phase 2: switch all attempted hosts in parallel. Downtime begins here.
	run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "cutover (downtime)" })
	run.LogInfo("phase 2: cutover (downtime window) across %d host(s)", len(attempted))
	if err := e.fanOut(ctx, attempted, params, emitFor, e.Installer.Switch); err != nil {
		// Some hosts may have flipped; some may not. Auto-rollback all.
		return e.autoRollback(ctx, run, attempted, params, emitFor, hostIdx,
			fmt.Errorf("switch failed: %w", err))
	}

	// ----- Phase 3: verify cluster health (attempted hosts only).
	run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "verifying cluster health" })
	run.LogInfo("phase 3: waiting for cluster to report all attempted hosts online")
	if err := waitAttemptedOnline(ctx, e.AdminPool, params, attempted); err != nil {
		return e.autoRollback(ctx, run, attempted, params, emitFor, hostIdx,
			fmt.Errorf("verify failed: %w", err))
	}

	// ----- Success: mark attempted hosts done, flip the cluster engine.
	completed := len(attempted)
	run.MutateState(func(s *tasks.OperationProgress) {
		for _, h := range attempted {
			if idx, ok := hostIdx[h.ID]; ok {
				s.HostStatuses[idx].State = tasks.HostSucceeded
				s.HostStatuses[idx].Detail = "Migrated"
			}
		}
		s.Current = &completed
	})

	if err := e.commitEngineFlip(ctx, params); err != nil {
		return fmt.Errorf("commit engine flip: %w", err)
	}

	// Verify pass for summary (informational, doesn't gate success).
	verify := Verify(ctx, e.AdminPool, params)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Migration complete"
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Hosts migrated", Value: strconv.Itoa(len(attempted))})
		if len(skippedIDs) > 0 {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Hosts skipped (offline)", Value: strconv.Itoa(len(skippedIDs))})
		}
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Buckit version", Value: params.TargetVersion})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Buckets", Value: fmt.Sprintf("%d / %d", verify.BucketsOK.OK, verify.BucketsOK.Total)})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Nodes reporting", Value: fmt.Sprintf("%d / %d", verify.NodesReporting.OK, verify.NodesReporting.Total)})
		if verify.SmokeOK {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Smoke test", Value: "ok"})
		} else {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Smoke test", Value: "fail"})
		}
		if verify.FailureNote != "" {
			s.FailureNote = verify.FailureNote
		}
	})
	return nil
}

// partitionHosts queries ServerInfo and partitions params.Hosts into hosts
// currently online (attempted) and hosts that aren't (skippedIDs is a set of
// HostRow.IDs). If ServerInfo fails, every host is considered attempted —
// the operator started a cutover; we don't get to refuse-by-default.
func (e *CutoverExecutor) partitionHosts(ctx context.Context, params CutoverParams) ([]domain.HostRow, map[string]bool, error) {
	if e.AdminPool == nil {
		// No admin pool — can't partition. Treat every host as attempted.
		return params.Hosts, map[string]bool{}, nil
	}
	client, err := e.AdminPool.Get(params.SourceClusterID, params.AdminCreds)
	if err != nil {
		return params.Hosts, map[string]bool{}, nil
	}
	info, err := client.ServerInfo(ctx)
	if err != nil || info == nil {
		return params.Hosts, map[string]bool{}, nil
	}
	online := map[string]bool{}
	for _, s := range info.Servers {
		if s.State != domain.NodeOnline {
			continue
		}
		host := hostnameFromMinioEndpoint(s.Endpoint)
		if host != "" {
			online[strings.ToLower(host)] = true
		}
	}
	if len(online) == 0 {
		// ServerInfo returned but every node was non-online — refuse to
		// proceed rather than silently flip nothing.
		return nil, nil, errors.New("ServerInfo reported zero online hosts")
	}
	attempted := make([]domain.HostRow, 0, len(params.Hosts))
	skipped := map[string]bool{}
	for _, h := range params.Hosts {
		if online[strings.ToLower(h.Hostname)] {
			attempted = append(attempted, h)
		} else {
			skipped[h.ID] = true
		}
	}
	return attempted, skipped, nil
}

// makeEmitter returns a per-host emit function that propagates StepEvents
// from PreStage/Switch into the run's HostOpStatus rows. Mirrors what the
// old per-host processHost goroutine did, just refactored so phase fan-outs
// can reuse it.
func (e *CutoverExecutor) makeEmitter(run *tasks.Run, hostIdx map[string]int) func(domain.HostRow) func(StepEvent) {
	return func(h domain.HostRow) func(StepEvent) {
		return func(ev StepEvent) {
			idx, ok := hostIdx[ev.HostID]
			if !ok {
				return
			}
			run.MutateState(func(s *tasks.OperationProgress) {
				if idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = stageToHostState(ev.Stage)
					s.HostStatuses[idx].Detail = ev.Detail
				}
				s.CurrentStep = ev.Hostname + ": " + string(ev.Stage)
			})
			if ev.Err != nil {
				run.LogError("%s: %s", ev.Hostname, ev.Err.Error())
			} else {
				run.LogInfo("%s: %s — %s", ev.Hostname, ev.Stage, ev.Detail)
			}
		}
	}
}

// fanOut runs phaseFn against every attempted host in parallel and waits for
// all to complete. Returns the first error; on any error errgroup cancels
// the shared context so remaining hosts abort quickly.
func (e *CutoverExecutor) fanOut(
	ctx context.Context,
	attempted []domain.HostRow,
	params CutoverParams,
	emitFor func(domain.HostRow) func(StepEvent),
	phaseFn func(context.Context, domain.HostRow, CutoverParams, func(StepEvent)) error,
) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range attempted {
		h := h
		g.Go(func() error {
			emit := emitFor(h)
			return phaseFn(gctx, h, params, emit)
		})
	}
	return g.Wait()
}

// failPreStage marks the pre-stage failure on the run state. No hosts have
// stopped minio yet, so there's nothing to roll back.
func (e *CutoverExecutor) failPreStage(run *tasks.Run, hostIdx map[string]int, cause error) error {
	run.MutateState(func(s *tasks.OperationProgress) {
		s.FailureNote = fmt.Sprintf("pre-stage failed: %s — cluster untouched", cause.Error())
		s.CurrentStep = "pre-stage failed"
	})
	run.LogError("pre-stage failed; cluster untouched: %s", cause.Error())
	_ = hostIdx
	return cause
}

// autoRollback reverts every attempted host after a switch or verify
// failure. Runs in two parallel phases — stop-buckit on every host,
// then start-minio on every host — because the distributed bootstrap
// guard rejects any node whose peer is still running a different
// binary. Doing both back-to-back per host (the rolling shape) means
// the first host to enable minio would see peers still running buckit
// and the checksum mismatch would fail-start the unit.
//
// Best-effort: rollback errors are logged but don't change the returned
// error (the operator sees the original cutover failure in FailureNote).
func (e *CutoverExecutor) autoRollback(
	ctx context.Context,
	run *tasks.Run,
	attempted []domain.HostRow,
	params CutoverParams,
	emitFor func(domain.HostRow) func(StepEvent),
	hostIdx map[string]int,
	cause error,
) error {
	run.MutateState(func(s *tasks.OperationProgress) {
		s.CurrentStep = "rolling back: stopping buckit"
		s.FailureNote = "cutover failed; rolling back attempted hosts: " + cause.Error()
	})
	run.LogError("cutover failed: %s — rolling back %d attempted host(s)", cause.Error(), len(attempted))

	// Use a fresh context for rollback so cancellation of the cutover ctx
	// doesn't kill the rollback mid-flight. Cap rollback total time.
	rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	stopErr := e.fanOut(rbCtx, attempted, params, emitFor, e.Installer.StopBuckit)
	if stopErr != nil {
		run.LogError("auto-rollback stop-buckit partial: %s", stopErr.Error())
		// Continue anyway — even a partial stop is better than leaving
		// some hosts running buckit. The start-minio phase will fail
		// loudly on whichever hosts didn't successfully stop.
	}

	run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "rolling back: starting minio" })
	startErr := e.fanOut(rbCtx, attempted, params, emitFor, e.Installer.StartMinio)
	if startErr != nil {
		run.LogError("auto-rollback start-minio partial: %s", startErr.Error())
		run.MutateState(func(s *tasks.OperationProgress) {
			s.FailureNote = fmt.Sprintf(
				"cutover failed (%s) AND rollback failed (%s). Manual intervention required: stop buckit.service and start minio.service on the affected hosts.",
				cause.Error(), startErr.Error())
		})
		return cause
	}
	if stopErr != nil {
		// MinIO came back even though stop-buckit had stragglers — log
		// the partial but treat the rollback as recovered.
		run.MutateState(func(s *tasks.OperationProgress) {
			s.FailureNote = fmt.Sprintf("cutover failed (%s); rollback recovered but stop-buckit had errors: %s", cause.Error(), stopErr.Error())
		})
	}

	run.LogInfo("auto-rollback complete — all attempted hosts back on MinIO")
	run.MutateState(func(s *tasks.OperationProgress) {
		for _, h := range attempted {
			if idx, ok := hostIdx[h.ID]; ok {
				s.HostStatuses[idx].State = tasks.HostFailed
				s.HostStatuses[idx].Detail = "Rolled back to MinIO"
			}
		}
	})
	return cause
}

// commitEngineFlip flips the Cluster row's Engine from EngineMinio to
// EngineBuckit, bumps version + lastFetchedAt, and stamps MigratedFrom for
// the UI's "Migrated from MinIO" banner.
func (e *CutoverExecutor) commitEngineFlip(ctx context.Context, params CutoverParams) error {
	c, err := e.Clusters.Get(ctx, params.SourceClusterID)
	if err != nil {
		return err
	}
	c.Engine = domain.EngineBuckit
	if params.Name != "" {
		c.Name = params.Name
	}
	if params.Description != "" {
		c.Description = params.Description
	}
	c.Version = params.TargetVersion
	now := time.Now().UTC()
	c.LastFetchedAt = &now
	c.LastActivityAt = now
	c.MigratedFrom = &domain.MigratedFrom{
		Product:     "minio",
		Version:     "",
		FinalizedAt: now,
	}
	if snap, sErr := ReadSnapshot(params.SnapshotPath); sErr == nil && snap != nil {
		c.MigratedFrom.Version = snap.Version
	}
	return e.Clusters.Put(ctx, c)
}

// waitAttemptedOnline polls ServerInfo until every host in `attempted` reports
// online, or params.ClusterHealthyTimeout (default 5m) elapses. Skipped hosts
// can be in any state — only the hosts we actually cut over count.
func waitAttemptedOnline(ctx context.Context, pool *admin.Pool, params CutoverParams, attempted []domain.HostRow) error {
	if pool == nil {
		return errors.New("verify: no admin pool")
	}
	timeout := params.ClusterHealthyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	client, err := pool.Get(params.SourceClusterID, params.AdminCreds)
	if err != nil {
		return fmt.Errorf("admin client: %w", err)
	}
	want := map[string]bool{}
	for _, h := range attempted {
		want[strings.ToLower(h.Hostname)] = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.ServerInfo(ctx)
		if err == nil && info != nil {
			online := map[string]bool{}
			for _, s := range info.Servers {
				if s.State != domain.NodeOnline {
					continue
				}
				host := hostnameFromMinioEndpoint(s.Endpoint)
				if host != "" {
					online[strings.ToLower(host)] = true
				}
			}
			allUp := true
			for h := range want {
				if !online[h] {
					allUp = false
					break
				}
			}
			if allUp {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("attempted hosts not online after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// stageToHostState maps the cutover Stage onto the wire-level HostOpState
// the UI's HostOpStatus table renders.
//
// StageRolledBack is treated as a *running* state: it's emitted by every
// step of StopBuckit + StartMinio during the rollback path, none of which
// are terminal. The actual terminal state ("Rolled back to MinIO" detail
// on HostSucceeded for manual rollback; HostFailed + "Rolled back to
// MinIO" detail for cutover auto-rollback) is set explicitly by the
// executor's final MutateState after all phases complete. Mapping
// StageRolledBack to HostFailed here would flash the per-host pill as
// "Failed" during normal rollback progress, which is confusing UX.
func stageToHostState(s Stage) tasks.HostOpState {
	switch s {
	case StagePending:
		return tasks.HostPending
	case StageDone:
		return tasks.HostSucceeded
	case StageFailed:
		return tasks.HostFailed
	default:
		return tasks.HostRunning
	}
}
