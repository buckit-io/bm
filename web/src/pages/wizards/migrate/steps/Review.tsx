// Preflight Check step. Runs two orchestrated phases against the real
// backend, plus a static plan summary:
//
//   1. POST /clusters/:id/migrate/snapshot — captures pre-migration
//      state to an on-disk JSON file. The cutover step reads this back.
//   2. POST /clusters/:id/migrate/preflight — runs the migration check
//      catalog (minio detected, mc-compatible version, drives ready,
//      space available, /etc/default writable, etc.).
//   3. Plan — static text describing the per-host cutover sequence.
//
// Each phase shows its detail inline beneath the phase row when done.

import { useEffect, useRef, useState } from "react";
import {
  MigrationDraft,
  MinioSnapshot,
  PreflightResult,
} from "../state";
import { Pill } from "../../../../components/Pill";
import { formatDuration } from "../../../../lib/format";
import {
  migratePreflight,
  migrateSnapshot,
} from "../../../../api/client";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

type PhaseState = "pending" | "running" | "done" | "failed";
type PhaseId = "snapshot" | "preflight" | "plan";

interface PhaseRow {
  id: PhaseId;
  label: string;
  state: PhaseState;
  detail?: string;
}

export function Review({ draft, update }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const fired = useRef(false);

  const [phases, setPhases] = useState<PhaseRow[]>([
    { id: "snapshot", label: "Capturing pre-migration snapshot", state: "pending" },
    { id: "preflight", label: "Running preflight checks", state: "pending" },
    { id: "plan", label: "Generating migration plan", state: "pending" },
  ]);
  const [reRunningPreflight, setReRunningPreflight] = useState(false);

  const setPhase = (id: PhaseId, patch: Partial<PhaseRow>) =>
    setPhases((rows) =>
      rows.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    );

  // runPreflight POSTs the migrate-preflight catalog and stores the
  // typed PreflightResult[] on the draft. The backend writes a history
  // row per call so the operator can audit when this last ran.
  const runPreflight = async (
    onComplete?: (failures: number) => void,
  ): Promise<void> => {
    setPhase("preflight", { state: "running", detail: undefined });
    update({ preflight: [] });
    try {
      const rows = await migratePreflight(draft.sourceClusterId, {
        // Send the wizard's hosts (including any per-host SSH port or
        // credential override). The backend prefers these over the
        // persisted node rows so operator edits made on the SSH page
        // take effect for the preflight SSH sessions.
        ssh: draft.ssh,
        // Version chosen on the Overview step. Without this the
        // backend's artifact_reachable check falls back to the stale
        // hardcoded default and reports "no artifact URL for version
        // v1.0.0".
        version: draft.version,
        hosts: hosts.map((h) => ({
          id: h.id,
          hostname: h.hostname,
          port: h.port || 22,
          probe: h.probe,
          sshOverride: h.sshOverride,
        })),
      });
      const list = rows as unknown as PreflightResult[];
      update({ preflight: list });
      const blocking = list.filter(
        (p) => p.severity === "blocking" && p.result === "fail",
      ).length;
      const warns = list.filter(
        (p) => p.result === "warn",
      ).length;
      setPhase("preflight", {
        state: "done",
        detail: blocking
          ? `${blocking} blocking ${blocking === 1 ? "failure" : "failures"}`
          : warns
            ? `${list.length} checks · ${warns} advisory`
            : `${list.length} checks passed`,
      });
      onComplete?.(blocking);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Preflight failed.";
      setPhase("preflight", { state: "failed", detail: message });
      onComplete?.(0);
    }
  };

  useEffect(() => {
    if (fired.current) return;
    fired.current = true;

    (async () => {
      // Phase 1: snapshot capture. The backend reads buckets / users /
      // groups / policies / lifecycle / notifications via admin + S3
      // and writes the JSON to ~/.config/bm/snapshots/<cluster>-<ts>.json.
      setPhase("snapshot", { state: "running" });
      try {
        const result = await migrateSnapshot(draft.sourceClusterId);
        // The wire response is { snapshot, summary, path }. The wizard
        // renders the counts shape (summary), not the full snapshot.
        const summary = (result as unknown as {
          summary: MinioSnapshot;
          path: string;
        });
        update({
          snapshot: summary.summary,
          snapshotPath: summary.path,
        });
        setPhase("snapshot", {
          state: "done",
          detail: `${hosts.length} hosts · ${summary.summary?.buckets ?? 0} buckets · ${summary.summary?.users ?? 0} users`,
        });
      } catch (err) {
        const message = err instanceof Error ? err.message : "Snapshot failed.";
        setPhase("snapshot", { state: "failed", detail: message });
        return;
      }

      // Phase 2: Preflight.
      await runPreflight((failures) => {
        // Phase 3: Plan — static.
        setPhase("plan", { state: "running" });
        setTimeout(() => {
          setPhase("plan", {
            state: "done",
            detail:
              failures > 0
                ? "Plan generated; resolve preflight blockers before cutover"
                : `Rolling, ~90 s per node, ~${formatDuration(hosts.length * 90)} total`,
          });
        }, 100);
      });
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onReRunPreflight = () => {
    setReRunningPreflight(true);
    runPreflight().finally(() => setReRunningPreflight(false));
  };

  const s = draft.snapshot;
  const blockingFails = draft.preflight.filter(
    (p) => p.severity === "blocking" && p.result === "fail",
  ).length;
  const advisoryWarns = draft.preflight.filter(
    (p) =>
      (p.severity === "advisory" && p.result === "warn") ||
      (p.severity === "blocking" && p.result === "warn"),
  ).length;

  const snapshotPhase = phases.find((p) => p.id === "snapshot")!;
  const preflightPhase = phases.find((p) => p.id === "preflight")!;
  const planPhase = phases.find((p) => p.id === "plan")!;

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          Preflight Check
        </h2>
        <p
          className="muted"
          style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
        >
          Inspecting the source cluster, capturing a snapshot, and
          validating readiness before cutover.
        </p>
      </header>

      {/* ── Phase 1: Capturing pre-migration snapshot ─────────────── */}
      <PhaseRowView row={snapshotPhase} />
      {snapshotPhase.state === "done" && s && (
        <div className="card card-stat" data-testid="migrate-snapshot-card">
          <div className="card-stat__title">Snapshot</div>
          <div className="card-stat__sub">
            <b>Buckets</b> {s.buckets}
            {s.largestBucket && (
              <>
                {" "}
                <span className="subtle">
                  · largest <span className="mono">{s.largestBucket.name}</span>
                  {" "}({s.largestBucket.size})
                </span>
              </>
            )}
          </div>
          <div className="card-stat__sub">
            <span className="subtle">
              {s.versioning} versioned · {s.lifecycle} with lifecycle ·{" "}
              {s.objectLock} with object-lock
            </span>
          </div>
          <div className="card-stat__sub">
            <b>IAM</b> {s.users} users · {s.groups} groups ·{" "}
            {s.customPolicies} custom policies · {s.serviceAccounts} service
            accounts
          </div>
          <div className="card-stat__sub">
            <b>Bucket configs</b> {s.policies} policies ·{" "}
            {s.notifications} notifications · {s.replicationTargets}{" "}
            replication targets
          </div>
          {s.offlineHosts && s.offlineHosts.length > 0 && (
            <div
              className="card-stat__sub"
              style={{ color: "var(--c-warning)" }}
            >
              <b>Offline at snapshot</b> {s.offlineHosts.length} host
              {s.offlineHosts.length === 1 ? "" : "s"} —{" "}
              <span className="mono">{s.offlineHosts.join(", ")}</span>
              <div
                className="subtle"
                style={{ fontSize: "var(--fs-xs)", marginTop: 2 }}
              >
                These hosts stay on MinIO. Re-run migration on each once
                it's back online.
              </div>
            </div>
          )}
          {draft.snapshotPath && (
            <div className="card-stat__sub subtle" style={{ fontSize: "var(--fs-xs)" }}>
              <span className="mono">{draft.snapshotPath}</span>
            </div>
          )}
        </div>
      )}
      {snapshotPhase.state === "failed" && (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>Snapshot failed: {snapshotPhase.detail}</span>
        </div>
      )}
      {s &&
        s.warnings.length > 0 &&
        snapshotPhase.state === "done" &&
        s.warnings.map((w, i) => (
          <div key={i} className="banner banner--warning">
            <span>⚠</span>
            <span>{w}</span>
          </div>
        ))}

      {/* ── Phase 2: Running preflight checks ─────────────────────── */}
      <PhaseRowView row={preflightPhase} />
      {preflightPhase.state === "done" && (
        <>
          {blockingFails > 0 ? (
            <div className="banner banner--danger">
              <span>✗</span>
              <span>
                {blockingFails} blocking preflight failure
                {blockingFails > 1 ? "s" : ""} — fix before starting
                migration.
              </span>
            </div>
          ) : advisoryWarns > 0 ? (
            <div className="banner banner--warning">
              <span>⚠</span>
              <span>
                {advisoryWarns} preflight warning
                {advisoryWarns > 1 ? "s" : ""} — review below, then
                continue.
              </span>
            </div>
          ) : (
            <div className="banner banner--success">
              <span>✓</span>
              <span>All preflight checks passed.</span>
            </div>
          )}

          <div className="card card--table" data-testid="migrate-preflight-table">
            <div
              className="hstack"
              style={{
                padding: "var(--s-3) var(--s-4)",
                borderBottom: "1px solid var(--c-border)",
                justifyContent: "space-between",
              }}
            >
              <h3 className="card-stat__title">Preflight checks</h3>
              <button
                className="btn btn--sm"
                onClick={onReRunPreflight}
                disabled={reRunningPreflight}
              >
                {reRunningPreflight ? "Re-running…" : "Re-run preflight"}
              </button>
            </div>
            <table className="table">
              <thead>
                <tr>
                  <th>Check</th>
                  <th style={{ width: 90 }}>Severity</th>
                  <th style={{ width: 110 }}>Result</th>
                </tr>
              </thead>
              <tbody>
                {draft.preflight.flatMap((p) => {
                  const failingHosts =
                    p.hostStatuses?.filter((s) => s.status !== "pass") ?? [];
                  return [
                    <tr key={p.id}>
                      <td>{p.label}</td>
                      <td>
                        <span
                          className="subtle"
                          style={{ fontSize: "var(--fs-xs)" }}
                        >
                          {p.severity === "blocking" ? "Blocking" : "Advisory"}
                        </span>
                      </td>
                      <td>{resultPill(p.result)}</td>
                    </tr>,
                    (p.detail || failingHosts.length > 0) && (
                      <tr key={p.id + "-d"} className="discover__detail">
                        <td colSpan={3}>
                          <div
                            className="vstack subtle"
                            style={{
                              gap: 2,
                              padding: "var(--s-2) 0",
                              fontSize: "var(--fs-xs)",
                            }}
                          >
                            {p.detail && <span>↳ {p.detail}</span>}
                            {failingHosts.map((s) => (
                              <span key={s.hostId}>
                                ↳ <span className="mono">{s.hostname}</span>
                                {s.message ? ` — ${s.message}` : ""}
                              </span>
                            ))}
                          </div>
                        </td>
                      </tr>
                    ),
                  ];
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
      {preflightPhase.state === "failed" && (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>Preflight failed: {preflightPhase.detail}</span>
        </div>
      )}

      {/* ── Phase 3: Generating migration plan ────────────────────── */}
      <PhaseRowView row={planPhase} />
      {planPhase.state === "done" && (
        <>
          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">
              Phase 1 — pre-stage <span className="subtle">(no downtime)</span>
            </h3>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              Runs concurrently across every online host. MinIO keeps
              serving. Any failure aborts the cutover with zero impact.
            </p>
            <ol
              style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}
            >
              <li>
                <span className="mono">curl -fSL -o /tmp/buckit.rpm &lt;url&gt;</span>{" "}
                <span className="subtle">(verify SHA256)</span>
              </li>
              <li>
                <span className="mono">dnf install -y /tmp/buckit.rpm</span>{" "}
                <span className="subtle">
                  (Buckit doesn't conflict with MinIO — different paths)
                </span>
              </li>
              <li>
                Write drop-in{" "}
                <span className="mono">
                  /etc/systemd/system/buckit.service.d/10-bm-migrated.conf
                </span>{" "}
                <span className="subtle">
                  (for using minio-user to run Buckit)
                </span>
              </li>
            </ol>
          </div>

          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">
              Phase 2 — cutover{" "}
              <span style={{ color: "var(--c-warning)" }}>
                (downtime begins)
              </span>
            </h3>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              Runs concurrently across every online host. Downtime is
              bounded by the slowest single host's stop + enable, not
              the sum.
            </p>
            <ol
              style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}
            >
              <li>
                <span className="mono">systemctl stop minio</span>
              </li>
              <li>
                <span className="mono">
                  systemctl daemon-reload && systemctl disable minio &&
                  systemctl enable --now buckit
                </span>
              </li>
            </ol>
          </div>

          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">
              Phase 3 — verify{" "}
              <span className="subtle">(downtime ends when satisfied)</span>
            </h3>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              Poll admin ServerInfo until every attempted host reports{" "}
              <span className="mono">online</span>. Default timeout 5
              minutes. If verify times out, every attempted host is
              auto-rolled-back to MinIO.
            </p>
          </div>

          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">
              Auto-rollback{" "}
              <span className="subtle">(on phase 2 or 3 failure)</span>
            </h3>
            <ol
              style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}
            >
              <li>
                <span className="mono">systemctl disable --now buckit</span>
              </li>
              <li>
                <span className="mono">dnf remove -y buckit</span>{" "}
                <span className="subtle">
                  (uninstalls the package; data and minio-user are
                  preserved)
                </span>
              </li>
              <li>
                Remove drop-in directory
              </li>
              <li>
                <span className="mono">systemctl enable --now minio</span>
              </li>
            </ol>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              Manual rollback after a successful cutover is also
              available on the next step.
            </p>
          </div>
        </>
      )}
    </div>
  );
}

function PhaseRowView({ row }: { row: PhaseRow }) {
  return (
    <div
      className="hstack"
      style={{
        gap: "var(--s-3)",
        alignItems: "center",
        padding: "var(--s-2) 0",
      }}
    >
      <span style={{ width: 18, textAlign: "center" }}>
        {row.state === "done" ? (
          <span style={{ color: "var(--c-success)" }}>✓</span>
        ) : row.state === "running" ? (
          <span style={{ color: "var(--c-info)" }}>⟳</span>
        ) : row.state === "failed" ? (
          <span style={{ color: "var(--c-danger)" }}>✗</span>
        ) : (
          <span className="subtle">·</span>
        )}
      </span>
      <span style={{ fontWeight: "var(--fw-medium)" }}>{row.label}</span>
      {row.detail && (
        <span className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          — {row.detail}
        </span>
      )}
    </div>
  );
}

function resultPill(r: PreflightResult["result"]) {
  switch (r) {
    case "pass":
      return <Pill tone="success" icon="✓">Pass</Pill>;
    case "warn":
      return <Pill tone="warning" icon="⚠">Warning</Pill>;
    case "fail":
      return <Pill tone="danger" icon="✗">Fail</Pill>;
    case "skipped":
      return <Pill tone="neutral">Skipped</Pill>;
  }
}
