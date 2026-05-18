// Preflight Check step. Runs three orchestrated phases:
//   1. Capturing pre-migration snapshot (per-host SSH discover + mc admin
//      info snapshot rolled into one phase from the operator's POV)
//   2. Running preflight checks
//   3. Generating migration plan
//
// Each phase shows its detail inline beneath the phase row when done.

import { useEffect, useRef, useState } from "react";
import {
  DiscoveredDrive,
  DiscoveryResult,
  MigrationDraft,
  MinioNodeInfo,
  MinioSnapshot,
  PreflightResult,
} from "../state";
import { Pill } from "../../../../components/Pill";
import { formatDuration } from "../../../../mock/data";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

type PhaseState = "pending" | "running" | "done";
type PhaseId = "snapshot" | "preflight" | "plan";

interface PhaseRow {
  id: PhaseId;
  label: string;
  state: PhaseState;
  detail?: string;
}

const TiB = 1024 ** 4;

// ── mock data ────────────────────────────────────────────────────

const SNAPSHOT: MinioSnapshot = {
  buckets: 142,
  largestBucket: { name: "logs-archive", size: "412 TiB" },
  versioning: 38,
  lifecycle: 21,
  objectLock: 4,
  users: 17,
  groups: 3,
  customPolicies: 11,
  serviceAccounts: 42,
  policies: 57,
  notifications: 9,
  replicationTargets: 3,
  warnings: [
    "This cluster replicates to external targets. Buckit will preserve the configuration; the targets must remain reachable post-migration.",
  ],
};

const PREFLIGHT_CHECKS: Omit<PreflightResult, "result">[] = [
  { id: "ssh", label: "SSH reachability", severity: "blocking" },
  { id: "sudo", label: "Sudo (passwordless)", severity: "blocking" },
  { id: "time", label: "Time sync (skew < 1s)", severity: "advisory" },
  { id: "admin", label: "MinIO admin API reachable on all nodes", severity: "blocking" },
  {
    id: "healthy",
    label: "Current MinIO cluster healthy",
    severity: "blocking",
    detail:
      "All nodes online, no in-flight heal / decommission / rebalance. Failed drives within parity tolerance are OK — Buckit will heal them after cutover.",
  },
  { id: "xl", label: "xl.meta format version compatible", severity: "blocking" },
  { id: "minio_sys", label: ".minio.sys/ readable on all drives", severity: "blocking" },
  { id: "env", label: "/etc/default/minio present and readable", severity: "blocking" },
  { id: "pkg", label: "Package manager available (dnf)", severity: "blocking" },
  { id: "rpm", label: "buckit-1.0.0.rpm reachable", severity: "blocking" },
  {
    id: "minio_pkg",
    label: "minio installed via package manager",
    severity: "advisory",
    detail: "Kept installed alongside Buckit to enable rollback at any time.",
  },
  {
    id: "no_conflict",
    label: "No package conflicts (minio ↔ buckit)",
    severity: "blocking",
    detail: "Verified buckit package does not claim /etc/default/minio",
  },
  { id: "root_creds", label: "Root credentials valid", severity: "blocking" },
  {
    id: "no_admin_ops",
    label: "No in-flight admin operations",
    severity: "blocking",
    detail: "No active healing, decommission, or rebalance jobs",
  },
];

function mockDrives(): DiscoveredDrive[] {
  const drives: DiscoveredDrive[] = [
    {
      device: "/dev/nvme0n1",
      mount: "/",
      sizeBytes: 256 * 1024 ** 3,
      fsType: "ext4",
      isBoot: true,
    },
  ];
  for (let i = 1; i <= 12; i++) {
    drives.push({
      device: `/dev/sd${String.fromCharCode(96 + i)}`,
      mount: `/data/disk${i}`,
      sizeBytes: 16 * TiB,
      fsType: "xfs",
    });
  }
  return drives;
}

// ── component ─────────────────────────────────────────────────────

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

  const runPreflight = (onComplete?: () => void) => {
    setPhase("preflight", { state: "running", detail: undefined });
    let i = 0;
    const out: PreflightResult[] = [];
    update({ preflight: [] });
    const tick = () => {
      if (i >= PREFLIGHT_CHECKS.length) {
        const blocking = out.filter(
          (p) => p.severity === "blocking" && p.result === "fail",
        ).length;
        const warns = out.filter(
          (p) =>
            (p.severity === "advisory" && p.result === "warn") ||
            p.result === "warn",
        ).length;
        setPhase("preflight", {
          state: "done",
          detail: blocking
            ? `${blocking} blocking ${blocking === 1 ? "failure" : "failures"}`
            : warns
              ? `${PREFLIGHT_CHECKS.length} checks · ${warns} advisory`
              : `${PREFLIGHT_CHECKS.length} checks passed`,
        });
        onComplete?.();
        return;
      }
      out.push({ ...PREFLIGHT_CHECKS[i], result: "pass" });
      update({ preflight: [...out] });
      i++;
      setTimeout(tick, 100);
    };
    tick();
  };

  useEffect(() => {
    if (fired.current) return;
    fired.current = true;

    // Phase 1: discover + snapshot combined.
    setPhase("snapshot", { state: "running" });
    setTimeout(() => {
      const discovery: Record<string, DiscoveryResult> = {};
      const detection: Record<string, MinioNodeInfo> = {};
      hosts.forEach((h) => {
        discovery[h.id] = {
          state: "done",
          os: "Ubuntu 24.04",
          arch: "amd64",
          kernel: "6.8.0-31-generic",
          cores: 16,
          ramGiB: 64,
          drives: mockDrives(),
          nic: "eno1 / 10 GbE",
          existingService: "minio",
          sudoOk: true,
        };
        detection[h.id] = {
          binaryPath: "/usr/local/bin/minio",
          binaryVersion: "RELEASE.2024-10-13T13-34-11Z",
          serviceUnit: "minio.service",
          serviceActive: true,
          envFile: "/etc/default/minio",
          minioVolumes: `https://${hosts[0]?.hostname || "node1"}{1...${hosts.length}}:9000/data/disk{1...12}`,
          minioOpts: "--console-address :9001",
          detectedPools: `1 pool, ${hosts.length}×12 drives, EC:4`,
        };
      });
      // Stagger snapshot completion so the phase row briefly shows
      // partial state, then jumps to done.
      setTimeout(() => {
        update({
          discovery,
          minioDetection: detection,
          snapshot: SNAPSHOT,
        });
        setPhase("snapshot", {
          state: "done",
          detail: `${hosts.length} hosts · ${SNAPSHOT.buckets} buckets · ${SNAPSHOT.users} users`,
        });

        // Phase 2: Preflight
        runPreflight(() => {
          // Phase 3: Plan — static
          setPhase("plan", { state: "running" });
          setTimeout(() => {
            setPhase("plan", {
              state: "done",
              detail: `Rolling, ~90 s per node, ~${formatDuration(hosts.length * 90)} total`,
            });
          }, 200);
        });
      }, 500);
    }, 600);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onReRunPreflight = () => {
    setReRunningPreflight(true);
    runPreflight(() => setReRunningPreflight(false));
  };

  const s = draft.snapshot;
  const m = Object.values(draft.minioDetection)[0];
  const sampleDriveCount =
    Object.values(draft.discovery)[0]?.drives?.filter((d) => !d.isBoot).length ?? 0;
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
      <PhaseRow row={snapshotPhase} />
      {snapshotPhase.state === "done" && (
        <div className="card card-stat">
          <div className="card-stat__title">Snapshot</div>
          <div className="card-stat__sub">
            <b>Hosts</b> {hosts.length} ·{" "}
            <b>MinIO</b>{" "}
            <span className="mono">{m?.binaryVersion ?? "—"}</span> ·{" "}
            <b>Drives / host</b> {sampleDriveCount} ·{" "}
            <b>Topology</b> {m?.detectedPools ?? "—"}
          </div>
          {s && (
            <>
              <div className="card-stat__sub">
                <b>Buckets</b> {s.buckets}{" "}
                <span className="subtle">
                  · {s.versioning} versioned · {s.lifecycle} with lifecycle
                </span>
              </div>
              <div className="card-stat__sub">
                <b>IAM</b> {s.users} users · {s.groups} groups ·{" "}
                {s.customPolicies} policies · {s.serviceAccounts} service
                accounts
              </div>
              <div className="card-stat__sub">
                <b>Bucket configs</b> {s.policies} policies ·{" "}
                {s.notifications} notifications · {s.replicationTargets}{" "}
                replication targets
              </div>
            </>
          )}
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
      <PhaseRow row={preflightPhase} />
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

          <div className="card card--table">
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
                {draft.preflight.flatMap((p) => [
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
                  p.detail && (
                    <tr key={p.id + "-d"} className="discover__detail">
                      <td
                        colSpan={3}
                        className="subtle"
                        style={{ fontSize: "var(--fs-xs)" }}
                      >
                        ↳ {p.detail}
                      </td>
                    </tr>
                  ),
                ])}
              </tbody>
            </table>
          </div>
        </>
      )}

      {/* ── Phase 3: Generating migration plan ────────────────────── */}
      <PhaseRow row={planPhase} />
      {planPhase.state === "done" && (
        <>
          <div className="card card-stat">
            <div className="card-stat__title">Plan</div>
            <div className="card-stat__sub">
              <b>Rolling</b> sequential, ~90 s per node
            </div>
            <div className="card-stat__sub">
              <b>Total</b> ~{formatDuration(hosts.length * 90)}
            </div>
            <div className="card-stat__sub">
              <b>Downtime per node</b> ~2–3 s
            </div>
            <div className="card-stat__sub">
              <b>Rollback</b> available after migration
            </div>
          </div>

          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">For each node, in sequence</h3>
            <ol
              style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}
            >
              <li>Wait for cluster quorum (other nodes must be healthy)</li>
              <li>
                scp <span className="mono">buckit-1.0.0.rpm</span> to{" "}
                <span className="mono">/tmp/</span>
              </li>
              <li>
                <span className="mono">
                  dnf install -y /tmp/buckit-1.0.0.rpm
                </span>{" "}
                <span className="subtle">
                  (minio still running; package lands binary + unit but
                  doesn't start the service)
                </span>
              </li>
              <li>
                Update <span className="mono">buckit.service</span> to use
                minio's user/group
              </li>
              <li>
                <span className="mono">systemctl daemon-reload</span>
              </li>
              <li>
                <span className="mono">systemctl stop minio</span>{" "}
                <span className="subtle">— downtime begins (~2–3 s)</span>
              </li>
              <li>
                <span className="mono">systemctl disable minio</span>
              </li>
              <li>
                <span className="mono">systemctl enable --now buckit</span>
              </li>
              <li>Wait for node-healthy probe (timeout 2 m) — downtime ends</li>
              <li>Wait for cluster-healthy probe (timeout 5 m) before next node</li>
            </ol>
          </div>

          <div className="card vstack" style={{ gap: "var(--s-2)" }}>
            <h3 className="card-stat__title">Rollback steps</h3>
            <ol
              style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}
            >
              <li>
                <span className="mono">systemctl stop buckit</span>{" "}
                <span className="subtle">— downtime begins (~2–3 s)</span>
              </li>
              <li>
                <span className="mono">systemctl disable buckit</span>
              </li>
              <li>
                <span className="mono">systemctl enable --now minio</span>
              </li>
              <li>Move to the next node</li>
            </ol>
          </div>
        </>
      )}
    </div>
  );
}

function PhaseRow({ row }: { row: PhaseRow }) {
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
