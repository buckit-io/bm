import { useEffect, useState } from "react";
import { MigrationDraft, PreflightResult } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

const CHECKS: Omit<PreflightResult, "result">[] = [
  { id: "ssh", label: "SSH reachability" },
  { id: "sudo", label: "Sudo (passwordless)" },
  { id: "time", label: "Time sync (skew < 1s)" },
  { id: "admin", label: "MinIO admin API reachable on all nodes" },
  { id: "healthy", label: "Current MinIO cluster healthy" },
  { id: "xl", label: "xl.meta format version compatible" },
  { id: "minio_sys", label: ".minio.sys/ readable on all drives" },
  { id: "env", label: "/etc/default/minio present and readable" },
  { id: "pkg", label: "Package manager available (dnf)" },
  { id: "rpm", label: "buckit-1.0.0.rpm reachable" },
  { id: "minio_pkg", label: "minio installed via package manager", detail: "Required for clean dnf remove on Finalize" },
  { id: "no_conflict", label: "No package conflicts (minio ↔ buckit)", detail: "Verified buckit package does not claim /etc/default/minio" },
  { id: "root_creds", label: "Root credentials valid" },
  { id: "no_admin_ops", label: "No in-flight admin operations", detail: "No active healing, decommission, or rebalance jobs" },
];

function pill(r: PreflightResult["result"]) {
  switch (r) {
    case "pass": return <Pill tone="success" icon="✓">Pass</Pill>;
    case "warn": return <Pill tone="warning" icon="⚠">Warning</Pill>;
    case "fail": return <Pill tone="danger" icon="✗">Fail</Pill>;
  }
}

export function Preflight({ draft, update }: Props) {
  const [running, setRunning] = useState(false);
  const run = () => {
    setRunning(true);
    update({ preflight: [] });
    let i = 0;
    const out: PreflightResult[] = [];
    const tick = () => {
      if (i >= CHECKS.length) {
        setRunning(false);
        return;
      }
      out.push({ ...CHECKS[i], result: "pass" });
      update({ preflight: [...out] });
      i++;
      setTimeout(tick, 120);
    };
    tick();
  };
  useEffect(() => {
    if (draft.preflight.length === 0) run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Preflight</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Migration-specific checks layered on top of the standard preflight.
          </p>
        </div>
        <button className="btn btn--sm" onClick={run} disabled={running}>Re-run</button>
      </header>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr><th>Check</th><th style={{ width: 110 }}>Nodes</th><th style={{ width: 110 }}>Result</th></tr>
          </thead>
          <tbody>
            {draft.preflight.map((p) => (
              <>
                <tr key={p.id}>
                  <td>{p.label}</td>
                  <td className="subtle">{draft.hosts.length} / {draft.hosts.length}</td>
                  <td>{pill(p.result)}</td>
                </tr>
                {p.detail && (
                  <tr key={p.id + "-d"} className="discover__detail">
                    <td colSpan={3} className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
                      ↳ {p.detail}
                    </td>
                  </tr>
                )}
              </>
            ))}
          </tbody>
        </table>
      </div>

      <div className="banner banner--info">
        <span>ℹ</span>
        <span>
          A failing "current cluster healthy" check is a hard block — migrating
          an already-degraded cluster is not supported in Phase 1.
        </span>
      </div>
    </div>
  );
}
