package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/tasks"
)

// RollbackExecutor implements tasks.Executor for OpMigrateRollback.
//
// Reverts hosts that already reached StageDone in the cutover back to MinIO:
// stop buckit.service, restore /etc/default/minio from the per-host backup,
// re-enable minio.service, wait healthy. Hosts that never made it past
// StagePending are left untouched (minio is still running on them).
//
// The body accepts an explicit `hosts` list; the API handler picks it from
// the operator's selection. When empty, every host in the source cluster is
// probed for buckit.service active and rolled back if so.
type RollbackExecutor struct {
	Installer    *Installer
	Clusters     *clusters.Repo
	ClusterAdmin *clusteradmin.Repo
	AdminPool    *admin.Pool
	SSHPool      *bmssh.Pool
}

// Validate decodes the params and runs invariant checks.
func (e *RollbackExecutor) Validate(req tasks.DispatchRequest) error {
	if e.Clusters == nil || e.ClusterAdmin == nil {
		return errors.New("rollback: repos not wired")
	}
	if len(req.Params) == 0 {
		return errors.New("rollback: params required")
	}
	var body MigrationBody
	if err := json.Unmarshal(req.Params, &body); err != nil {
		return fmt.Errorf("rollback: decode params: %w", err)
	}
	params := FromMigrationBody(body)
	// Rollback uses the same params shape as cutover but doesn't require a
	// snapshot — the env-file backup on each host is the source of truth.
	if params.SourceClusterID == "" {
		return errors.New("rollback: sourceClusterId required")
	}
	if len(params.Hosts) == 0 {
		return errors.New("rollback: at least one host required")
	}
	if params.SSH.User == "" {
		return errors.New("rollback: ssh user required")
	}
	return nil
}

// Execute reverses the cutover. Returns nil when every requested host
// reaches StageRolledBack, or once we've decided the work is a no-op.
func (e *RollbackExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	var body MigrationBody
	if err := json.Unmarshal(run.Params, &body); err != nil {
		return fmt.Errorf("rollback: decode params: %w", err)
	}
	params := FromMigrationBody(body)

	// Admin creds are loaded best-effort. If the cluster row already exists
	// but creds are missing (operator removed them mid-migration), we still
	// proceed — rollback is purely SSH-side.
	if creds, err := e.ClusterAdmin.Get(ctx, params.SourceClusterID); err == nil {
		params.AdminCreds = creds
	}

	statuses := make([]tasks.HostOpStatus, len(params.Hosts))
	for i, h := range params.Hosts {
		statuses[i] = tasks.HostOpStatus{HostID: h.ID, Hostname: h.Hostname, State: tasks.HostPending}
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = statuses
		total := len(params.Hosts)
		zero := 0
		s.Total = &total
		s.Current = &zero
		s.CurrentStep = "starting rollback"
	})

	hostIdx := map[string]int{}
	for i, h := range params.Hosts {
		hostIdx[h.ID] = i
	}

	// Partition hosts into "running buckit" (rollback target) and
	// "already on MinIO" (skip). Probing serially keeps the SSH pool
	// busy modestly during a phase that's a no-op for already-on-MinIO
	// hosts.
	targets := make([]domain.HostRow, 0, len(params.Hosts))
	skipped := 0
	for _, h := range params.Hosts {
		if err := ctx.Err(); err != nil {
			run.MutateState(func(s *tasks.OperationProgress) {
				s.FailureNote = "rollback canceled at " + h.Hostname
			})
			return err
		}
		active, probeErr := e.probeBuckitActive(ctx, h, params)
		if probeErr != nil {
			run.LogWarn("%s: probe buckit.service failed: %s — assuming buckit active", h.Hostname, probeErr.Error())
			active = true
		}
		if !active {
			run.LogInfo("%s: buckit.service not active — skipping", h.Hostname)
			run.MutateState(func(s *tasks.OperationProgress) {
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = tasks.HostSucceeded
					s.HostStatuses[idx].Detail = "Already on MinIO"
				}
			})
			skipped++
			continue
		}
		targets = append(targets, h)
	}

	emitFor := func(h domain.HostRow) func(StepEvent) {
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

	completed := 0
	if len(targets) > 0 {
		// Phase 1: stop buckit + uninstall + remove drop-in on every
		// target in parallel. Must complete across all hosts before
		// phase 2 starts minio — otherwise the first host to enable
		// minio.service dials peers still running buckit, hits the
		// distributed binary-checksum guard, and fails to start.
		run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "stopping buckit" })
		if err := e.fanOut(ctx, targets, params, emitFor, e.Installer.StopBuckit); err != nil {
			run.MutateState(func(s *tasks.OperationProgress) {
				s.FailureNote = fmt.Sprintf("rollback (stop buckit) failed: %s", err.Error())
			})
			return fmt.Errorf("stop buckit: %w", err)
		}

		// Phase 2: enable --now minio on every target in parallel.
		run.MutateState(func(s *tasks.OperationProgress) { s.CurrentStep = "starting minio" })
		if err := e.fanOut(ctx, targets, params, emitFor, e.Installer.StartMinio); err != nil {
			run.MutateState(func(s *tasks.OperationProgress) {
				s.FailureNote = fmt.Sprintf("rollback (start minio) failed: %s — manual intervention may be required", err.Error())
			})
			return fmt.Errorf("start minio: %w", err)
		}
		completed = len(targets)
		c := completed
		run.MutateState(func(s *tasks.OperationProgress) {
			s.Current = &c
			for _, h := range targets {
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = tasks.HostSucceeded
					s.HostStatuses[idx].Detail = "Rolled back to MinIO"
				}
			}
		})
	}

	// If at least one host was actually rolled back, flip the cluster row's
	// engine back to MinIO. A pure no-op rollback (every host already on
	// MinIO) leaves the cluster row alone.
	if completed > 0 {
		if err := e.commitEngineRevert(ctx, params); err != nil {
			return fmt.Errorf("revert cluster engine: %w", err)
		}
	}

	run.MutateState(func(s *tasks.OperationProgress) {
		switch {
		case completed == 0 && skipped > 0:
			s.Detail = "No hosts required rollback"
		case completed > 0 && skipped == 0:
			s.Detail = "Rollback complete"
		default:
			s.Detail = "Rollback partial — some hosts already on MinIO"
		}
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Rolled back", Value: strconv.Itoa(completed)})
		if skipped > 0 {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Already MinIO", Value: strconv.Itoa(skipped)})
		}
	})
	return nil
}

// fanOut runs phaseFn against every target host in parallel and waits for
// all to complete. Returns the first error; errgroup cancels its shared
// context on any failure so remaining hosts abort quickly.
func (e *RollbackExecutor) fanOut(
	ctx context.Context,
	targets []domain.HostRow,
	params CutoverParams,
	emitFor func(domain.HostRow) func(StepEvent),
	phaseFn func(context.Context, domain.HostRow, CutoverParams, func(StepEvent)) error,
) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range targets {
		h := h
		g.Go(func() error {
			emit := emitFor(h)
			return phaseFn(gctx, h, params, emit)
		})
	}
	return g.Wait()
}

// probeBuckitActive checks whether buckit.service is currently the active
// service on the host. Returns (active=true) when systemctl reports active;
// any non-zero exit is treated as inactive. Errors propagate so the caller
// can decide whether to fall through.
func (e *RollbackExecutor) probeBuckitActive(ctx context.Context, host domain.HostRow, params CutoverParams) (bool, error) {
	if e.SSHPool == nil {
		// Without an SSH pool we can't probe — assume active.
		return true, errors.New("rollback: no ssh pool")
	}
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := e.SSHPool.Get(probeCtx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		return false, err
	}
	r, err := bmssh.Run(probeCtx, client, "systemctl is-active buckit.service")
	if err != nil {
		return false, err
	}
	return r.ExitCode == 0, nil
}

// commitEngineRevert flips the cluster row's Engine back to MinIO and
// clears MigratedFrom. The rollback caller has already confirmed that at
// least one host was reverted.
func (e *RollbackExecutor) commitEngineRevert(ctx context.Context, params CutoverParams) error {
	c, err := e.Clusters.Get(ctx, params.SourceClusterID)
	if err != nil {
		return err
	}
	c.Engine = domain.EngineMinio
	now := time.Now().UTC()
	c.LastFetchedAt = &now
	c.LastActivityAt = now
	// Clear MigratedFrom so the UI's "Migrated from MinIO" banner doesn't
	// linger after a rollback.
	c.MigratedFrom = nil
	// Restore the source MinIO version if we can recover it; the Cluster
	// row's Version was overwritten on cutover commit, so falling back to
	// "" is acceptable — Refresh will repopulate it on next probe.
	c.Version = ""
	return e.Clusters.Put(ctx, c)
}
