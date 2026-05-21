package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/buckit-io/bm/internal/tasks"
)

const sseHeartbeat = 15 * time.Second

// streamOperation pipes Frames from the task hub into an SSE response. Honors
// Last-Event-ID by skipping replayed events (the orchestrator publishes a
// snapshot first, then live events; the snapshot carries the full event list
// so clients reconnect lossless).
func streamOperation(w http.ResponseWriter, r *http.Request, mgr *tasks.Manager, taskID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no_streaming", "server does not support streaming")
		return
	}

	ch, unsub, err := mgr.Subscribe(taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if anyone proxies us
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	ctx := r.Context()
	eventID := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame, ok := <-ch:
			if !ok {
				// Hub closed. Tell the client this is the end of the stream
				// rather than the connection dropping mid-flight.
				writeSSE(w, eventID, "end", `{"reason":"hub_closed"}`)
				flusher.Flush()
				return
			}
			eventID++
			if frame.State != nil {
				body, _ := json.Marshal(frame.State)
				writeSSE(w, eventID, "state", string(body))
				if frame.State.State.IsTerminal() {
					flusher.Flush()
					// Keep the connection open so the client gets the hub_closed
					// "end" frame on the next read; that gives the EventSource
					// reconnect logic a clean signal that this task is done.
					continue
				}
			}
			if frame.Event != nil {
				body, _ := json.Marshal(frame.Event)
				writeSSE(w, eventID, "log", string(body))
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, id int, event, data string) {
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, data)
}

// streamCtx pulls the request context (helper used by tests).
func streamCtx(r *http.Request) context.Context { return r.Context() }
