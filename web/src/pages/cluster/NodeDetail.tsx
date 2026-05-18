import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { useCluster, useNode } from "../../api/hooks";
import { Pill } from "../../components/Pill";
import { formatBytes, formatDuration } from "../../mock/data";
import { OperationDef } from "./operations/defs";
import { OperationModal } from "./operations/OperationModal";
import {
  NODE_OPERATIONS,
  NODE_GROUP_LABELS,
} from "./operations/nodeCatalog";

// Ops grouped by NODE_GROUP_LABELS for menu rendering; preserves the
// catalog's declared order.
function groupedNodeOps(): { group: string; ops: OperationDef[] }[] {
  const groups: { group: string; ops: OperationDef[] }[] = [];
  for (const op of NODE_OPERATIONS) {
    let bucket = groups.find((g) => g.group === op.group);
    if (!bucket) {
      bucket = { group: op.group, ops: [] };
      groups.push(bucket);
    }
    bucket.ops.push(op);
  }
  return groups;
}

// Per-op gating: SSH-flavored ops need cluster.sshConfigured; API-
// flavored ops (logs/trace navigate) need this node's API to be up.
function opRequires(op: OperationDef): "ssh" | "api" | undefined {
  if (op.flavor === "navigate") return "api";
  return "ssh";
}

export function NodeDetail() {
  const { clusterId, nodeId } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data: cluster } = useCluster(clusterId);
  const { data: node, isLoading } = useNode(clusterId, nodeId);

  const [actionsOpen, setActionsOpen] = useState(false);
  const [activeOp, setActiveOp] = useState<OperationDef | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const groups = useMemo(() => groupedNodeOps(), []);

  useEffect(() => {
    if (!actionsOpen) return;
    const onClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setActionsOpen(false);
      }
    };
    window.addEventListener("mousedown", onClick);
    return () => window.removeEventListener("mousedown", onClick);
  }, [actionsOpen]);

  if (isLoading) return <p className="muted">Loading…</p>;
  if (!node) return <p className="muted">Node not found.</p>;

  const dataDrives = node.drives.filter((d) => !d.isBoot);

  const sshOk = cluster?.sshConfigured ?? false;
  const apiOk = node.apiAccessible;

  const isDisabled = (op: OperationDef) => {
    const r = opRequires(op);
    return (r === "ssh" && !sshOk) || (r === "api" && !apiOk);
  };

  const disabledHint = (op: OperationDef) => {
    const r = opRequires(op);
    return r === "ssh" && !sshOk
      ? "SSH is not configured for this cluster."
      : r === "api" && !apiOk
        ? "S3 API is not reachable on this node."
        : undefined;
  };

  const onPick = (op: OperationDef) => {
    if (!cluster) return;
    setActionsOpen(false);
    if (op.flavor === "navigate" && op.navigateTo) {
      navigate(op.navigateTo(cluster, { nodeId: node.id }));
      return;
    }
    setActiveOp(op);
  };

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
        <div className="cdetail__menu-wrap" ref={menuRef}>
          <button
            className="btn btn--primary"
            onClick={() => setActionsOpen((v) => !v)}
            aria-haspopup="menu"
            aria-expanded={actionsOpen}
          >
            Actions ▾
          </button>
          {actionsOpen && (
            <div className="cdetail__menu cdetail__menu--wide" role="menu">
              {groups.map(({ group, ops }) => (
                <div key={group}>
                  <div className="cdetail__menu-group">
                    {NODE_GROUP_LABELS[group] ?? group}
                  </div>
                  {ops.map((op) => (
                    <button
                      key={op.id}
                      className={
                        "cdetail__menu-item cdetail__menu-item--stacked" +
                        (op.danger ? " is-danger" : "")
                      }
                      onClick={() => onPick(op)}
                      disabled={isDisabled(op)}
                      title={disabledHint(op)}
                      role="menuitem"
                    >
                      <span>{op.label}</span>
                      <span
                        className="subtle"
                        style={{ fontSize: "var(--fs-xs)" }}
                      >
                        {op.description}
                      </span>
                    </button>
                  ))}
                </div>
              ))}
            </div>
          )}
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

      {activeOp && cluster && (
        <OperationModal
          def={activeOp}
          cluster={cluster}
          targetHostIds={[node.id]}
          scopeLabel={`on ${node.hostname}`}
          onClose={() => {
            setActiveOp(null);
            qc.invalidateQueries({ queryKey: ["node", clusterId, nodeId] });
            qc.invalidateQueries({ queryKey: ["cluster", clusterId] });
            qc.invalidateQueries({ queryKey: ["history"] });
          }}
        />
      )}
    </section>
  );
}
