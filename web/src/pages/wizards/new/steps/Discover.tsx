import { useEffect, useRef, useState } from "react";
import { NewClusterDraft, DiscoveryResult } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

function statusPill(s: DiscoveryResult["state"]) {
  switch (s) {
    case "done": return <Pill tone="success" icon="✓">Done</Pill>;
    case "running": return <Pill tone="info" icon="⟳">…</Pill>;
    case "failed": return <Pill tone="danger" icon="✗">Timeout</Pill>;
    default: return <Pill tone="neutral">Pending</Pill>;
  }
}

export function Discover({ draft, update }: Props) {
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const [expanded, setExpanded] = useState<string | null>(null);
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    const initial: Record<string, DiscoveryResult> = {};
    validHosts.forEach((h) => (initial[h.id] = { state: "pending" }));
    update({ discovery: initial });

    let i = 0;
    const tick = () => {
      if (i >= validHosts.length) return;
      const h = validHosts[i];
      i++;
      update({
        discovery: {
          ...initial,
          [h.id]: { state: "running" },
        },
      });
      setTimeout(() => {
        // mutate the result inline; React state must be set from latest
        initial[h.id] =
          i === validHosts.length && validHosts.length >= 8
            ? { state: "failed" }
            : {
                state: "done",
                os: "Ubuntu 24.04",
                kernel: "6.8.0-31-generic",
                cores: 16,
                ramGiB: 64,
                drives: 12,
                driveSizeTiB: 16,
                nic: "eno1 / 10 GbE",
              };
        update({ discovery: { ...initial } });
        tick();
      }, 600 + Math.random() * 600);
    };
    tick();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const done = Object.values(draft.discovery).filter((r) => r.state === "done").length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Discovery</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Probing each host over SSH for OS, disks, NIC and existing services.
        </p>
      </header>

      <div className="hstack">
        <span className="subtle">Progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 280 }}>
          <div
            className="progress__bar"
            style={{ width: `${(done / validHosts.length) * 100 || 0}%` }}
          />
        </div>
        <span className="subtle">
          {done} / {validHosts.length} complete
        </span>
      </div>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>OS</th>
              <th>Cores</th>
              <th>RAM</th>
              <th>Disks</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {validHosts.map((h) => {
              const r = draft.discovery[h.id] ?? { state: "pending" as const };
              return (
                <>
                  <tr
                    key={h.id}
                    onClick={() => setExpanded(expanded === h.id ? null : h.id)}
                    style={{ cursor: "pointer" }}
                  >
                    <td>
                      <span className="mono">{h.hostname}</span>
                      <span className="subtle" style={{ marginLeft: 6 }}>
                        {expanded === h.id ? "▾" : "▸"}
                      </span>
                    </td>
                    <td>{r.os ?? "—"}</td>
                    <td>{r.cores ?? "—"}</td>
                    <td>{r.ramGiB ? `${r.ramGiB} GiB` : "—"}</td>
                    <td>
                      {r.drives ? `${r.drives} × ${r.driveSizeTiB} TiB` : "—"}
                    </td>
                    <td>{statusPill(r.state)}</td>
                  </tr>
                  {expanded === h.id && r.state === "done" && (
                    <tr key={h.id + "-detail"} className="discover__detail">
                      <td colSpan={6}>
                        <div className="vstack subtle" style={{ gap: 4, padding: "var(--s-2) 0" }}>
                          <span><b>Kernel</b> {r.kernel}</span>
                          <span><b>NIC</b> {r.nic}</span>
                          <span><b>Time skew vs manager</b> &lt; 1s</span>
                          <span><b>Existing services</b> none detected</span>
                        </div>
                      </td>
                    </tr>
                  )}
                </>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
