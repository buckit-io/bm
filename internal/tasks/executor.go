package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/buckit-io/bm/internal/store"
)

// Executor implements an OpKind. Validate is called synchronously during
// Dispatch (so the operator gets a 400 if params are malformed); Execute runs
// in its own goroutine with cancellation tied to the per-task context.
type Executor interface {
	// Validate inspects the dispatch request's params blob. Return a
	// descriptive error to reject the dispatch with 400.
	Validate(req DispatchRequest) error
	// Execute runs the op. Helpers on Run publish progress/events. A nil
	// return means succeeded; a non-nil error means failed (the orchestrator
	// rolls it into the history row's failureNote). Honor ctx.Done() to
	// support cancellation.
	Execute(ctx context.Context, run *Run) error
}

// Run is the per-invocation context passed to Executor.Execute.
type Run struct {
	TaskID    string
	ClusterID string
	Kind      OpKind
	Params    json.RawMessage
	Targets   []string
	Hub       *Hub
	Store     *store.Store
}

// LogInfo publishes an info-level OpEvent.
func (r *Run) LogInfo(format string, args ...any)  { r.log("info", format, args...) }
func (r *Run) LogOK(format string, args ...any)    { r.log("ok", format, args...) }
func (r *Run) LogWarn(format string, args ...any)  { r.log("warn", format, args...) }
func (r *Run) LogError(format string, args ...any) { r.log("error", format, args...) }

func (r *Run) log(level, format string, args ...any) {
	r.Hub.PublishEvent(OpEvent{Level: level, Text: fmt.Sprintf(format, args...)})
}

// MutateState atomically updates the operation progress snapshot.
func (r *Run) MutateState(fn func(*OperationProgress)) {
	r.Hub.MutateState(fn)
}

var (
	registryMu sync.RWMutex
	registry   = map[OpKind]Executor{}
)

// Register makes an executor available for kind. Panics if kind is already
// registered — registration is package-init time, not user-facing.
func Register(kind OpKind, e Executor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("tasks: kind %q already registered", kind))
	}
	registry[kind] = e
}

// OverwriteRegister installs e under kind even when an existing executor is
// registered. Used by package-level Register helpers (RegisterNoop,
// RegisterSshProbe, deploy.Register) that want to be idempotent across test
// invocations.
func OverwriteRegister(kind OpKind, e Executor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[kind] = e
}

// Lookup returns the executor for kind, or nil if unknown.
func Lookup(kind OpKind) Executor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[kind]
}

// IsRegistered reports whether kind has an executor.
func IsRegistered(kind OpKind) bool { return Lookup(kind) != nil }
