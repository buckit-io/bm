import { NewClusterDraft } from "../state";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

export function Topology({ draft, update }: Props) {
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const nodes = validHosts.length;
  const t = draft.topology;
  const totalDrives = nodes * t.drivesPerNode;
  const rawTiB = totalDrives * t.driveSizeTiB;
  const usableTiB = rawTiB - rawTiB * (t.parity / t.setSize);

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Topology</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Computed defaults shown below. Adjust parity and drive selection if needed.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Pool 1</h3>
        <div className="topology__grid">
          <div>
            <div className="field-label">Nodes</div>
            <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>{nodes}</div>
          </div>
          <div>
            <div className="field-label">Drives / node</div>
            <input
              className="input"
              type="number"
              value={t.drivesPerNode}
              onChange={(e) =>
                update({
                  topology: {
                    ...t,
                    drivesPerNode: parseInt(e.target.value, 10) || 1,
                  },
                })
              }
            />
          </div>
          <div>
            <div className="field-label">Set size</div>
            <select
              className="select"
              value={t.setSize}
              onChange={(e) =>
                update({ topology: { ...t, setSize: parseInt(e.target.value, 10) } })
              }
            >
              {[8, 12, 16, 24].map((n) => (
                <option key={n}>{n}</option>
              ))}
            </select>
          </div>
          <div>
            <div className="field-label">Parity</div>
            <select
              className="select"
              value={t.parity}
              onChange={(e) =>
                update({
                  topology: {
                    ...t,
                    parity: parseInt(e.target.value, 10) as Props["draft"]["topology"]["parity"],
                  },
                })
              }
            >
              {[2, 3, 4, 6, 8].map((n) => (
                <option key={n}>{n}</option>
              ))}
            </select>
          </div>
        </div>

        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            Usable capacity: <b>~{Math.round(usableTiB)} TiB</b> of {Math.round(rawTiB)} TiB raw
          </div>
          <button className="btn btn--sm">+ Add another pool</button>
        </div>

        <div className="banner banner--info">
          <span>ℹ</span>
          <span>
            EC:{t.parity} tolerates loss of up to {t.parity} drives per set of {t.setSize}.
          </span>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Disk selection</h3>
        <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          Boot drives are excluded by default. Click <i>Select drives</i> on a node to override.
        </p>
        <div className="card card--table">
          <table className="table">
            <thead>
              <tr>
                <th>Node</th>
                <th>Drives selected</th>
                <th style={{ textAlign: "right" }}></th>
              </tr>
            </thead>
            <tbody>
              {validHosts.map((h) => (
                <tr key={h.id}>
                  <td className="mono">{h.hostname}</td>
                  <td>
                    <span className="subtle">
                      {t.drivesPerNode} of {t.drivesPerNode + 1} (boot excluded)
                    </span>
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button className="btn btn--ghost btn--sm">Select drives…</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
