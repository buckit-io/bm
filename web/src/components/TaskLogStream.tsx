import { useEffect, useRef, useState } from "react";
import { LogLine, subscribeTaskLog } from "../mock/api";
import "./TaskLogStream.css";

interface TaskLogStreamProps {
  taskId: string;
  maxLines?: number;
  filter?: string | null;
  // Hide the pause/resume control. Used on wizard Deploy step where
  // there's no value in pausing a live deploy log.
  showPause?: boolean;
}

export function TaskLogStream({
  taskId,
  maxLines = 200,
  filter,
  showPause = true,
}: TaskLogStreamProps) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [paused, setPaused] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    const unsub = subscribeTaskLog(taskId, (line) => {
      if (pausedRef.current) return;
      setLines((prev) => {
        const next = [...prev, line];
        if (next.length > maxLines) next.splice(0, next.length - maxLines);
        return next;
      });
    });
    return unsub;
  }, [taskId, maxLines]);

  useEffect(() => {
    if (paused || !scrollRef.current) return;
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [lines, paused]);

  const visible = filter
    ? lines.filter((l) => l.source === filter)
    : lines;

  return (
    <div className="logstream">
      <header className="logstream__header">
        <span className="muted" style={{ fontSize: "var(--fs-sm)" }}>
          Live log {filter ? `· filtered to ${filter}` : ""}
        </span>
        {showPause && (
          <button className="btn btn--sm btn--ghost" onClick={() => setPaused((v) => !v)}>
            {paused ? "▶ Resume" : "⏸ Pause"}
          </button>
        )}
      </header>
      <div className="logstream__body" ref={scrollRef}>
        {visible.length === 0 ? (
          <div className="subtle" style={{ padding: "var(--s-3)" }}>
            Waiting for log lines…
          </div>
        ) : (
          visible.map((l, i) => (
            <div key={i} className="logstream__line">
              <span className="logstream__ts">
                {new Date(l.ts).toLocaleTimeString()}
              </span>
              <span className="logstream__src">{l.source}</span>
              <span className="logstream__text">{l.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
