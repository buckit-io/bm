// Unified modal for every cluster operation. Lifecycle:
//
//   input → dispatching → terminal              (signal flavor)
//   input → dispatching → running → terminal    (orchestrated flavor)
//
// Lock rules:
//   - input phase: no backdrop/ESC/X close. Cancel button closes.
//   - dispatching: no close at all.
//   - running (orchestrated only): no close.
//   - terminal: Close button only.
//
// Browser tab close triggers a beforeunload warning while a non-
// terminal phase is active; result is still recorded in History.

import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  cancelOperation,
  dispatchOperation,
  getOperationProgress,
  HostOpState,
  OperationProgress,
  OpEvent,
} from "../../../mock/api";
import { Pill } from "../../../components/Pill";
import { Cluster } from "../../../mock/data";
import { OperationDef } from "./defs";

interface Props {
  def: OperationDef;
  cluster: Cluster;
  // Restrict the orchestrated progression to a subset of hosts. Used
  // by the bulk-selection bar on /clusters/:id (N hosts) and the per-
  // node Actions menu (one host). Cluster-wide ops omit this prop.
  targetHostIds?: string[];
  // Free-text scope label shown after the cluster name in the header.
  // e.g., "on 3 hosts" or "on prod-east-node1". Optional; the dispatch
  // path computes its own label for the History row.
  scopeLabel?: string;
  onClose: (result: "success" | "failed" | "canceled") => void;
}

type Phase = "input" | "dispatching" | "running" | "terminal";

export function OperationModal({
  def,
  cluster,
  targetHostIds,
  scopeLabel,
  onClose,
}: Props) {
  const [phase, setPhase] = useState<Phase>("input");
  const [stepIdx, setStepIdx] = useState(0);
  const [params, setParams] = useState<unknown>(def.initialParams);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [progress, setProgress] = useState<OperationProgress | null>(null);
  const pollRef = useRef<number | null>(null);

  // beforeunload guard while we're not in a terminal phase.
  useEffect(() => {
    if (phase === "terminal") return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Modern browsers ignore the message but still show a generic
      // warning when preventDefault is called.
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [phase]);

  // Poll the operation progress while running.
  useEffect(() => {
    if (phase !== "running") return;
    if (!taskId) return;
    const tick = () => {
      const p = getOperationProgress(taskId);
      if (!p) return;
      setProgress(p);
      // Transition to terminal only when (a) the top-level state has
      // settled and (b) no host is still in flight. The second guard
      // matters during a cancel: state flips to "canceled" right
      // away, but the in-flight host needs another moment to wrap up.
      if (p.state !== "running") {
        const anyHostRunning = p.hostStatuses?.some(
          (h) => h.state === "running",
        );
        if (!anyHostRunning) {
          setPhase("terminal");
          if (pollRef.current) window.clearInterval(pollRef.current);
          pollRef.current = null;
        }
      }
    };
    tick();
    pollRef.current = window.setInterval(tick, 500);
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current);
      pollRef.current = null;
    };
  }, [phase, taskId]);

  const currentStep = def.inputSteps[stepIdx];
  const isLastStep = stepIdx === def.inputSteps.length - 1;
  const canAdvance = currentStep
    ? currentStep.canAdvance(params, cluster)
    : false;

  async function dispatch() {
    setPhase("dispatching");
    const res = await dispatchOperation(
      cluster.id,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      def.opKind as any,
      params as Record<string, unknown>,
      { targetHostIds },
    );
    setTaskId(res.taskId);
    const p = getOperationProgress(res.taskId);
    setProgress(p);
    // Signal flavor: progress is already in terminal state (succeeded).
    // Orchestrated: progress is "running" and the poll above takes over.
    if (def.flavor === "signal" || p?.state !== "running") {
      setPhase("terminal");
    } else {
      setPhase("running");
    }
  }

  function onPrimary() {
    if (!isLastStep) {
      setStepIdx((i) => i + 1);
      return;
    }
    dispatch();
  }

  const result: "success" | "failed" | "canceled" =
    progress?.state === "succeeded"
      ? "success"
      : progress?.state === "failed"
        ? "failed"
        : progress?.state === "canceled"
          ? "canceled"
          : "success";

  return (
    <div className="modal-backdrop" onClick={(e) => e.stopPropagation()}>
      <div
        className="card modal modal--lg"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <header style={{ paddingBottom: "var(--s-2)" }}>
          <h2 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
            {(def.dynamicLabel ? def.dynamicLabel(cluster) : def.label).replace(/…$/, "")}{" "}
            <span className="subtle" style={{ fontWeight: 400 }}>
              · {cluster.name}
              {scopeLabel ? ` · ${scopeLabel}` : ""}
            </span>
          </h2>
        </header>

        {/* ── Body — phase-dependent ───────────────────────────── */}
        {phase === "input" && currentStep && (
          <div className="vstack" style={{ gap: "var(--s-3)" }}>
            {currentStep.render({
              params,
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              setParams: (next: any) => setParams(next),
              cluster,
            })}
          </div>
        )}

        {phase === "dispatching" && (
          <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
            Sending command…
          </p>
        )}

        {phase === "running" && (
          <RunningBody progress={progress} />
        )}

        {phase === "terminal" && (
          <TerminalBody progress={progress} />
        )}

        {/* ── Footer — phase-dependent buttons ─────────────────── */}
        <div
          className="hstack"
          style={{ justifyContent: "flex-end", marginTop: "var(--s-3)" }}
        >
          {phase === "input" && (
            <>
              {stepIdx > 0 && (
                <button
                  className="btn"
                  onClick={() => setStepIdx((i) => i - 1)}
                >
                  ← Back
                </button>
              )}
              <button className="btn" onClick={() => onClose("canceled")}>
                Cancel
              </button>
              <button
                className={
                  "btn btn--primary" +
                  (currentStep?.danger ? " btn--danger" : "")
                }
                onClick={onPrimary}
                disabled={!canAdvance}
              >
                {isLastStep
                  ? currentStep?.nextLabel ?? "Continue"
                  : "Continue →"}
              </button>
            </>
          )}
          {phase === "running" && def.cancellable && taskId && (
            <button
              className="btn btn--danger"
              onClick={() => cancelOperation(taskId)}
              disabled={progress?.state !== "running"}
            >
              {progress?.state === "canceled"
                ? "Canceling…"
                : (def.cancelLabel ?? "Cancel after current host")}
            </button>
          )}
          {phase === "terminal" && (
            <button
              className="btn btn--primary"
              onClick={() => onClose(result)}
            >
              Close
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function ProgressBar({ progress }: { progress: OperationProgress }) {
  const pct =
    progress.total && progress.total > 0
      ? Math.round(((progress.current ?? 0) / progress.total) * 100)
      : 0;
  return (
    <div className="hstack">
      <span className="subtle">Progress</span>
      <div className="progress" style={{ flex: 1, maxWidth: 320 }}>
        <div className="progress__bar" style={{ width: `${pct}%` }} />
      </div>
      {progress.total != null && (
        <span className="subtle">
          {progress.current ?? 0} / {progress.total}
        </span>
      )}
    </div>
  );
}

function RunningBody({ progress }: { progress: OperationProgress | null }) {
  if (!progress) return <p className="muted">Working…</p>;
  const hasHosts = !!progress.hostStatuses && progress.hostStatuses.length > 0;
  const hasEvents = !!progress.events && progress.events.length > 0;
  return (
    <div className="vstack" style={{ gap: "var(--s-3)" }}>
      {hasHosts && <ProgressBar progress={progress} />}
      {!hasHosts && progress.detail && (
        <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
          {progress.detail}
        </p>
      )}
      <HostStatusList statuses={progress.hostStatuses} />
      {hasEvents && <EventLog events={progress.events!} />}
    </div>
  );
}

function EventLog({ events }: { events: OpEvent[] }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    // Keep the latest line visible as new events stream in.
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [events.length]);
  return (
    <div
      ref={scrollRef}
      className="card mono"
      style={{
        padding: "var(--s-2) var(--s-3)",
        background: "var(--c-surface-2)",
        fontSize: "var(--fs-xs)",
        maxHeight: 280,
        overflowY: "auto",
        lineHeight: 1.6,
      }}
    >
      {events.map((e, i) => (
        <div key={i} className="hstack" style={{ gap: "var(--s-2)", alignItems: "baseline" }}>
          <span className="subtle" style={{ minWidth: 64 }}>
            {formatEventTs(e.ts)}
          </span>
          <span style={{ minWidth: 14 }}>{eventGlyph(e.level)}</span>
          <span style={{ color: eventColor(e.level) }}>{e.text}</span>
        </div>
      ))}
    </div>
  );
}

function formatEventTs(ts: string): string {
  try {
    const d = new Date(ts);
    return d.toLocaleTimeString(undefined, { hour12: false });
  } catch {
    return "";
  }
}

function eventGlyph(level?: OpEvent["level"]): string {
  switch (level) {
    case "ok": return "✓";
    case "warn": return "⚠";
    case "error": return "✗";
    default: return "·";
  }
}

function eventColor(level?: OpEvent["level"]): string | undefined {
  switch (level) {
    case "ok": return "var(--c-success)";
    case "warn": return "var(--c-warning)";
    case "error": return "var(--c-danger)";
    default: return undefined;
  }
}

function HostStatusList({
  statuses,
}: {
  statuses?: OperationProgress["hostStatuses"];
}) {
  if (!statuses || statuses.length === 0) return null;
  return (
    <div
      className="card"
      style={{
        padding: 0,
        // ~7 rows visible; scrolls for larger deployments.
        maxHeight: 320,
        overflowY: "auto",
      }}
    >
      {statuses.map((h) => (
        <div
          key={h.hostId}
          className="rolling-row"
          style={{ gridTemplateColumns: "1fr 130px 60px" }}
        >
          <div className="rolling-row__name">{h.hostname}</div>
          <div>{hostStatePill(h.state)}</div>
          <div
            className="subtle"
            style={{ fontSize: "var(--fs-xs)", textAlign: "right" }}
          >
            {h.durationSec ? `${h.durationSec}s` : ""}
          </div>
        </div>
      ))}
    </div>
  );
}

function hostStatePill(s: HostOpState) {
  switch (s) {
    case "pending":
      return <Pill tone="neutral" icon="·">Pending</Pill>;
    case "running":
      return <Pill tone="info" icon="⟳">Running</Pill>;
    case "succeeded":
      return <Pill tone="success" icon="✓">Succeeded</Pill>;
    case "failed":
      return <Pill tone="danger" icon="✗">Failed</Pill>;
  }
}

function TerminalBody({
  progress,
}: {
  progress: OperationProgress | null;
}) {
  if (!progress) return null;
  const hasHostList = !!progress.hostStatuses && progress.hostStatuses.length > 0;
  const hasSummary = !!progress.summary && progress.summary.length > 0;
  const hasEvents = !!progress.events && progress.events.length > 0;

  // Orchestrated success: progress bar at 100% + host list (all
  // green) carries the message. No banner needed.
  if (hasHostList && progress.state === "succeeded") {
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <ProgressBar progress={progress} />
        {hasSummary && <SummaryTable rows={progress.summary!} />}
        <HostStatusList statuses={progress.hostStatuses} />
        {hasEvents && <EventLog events={progress.events!} />}
      </div>
    );
  }

  // Event-only success (e.g., heal): summary + event log. No banner —
  // the events tell the story.
  if (!hasHostList && hasEvents && progress.state === "succeeded") {
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        {progress.detail && (
          <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
            {progress.detail}
          </p>
        )}
        {hasSummary && <SummaryTable rows={progress.summary!} />}
        <EventLog events={progress.events!} />
      </div>
    );
  }

  const banner =
    progress.state === "succeeded" ? (
      <div className="banner banner--success">
        <span>✓</span>
        <span>{progress.detail ?? "Done."}</span>
      </div>
    ) : progress.state === "failed" ? (
      <div className="banner banner--danger">
        <span>✗</span>
        <span>{progress.failureNote ?? progress.detail ?? "Failed."}</span>
      </div>
    ) : (
      <div className="banner banner--warning">
        <span>⚠</span>
        <span>{progress.detail ?? "Canceled."}</span>
      </div>
    );

  return (
    <div className="vstack" style={{ gap: "var(--s-3)" }}>
      {hasHostList && <ProgressBar progress={progress} />}
      {banner}
      {hasSummary && <SummaryTable rows={progress.summary!} />}
      <HostStatusList statuses={progress.hostStatuses} />
      {hasEvents && <EventLog events={progress.events!} />}
    </div>
  );
}

function SummaryTable({ rows }: { rows: { label: string; value: string }[] }) {
  return (
    <div
      className="card"
      style={{
        padding: "var(--s-3) var(--s-4)",
        background: "var(--c-surface-2)",
      }}
    >
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          rowGap: "var(--s-2)",
          columnGap: "var(--s-4)",
          fontSize: "var(--fs-sm)",
        }}
      >
        {rows.map((r) => (
          <div key={r.label} style={{ display: "contents" }}>
            <div className="subtle">{r.label}</div>
            <div className="mono">{r.value}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

// Used by the menu when a navigate-flavor item is clicked — bypasses
// the modal entirely.
export function useNavigateOp() {
  return useNavigate();
}
