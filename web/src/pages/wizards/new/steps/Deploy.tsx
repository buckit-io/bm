import { useEffect, useRef, useState } from "react";
import { NewClusterDraft, DeployNodeState } from "../state";
import { TaskLogStream } from "../../../../components/TaskLogStream";
import { newClusterDeploy, getOperationProgress } from "../../../../api/client";
import { subscribeOperationEvents, SseError } from "../../../../api/sse";
import type { HostOpState, OperationProgress } from "../../../../api/types";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

// stateLabel is the human-readable string the per-host row renders for a
// given wire-level HostOpState. The deploy installer populates the
// HostOpStatus.detail field with the active stage's free-text status
// ("Fetching …", "systemctl enable --now …", "Service healthy") which
// shows alongside this label.
const STATE_LABELS: Record<HostOpState, string> = {
  pending: "Pending",
  running: "Running",
  succeeded: "Healthy",
  failed: "Failed",
};

const STATE_GLYPH: Record<HostOpState, string> = {
  pending: "●",
  running: "⟳",
  succeeded: "✓",
  failed: "✗",
};

// hostStateToDeployState maps the wire-level HostOpState onto the
// wizard's DeployNodeState shape. The wizard's draft type is left
// in place so any code reading draft.deploy.perNode keeps working;
// state granularity collapses from the old 7-stage enum to the
// 4-state wire enum, but the operator still sees the active stage
// in `lastEvent` (the detail string) and the live log stream.
function hostStateToDeployState(s: HostOpState): DeployNodeState["state"] {
  switch (s) {
    case "pending":
      return "pending";
    case "running":
      return "starting"; // generic "in flight" — actual stage is in lastEvent
    case "succeeded":
      return "healthy";
    case "failed":
      return "failed";
  }
}

export function Deploy({ draft, update }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const started = useRef(false);
  const doneRef = useRef(draft.done);
  doneRef.current = draft.done;
  const [taskId, setTaskId] = useState<string | null>(null);
  const [dispatchError, setDispatchError] = useState<string | null>(null);

  // Dispatch the deploy on first mount. The wizard guarantees this
  // step is only entered once per draft.
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const startedAt = new Date().toISOString();
    const init: Record<string, DeployNodeState> = {};
    hosts.forEach((h) => {
      init[h.id] = { state: "pending", elapsedSec: 0, lastEvent: "Queued" };
    });
    update({
      deploy: { startedAt, perNode: init, overallPct: 0, canceled: false },
    });

    newClusterDeploy(draft)
      .then((res) => setTaskId(res.taskId))
      .catch((err) => {
        const message = err instanceof Error ? err.message : "Deploy dispatch failed.";
        setDispatchError(message);
        update({
          deploy: {
            startedAt,
            perNode: Object.fromEntries(
              hosts.map((h) => [
                h.id,
                {
                  state: "failed",
                  elapsedSec: 0,
                  lastEvent: message,
                } as DeployNodeState,
              ]),
            ),
            overallPct: 0,
            canceled: false,
          },
        });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Subscribe to operation events once we have a taskId. The SSE stream
  // pushes a state snapshot on connect, plus incremental updates as the
  // deploy runs. We mutate draft.deploy on every state frame so the
  // wizard's footer (Next button) reacts to overallPct === 100.
  useEffect(() => {
    if (!taskId) return;
    const ctrl = new AbortController();
    const startedAt = draft.deploy.startedAt ?? new Date().toISOString();

    const onProgress = (p: OperationProgress) => {
      const per: Record<string, DeployNodeState> = {};
      const elapsedSec = Math.max(
        0,
        Math.round((Date.now() - new Date(startedAt).getTime()) / 1000),
      );
      const failureDetail = p.failureNote || p.detail || "Deploy failed.";
      for (const h of hosts) {
        const hs = p.hostStatuses?.find((x) => x.hostId === h.id);
        if (!hs) {
          per[h.id] = {
            state: "pending",
            elapsedSec: 0,
            lastEvent: "Queued",
          };
          continue;
        }
        const failedHostState =
          p.state === "failed" && hs.state === "running" ? "failed" : hs.state;
        per[h.id] = {
          state: hostStateToDeployState(failedHostState),
          elapsedSec: hs.durationSec ?? elapsedSec,
          lastEvent:
            failedHostState === "failed"
              ? failureDetail
              : hs.detail ?? STATE_LABELS[hs.state],
        };
      }
      const total = p.total ?? hosts.length;
      const current = p.current ?? 0;
      const pct =
        p.state === "succeeded"
          ? 100
          : total > 0
            ? Math.min(99, Math.round((current / total) * 100))
            : 0;
      update({
        deploy: {
          startedAt,
          perNode: per,
          overallPct: pct,
          canceled: p.state === "canceled",
        },
        done: applyVerifySummary(doneRef.current, p.summary),
      });
    };

    getOperationProgress(taskId, ctrl.signal)
      .then(onProgress)
      .catch(() => {});

    subscribeOperationEvents(taskId, {
      signal: ctrl.signal,
      onProgress,
    }).catch(async (err) => {
      if (err instanceof DOMException && err.name === "AbortError") return;
      // Stream dropped before terminal — fall back to a one-shot snapshot
      // fetch so the wizard isn't stuck mid-flight.
      try {
        const snap = await getOperationProgress(taskId);
        onProgress(snap);
      } catch {
        const message = err instanceof SseError ? err.message : "Stream closed.";
        setDispatchError(message);
      }
    });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          Deploying {draft.name || "cluster"}
        </h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          {dispatchError
            ? `✗ ${dispatchError}`
            : Object.values(draft.deploy.perNode).some((n) => n.state === "failed")
              ? "✗ Deployment failed"
            : draft.deploy.overallPct === 100
              ? "✓ Cluster healthy"
              : "● Running"}
        </p>
      </header>

      <div className="hstack">
        <span className="subtle">Overall progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 320 }}>
          <div className="progress__bar" style={{ width: `${draft.deploy.overallPct}%` }} />
        </div>
        <span className="subtle">{draft.deploy.overallPct}%</span>
      </div>

      <div className="card" style={{ padding: 0 }}>
        {hosts.map((h) => {
          const ns = draft.deploy.perNode[h.id] ?? {
            state: "pending" as const,
            elapsedSec: 0,
            lastEvent: "—",
          };
          return (
            <div key={h.id} className="rolling-row">
              <div className="rolling-row__name">{h.hostname}</div>
              <div className="rolling-row__state">
                <span style={{ marginRight: 6 }}>
                  {STATE_GLYPH[wizardStateToWire(ns.state)]}
                </span>
                {STATE_LABELS[wizardStateToWire(ns.state)]}
              </div>
              <div className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
                {ns.elapsedSec}s
              </div>
              <div className="rolling-row__event">{ns.lastEvent}</div>
            </div>
          );
        })}
      </div>

      {taskId && <TaskLogStream taskId={taskId} showPause={false} />}
    </div>
  );
}

function applyVerifySummary(
  done: NewClusterDraft["done"],
  summary?: OperationProgress["summary"],
): NewClusterDraft["done"] {
  if (!summary || summary.length === 0) return done;
  const next = { ...done };
  const consoleUrl = summaryValue(summary, "Console URL");
  if (consoleUrl) next.consoleUrl = consoleUrl;
  const clusterId = summaryValue(summary, "Cluster ID");
  if (clusterId) next.clusterId = clusterId;
  const nodesHealthy = summaryFraction(summary, "Nodes healthy");
  if (nodesHealthy) next.nodesHealthy = nodesHealthy.ok;
  const poolsOnline = summaryFraction(summary, "Pools online");
  if (poolsOnline) next.poolsOnline = poolsOnline.ok;
  const smoke = summaryValue(summary, "Read/write smoke test");
  if (smoke) next.smokeTestPassed = smoke.toLowerCase() === "passed";
  return next;
}

function summaryValue(
  summary: NonNullable<OperationProgress["summary"]>,
  label: string,
): string | undefined {
  return summary.find((item) => item.label === label)?.value;
}

function summaryFraction(
  summary: NonNullable<OperationProgress["summary"]>,
  label: string,
): { ok: number; total: number } | null {
  const value = summaryValue(summary, label);
  if (!value) return null;
  const match = value.match(/^(\d+)\s*\/\s*(\d+)$/);
  if (!match) return null;
  return { ok: Number(match[1]), total: Number(match[2]) };
}

// wizardStateToWire maps the wizard's DeployNodeState back to a wire-level
// HostOpState for display. The wizard's enum is a superset (7 stages); the
// running stages all fold to "running" for the row's pill.
function wizardStateToWire(s: DeployNodeState["state"]): HostOpState {
  switch (s) {
    case "pending":
      return "pending";
    case "healthy":
      return "succeeded";
    case "failed":
      return "failed";
    default:
      return "running";
  }
}
