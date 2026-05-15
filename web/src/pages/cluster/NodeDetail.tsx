import { useParams } from "react-router-dom";
import { useNode } from "../../api/hooks";
import { Pill } from "../../components/Pill";
import { formatBytes, formatDuration } from "../../mock/data";

export function NodeDetail() {
  const { clusterId, nodeId } = useParams();
  const { data: node, isLoading } = useNode(clusterId, nodeId);
  if (isLoading) return <p className="muted">Loading…</p>;
  if (!node) return <p className="muted">Node not found.</p>;

  const dataDrives = node.drives.filter((d) => !d.isBoot);

  return (
    <section className="vstack" style={{ gap: "var(--s-5)" }}>
      <header
        className="hstack"
        style={{ justifyContent: "space-between", alignItems: "flex-start" }}
      >
        <div>
          <h1 style={{ fontSize: "var(--fs-2xl)", fontWeight: 600 }}>
            {node.hostname}
          </h1>
          <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
            <Pill tone={node.state === "online" ? "success" : "warning"} icon="●">
              {node.state[0].toUpperCase() + node.state.slice(1)}
            </Pill>{" "}
            · Buckit {node.version} · uptime {formatDuration(node.uptimeSec)}
          </p>
        </div>
        <div className="hstack">
          <button className="btn btn--sm">Restart</button>
          <button className="btn btn--sm">Actions ▾</button>
        </div>
      </header>

      <div className="cards-row">
        <div className="card card-stat">
          <div className="card-stat__title">System</div>
          <div className="card-stat__sub">
            <b>OS</b> {node.os ?? <span className="subtle">—</span>}
          </div>
          <div className="card-stat__sub">
            <b>Kernel</b>{" "}
            <span className="mono">{node.kernel ?? <span className="subtle">—</span>}</span>
          </div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Hardware</div>
          <div className="card-stat__sub">
            <b>CPU</b>{" "}
            {node.cpuModel ? (
              <>
                {node.cpuModel}
                {node.cpuCores !== undefined && (
                  <span className="subtle">
                    {" "}· {node.cpuCores}c
                    {node.cpuThreads !== undefined && `/${node.cpuThreads}t`}
                    {node.cpuMaxMHz !== undefined && ` · ${(node.cpuMaxMHz / 1000).toFixed(1)} GHz`}
                  </span>
                )}
              </>
            ) : (
              <span className="subtle">—</span>
            )}
          </div>
          <div className="card-stat__sub">
            <b>RAM</b>{" "}
            {node.ramBytes ? formatBytes(node.ramBytes) : <span className="subtle">—</span>}
          </div>
          <div className="card-stat__sub">
            <b>Network</b>{" "}
            {node.nic ? (
              <>
                {node.nic.iface}{" "}
                <span className="subtle">
                  ({node.nic.speedMbps >= 1000
                    ? `${node.nic.speedMbps / 1000} GbE`
                    : `${node.nic.speedMbps} Mbps`})
                </span>
              </>
            ) : (
              <span className="subtle">—</span>
            )}
          </div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Service</div>
          <div className="card-stat__sub"><b>Unit</b> buckit.service (active, enabled)</div>
          <div className="card-stat__sub"><b>Listen</b> :9000, :9001</div>
          <div className="card-stat__sub"><b>Last restart</b> 3 days ago</div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Connectivity</div>
          <div className="card-stat__sub">
            <b>Ping</b> {node.pingable ? "● reachable" : "✗ unreachable"}
          </div>
          <div className="card-stat__sub">
            <b>SSH</b> {node.sshable ? "● reachable" : "✗ failed"}
          </div>
          <div className="card-stat__sub">
            <b>S3 API</b> {node.apiAccessible ? "● ok" : "✗ down"}
          </div>
          <div className="card-stat__sub">
            <b>Console</b> {node.consoleAccessible ? "● ok" : "✗ down"}
          </div>
        </div>
      </div>

      <div className="card card--table">
        <div
          className="hstack"
          style={{
            padding: "var(--s-3) var(--s-4)",
            borderBottom: "1px solid var(--c-border)",
            justifyContent: "space-between",
          }}
        >
          <h2 className="card-stat__title">Drives</h2>
          <span className="subtle">{dataDrives.length} drives</span>
        </div>
        <table className="table">
          <thead>
            <tr>
              <th>Mount</th>
              <th>Device</th>
              <th>Size</th>
              <th>Used</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {dataDrives.map((d) => (
              <tr key={d.mount}>
                <td className="mono">{d.mount}</td>
                <td className="mono subtle">{d.device}</td>
                <td>{formatBytes(d.sizeBytes)}</td>
                <td className="subtle">
                  {Math.round((d.usedBytes / d.sizeBytes) * 100)}%
                </td>
                <td>
                  {d.state === "ready" ? (
                    <Pill tone="success" icon="●">Ready</Pill>
                  ) : d.state === "healing" ? (
                    <Pill tone="warning" icon="⟳">Healing {d.healingPct}%</Pill>
                  ) : (
                    <Pill tone="danger" icon="✗">Failed</Pill>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
