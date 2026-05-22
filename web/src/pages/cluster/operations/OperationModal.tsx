// Unified modal for every cluster operation. Lifecycle:
//
//   input → dispatching → running → terminal    (signal flavor)
//   input → dispatching → running → terminal    (orchestrated flavor)
//
// Lock rules:
//   - input phase: no backdrop/ESC/X close. Cancel button closes.
//   - dispatching: no close at all.
//   - running: no close.
//   - terminal: Close button only.
//
// Browser tab close triggers a beforeunload warning while a non-
// terminal phase is active; result is still recorded in History.

import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  cancelOperation as cancelOperationApi,
  deleteCluster,
  dispatchOperation,
  getOperationProgress,
} from "../../../api/client";
import { subscribeOperationEvents } from "../../../api/sse";
import type {
  Cluster,
  HostOpState,
  OperationProgress,
  OpEvent,
  OpKind,
} from "../../../api/types";
import { Pill } from "../../../components/Pill";
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
  const abortRef = useRef<AbortController | null>(null);

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

  // Subscribe to /operations/:taskId/events while the op is running. The
  // backend pushes a state snapshot on connect plus incremental updates
  // until a terminal frame closes the stream. We flip into the terminal
  // phase when the snapshot reports a non-running state AND no host is
  // still in flight (the second guard handles cancellation: state goes
  // canceled immediately, but the in-flight host needs another moment to
  // wrap up).
  useEffect(() => {
    if (phase !== "running") return;
    if (!taskId) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    let terminal = false;
    subscribeOperationEvents(taskId, {
      signal: ctrl.signal,
      onProgress: (p) => {
        setProgress(p);
        if (p.state !== "running") {
          const anyHostRunning = p.hostStatuses?.some(
            (h) => h.state === "running",
          );
          if (!anyHostRunning) {
            terminal = true;
            setPhase("terminal");
          }
        }
      },
    }).catch((err) => {
      // Aborted streams are expected on unmount / phase change.
      if (err instanceof DOMException && err.name === "AbortError") return;
      // Stream errored before terminal — fall back to a one-shot snapshot
      // fetch so we don't strand the modal in "running" indefinitely.
      if (terminal) return;
      getOperationProgress(taskId)
        .then((p) => {
          setProgress(p);
          setPhase("terminal");
        })
        .catch(() => {
          // Last-ditch: flip to terminal with whatever we have so the
          // operator can close the modal.
          setPhase("terminal");
        });
    });
    return () => ctrl.abort();
  }, [phase, taskId]);

  const currentStep = def.inputSteps[stepIdx];
  const isLastStep = stepIdx === def.inputSteps.length - 1;
  const canAdvance = currentStep
    ? currentStep.canAdvance(params, cluster)
    : false;

  async function dispatch() {
    setPhase("dispatching");
    // delete flavor: not an operation in the dispatcher sense — just a
    // one-shot DELETE /clusters/:id. No taskId, no SSE, no history row.
    // Modal flips straight to terminal phase on success.
    if (def.flavor === "delete") {
      try {
        await deleteCluster(cluster.id);
        setProgress({
          taskId: "delete",
          state: "succeeded",
          detail: `${cluster.name} removed from this manager.`,
        });
      } catch (err) {
        const message = err instanceof Error ? err.message : "Delete failed.";
        setProgress({
          taskId: "delete",
          state: "failed",
          failureNote: message,
          detail: message,
        });
      }
      setPhase("terminal");
      return;
    }

    try {
      const opLabel = (def.dynamicLabel ? def.dynamicLabel(cluster) : def.label).replace(/…$/, "");
      const res = await dispatchOperation({
        clusterId: cluster.id,
        kind: def.opKind as OpKind,
        params: params as Record<string, unknown>,
        targetHostIds,
        clusterName: cluster.name,
        opLabel,
      });
      setTaskId(res.taskId);
      // Pull the initial snapshot so the modal renders something while
      // the SSE subscription effect spins up.
      const snapshot = await getOperationProgress(res.taskId).catch(() => null);
      if (snapshot) setProgress(snapshot);
      // Signal ops are also executed by the async task manager. Their first
      // snapshot can still be "running" for a moment, so do not force the
      // terminal phase until the backend reports a terminal state.
      if (snapshot && snapshot.state !== "running") {
        setPhase("terminal");
      } else {
        setPhase("running");
      }
    } catch (err) {
      // Dispatch rejection (validation_failed, cluster_busy, etc).
      // Surface via the existing terminal-phase failure path.
      const message = err instanceof Error ? err.message : "Dispatch failed.";
      setProgress({
        taskId: "dispatch-failed",
        state: "failed",
        failureNote: message,
        detail: message,
      });
      setPhase("terminal");
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
              onClick={() => {
                cancelOperationApi(taskId).catch(() => {
                  // Best-effort — the SSE stream will surface the
                  // canceled state when it lands.
                });
              }}
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
      {hasEvents && (
        <>
          <SectionLabel>Live events</SectionLabel>
          <EventLog events={progress.events!} />
        </>
      )}
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
  const showSummary = shouldShowSummary(progress);

  // Orchestrated success: progress bar at 100% + host list (all
  // green) carries the message. No banner needed.
  if (hasHostList && progress.state === "succeeded") {
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <ProgressBar progress={progress} />
        {hasSummary && showSummary && (
          <>
            <SectionLabel>Result</SectionLabel>
            <SummaryTable rows={progress.summary!} />
          </>
        )}
        <HostStatusList statuses={progress.hostStatuses} />
        {hasEvents && (
          <>
            <SectionLabel>Events</SectionLabel>
            <EventLog events={progress.events!} />
          </>
        )}
      </div>
    );
  }

  // Event-only success (e.g., signal ops, heal): keep the outcome visibly
  // separate from the activity log so fast ops don't look like two logs.
  if (!hasHostList && hasEvents && progress.state === "succeeded") {
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <div className="banner banner--success">
          <span>✓</span>
          <span>{progress.detail ?? "Done."}</span>
        </div>
        {hasSummary && showSummary && (
          <>
            <SectionLabel>Result</SectionLabel>
            <SummaryTable rows={progress.summary!} />
          </>
        )}
        <SectionLabel>Events</SectionLabel>
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
      {hasSummary && showSummary && (
        <>
          <SectionLabel>Result</SectionLabel>
          <SummaryTable rows={progress.summary!} />
        </>
      )}
      <HostStatusList statuses={progress.hostStatuses} />
      {hasEvents && (
        <>
          <SectionLabel>Events</SectionLabel>
          <EventLog events={progress.events!} />
        </>
      )}
    </div>
  );
}

function shouldShowSummary(progress: OperationProgress): boolean {
  if (!progress.summary || progress.summary.length === 0) return false;
  if (progress.summary.length !== 1) return true;
  const row = progress.summary[0];
  const detail = progress.detail?.trim().toLowerCase();
  const label = row.label.trim().toLowerCase();
  const value = row.value.trim().toLowerCase();

  // Signal-style ops often emit:
  //   detail: "S3 API unfrozen"
  //   summary: [{ label: "Result", value: "unfrozen" }]
  // The banner already communicates the outcome clearly, so the single-row
  // summary just repeats it.
  if (label === "result" && detail && detail.includes(value)) {
    return false;
  }
  return true;
}

function SectionLabel({ children }: { children: string }) {
  return (
    <div
      className="subtle"
      style={{
        fontSize: "var(--fs-xs)",
        fontWeight: 600,
        letterSpacing: "0.08em",
        textTransform: "uppercase",
      }}
    >
      {children}
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
