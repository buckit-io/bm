package tasks

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/buckit-io/bm/internal/store"
)

// Errors returned by Dispatch. Wired to specific HTTP status codes by the
// router.
var (
	ErrUnknownKind  = errors.New("unknown operation kind")
	ErrClusterBusy  = errors.New("cluster already has an operation in flight")
	ErrTaskNotFound = errors.New("task not found")
	ErrNotRunning   = errors.New("task already terminal")
)

// Manager owns the orchestrator state: per-cluster locks, in-memory taskId
// hubs, and the bbolt-backed history table. Construct one per process.
type Manager struct {
	store *store.Store
	hist  *historyStore

	clusterLocksMu sync.Mutex
	clusterLocks   map[string]*sync.Mutex // held while an op is in flight on that cluster

	runsMu sync.Mutex
	runs   map[string]*runState // taskId → live state
}

type runState struct {
	hub       *Hub
	cancel    context.CancelFunc
	clusterID string
	startedAt time.Time
}

// NewManager wires the manager against an open store. Caller is expected to
// call RegisterNoop() (or other Register* functions) before Dispatch is called.
func NewManager(s *store.Store) *Manager {
	return &Manager{
		store:        s,
		hist:         newHistoryStore(s),
		clusterLocks: map[string]*sync.Mutex{},
		runs:         map[string]*runState{},
	}
}

// Dispatch validates the request, takes the per-cluster lock, inserts a
// running history row, and spawns the executor in a goroutine. Returns the
// taskId clients use to subscribe to progress.
func (m *Manager) Dispatch(ctx context.Context, req DispatchRequest) (string, error) {
	exec := Lookup(req.Kind)
	if exec == nil {
		return "", fmt.Errorf("%w: %s", ErrUnknownKind, req.Kind)
	}
	if req.ClusterID == "" {
		return "", fmt.Errorf("clusterId is required")
	}
	if err := exec.Validate(req); err != nil {
		return "", err
	}

	lock := m.clusterLock(req.ClusterID)
	if !lock.TryLock() {
		return "", fmt.Errorf("%w: %s", ErrClusterBusy, req.ClusterID)
	}

	taskID, err := newTaskID()
	if err != nil {
		lock.Unlock()
		return "", err
	}
	now := time.Now().UTC()
	entry := HistoryEntry{
		ID:          taskID,
		At:          now,
		OpKind:      req.Kind,
		OpLabel:     orDefault(req.OpLabel, string(req.Kind)),
		ClusterID:   req.ClusterID,
		ClusterName: orDefault(req.ClusterName, req.ClusterID),
		HostScope:   req.HostScope,
		Status:      StateRunning,
	}
	if err := m.hist.Insert(ctx, entry); err != nil {
		lock.Unlock()
		return "", fmt.Errorf("history insert: %w", err)
	}

	hub := NewHub(taskID)
	runCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{hub: hub, cancel: cancel, clusterID: req.ClusterID, startedAt: now}
	m.putRun(taskID, rs)

	go m.executeRun(runCtx, exec, req, taskID, rs)

	return taskID, nil
}

func (m *Manager) executeRun(ctx context.Context, exec Executor, req DispatchRequest, taskID string, rs *runState) {
	defer m.clusterLock(rs.clusterID).Unlock()
	defer rs.cancel() // satisfy lostcancel — graceful path completes before this fires

	run := &Run{
		TaskID:    taskID,
		ClusterID: req.ClusterID,
		Kind:      req.Kind,
		Params:    req.Params,
		Targets:   req.TargetHostIDs,
		Hub:       rs.hub,
		Store:     m.store,
	}

	execErr := safeExecute(exec, ctx, run)
	endedAt := time.Now().UTC()
	duration := endedAt.Sub(rs.startedAt).Seconds()

	finalState := rs.hub.Snapshot()
	finalState.TaskID = taskID
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
		finalState.State = StateCanceled
		if finalState.FailureNote == "" {
			finalState.FailureNote = "canceled"
		}
	case execErr != nil:
		finalState.State = StateFailed
		if finalState.FailureNote == "" {
			finalState.FailureNote = execErr.Error()
		}
	default:
		finalState.State = StateSucceeded
	}

	result := snapshotResult(&finalState)
	updateErr := m.hist.Update(context.Background(), taskID, func(e *HistoryEntry) {
		e.Status = finalState.State
		d := duration
		e.DurationSec = &d
		e.FailureNote = finalState.FailureNote
		e.Result = &result
	})
	if updateErr != nil {
		fmt.Printf("tasks: history update for %s failed: %v\n", taskID, updateErr)
	}

	rs.hub.CloseAfterTerminal(finalState)

	// Drop in-memory record so /operations/:id 404s after the grace window.
	go func() {
		time.Sleep(postTerminalGrace + time.Second)
		m.removeRun(taskID)
	}()

	if _, err := m.hist.Sweep(context.Background()); err != nil {
		fmt.Printf("tasks: history sweep failed: %v\n", err)
	}
}

func safeExecute(exec Executor, ctx context.Context, run *Run) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("executor panicked: %v", rec)
		}
	}()
	return exec.Execute(ctx, run)
}

// Snapshot returns the live progress for taskId, or ErrTaskNotFound.
func (m *Manager) Snapshot(taskID string) (OperationProgress, error) {
	rs := m.getRun(taskID)
	if rs == nil {
		return OperationProgress{}, ErrTaskNotFound
	}
	return rs.hub.Snapshot(), nil
}

// Subscribe returns a frame channel + unsubscribe for taskId, or ErrTaskNotFound.
func (m *Manager) Subscribe(taskID string) (<-chan Frame, func(), error) {
	rs := m.getRun(taskID)
	if rs == nil {
		return nil, nil, ErrTaskNotFound
	}
	ch, unsub := rs.hub.Subscribe()
	return ch, unsub, nil
}

// Cancel signals the executor for taskId to stop. Returns ErrTaskNotFound or
// ErrNotRunning as appropriate.
func (m *Manager) Cancel(taskID string) error {
	rs := m.getRun(taskID)
	if rs == nil {
		return ErrTaskNotFound
	}
	state := rs.hub.Snapshot().State
	if state.IsTerminal() {
		return ErrNotRunning
	}
	rs.cancel()
	return nil
}

// ListHistory returns history rows matching f, newest-first.
func (m *Manager) ListHistory(ctx context.Context, f HistoryFilter) ([]HistoryEntry, error) {
	return m.hist.List(ctx, f)
}

// RecordImmediate writes a terminal history row directly, bypassing the
// orchestrator. Used by request/response endpoints (e.g., migrate preflight)
// that want an audit row but have no executor body to run — the work already
// happened synchronously inside the HTTP handler. The id + at fields are set
// automatically when zero.
func (m *Manager) RecordImmediate(ctx context.Context, entry HistoryEntry) error {
	if entry.ID == "" {
		id, err := newTaskID()
		if err != nil {
			return err
		}
		entry.ID = id
	}
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if !entry.Status.IsTerminal() {
		entry.Status = StateSucceeded
	}
	return m.hist.Insert(ctx, entry)
}

// ClearHistory deletes history rows. If before.IsZero(), wipes all. Returns
// the count deleted.
func (m *Manager) ClearHistory(ctx context.Context, before time.Time) (int, error) {
	return m.hist.Clear(ctx, before)
}

// Shutdown tears down all in-flight runs. Called at process shutdown.
func (m *Manager) Shutdown() {
	m.runsMu.Lock()
	runs := make([]*runState, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.runsMu.Unlock()
	for _, r := range runs {
		r.cancel()
		r.hub.Shutdown()
	}
}

func (m *Manager) clusterLock(clusterID string) *sync.Mutex {
	m.clusterLocksMu.Lock()
	defer m.clusterLocksMu.Unlock()
	lock, ok := m.clusterLocks[clusterID]
	if !ok {
		lock = &sync.Mutex{}
		m.clusterLocks[clusterID] = lock
	}
	return lock
}

func (m *Manager) putRun(taskID string, rs *runState) {
	m.runsMu.Lock()
	m.runs[taskID] = rs
	m.runsMu.Unlock()
}

func (m *Manager) getRun(taskID string) *runState {
	m.runsMu.Lock()
	defer m.runsMu.Unlock()
	return m.runs[taskID]
}

func (m *Manager) removeRun(taskID string) {
	m.runsMu.Lock()
	delete(m.runs, taskID)
	m.runsMu.Unlock()
}

func newTaskID() (string, error) {
	entropy := ulid.Monotonic(rand.Reader, 0)
	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func snapshotResult(p *OperationProgress) OperationResult {
	return OperationResult{
		State:        p.State,
		Detail:       p.Detail,
		Summary:      append([]SummaryItem(nil), p.Summary...),
		HostStatuses: append([]HostOpStatus(nil), p.HostStatuses...),
		FailureNote:  p.FailureNote,
	}
}
