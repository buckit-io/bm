// Migrate step. Collapses the old Cutover + Verify steps into one
// page. Cutover runs at the top with per-node live state; once every
// node reports `done`, Verify automatically kicks off and renders
// underneath. Final "Go to cluster" button when verify completes.

import { useEffect, useRef, useState } from "react";
import {
  CutoverNodeState,
  MigrationDraft,
  VerifyResult,
} from "../state";
import { Pill } from "../../../../components/Pill";
import { TaskLogStream } from "../../../../components/TaskLogStream";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
  onFinish: () => void;
}

// Stage progression mirrors the install-first sequence in Plan: the
// package lands first while minio is still running, then a quick stop
// + drop-in + enable cycle. Most of the per-node time is in the
// install + health-wait phases.
const STAGES: CutoverNodeState["state"][] = [
  "uploading_pkg",
  "installing",
  "switching_unit",
  "waiting_health",
  "waiting_cluster",
  "done",
];

const STAGE_LABEL: Record<CutoverNodeState["state"], string> = {
  pending: "Pending",
  stopping_minio: "Stopping minio",
  uploading_pkg: "Uploading package",
  installing: "Installing buckit",
  switching_unit: "Switching service",
  waiting_health: "Waiting node-healthy",
  waiting_cluster: "Waiting cluster-healthy",
  done: "Buckit healthy",
  rolled_back: "Rolled back",
  failed: "Failed",
};

function stagePill(s: CutoverNodeState["state"]) {
  if (s === "done") return <Pill tone="success" icon="✓">{STAGE_LABEL[s]}</Pill>;
  if (s === "pending") return <Pill tone="neutral" icon="·">{STAGE_LABEL[s]}</Pill>;
  if (s === "rolled_back") return <Pill tone="warning" icon="↺">{STAGE_LABEL[s]}</Pill>;
  if (s === "failed") return <Pill tone="danger" icon="✗">{STAGE_LABEL[s]}</Pill>;
  return <Pill tone="info" icon="⟳">{STAGE_LABEL[s]}</Pill>;
}

function makeVerify(d: MigrationDraft): VerifyResult {
  const s = d.snapshot!;
  const n = d.hosts.filter((h) => h.hostname.trim()).length;
  return {
    clusterHealthy: true,
    nodesReporting: { ok: n, total: n },
    bucketsOk: { ok: s.buckets, total: s.buckets },
    objectsSampled: { ok: 1000, total: 1000 },
    users: { ok: s.users, total: s.users },
    groups: { ok: s.groups, total: s.groups },
    policies: { ok: s.customPolicies, total: s.customPolicies },
    serviceAccounts: { ok: s.serviceAccounts, total: s.serviceAccounts },
    bucketPolicies: { ok: s.policies, total: s.policies },
    lifecycle: { ok: s.lifecycle, total: s.lifecycle },
    notifications: { ok: s.notifications, total: s.notifications },
    smokeOk: true,
  };
}

function verifyRow(label: string, sub: { ok: number; total: number }) {
  const ok = sub.ok === sub.total;
  return (
    <tr key={label}>
      <td>{label}</td>
      <td className="num">
        {sub.ok} / {sub.total}
      </td>
      <td>
        {ok ? (
          <Pill tone="success" icon="✓">Match</Pill>
        ) : (
          <Pill tone="danger" icon="✗">Mismatch</Pill>
        )}
      </td>
    </tr>
  );
}

export function Migrate({ draft, update, onFinish }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const started = useRef(false);
  const verifyFired = useRef(false);
  const [confirmRollback, setConfirmRollback] = useState(false);

  // Mirror cutover.paused into a ref so the tick closure (set up in
  // the mount effect) can see the latest value without re-binding.
  const pausedRef = useRef(draft.cutover.paused);
  pausedRef.current = draft.cutover.paused;

  const togglePause = () => {
    update({
      cutover: { ...draft.cutover, paused: !draft.cutover.paused },
    });
  };

  const rollbackCompletedNodes = () => {
    // Flip every node currently in `done` state to `rolled_back`. Resets
    // the verify result so the operator sees they're back to the
    // pre-migration baseline. History row would be written here in the
    // real backend.
    const per = { ...draft.cutover.perNode };
    let rolledBack = 0;
    for (const h of hosts) {
      const s = per[h.id];
      if (s && s.state === "done") {
        per[h.id] = { ...s, state: "rolled_back" };
        rolledBack++;
      }
    }
    update({
      cutover: {
        ...draft.cutover,
        perNode: per,
        overallNodesDone: draft.cutover.overallNodesDone - rolledBack,
      },
      verify: null,
    });
    setConfirmRollback(false);
  };

  // ── cutover orchestration ──────────────────────────────────────
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const per: Record<string, CutoverNodeState> = {};
    hosts.forEach((h) => (per[h.id] = { state: "pending" }));
    update({ cutover: { perNode: per, overallNodesDone: 0, paused: false } });

    let nodeIdx = 0;
    let stageIdx = 0;
    const tick = () => {
      if (nodeIdx >= hosts.length) return;
      // Pause-check: only between nodes (stageIdx === 0 means we're
      // about to start a fresh node, either the first or just wrapped
      // from the previous node's `done` state). Pausing mid-node would
      // be unsafe — wait until the in-flight node finishes its 9-step
      // sequence first.
      if (stageIdx === 0 && pausedRef.current) {
        setTimeout(tick, 500);
        return;
      }
      const h = hosts[nodeIdx];
      if (stageIdx === 0) {
        per[h.id] = {
          state: STAGES[0],
          cutoverStartedAt: new Date().toISOString(),
        };
      } else {
        per[h.id] = { ...per[h.id], state: STAGES[stageIdx] };
      }
      if (stageIdx === STAGES.length - 1) {
        per[h.id].durationSec = 60 + Math.round(Math.random() * 20);
      }
      update({
        cutover: {
          perNode: { ...per },
          overallNodesDone:
            stageIdx === STAGES.length - 1 ? nodeIdx + 1 : nodeIdx,
          paused: false,
        },
      });
      if (stageIdx >= STAGES.length - 1) {
        stageIdx = 0;
        nodeIdx++;
      } else {
        stageIdx++;
      }
      setTimeout(tick, 350);
    };
    setTimeout(tick, 400);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── verify auto-kicks off once cutover completes ───────────────
  const cutoverDone = draft.cutover.overallNodesDone >= hosts.length;
  useEffect(() => {
    if (!cutoverDone) return;
    if (verifyFired.current || draft.verify) return;
    verifyFired.current = true;
    setTimeout(() => update({ verify: makeVerify(draft) }), 700);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cutoverDone]);

  const done = draft.cutover.overallNodesDone;
  const pct = Math.round((done / Math.max(hosts.length, 1)) * 100);
  const v = draft.verify;

  // Count rollback-eligible nodes (those that completed cutover and are
  // still on buckit). Hidden when the operator is on a fresh page or
  // every completed node has already been rolled back.
  const completedCount = hosts.filter(
    (h) => draft.cutover.perNode[h.id]?.state === "done",
  ).length;

  // Once any node has been rolled back, the operator has explicitly
  // halted the natural cutover flow — Pause stops being relevant.
  const anyRolledBack = hosts.some(
    (h) => draft.cutover.perNode[h.id]?.state === "rolled_back",
  );
  const showPause = !cutoverDone && !anyRolledBack;

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header
        className="hstack"
        style={{ justifyContent: "space-between", alignItems: "flex-start" }}
      >
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
            Migrating {draft.name} → Buckit
          </h2>
          <p
            className="muted"
            style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
          >
            ●{" "}
            {cutoverDone
              ? "Cutover complete · verifying"
              : draft.cutover.paused
                ? "Paused"
                : "Cutover in progress"}{" "}
            · {done}/{hosts.length} nodes
          </p>
        </div>
        <div className="hstack" style={{ gap: "var(--s-2)" }}>
          {showPause && (
            <button className="btn btn--sm" onClick={togglePause}>
              {draft.cutover.paused
                ? "Resume"
                : "Pause after current node"}
            </button>
          )}
          {completedCount > 0 && (
            <button
              className="btn btn--sm btn--danger"
              onClick={() => setConfirmRollback(true)}
            >
              Rollback completed nodes
            </button>
          )}
        </div>
      </header>

      <div className="hstack">
        <span className="subtle">Overall progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 320 }}>
          <div className="progress__bar" style={{ width: `${pct}%` }} />
        </div>
        <span className="subtle">
          {done} / {hosts.length}
        </span>
      </div>

      {/* Per-node cutover state */}
      <div className="card" style={{ padding: 0 }}>
        {hosts.map((h) => {
          const s =
            draft.cutover.perNode[h.id] ?? ({ state: "pending" } as const);
          return (
            <div
              key={h.id}
              className="rolling-row"
              style={{ gridTemplateColumns: "200px 1fr 90px" }}
            >
              <div className="rolling-row__name">{h.hostname}</div>
              <div>{stagePill(s.state)}</div>
              <div
                className="subtle"
                style={{ fontSize: "var(--fs-xs)" }}
              >
                {s.durationSec ? `${s.durationSec}s` : ""}
              </div>
            </div>
          );
        })}
      </div>

      {/* Live log */}
      <div className="card card-stat">
        <div className="card-stat__title">Live log</div>
        <TaskLogStream taskId="migrate-cutover" showPause={false} />
      </div>

      {/* Verify section — only renders after cutover completes */}
      {cutoverDone && (
        <>
          <header>
            <h3 className="card-stat__title">Verify</h3>
            <p
              className="muted"
              style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
            >
              Comparing post-migration state against the pre-migration
              snapshot.
            </p>
          </header>

          {!v ? (
            <p className="muted">Running post-migration verification…</p>
          ) : (
            <>
              <div className="card card--table">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Check</th>
                      <th className="num">Result</th>
                      <th style={{ width: 110 }}>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>Cluster health</td>
                      <td className="num">—</td>
                      <td>
                        {v.clusterHealthy ? (
                          <Pill tone="success" icon="●">Healthy</Pill>
                        ) : (
                          <Pill tone="warning">Degraded</Pill>
                        )}
                      </td>
                    </tr>
                    {verifyRow("Nodes reporting", v.nodesReporting)}
                    {verifyRow("Buckets", v.bucketsOk)}
                    {verifyRow("Objects (sampled, 1000)", v.objectsSampled)}
                    {verifyRow("IAM users", v.users)}
                    {verifyRow("IAM groups", v.groups)}
                    {verifyRow("Custom policies", v.policies)}
                    {verifyRow("Service accounts", v.serviceAccounts)}
                    {verifyRow("Bucket policies", v.bucketPolicies)}
                    {verifyRow("Lifecycle rules", v.lifecycle)}
                    {verifyRow("Notification configs", v.notifications)}
                    <tr>
                      <td>Smoke test (PUT/GET/DELETE 1 KiB)</td>
                      <td className="num">—</td>
                      <td>
                        {v.smokeOk ? (
                          <Pill tone="success" icon="✓">Passed</Pill>
                        ) : (
                          <Pill tone="danger">Failed</Pill>
                        )}
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>

              <div className="banner banner--success">
                <span>✓</span>
                <span>
                  Migration complete. Rollback to MinIO remains available
                  indefinitely from the cluster detail page.
                </span>
              </div>

              <div className="hstack" style={{ justifyContent: "flex-end" }}>
                <button className="btn btn--primary" onClick={onFinish}>
                  Go to cluster
                </button>
              </div>
            </>
          )}
        </>
      )}

      {confirmRollback && (
        <div
          className="modal-backdrop"
          onClick={() => setConfirmRollback(false)}
        >
          <div
            className="card modal"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
              Rollback completed nodes?
            </h3>
            <p style={{ fontSize: "var(--fs-sm)" }}>
              {completedCount} node{completedCount === 1 ? "" : "s"} will
              be rolled back to MinIO:{" "}
              <span className="mono">systemctl stop buckit</span> →{" "}
              <span className="mono">systemctl enable --now minio</span>{" "}
              on each. Data, envfile, and{" "}
              <span className="mono">.minio.sys/</span> are untouched.
            </p>
            <div className="hstack" style={{ justifyContent: "flex-end" }}>
              <button
                className="btn"
                onClick={() => setConfirmRollback(false)}
              >
                Cancel
              </button>
              <button
                className="btn btn--danger"
                onClick={rollbackCompletedNodes}
              >
                Rollback {completedCount} node
                {completedCount === 1 ? "" : "s"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
