import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  useCluster,
  useClusterHealthInfo,
  useClusterSshConfig,
  useNode,
  useRefreshCluster,
} from "../../api/hooks";
import type { HostHealthInfo } from "../../api/types";
import { Pill } from "../../components/Pill";
import { formatBytes, formatDuration, formatRelative } from "../../lib/format";
import { OperationDef } from "./operations/defs";
import { OperationModal } from "./operations/OperationModal";
import { SshRequiredDialog } from "./SshRequiredDialog";
import {
  NODE_OPERATIONS,
  NODE_GROUP_LABELS,
} from "./operations/nodeCatalog";
import type { ClusterDetailNavigationState, NodeDetailNavigationState } from "./navigationState";

function statusDot(ok: boolean) {
  return (
    <span
      aria-hidden
      style={{
        color: ok ? "var(--c-success)" : "var(--c-danger)",
        fontSize: "0.9em",
      }}
    >
      ●
    </span>
  );
}

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

// Per-op gating. Every current node op — including the "View service
// log" navigate op, which runs journalctl over SSH from the next
// screen (see internal/api/node_logs_handler.go) — needs SSH to the
// cluster. There are no API-gated node ops today.
function opRequires(op: OperationDef): "ssh" | "api" | undefined {
  void op;
  return "ssh";
}

// hostnameFromAddr strips the :port suffix from the upstream's reported
// endpoint so we can join HealthInfo hosts against domain.Node by hostname.
// Mirrors hostnameFromEndpoint in internal/api/m4_handlers.go.
function hostnameFromAddr(addr: string): string {
  let host = addr;
  const scheme = host.indexOf("://");
  if (scheme >= 0) host = host.slice(scheme + 3);
  const colon = host.indexOf(":");
  if (colon >= 0) host = host.slice(0, colon);
  const slash = host.indexOf("/");
  if (slash >= 0) host = host.slice(0, slash);
  return host.trim().toLowerCase();
}

function findHostFacts(
  hosts: HostHealthInfo[] | undefined,
  hostname: string,
): HostHealthInfo | undefined {
  if (!hosts || hosts.length === 0) return undefined;
  const want = hostname.trim().toLowerCase();
  const exact = hosts.find((h) => hostnameFromAddr(h.addr) === want);
  if (exact) return exact;
  // Fallback for single-host clusters: the upstream often reports its own
  // bind address (127.0.0.1, an internal IP, or the short hostname) which
  // won't match the operator-friendly name on the node row. When there's
  // only one host in the bundle, the join is unambiguous.
  if (hosts.length === 1) return hosts[0];
  return undefined;
}

export function NodeDetail() {
  const { clusterId, nodeId } = useParams();
  const location = useLocation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data: cluster } = useCluster(clusterId);
  const { data: sshConfig } = useClusterSshConfig(clusterId);
  const { data: node, isLoading } = useNode(clusterId, nodeId);
  const {
    data: healthInfo,
    isLoading: healthLoading,
    isError: healthErrored,
  } = useClusterHealthInfo(clusterId);
  const { mutateAsync: refreshClusterAsync } = useRefreshCluster(clusterId);
  const cameFromClusterDetail =
    (location.state as NodeDetailNavigationState | null)?.fromClusterDetail === true;

  const [actionsOpen, setActionsOpen] = useState(false);
  const [activeOp, setActiveOp] = useState<OperationDef | null>(null);
  const [showSshRequired, setShowSshRequired] = useState(false);
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

  // Auto-refresh polling — mirrors ClusterDetail. /refresh writes node State
  // and drives synchronously, then kicks off async ping/HTTP/SSH probes that
  // update connectivity flags ~1-6s later. We invalidate the single-node
  // query at those follow-up moments so the Connectivity card actually
  // reflects the latest probe results, not the snapshot from page mount.
  const refreshRunningRef = useRef(false);
  const followupTimersRef = useRef<number[]>([]);
  useEffect(() => {
    if (!clusterId || !nodeId) return;
    const clearFollowups = () => {
      for (const id of followupTimersRef.current) window.clearTimeout(id);
      followupTimersRef.current = [];
    };
    const scheduleProbePickup = () => {
      clearFollowups();
      for (const delayMs of [1000, 3000, 6000]) {
        const t = window.setTimeout(() => {
          qc.invalidateQueries({ queryKey: ["node", clusterId, nodeId] });
          qc.invalidateQueries({ queryKey: ["nodes", clusterId] });
        }, delayMs);
        followupTimersRef.current.push(t);
      }
    };
    const tick = () => {
      if (refreshRunningRef.current) return;
      refreshRunningRef.current = true;
      refreshClusterAsync()
        .then(() => scheduleProbePickup())
        .catch(() => {})
        .finally(() => {
          refreshRunningRef.current = false;
        });
    };
    tick(); // refresh on mount so the page never opens with stale data
    const id = window.setInterval(tick, 15 * 1000);
    return () => {
      window.clearInterval(id);
      clearFollowups();
    };
  }, [clusterId, nodeId, qc, refreshClusterAsync]);

  if (isLoading) return <p className="muted">Loading…</p>;
  if (!node) return <p className="muted">Node not found.</p>;

  const dataDrives = node.drives.filter((d) => !d.isBoot);

  // Merge facts: /healthinfo is authoritative for kernel/CPU/RAM/NIC (admin
  // /info only carries `OS` + `Arch`). Node-level fields are the fallback so
  // the cards still render before the healthinfo fetch resolves.
  const hostFacts = findHostFacts(healthInfo?.hosts, node.hostname);
  const osInfo = hostFacts?.os;
  const memInfo = hostFacts?.mem;
  const cpuPrimary = hostFacts?.cpus?.[0];
  const cpuPackages = hostFacts?.cpus?.length ?? 0;
  // madmin reports CPU.Cores per physical package; sum across sockets.
  const cpuCoresTotal = hostFacts?.cpus?.reduce((n, c) => n + (c.cores ?? 0), 0);

  // OS: prefer the richer platform+version+family triple. Fall back to the
  // bare "linux"/"darwin" string when healthinfo hasn't loaded.
  const platformBase = [osInfo?.platform, osInfo?.platformVersion]
    .filter(Boolean)
    .join(" ");
  const osLabel = platformBase || node.os || undefined;
  const osFamily = osInfo?.platformFamily;
  const kernel = osInfo?.kernelVersion ?? node.kernel;
  const kernelArch = osInfo?.kernelArch;
  const bootTimeISO =
    osInfo?.bootTime && osInfo.bootTime > 0
      ? new Date(osInfo.bootTime * 1000).toISOString()
      : undefined;

  const cpuModel = cpuPrimary?.modelName || node.cpuModel;
  const cpuVendor = cpuPrimary?.vendorId;
  const cpuCores = cpuCoresTotal || node.cpuCores;
  const cpuMaxMHz =
    cpuPrimary?.mhz && cpuPrimary.mhz > 0
      ? Math.round(cpuPrimary.mhz)
      : node.cpuMaxMHz;

  const ramTotal = memInfo?.total || node.ramBytes;
  const ramFree = memInfo?.free;

  const primaryNic = node.nic
    ? { iface: node.nic.iface, driver: undefined, speedMbps: node.nic.speedMbps }
    : hostFacts?.nics?.[0]
      ? {
          iface: hostFacts.nics[0].interface ?? "—",
          driver: hostFacts.nics[0].driver,
          speedMbps: undefined as number | undefined,
        }
      : undefined;

  // "Loading…" only when the request is in flight AND the field has no
  // node-level fallback. After error/no-creds, fall back to "—".
  const factsLoading = healthLoading && !hostFacts;
  const blankCell = factsLoading ? (
    <span className="subtle">Loading…</span>
  ) : (
    <span className="subtle">—</span>
  );
  const factsUnavailable =
    !healthLoading && (healthErrored || (healthInfo !== undefined && !hostFacts));

  const sshOk = !!sshConfig;
  const engineMismatch = (op: OperationDef): boolean =>
    !!op.buckitOnly && !!cluster && cluster.engine !== "buckit";

  const disabledHint = (op: OperationDef) => {
    if (engineMismatch(op)) {
      return "Only available on Buckit clusters. Migrate this cluster from MinIO to enable it.";
    }
    if (opRequires(op) === "ssh" && !sshOk) {
      return "SSH is not configured for this cluster.";
    }
    return undefined;
  };

  const onPick = (op: OperationDef) => {
    if (!cluster) return;
    if (engineMismatch(op)) return;
    setActionsOpen(false);
    const requirement = opRequires(op);
    if (requirement === "ssh" && !sshOk) {
      setShowSshRequired(true);
      return;
    }
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
          <div
            className="subtle"
            style={{ marginBottom: "var(--s-2)", fontSize: "var(--fs-sm)" }}
          >
            <Link to="/clusters">Clusters</Link>
            <span> / </span>
            <Link
              to={`/clusters/${clusterId}`}
              state={
                cameFromClusterDetail
                  ? ({ skipInitialRefresh: true } satisfies ClusterDetailNavigationState)
                  : undefined
              }
            >
              {cluster?.name ?? clusterId}
            </Link>
            <span> / </span>
            <span>{node.hostname}</span>
          </div>
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
            data-testid="node-actions-toggle"
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
                  {ops.map((op) => {
                    const disabled = engineMismatch(op);
                    return (
                      <button
                        key={op.id}
                        className={
                          "cdetail__menu-item cdetail__menu-item--stacked" +
                          (op.danger ? " is-danger" : "")
                        }
                        data-testid={`node-action-${op.id}`}
                        onClick={() => onPick(op)}
                        disabled={disabled}
                        title={disabled ? disabledHint(op) : undefined}
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
                    );
                  })}
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
            <b>OS</b>{" "}
            {osLabel ? (
              <>
                {osLabel}
                {osFamily && osFamily.toLowerCase() !== osLabel.toLowerCase() && (
                  <span className="subtle"> · {osFamily}</span>
                )}
              </>
            ) : (
              blankCell
            )}
          </div>
          <div className="card-stat__sub">
            <b>Kernel</b>{" "}
            {kernel ? (
              <>
                <span className="mono">{kernel}</span>
                {kernelArch && <span className="subtle"> · {kernelArch}</span>}
              </>
            ) : (
              blankCell
            )}
          </div>
          {bootTimeISO && (
            <div className="card-stat__sub">
              <b>Booted</b>{" "}
              <span title={bootTimeISO}>
                {bootTimeISO.replace("T", " ").replace(/\.\d+Z$/, "Z")}
              </span>{" "}
              <span className="subtle">· {formatRelative(bootTimeISO)}</span>
            </div>
          )}
          {factsUnavailable && !kernel && (
            <div
              className="card-stat__sub subtle"
              style={{ fontSize: "var(--fs-xs)" }}
            >
              Hardware facts require admin credentials.
            </div>
          )}
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Hardware</div>
          <div className="card-stat__sub">
            <b>CPU</b>{" "}
            {cpuModel || cpuVendor || cpuCores !== undefined ? (
              <>
                {cpuModel ?? cpuVendor ?? "CPU"}
                {(cpuVendor && cpuModel && cpuVendor !== cpuModel) ||
                cpuCores !== undefined ||
                cpuMaxMHz !== undefined ||
                cpuPackages > 1 ? (
                  <span className="subtle">
                    {cpuVendor && cpuModel && !cpuModel.includes(cpuVendor) && ` · ${cpuVendor}`}
                    {cpuPackages > 1 && ` · ${cpuPackages}× sockets`}
                    {cpuCores !== undefined && ` · ${cpuCores} cores`}
                    {node.cpuThreads !== undefined && ` / ${node.cpuThreads}t`}
                    {cpuMaxMHz !== undefined && ` · ${(cpuMaxMHz / 1000).toFixed(1)} GHz`}
                  </span>
                ) : null}
              </>
            ) : (
              blankCell
            )}
          </div>
          <div className="card-stat__sub">
            <b>RAM</b>{" "}
            {ramTotal ? (
              <>
                {formatBytes(ramTotal)}
                {ramFree !== undefined && ramFree > 0 && (
                  <span className="subtle"> · {formatBytes(ramFree)} free</span>
                )}
              </>
            ) : (
              blankCell
            )}
          </div>
          <div className="card-stat__sub">
            <b>Network</b>{" "}
            {primaryNic ? (
              <>
                {primaryNic.iface}{" "}
                <span className="subtle">
                  (
                  {primaryNic.speedMbps !== undefined
                    ? primaryNic.speedMbps >= 1000
                      ? `${primaryNic.speedMbps / 1000} GbE`
                      : `${primaryNic.speedMbps} Mbps`
                    : primaryNic.driver ?? "—"}
                  )
                </span>
              </>
            ) : (
              blankCell
            )}
          </div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Connectivity</div>
          <div className="card-stat__sub">
            <b>Ping</b> {statusDot(node.pingable)} {node.pingable ? "reachable" : "unreachable"}
          </div>
          <div className="card-stat__sub">
            <b>SSH</b> {statusDot(node.sshable)} {node.sshable ? "reachable" : "failed"}
          </div>
          <div className="card-stat__sub">
            <b>S3 API</b> {statusDot(node.apiAccessible)} {node.apiAccessible ? "ok" : "down"}
          </div>
          <div className="card-stat__sub">
            <b>Console</b> {statusDot(node.consoleAccessible)} {node.consoleAccessible ? "ok" : "down"}
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
                  ) : d.state === "unknown" ? (
                    <Pill tone="neutral" icon="?">Unknown</Pill>
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
            refreshClusterAsync()
              .then(() => {
                qc.invalidateQueries({ queryKey: ["nodes", clusterId] });
              })
              .catch(() => {})
              .finally(() => {
                qc.invalidateQueries({ queryKey: ["node", clusterId, nodeId] });
                qc.invalidateQueries({ queryKey: ["cluster", clusterId] });
                qc.invalidateQueries({ queryKey: ["clusters"] });
              });
            qc.invalidateQueries({ queryKey: ["history"] });
          }}
        />
      )}
      {showSshRequired && cluster && (
        <SshRequiredDialog
          clusterName={cluster.name}
          onClose={() => setShowSshRequired(false)}
          onConfigure={() => {
            setShowSshRequired(false);
            navigate(`/clusters/${cluster.id}/ssh`);
          }}
        />
      )}
    </section>
  );
}
