package tasks

import (
	"sync"
	"time"
)

const (
	subscriberBuffer = 256
	postTerminalGrace = 30 * time.Second
)

// Frame is what the hub publishes to subscribers. State frames update the
// snapshot; event frames flow through as discrete activity entries. SSE
// handlers translate each frame into an `event: state` or `event: log` line.
type Frame struct {
	State *OperationProgress // non-nil for snapshot updates
	Event *OpEvent           // non-nil for activity events
}

// Hub is the per-task fan-out pub/sub used by the orchestrator.
//
// Lifecycle:
//   1. Orchestrator creates a Hub.
//   2. SSE handlers Subscribe — each gets a buffered channel + unsubscribe fn.
//   3. Executor calls PublishState / PublishEvent as it runs.
//   4. On terminal: orchestrator calls Close(terminal) — the hub flushes the
//      terminal state, sleeps postTerminalGrace, then closes all subscriber
//      channels and refuses new subscribers.
//
// Slow subscribers don't block the publisher: if a subscriber's buffer is
// full at publish time, that subscriber's channel is closed (the SSE handler
// treats the closed channel as "stream gone, reconnect" and the EventSource
// reconnect loop picks up where it left off via Last-Event-ID).
type Hub struct {
	mu       sync.Mutex
	state    OperationProgress
	subs     map[*subscription]struct{}
	closed   bool
	closeCh  chan struct{}
}

type subscription struct {
	ch chan Frame
}

// NewHub returns a Hub seeded with initial.State == running.
func NewHub(taskID string) *Hub {
	return &Hub{
		state: OperationProgress{
			TaskID: taskID,
			State:  StateRunning,
		},
		subs:    make(map[*subscription]struct{}),
		closeCh: make(chan struct{}),
	}
}

// Subscribe returns a channel of frames + an unsubscribe function. The
// unsubscribe is safe to call multiple times and must be called to release
// resources. If the hub is already closed, the returned channel is closed.
func (h *Hub) Subscribe() (<-chan Frame, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sub := &subscription{ch: make(chan Frame, subscriberBuffer)}
	if h.closed {
		close(sub.ch)
		return sub.ch, func() {}
	}
	h.subs[sub] = struct{}{}

	// Send the current snapshot so late subscribers don't miss state.
	sub.ch <- Frame{State: cloneState(&h.state)}

	return sub.ch, func() { h.unsubscribe(sub) }
}

func (h *Hub) unsubscribe(sub *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	close(sub.ch)
}

// Snapshot returns a copy of the current OperationProgress. Safe to call from
// any goroutine.
func (h *Hub) Snapshot() OperationProgress {
	h.mu.Lock()
	defer h.mu.Unlock()
	return *cloneState(&h.state)
}

// PublishState replaces the snapshot with state and fans it out. Callers that
// want to mutate fields incrementally should fetch the snapshot, modify, and
// publish the new value — the hub takes ownership of the passed-in struct.
func (h *Hub) PublishState(state OperationProgress) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.state = state
	h.fanout(Frame{State: cloneState(&h.state)})
}

// MutateState atomically applies fn to the snapshot and publishes the result.
// fn MUST NOT call back into the hub.
func (h *Hub) MutateState(fn func(*OperationProgress)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	fn(&h.state)
	h.fanout(Frame{State: cloneState(&h.state)})
}

// PublishEvent appends ev to the snapshot's event list and fans the event out.
// The event's timestamp is set to time.Now() if zero.
func (h *Hub) PublishEvent(ev OpEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	h.state.Events = append(h.state.Events, ev)
	evCopy := ev
	h.fanout(Frame{Event: &evCopy})
}

// CloseAfterTerminal records the final state, fans it out, then schedules
// channel teardown after the grace period so late SSE readers still see the
// terminal frame. Safe to call multiple times; subsequent calls are no-ops.
func (h *Hub) CloseAfterTerminal(final OperationProgress) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.state = final
	h.fanout(Frame{State: cloneState(&h.state)})
	h.mu.Unlock()

	go func() {
		select {
		case <-time.After(postTerminalGrace):
		case <-h.closeCh:
		}
		h.shutdown()
	}()
}

// Shutdown immediately tears down all subscriptions. Used at process shutdown.
func (h *Hub) Shutdown() {
	close(h.closeCh)
	h.shutdown()
}

func (h *Hub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subs {
		close(sub.ch)
	}
	h.subs = nil
}

// fanout must be called with h.mu held.
func (h *Hub) fanout(f Frame) {
	for sub := range h.subs {
		select {
		case sub.ch <- f:
		default:
			// Slow consumer: drop them. They reconnect via Last-Event-ID.
			delete(h.subs, sub)
			close(sub.ch)
		}
	}
}

func cloneState(s *OperationProgress) *OperationProgress {
	cp := *s
	if s.HostStatuses != nil {
		cp.HostStatuses = append([]HostOpStatus(nil), s.HostStatuses...)
	}
	if s.Summary != nil {
		cp.Summary = append([]SummaryItem(nil), s.Summary...)
	}
	if s.Events != nil {
		cp.Events = append([]OpEvent(nil), s.Events...)
	}
	if s.Current != nil {
		v := *s.Current
		cp.Current = &v
	}
	if s.Total != nil {
		v := *s.Total
		cp.Total = &v
	}
	return &cp
}
