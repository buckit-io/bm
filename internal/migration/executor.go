package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// CutoverExecutor implements tasks.Executor for OpMigrateCutover.
//
// Sequential per-host pipeline: stop minio → install buckit → swap unit →
// wait local-healthy → wait cluster-healthy → next host. Any host failure
// halts the loop; remaining hosts stay on minio. The persisted Cluster row
// flips from EngineMinio to EngineBuckit only after every host reports done
// AND the post-cutover Verify pass completes.
type CutoverExecutor struct {
	Installer    *Installer
	Clusters     *clusters.Repo
	ClusterAdmin *clusteradmin.Repo
	// AdminPool resolves a cached *admin.Client per cluster. The cluster-
	// healthy wait between hosts uses this to call ServerInfo.
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

// Execute runs the cutover. Returns nil when every host reaches StageDone
// AND the engine flip persisted; non-nil otherwise (the orchestrator marks
// the history row as failed and the cluster is left mid-cutover).
func (e *CutoverExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	var body MigrationBody
	if err := json.Unmarshal(run.Params, &body); err != nil {
		return fmt.Errorf("cutover: decode params: %w", err)
	}
	params := FromMigrationBody(body)
	if err := params.Validate(); err != nil {
		return err
	}

	// Load admin creds for the source cluster so we can drive the cluster-
	// healthy poll between hosts.
	creds, err := e.ClusterAdmin.Get(ctx, params.SourceClusterID)
	if err != nil {
		return fmt.Errorf("cutover: load admin creds: %w", err)
	}
	params.AdminCreds = creds

	// Seed per-host pending state.
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
		s.CurrentStep = "starting cutover"
	})

	hostIdx := map[string]int{}
	for i, h := range params.Hosts {
		hostIdx[h.ID] = i
	}

	processHost := func(h domain.HostRow) error {
		stepCh := make(chan StepEvent, 32)
		go func() {
			for ev := range stepCh {
				idx := hostIdx[ev.HostID]
				stage := ev.Stage
				detail := ev.Detail
				run.MutateState(func(s *tasks.OperationProgress) {
					if idx < len(s.HostStatuses) {
						s.HostStatuses[idx].State = stageToHostState(stage)
						s.HostStatuses[idx].Detail = detail
					}
					s.CurrentStep = ev.Hostname + ": " + string(stage)
				})
				if ev.Err != nil {
					run.LogError("%s: %s", ev.Hostname, ev.Err.Error())
				} else {
					run.LogInfo("%s: %s — %s", ev.Hostname, stage, detail)
				}
			}
		}()
		emit := func(ev StepEvent) { stepCh <- ev }
		err := e.Installer.Install(ctx, h, params, emit)
		close(stepCh)
		return err
	}

	// Sequential cutover. Halt on first failure so the operator can roll
	// back the affected hosts without further damage.
	completed := 0
	for i, h := range params.Hosts {
		if err := ctx.Err(); err != nil {
			return e.markCanceled(run, params, h, err)
		}

		if err := processHost(h); err != nil {
			// Distinguish cancellation from a real host failure. The
			// orchestrator's terminal-state mapping already flips StateFailed
			// → StateCanceled when ctx.Err() == context.Canceled, but the
			// FailureNote we set here is preserved verbatim — the operator
			// reads it on the History page, so phrasing matters.
			if errors.Is(ctx.Err(), context.Canceled) {
				return e.markCanceled(run, params, h, err)
			}
			run.MutateState(func(s *tasks.OperationProgress) {
				s.FailureNote = fmt.Sprintf("cutover failed on %s: %s", h.Hostname, err.Error())
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = tasks.HostFailed
					s.HostStatuses[idx].Detail = err.Error()
				}
			})
			return fmt.Errorf("host %s: %w", h.Hostname, err)
		}

		completed++
		c := completed
		run.MutateState(func(s *tasks.OperationProgress) { s.Current = &c })

		// Cluster-healthy wait between hosts. Skipped after the last host
		// because the post-cutover Verify step does the equivalent.
		if i < len(params.Hosts)-1 {
			run.MutateState(func(s *tasks.OperationProgress) {
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = stageToHostState(StageWaitingCluster)
					s.HostStatuses[idx].Detail = "Waiting cluster-healthy"
				}
				s.CurrentStep = h.Hostname + ": waiting_cluster"
			})
			run.LogInfo("%s: waiting cluster-healthy", h.Hostname)
			if err := waitClusterHealthy(ctx, e.AdminPool, params); err != nil {
				run.MutateState(func(s *tasks.OperationProgress) {
					s.FailureNote = fmt.Sprintf("cluster did not stabilize after %s: %s", h.Hostname, err.Error())
				})
				return fmt.Errorf("cluster health after %s: %w", h.Hostname, err)
			}
			run.MutateState(func(s *tasks.OperationProgress) {
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = tasks.HostSucceeded
					s.HostStatuses[idx].Detail = "Done"
				}
			})
		} else {
			run.MutateState(func(s *tasks.OperationProgress) {
				if idx := hostIdx[h.ID]; idx < len(s.HostStatuses) {
					s.HostStatuses[idx].State = tasks.HostSucceeded
					s.HostStatuses[idx].Detail = "Done"
				}
			})
		}
	}

	// All hosts succeeded — flip the persisted Cluster row.
	if err := e.commitEngineFlip(ctx, params); err != nil {
		return fmt.Errorf("commit engine flip: %w", err)
	}

	// Verify pass — informational. Failures don't auto-rollback.
	verify := Verify(ctx, e.AdminPool, params)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Migration complete"
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Hosts migrated", Value: strconv.Itoa(len(params.Hosts))})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Buckit version", Value: params.TargetVersion})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Buckets", Value: fmt.Sprintf("%d / %d", verify.BucketsOK.OK, verify.BucketsOK.Total)})
		s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Nodes reporting", Value: fmt.Sprintf("%d / %d", verify.NodesReporting.OK, verify.NodesReporting.Total)})
		if verify.SmokeOK {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Smoke test", Value: "ok"})
		} else {
			s.Summary = append(s.Summary, tasks.SummaryItem{Label: "Smoke test", Value: "fail"})
		}
		if verify.FailureNote != "" {
			s.FailureNote = verify.FailureNote // surfaces as a warning on the history row
		}
	})
	return nil
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
		Version:     "", // recorded via the snapshot's Version field, not the cluster row
		FinalizedAt: now,
	}
	// Recover the snapshot to record the source MinIO version.
	if snap, sErr := ReadSnapshot(params.SnapshotPath); sErr == nil && snap != nil {
		c.MigratedFrom.Version = snap.Version
	}
	return e.Clusters.Put(ctx, c)
}

// markCanceled records the partial cutover state into the run's progress
// before returning the cancellation error. The orchestrator uses this to
// surface a clean "canceled at <host> in stage <stage>" history row.
//
// The earlier hosts (already past StageDone) keep their HostSucceeded state
// so the operator sees exactly what was migrated. The current host gets a
// dedicated Detail string, and any later hosts stay HostPending — they are
// still on MinIO. Rollback can target the earlier set.
func (e *CutoverExecutor) markCanceled(run *tasks.Run, params CutoverParams, current domain.HostRow, cause error) error {
	stage := "unknown"
	run.MutateState(func(s *tasks.OperationProgress) {
		for i, hs := range s.HostStatuses {
			if hs.HostID == current.ID {
				if hs.Detail != "" {
					stage = hs.Detail
				}
				s.HostStatuses[i].State = tasks.HostFailed
				s.HostStatuses[i].Detail = "Canceled mid-host"
				break
			}
		}
		s.FailureNote = fmt.Sprintf("cutover canceled at %s (stage: %s)", current.Hostname, stage)
		s.CurrentStep = "canceled"
	})
	run.LogWarn("cutover canceled at %s — earlier hosts remain on Buckit; later hosts still on MinIO", current.Hostname)
	return cause
}

// waitClusterHealthy polls admin.ServerInfo until every server reports
// online. Honors params.ClusterHealthyTimeout; default 120s. Used between
// hosts so the next host doesn't start while the cluster is still settling.
func waitClusterHealthy(ctx context.Context, pool *admin.Pool, params CutoverParams) error {
	if pool == nil {
		return errors.New("cluster health: no admin pool")
	}
	timeout := params.ClusterHealthyTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	deadline := time.Now().Add(timeout)
	client, err := pool.Get(params.SourceClusterID, params.AdminCreds)
	if err != nil {
		return fmt.Errorf("admin client: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.ServerInfo(ctx)
		if err == nil && info != nil {
			online := 0
			for _, s := range info.Servers {
				if s.State == domain.NodeOnline {
					online++
				}
			}
			if len(info.Servers) > 0 && online == len(info.Servers) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cluster not healthy after %s", timeout)
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
func stageToHostState(s Stage) tasks.HostOpState {
	switch s {
	case StagePending:
		return tasks.HostPending
	case StageDone:
		return tasks.HostSucceeded
	case StageFailed:
		return tasks.HostFailed
	case StageRolledBack:
		return tasks.HostFailed
	default:
		return tasks.HostRunning
	}
}
