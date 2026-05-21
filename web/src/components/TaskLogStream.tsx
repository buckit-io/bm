import { useEffect, useRef, useState } from "react";
import { getOperationProgress } from "../api/client";
import { subscribeOperationEvents, SseError } from "../api/sse";
import type { OpEvent, OperationProgress } from "../api/types";
import "./TaskLogStream.css";

interface TaskLogStreamProps {
  taskId: string;
  maxLines?: number;
  // Hide the pause/resume control. Used on wizard Deploy/Migrate steps
  // where there's no value in pausing a live deploy log.
  showPause?: boolean;
}

export function TaskLogStream({
  taskId,
  maxLines = 200,
  showPause = true,
}: TaskLogStreamProps) {
  const [lines, setLines] = useState<OpEvent[]>([]);
  const [paused, setPaused] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    setLines([]);
    setStreamError(null);
    const ctrl = new AbortController();
    const applySnapshotEvents = (progress: OperationProgress) => {
      if (pausedRef.current) return;
      if (!progress.events || progress.events.length === 0) return;
      setLines(progress.events.slice(-maxLines));
    };

    getOperationProgress(taskId, ctrl.signal)
      .then(applySnapshotEvents)
      .catch(() => {});

    subscribeOperationEvents(taskId, {
      signal: ctrl.signal,
      onProgress: applySnapshotEvents,
      onEvent: (ev) => {
        if (pausedRef.current) return;
        setLines((prev) => {
          const next = [...prev, ev];
          if (next.length > maxLines) next.splice(0, next.length - maxLines);
          return next;
        });
      },
    }).catch((err) => {
      if (err instanceof DOMException && err.name === "AbortError") return;
      if (err instanceof SseError && err.status === 501) {
        setStreamError("Live log not yet wired for this view.");
        return;
      }
      const message = err instanceof Error ? err.message : "Stream closed.";
      setStreamError(message);
    });
    return () => ctrl.abort();
  }, [taskId, maxLines]);

  useEffect(() => {
    if (paused || !scrollRef.current) return;
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [lines, paused]);

  return (
    <div className="logstream">
      <header className="logstream__header">
        <span className="muted" style={{ fontSize: "var(--fs-sm)" }}>
          Live log
        </span>
        {showPause && (
          <button className="btn btn--sm btn--ghost" onClick={() => setPaused((v) => !v)}>
            {paused ? "▶ Resume" : "⏸ Pause"}
          </button>
        )}
      </header>
      <div className="logstream__body" ref={scrollRef}>
        {streamError ? (
          <div className="subtle" style={{ padding: "var(--s-3)" }}>
            {streamError}
          </div>
        ) : lines.length === 0 ? (
          <div className="subtle" style={{ padding: "var(--s-3)" }}>
            Waiting for log lines…
          </div>
        ) : (
          lines.map((l, i) => (
            <div
              key={i}
              className={"logstream__line logstream__line--" + (l.level ?? "info")}
            >
              <span className="logstream__ts">
                {formatTs(l.ts)}
              </span>
              <span className="logstream__src">{glyph(l.level)}</span>
              <span className="logstream__text">{l.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleTimeString();
  } catch {
    return "";
  }
}

function glyph(level?: OpEvent["level"]): string {
  switch (level) {
    case "ok":
      return "✓";
    case "warn":
      return "⚠";
    case "error":
      return "✗";
    default:
      return "·";
  }
}
