import { useEffect, useState } from "react";
import { NewClusterDraft, PreflightResult } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

const CHECKS: Omit<PreflightResult, "result">[] = [
  { id: "ssh", label: "SSH reachability" },
  { id: "sudo", label: "Sudo (passwordless)" },
  { id: "time", label: "Time sync (skew < 1s)" },
  { id: "free", label: "Free space on selected drives" },
  { id: "ports", label: "Inter-node port reachability", detail: "Verified ports: 9000, 9001" },
  { id: "dns", label: "DNS / hostname resolution" },
  { id: "pkg", label: "Package manager available (dnf)" },
  { id: "rpm", label: "buckit-1.0.0.rpm reachable" },
  { id: "buckit_pkg", label: "Existing buckit package" },
  { id: "minio_svc", label: "Existing minio service" },
  { id: "ports_conflict", label: "Conflicting listeners on 9000/9001" },
  { id: "ulimit", label: "Kernel ulimit (nofile ≥ 65536)" },
];

function resultPill(r: PreflightResult["result"]) {
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
      const base = CHECKS[i];
      // pseudo-random result distribution: mostly pass, a couple warnings
      const result: PreflightResult["result"] =
        base.id === "dns" || base.id === "minio_svc" ? "warn" : "pass";
      const detailMap: Record<string, string> = {
        dns: "node5 cannot resolve node8.example.com",
        minio_svc: "node3 has minio installed but not running",
      };
      out.push({ ...base, result, detail: detailMap[base.id] ?? base.detail });
      update({ preflight: [...out] });
      i++;
      setTimeout(tick, 150);
    };
    tick();
  };

  useEffect(() => {
    if (draft.preflight.length === 0) run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const warnings = draft.preflight.filter((p) => p.result === "warn").length;
  const failures = draft.preflight.filter((p) => p.result === "fail").length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Preflight</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Verifying the nodes are ready to receive Buckit.
          </p>
        </div>
        <div className="hstack">
          {warnings > 0 && (
            <Pill tone="warning" icon="⚠">
              {warnings} warning{warnings > 1 ? "s" : ""}
            </Pill>
          )}
          {failures > 0 && <Pill tone="danger" icon="✗">{failures} failed</Pill>}
          <button className="btn btn--sm" onClick={run} disabled={running}>
            Re-run
          </button>
        </div>
      </header>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Check</th>
              <th style={{ width: 120 }}>Nodes</th>
              <th style={{ width: 110 }}>Result</th>
            </tr>
          </thead>
          <tbody>
            {draft.preflight.map((p) => (
              <>
                <tr key={p.id}>
                  <td>{p.label}</td>
                  <td className="subtle">8 / 8</td>
                  <td>{resultPill(p.result)}</td>
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

      {warnings > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            Warnings detected. You can continue, but resolving them is recommended.
          </span>
        </div>
      )}
    </div>
  );
}
