package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// NoopParams configures the noop executor. All fields are optional.
type NoopParams struct {
	// Events to emit before terminating. Defaults to 5.
	Events int `json:"events,omitempty"`
	// Interval between events. Defaults to 200ms.
	IntervalMs int `json:"intervalMs,omitempty"`
	// If > 0, the executor fails after this many events with FailureNote.
	FailAfter   int    `json:"failAfter,omitempty"`
	FailureNote string `json:"failureNote,omitempty"`
	// Optional per-host progression to simulate orchestrated ops.
	PerHost []NoopHost `json:"perHost,omitempty"`
}

type NoopHost struct {
	HostID   string `json:"hostId"`
	Hostname string `json:"hostname"`
}

type noopExecutor struct{}

// RegisterNoop installs the noop executor under OpNoop. Idempotent across
// calls — useful for tests that bring a fresh manager up.
func RegisterNoop() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[OpNoop] = noopExecutor{}
}

func (noopExecutor) Validate(req DispatchRequest) error {
	if len(req.Params) == 0 {
		return nil
	}
	var p NoopParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return fmt.Errorf("noop: invalid params: %w", err)
	}
	if p.Events < 0 || p.FailAfter < 0 || p.IntervalMs < 0 {
		return fmt.Errorf("noop: negative param")
	}
	return nil
}

func (noopExecutor) Execute(ctx context.Context, run *Run) error {
	p := NoopParams{Events: 5, IntervalMs: 200}
	if len(run.Params) > 0 {
		_ = json.Unmarshal(run.Params, &p)
	}
	if p.Events <= 0 {
		p.Events = 5
	}
	if p.IntervalMs <= 0 {
		p.IntervalMs = 200
	}

	if len(p.PerHost) > 0 {
		statuses := make([]HostOpStatus, len(p.PerHost))
		for i, h := range p.PerHost {
			statuses[i] = HostOpStatus{HostID: h.HostID, Hostname: h.Hostname, State: HostPending}
		}
		run.MutateState(func(s *OperationProgress) {
			s.HostStatuses = statuses
			s.Total = intPtr(len(p.PerHost))
			s.Current = intPtr(0)
		})
	}

	interval := time.Duration(p.IntervalMs) * time.Millisecond

	for i := 0; i < p.Events; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		run.LogInfo("noop event %d/%d", i+1, p.Events)

		if len(p.PerHost) > 0 {
			run.MutateState(func(s *OperationProgress) {
				if i < len(s.HostStatuses) {
					s.HostStatuses[i].State = HostSucceeded
				}
				s.Current = intPtr(i + 1)
			})
		}

		if p.FailAfter > 0 && i+1 >= p.FailAfter {
			note := p.FailureNote
			if note == "" {
				note = fmt.Sprintf("noop: synthetic failure after %d events", p.FailAfter)
			}
			run.LogError("%s", note)
			return fmt.Errorf("%s", note)
		}
	}

	run.LogOK("noop completed (%d events)", p.Events)
	run.MutateState(func(s *OperationProgress) {
		s.Detail = fmt.Sprintf("noop completed (%d events)", p.Events)
	})
	return nil
}

func intPtr(v int) *int { return &v }
