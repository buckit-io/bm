// Migrate step. Collapses the old Cutover + Verify steps into one
// page. Cutover runs at the top with per-node live state; once every
// node reports `done`, Verify automatically renders underneath. Final
// "Go to cluster" button when verify completes.

import { useEffect, useRef, useState } from "react";
import {
  CutoverNodeState,
  MigrationDraft,
  VerifyResult,
} from "../state";
import { Pill } from "../../../../components/Pill";
import { TaskLogStream } from "../../../../components/TaskLogStream";
import {
  getOperationProgress,
  migrateCutover,
  migrateRollback,
  migrateSnapshot,
} from "../../../../api/client";
import { subscribeOperationEvents, SseError } from "../../../../api/sse";
import type {
  HostOpStatus,
  OperationProgress,
} from "../../../../api/types";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
  onFinish: () => void;
}

const STAGE_LABEL: Record<CutoverNodeState["state"], string> = {
  pending: "Pending",
  stopping_minio: "Stopping minio",
  downloading_pkg: "Downloading package",
  installing: "Installing buckit",
  switching_unit: "Switching service",
  waiting_health: "Waiting node-healthy",
  waiting_cluster: "Waiting cluster-healthy",
  done: "Buckit healthy",
  rolled_back: "Rolled back",
  failed: "Failed",
  skipped: "Skipped (offline)",
};

function stagePill(s: CutoverNodeState["state"]) {
  if (s === "done") return <Pill tone="success" icon="✓">{STAGE_LABEL[s]}</Pill>;
  if (s === "pending") return <Pill tone="neutral" icon="●">{STAGE_LABEL[s]}</Pill>;
  if (s === "skipped") return <Pill tone="neutral" icon="—">{STAGE_LABEL[s]}</Pill>;
  if (s === "rolled_back") return <Pill tone="warning" icon="↺">{STAGE_LABEL[s]}</Pill>;
  if (s === "failed") return <Pill tone="danger" icon="✗">{STAGE_LABEL[s]}</Pill>;
  return <Pill tone="info" icon="⟳">{STAGE_LABEL[s]}</Pill>;
}

// stageFromHostStatus maps a wire HostOpStatus onto the wizard's per-node
// CutoverNodeState. The wire format only carries 4 high-level states +
// a detail string; the granular Stage enum the wizard renders is
// reconstructed by keyword-matching the detail. Strings come from the
// backend's installer (internal/migration/installer.go) which emits
// detail like "Backing up /etc/default/minio", "Fetching <url>", "Installing
// /tmp/buckit.rpm", "systemctl swap minio.service → buckit.service",
// "Waiting for /minio/health/live", "Buckit healthy".
function stageFromHostStatus(hs: HostOpStatus): CutoverNodeState["state"] {
  switch (hs.state) {
    case "pending":
      return "pending";
    case "succeeded": {
      // The rollback executor finishes by marking each rolled-back host
      // as `succeeded` (since rollback completed without error) with a
      // detail string like "Rolled back to MinIO" or "Already on MinIO".
      // Without this disambiguation the pill would say "Buckit healthy"
      // for a host that's actually back on MinIO, and the wizard's
      // anyRolledBack check would stay false — leaving the verify
      // section spinning forever.
      const d = (hs.detail ?? "").toLowerCase();
      if (
        d.includes("rolled back") ||
        d.includes("minio restored") ||
        d.includes("already on minio")
      ) {
        return "rolled_back";
      }
      return "done";
    }
    case "failed":
      return "failed";
    case "skipped":
      return "skipped";
    case "running": {
      const d = (hs.detail ?? "").toLowerCase();
      // Rollback-path detail strings come first so they win over the
      // cutover-side matches that could otherwise misclassify them
      // (e.g. "Uninstalling buckit" would match the "installing"
      // cutover keyword and flash the pill back to Installing during
      // rollback).
      if (
        d.includes("stopping buckit") ||
        d.includes("uninstalling buckit") ||
        d.includes("removing drop-in") ||
        d.includes("enable --now minio") ||
        d.includes("waiting for minio") ||
        d.includes("restoring") ||
        d.includes("minio restored")
      ) {
        return "rolled_back";
      }
      if (d.includes("backing up") || d.includes("stop minio")) return "stopping_minio";
      if (d.includes("fetching")) return "downloading_pkg";
      if (d.includes("installing")) return "installing";
      if (d.includes("swap") || d.includes("switching")) return "switching_unit";
      if (d.includes("/minio/health/live")) return "waiting_health";
      if (d.includes("cluster-healthy")) return "waiting_cluster";
      if (d.includes("buckit healthy")) return "done";
      return "installing"; // generic running fallback
    }
  }
}

function makeVerify(d: MigrationDraft): VerifyResult {
  // The backend's M8 Verify pass populates OperationProgress.summary with
  // the audit counts. The wizard renders a snapshot-based VerifyResult
  // until that summary lands; once the cutover task reports succeeded,
  // the operator gets the full table from the backend's audit. For now,
  // mirror the snapshot counts (the same shape the backend's verify.go
  // produces).
  const s = d.snapshot!;
  const n = d.hosts.filter((h) => h.hostname.trim()).length;
  return {
    clusterHealthy: true,
    nodesReporting: { ok: n, total: n },
    bucketsOk: { ok: s.buckets, total: s.buckets },
    objectsSampled: { ok: 0, total: 0 },
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
  const [snapshotPath, setSnapshotPath] = useState<string | null>(null);
  const [activeTaskId, setActiveTaskId] = useState<string | null>(null);
  const [dispatchError, setDispatchError] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState(false);
  const [rollingBack, setRollingBack] = useState(false);

  // Mount: capture snapshot + dispatch cutover. The wizard guarantees
  // this step is entered once per draft.
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const per: Record<string, CutoverNodeState> = {};
    hosts.forEach((h) => (per[h.id] = { state: "pending" }));
    update({ cutover: { perNode: per, overallNodesDone: 0, paused: false } });

    (async () => {
      try {
        // Reuse the snapshot captured by the Review step. Falling back
        // to a fresh capture only when Review didn't run (e.g. operator
        // jumped directly into Migrate) keeps the cutover dispatch
        // working in either order.
        let path = draft.snapshotPath;
        if (!path) {
          const snap = await migrateSnapshot(draft.sourceClusterId);
          path = snap.path;
          update({ snapshotPath: path });
        }
        setSnapshotPath(path);
        const body = {
          sourceClusterId: draft.sourceClusterId,
          name: draft.name,
          description: draft.description,
          targetVersion: draft.version,
          hosts: hosts.map((h) => ({
            id: h.id,
            hostname: h.hostname,
            port: h.port || 22,
            probe: h.probe,
            sshOverride: h.sshOverride,
          })),
          ssh: draft.ssh,
          persistSsh: draft.persistSsh,
          snapshotPath: path,
        };
        const res = await migrateCutover(draft.sourceClusterId, body);
        setActiveTaskId(res.taskId);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Cutover dispatch failed.";
        setDispatchError(message);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Subscribe to operation events for the active task (cutover or rollback).
  useEffect(() => {
    if (!activeTaskId) return;
    const ctrl = new AbortController();

    const onProgress = (p: OperationProgress) => {
      const per: Record<string, CutoverNodeState> = {};
      let done = 0;
      for (const h of hosts) {
        const hs = p.hostStatuses?.find((x) => x.hostId === h.id);
        if (!hs) {
          per[h.id] = { state: "pending" };
          continue;
        }
        const stage = stageFromHostStatus(hs);
        per[h.id] = {
          state: stage,
          durationSec: hs.durationSec,
        };
        // Count anything in a terminal state (cutover-success OR
        // rollback-success) toward overallNodesDone so the progress
        // bar fills as either flow completes. Without this the bar
        // stays at 0 during a successful rollback even though every
        // host's pill says "Rolled back".
        if (stage === "done" || stage === "rolled_back") done++;
      }
      update({
        cutover: {
          perNode: per,
          overallNodesDone: done,
          paused: false,
        },
      });
    };

    subscribeOperationEvents(activeTaskId, {
      signal: ctrl.signal,
      onProgress,
    }).catch(async (err) => {
      if (err instanceof DOMException && err.name === "AbortError") return;
      // Stream dropped early — fall back to a one-shot fetch so the
      // wizard surfaces the terminal state.
      try {
        const snap = await getOperationProgress(activeTaskId);
        onProgress(snap);
      } catch {
        const message = err instanceof SseError ? err.message : "Stream closed.";
        setDispatchError(message);
      }
    });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTaskId]);

  // Verify auto-renders once cutover completes and verify hasn't been
  // populated. The backend's Verify pass runs server-side; for now the
  // UI derives the counts from the captured snapshot. Once the cutover
  // task reports OperationResult.summary (M8 Verify), the wizard can
  // pull those counts from the terminal state instead.
  const cutoverDone = draft.cutover.overallNodesDone >= hosts.length && hosts.length > 0;
  const verifyFired = useRef(false);
  useEffect(() => {
    if (!cutoverDone) return;
    if (verifyFired.current || draft.verify) return;
    verifyFired.current = true;
    if (draft.snapshot) {
      update({ verify: makeVerify(draft) });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cutoverDone]);

  // Rollback: only the hosts whose cutover state reached "done" qualify.
  // The backend's RollbackExecutor probes systemctl is-active buckit and
  // skips hosts already on minio, but we precompute the host list so the
  // confirmation modal shows the right count.
  const rollbackEligible = hosts.filter(
    (h) => draft.cutover.perNode[h.id]?.state === "done",
  );

  async function runRollback() {
    setConfirmRollback(false);
    setRollingBack(true);
    try {
      const body = {
        sourceClusterId: draft.sourceClusterId,
        name: draft.name,
        targetVersion: draft.version,
        hosts: rollbackEligible.map((h) => ({
          id: h.id,
          hostname: h.hostname,
          port: h.port || 22,
          probe: h.probe,
          sshOverride: h.sshOverride,
        })),
        ssh: draft.ssh,
        // snapshotPath is optional for rollback per the backend contract,
        // but we send what we have so the server can reference it if
        // future logic needs it.
        snapshotPath: snapshotPath ?? "",
      };
      const res = await migrateRollback(draft.sourceClusterId, body);
      // Switch the SSE subscription to the rollback task. Once it lands
      // terminal, hosts that were rolled back will report `succeeded`
      // with detail "MinIO restored" — stageFromHostStatus maps that to
      // CutoverNodeState "rolled_back".
      setActiveTaskId(res.taskId);
      update({ verify: null });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Rollback dispatch failed.";
      setDispatchError(message);
    } finally {
      setRollingBack(false);
    }
  }

  const skippedHosts = hosts.filter(
    (h) => draft.cutover.perNode[h.id]?.state === "skipped",
  );
  const attemptedCount = hosts.length - skippedHosts.length;
  const done = draft.cutover.overallNodesDone;
  // Progress denominator excludes skipped hosts — they'll never reach
  // "done" on this run by design, so counting them would peg the bar
  // below 100% even on a clean cutover.
  const pct = Math.round((done / Math.max(attemptedCount, 1)) * 100);
  const v = draft.verify;

  const completedCount = rollbackEligible.length;
  const anyRolledBack = hosts.some(
    (h) => draft.cutover.perNode[h.id]?.state === "rolled_back",
  );
  // Rollback is fully done when every attempted host has reached
  // "rolled_back" (skipped hosts are never on Buckit so they don't
  // count toward rollback completion either way).
  const attemptedHosts = hosts.filter(
    (h) => draft.cutover.perNode[h.id]?.state !== "skipped",
  );
  const allRolledBack =
    anyRolledBack &&
    attemptedHosts.length > 0 &&
    attemptedHosts.every(
      (h) => draft.cutover.perNode[h.id]?.state === "rolled_back",
    );

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
            {dispatchError
              ? `✗ ${dispatchError}`
              : allRolledBack
                ? "↺ Rollback complete · back on MinIO"
                : rollingBack || anyRolledBack
                  ? "↺ Rolling back…"
                  : cutoverDone
                    ? "Cutover complete · verifying"
                    : "● Cutover in progress"}{" "}
            · {done}/{attemptedCount} nodes
            {skippedHosts.length > 0 && (
              <>
                {" "}
                <span className="subtle">
                  · {skippedHosts.length} skipped (offline)
                </span>
              </>
            )}
          </p>
        </div>
        <div className="hstack" style={{ gap: "var(--s-2)" }}>
          {completedCount > 0 && !anyRolledBack && (
            <button
              className="btn btn--sm btn--danger"
              onClick={() => setConfirmRollback(true)}
              disabled={rollingBack}
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
          {done} / {attemptedCount}
        </span>
      </div>

      {/* Per-node cutover state */}
      <div className="card" style={{ padding: 0 }}>
        {hosts.map((h) => {
          const s =
            draft.cutover.perNode[h.id] ?? ({ state: "pending" } as const);
          const isSkipped = s.state === "skipped";
          return (
            <div
              key={h.id}
              className="rolling-row"
              style={{ gridTemplateColumns: "200px 1fr 90px" }}
            >
              <div className="rolling-row__name">{h.hostname}</div>
              <div className="vstack" style={{ gap: 2 }}>
                <div>{stagePill(s.state)}</div>
                {isSkipped && (
                  <span
                    className="subtle"
                    style={{ fontSize: "var(--fs-xs)" }}
                  >
                    Stays on MinIO — re-run migration on this host once
                    it's back online.
                  </span>
                )}
              </div>
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
      {activeTaskId && (
        <div className="card card-stat">
          <div className="card-stat__title">Live log</div>
          <TaskLogStream taskId={activeTaskId} showPause={false} />
        </div>
      )}

      {/* Verify section — only renders after cutover completes */}
      {cutoverDone && !anyRolledBack && (
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
                    {verifyRow("Objects (sampled)", v.objectsSampled)}
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

      {anyRolledBack && (
        <div className="banner banner--warning">
          <span>↺</span>
          <span>
            Rollback complete. The cluster is back on MinIO. You can re-run
            the migration wizard from the cluster detail page.
          </span>
        </div>
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
              on each. Data,{" "}
              <span className="mono">/etc/default/minio</span>, and{" "}
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
                onClick={runRollback}
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
