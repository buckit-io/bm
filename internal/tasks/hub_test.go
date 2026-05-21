package tasks

import (
	"testing"
	"time"
)

func TestHubFanout(t *testing.T) {
	h := NewHub("t1")
	c1, u1 := h.Subscribe()
	c2, u2 := h.Subscribe()
	defer u1()
	defer u2()

	// Subscribers get the initial snapshot frame.
	drainOne(t, c1)
	drainOne(t, c2)

	h.PublishEvent(OpEvent{Text: "step 1"})
	for _, ch := range []<-chan Frame{c1, c2} {
		select {
		case f := <-ch:
			if f.Event == nil || f.Event.Text != "step 1" {
				t.Fatalf("want event step 1, got %+v", f)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber missed event")
		}
	}
}

func TestHubSlowSubscriberEvicted(t *testing.T) {
	h := NewHub("t1")
	ch, unsub := h.Subscribe()
	defer unsub()
	drainOne(t, ch) // initial snapshot

	// Fill the buffer without reading.
	for i := 0; i < subscriberBuffer+5; i++ {
		h.PublishEvent(OpEvent{Text: "spam"})
	}

	// Drain until closed.
	closed := false
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	if !closed {
		t.Fatal("slow subscriber was not evicted")
	}
}

func TestHubLateSubscriberSeesSnapshot(t *testing.T) {
	h := NewHub("t1")
	h.MutateState(func(s *OperationProgress) {
		s.Detail = "halfway"
	})

	ch, unsub := h.Subscribe()
	defer unsub()
	select {
	case f := <-ch:
		if f.State == nil || f.State.Detail != "halfway" {
			t.Fatalf("late subscriber missed snapshot: %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot delivered")
	}
}

func TestHubCloseAfterTerminal(t *testing.T) {
	// Shorten grace via direct hub manipulation: spin up a sub, publish
	// terminal, then close immediately and observe channel close.
	h := NewHub("t1")
	ch, unsub := h.Subscribe()
	defer unsub()
	drainOne(t, ch)

	final := OperationProgress{TaskID: "t1", State: StateSucceeded}
	h.CloseAfterTerminal(final)
	// Receive terminal frame.
	select {
	case f := <-ch:
		if f.State == nil || f.State.State != StateSucceeded {
			t.Fatalf("expected terminal succeeded frame, got %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("missed terminal frame")
	}

	// Trigger immediate shutdown (avoids waiting the 30s grace).
	h.Shutdown()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel close after Shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not close after Shutdown")
	}
}

func drainOne(t *testing.T, ch <-chan Frame) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected initial frame")
	}
}
