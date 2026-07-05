import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  useCluster,
  useClusterSshConfig,
  useNodes,
  useRefreshCluster,
} from "../../api/hooks";
import { Pill } from "../../components/Pill";
import { Cluster, Node } from "../../api/types";
import { formatBytes } from "../../lib/format";
import { OperationDef } from "./operations/defs";
import { OperationModal } from "./operations/OperationModal";
import { SshRequiredDialog } from "./SshRequiredDialog";
import { CLUSTER_OPERATIONS, GROUP_LABELS } from "./operations/catalog";
import { BULK_HOST_OPERATIONS } from "./operations/bulkHostCatalog";
import "./ClusterDetail.css";

// ---- small render helpers ----

function probeDot(ok: boolean, label: string) {
  return (
    <span
      className={"probe " + (ok ? "is-ok" : "is-bad")}
      title={`${label}: ${ok ? "ok" : "failed"}`}
      aria-label={`${label}: ${ok ? "ok" : "failed"}`}
    >
      {ok ? "●" : "✗"}
    </span>
  );
}

function nodeStatePill(n: Node) {
  switch (n.state) {
    case "online":
      return <Pill tone="success" icon="●">Online</Pill>;
    case "degraded":
      return <Pill tone="warning" icon="⚠">Degraded</Pill>;
    case "offline":
      return <Pill tone="danger" icon="✗">Offline</Pill>;
    case "unreachable":
      return <Pill tone="danger" icon="✗">Unreachable</Pill>;
    default:
      return <Pill tone="neutral" icon="○">Unknown</Pill>;
  }
}

function clusterHealthPill(c: Cluster) {
  switch (c.health) {
    case "healthy":
      return <Pill tone="success" icon="●">Healthy</Pill>;
    case "degraded":
      return <Pill tone="warning" icon="⚠">Degraded</Pill>;
    case "critical":
      return <Pill tone="danger" icon="✗">Critical</Pill>;
    default:
      return <Pill tone="neutral" icon="○">Unknown</Pill>;
  }
}

// Per-pool health rollup. Mirrors the cluster-level rule but scoped to one
// pool's nodes and drives.
type PoolHealth = "healthy" | "degraded" | "critical";

interface PoolSummary {
  pool: number;
  nodes: { total: number; online: number; degraded: number; offline: number };
  drives: { total: number; ready: number; healing: number; failed: number };
  health: PoolHealth;
}

function summarizePools(nodes: Node[], parity: number): PoolSummary[] {
  const map = new Map<number, PoolSummary>();
  for (const n of nodes) {
    let p = map.get(n.pool);
    if (!p) {
      p = {
        pool: n.pool,
        nodes: { total: 0, online: 0, degraded: 0, offline: 0 },
        drives: { total: 0, ready: 0, healing: 0, failed: 0 },
        health: "healthy",
      };
      map.set(n.pool, p);
    }
    p.nodes.total++;
    if (n.state === "online") p.nodes.online++;
    else if (n.state === "degraded") p.nodes.degraded++;
    else p.nodes.offline++;
    for (const d of n.drives ?? []) {
      if (d.isBoot) continue;
      if (d.state === "unknown") continue; // admin API down; exclude from counts
      p.drives.total++;
      if (d.state === "ready") p.drives.ready++;
      else if (d.state === "healing") p.drives.healing++;
      else p.drives.failed++;
    }
  }
  for (const p of map.values()) {
    if (p.drives.failed > parity || p.nodes.offline > parity) {
      p.health = "critical";
    } else if (
      p.nodes.degraded > 0 ||
      p.nodes.offline > 0 ||
      p.drives.healing > 0 ||
      p.drives.failed > 0
    ) {
      p.health = "degraded";
    } else {
      p.health = "healthy";
    }
  }
  return Array.from(map.values()).sort((a, b) => a.pool - b.pool);
}

function poolHealthPill(h: PoolHealth) {
  switch (h) {
    case "healthy":
      return <Pill tone="success" icon="●">Healthy</Pill>;
    case "degraded":
      return <Pill tone="warning" icon="⚠">Degraded</Pill>;
    case "critical":
      return <Pill tone="danger" icon="✗">Critical</Pill>;
  }
}

// Severity ordering used when picking which pools to show first in the
// collapsed view. Worst health rises to the top so a problem can never
// be hidden behind healthy pools.
const POOL_RANK: Record<PoolHealth, number> = {
  critical: 0,
  degraded: 1,
  healthy: 2,
};

// Limit of pools shown before the "Show N more" affordance appears.
const POOLS_LIMIT = 2;

// ---- sort plumbing ----

type SortKey =
  | "host"
  | "pool"
  | "state"
  | "version"
  | "ping"
  | "ssh"
  | "api"
  | "console"
  | "kernel"
  | "uptime";
type SortDir = "asc" | "desc";

// State severity used for sorting: online ranks lowest so ascending puts
// healthy nodes at the top.
const STATE_RANK: Record<Node["state"], number> = {
  online: 0,
  degraded: 1,
  unreachable: 2,
  offline: 3,
  unknown: 4,
};

function sortValue(n: Node, key: SortKey): string | number {
  switch (key) {
    case "host":    return n.hostname;
    case "pool":    return n.pool;
    case "state":   return STATE_RANK[n.state];
    case "version": return n.version ?? "";
    case "ping":    return n.pingable ? 1 : 0;
    case "ssh":     return n.sshable ? 1 : 0;
    case "api":     return n.apiAccessible ? 1 : 0;
    case "console": return n.consoleAccessible ? 1 : 0;
    case "kernel":  return n.kernel ?? "";
    case "uptime":  return n.uptimeSec ?? 0;
  }
}

// compareNodes always applies a (pool asc, hostname asc) tiebreaker after
// the primary sort. That way the default view groups by pool with hosts
// alphabetical within each pool, and every other sort produces a stable
// secondary ordering instead of shuffling tied rows.
function compareNodes(a: Node, b: Node, key: SortKey, dir: SortDir) {
  const av = sortValue(a, key);
  const bv = sortValue(b, key);
  let cmp =
    typeof av === "string"
      ? av.localeCompare(bv as string)
      : (av as number) - (bv as number);
  cmp = dir === "asc" ? cmp : -cmp;
  if (cmp !== 0) return cmp;
  if (key !== "pool") {
    const dp = a.pool - b.pool;
    if (dp !== 0) return dp;
  }
  if (key !== "host") {
    return a.hostname.localeCompare(b.hostname);
  }
  return 0;
}

interface SortHeaderProps {
  active: boolean;
  dir: SortDir;
  label: string;
  align?: "left" | "right" | "center";
  onClick: () => void;
  title?: string;
}

function SortHeader({ active, dir, label, align = "left", onClick, title }: SortHeaderProps) {
  return (
    <button
      type="button"
      className={"sort-th" + (active ? " is-active" : "")}
      style={{ justifyContent: align === "right" ? "flex-end" : align === "center" ? "center" : "flex-start" }}
      onClick={onClick}
      title={title}
    >
      <span>{label}</span>
      <span className="sort-th__arrow" aria-hidden>
        {active ? (dir === "asc" ? "↑" : "↓") : "↕"}
      </span>
    </button>
  );
}

// ---- the page ----


interface ActiveOp {
  op: OperationDef;
  targetHostIds?: string[];
  scopeLabel?: string;
}

function clusterOpRequiresSSH(op: OperationDef): boolean {
  return op.group === "ssh";
}

const BUCKIT_ONLY_HINT =
  "Only available on Buckit clusters. Migrate this cluster from MinIO to enable it.";

function opDisabledForEngine(op: OperationDef, cluster: Cluster): boolean {
  return !!op.buckitOnly && cluster.engine !== "buckit";
}

function isLocalManualCluster(cluster: Cluster, nodes: Node[] | undefined): boolean {
  if (cluster.nodeCount !== 1 || nodes?.length !== 1) return false;
  const os = (nodes[0].os ?? "").toLowerCase();
  return os === "darwin" || os === "windows";
}

export function ClusterDetailLayout() {
  const { clusterId } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data: cluster, isLoading } = useCluster(clusterId);
  const { data: sshConfig } = useClusterSshConfig(clusterId);
  const { data: nodes } = useNodes(clusterId);
  const { mutateAsync: refreshClusterAsync } = useRefreshCluster(clusterId);
  const [initialRefreshDone, setInitialRefreshDone] = useState(false);
  const refreshRunningRef = useRef(false);
  const followupTimersRef = useRef<number[]>([]);

  function clearFollowupTimers() {
    for (const id of followupTimersRef.current) window.clearTimeout(id);
    followupTimersRef.current = [];
  }

  function scheduleProbePickup() {
    if (!clusterId) return;
    clearFollowupTimers();
    for (const delayMs of [1000, 3000, 6000]) {
      const timer = window.setTimeout(() => {
        qc.invalidateQueries({ queryKey: ["nodes", clusterId] });
      }, delayMs);
      followupTimersRef.current.push(timer);
    }
  }

  function triggerRefresh() {
    if (!clusterId || refreshRunningRef.current) return;
    refreshRunningRef.current = true;
    refreshClusterAsync()
      .then(() => {
        scheduleProbePickup();
      })
      .catch(() => {})
      .finally(() => {
        refreshRunningRef.current = false;
        qc.invalidateQueries({ queryKey: ["cluster", clusterId] });
        qc.invalidateQueries({ queryKey: ["clusters"] });
        qc.invalidateQueries({ queryKey: ["nodes", clusterId] });
      });
  }

  useEffect(() => {
    if (!clusterId) {
      setInitialRefreshDone(true);
      return;
    }
    let canceled = false;
    refreshRunningRef.current = true;
    setInitialRefreshDone(false);
    refreshClusterAsync()
      .then(() => {
        scheduleProbePickup();
      })
      .catch(() => {})
      .finally(() => {
        refreshRunningRef.current = false;
        if (!canceled) setInitialRefreshDone(true);
      });
    return () => {
      canceled = true;
      clearFollowupTimers();
    };
  }, [clusterId, qc, refreshClusterAsync]);

  useEffect(() => {
    if (!clusterId) return;
    const id = window.setInterval(() => {
      if (refreshRunningRef.current) return;
      refreshRunningRef.current = true;
      refreshClusterAsync()
        .then(() => {
          scheduleProbePickup();
        })
        .catch(() => {})
        .finally(() => {
          refreshRunningRef.current = false;
        });
    }, 15 * 1000);
    return () => {
      window.clearInterval(id);
      clearFollowupTimers();
    };
  }, [clusterId, qc, refreshClusterAsync]);

  // ---- selection ----
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const toggleNode = (id: string) =>
    setSelected((s) => {
      const next = new Set(s);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  // ---- per-column filters ----
  const [hostFilter, setHostFilter] = useState("");
  const [poolFilter, setPoolFilter] = useState<"all" | number>("all");
  const [stateFilter, setStateFilter] = useState<"all" | Node["state"]>("all");
  const [versionFilter, setVersionFilter] = useState<string>("all");
  const [kernelFilter, setKernelFilter] = useState("");

  // ---- sort ----
  // Default: pool asc, hostname asc within pool (the tiebreaker in
  // compareNodes makes the secondary ordering automatic).
  const [sortKey, setSortKey] = useState<SortKey | null>("pool");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  // Pools card expand state. Default collapsed when there are more than
  // POOLS_LIMIT pools.
  const [poolsExpanded, setPoolsExpanded] = useState(false);
  const sortBy = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  };
  const sortActive = (key: SortKey) => sortKey === key;

  // ---- derived: distinct values for filter dropdowns ----
  const distinctPools = useMemo(
    () => Array.from(new Set((nodes ?? []).map((n) => n.pool))).sort(),
    [nodes],
  );
  const distinctVersions = useMemo(
    () =>
      Array.from(
        new Set((nodes ?? []).map((n) => n.version).filter(Boolean)),
      ) as string[],
    [nodes],
  );

  // ---- filtered + sorted list ----
  const filtered = useMemo(() => {
    const host = hostFilter.trim().toLowerCase();
    const kern = kernelFilter.trim().toLowerCase();
    const list = (nodes ?? []).filter((n) => {
      if (host && !n.hostname.toLowerCase().includes(host)) return false;
      if (poolFilter !== "all" && n.pool !== poolFilter) return false;
      if (stateFilter !== "all" && n.state !== stateFilter) return false;
      if (versionFilter !== "all" && n.version !== versionFilter) return false;
      if (kern && !(n.kernel ?? "").toLowerCase().includes(kern)) return false;
      return true;
    });
    if (sortKey) {
      list.sort((a, b) => compareNodes(a, b, sortKey, sortDir));
    }
    return list;
  }, [nodes, hostFilter, poolFilter, stateFilter, versionFilter, kernelFilter, sortKey, sortDir]);

  // Header checkbox indeterminate state.
  const headerRef = useRef<HTMLInputElement>(null);
  const visibleSelected = filtered.filter((n) => selected.has(n.id)).length;
  useEffect(() => {
    if (headerRef.current) {
      headerRef.current.indeterminate =
        visibleSelected > 0 && visibleSelected < filtered.length;
    }
  }, [visibleSelected, filtered.length]);
  const toggleAllVisible = () => {
    setSelected((s) => {
      const next = new Set(s);
      const allOn = filtered.every((n) => next.has(n.id));
      if (allOn) filtered.forEach((n) => next.delete(n.id));
      else filtered.forEach((n) => next.add(n.id));
      return next;
    });
  };

  // ---- cluster + host actions ----
  const [actionsOpen, setActionsOpen] = useState(false);
  const actionsMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!actionsOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as globalThis.Node | null;
      if (
        actionsMenuRef.current &&
        target &&
        !actionsMenuRef.current.contains(target)
      ) {
        setActionsOpen(false);
      }
    };
    window.addEventListener("mousedown", onMouseDown);
    return () => window.removeEventListener("mousedown", onMouseDown);
  }, [actionsOpen]);

  // ---- gear / settings menu ----
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!settingsOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as globalThis.Node | null;
      if (
        settingsMenuRef.current &&
        target &&
        !settingsMenuRef.current.contains(target)
      ) {
        setSettingsOpen(false);
      }
    };
    window.addEventListener("mousedown", onMouseDown);
    return () => window.removeEventListener("mousedown", onMouseDown);
  }, [settingsOpen]);
  const [activeOp, setActiveOp] = useState<ActiveOp | null>(null);
  const [showSshRequired, setShowSshRequired] = useState(false);
  const sshConfigured = !!sshConfig;
  const localManualCluster = cluster
    ? isLocalManualCluster(cluster, nodes)
    : false;
  const runHostAction = (op: OperationDef) => {
    if (!cluster || selected.size === 0) return;
    if (localManualCluster) {
      return;
    }
    if (!sshConfigured) {
      setShowSshRequired(true);
      return;
    }
    const selectedNodes = (nodes ?? []).filter((n) => selected.has(n.id));
    const scopeLabel =
      selectedNodes.length === 1
        ? `on ${selectedNodes[0].hostname}`
        : `on ${selectedNodes.length} hosts`;
    setActiveOp({
      op,
      targetHostIds: selectedNodes.map((n) => n.id),
      scopeLabel,
    });
  };

  if (isLoading || !initialRefreshDone) {
    return <p className="muted">Refreshing cluster…</p>;
  }
  if (!cluster) return <p className="muted">Cluster not found.</p>;

  const summary = cluster.healthSummary;
  const usedPct = cluster.usableBytes
    ? Math.round((cluster.usedBytes / cluster.usableBytes) * 100)
    : 0;
  const poolSummaries = summarizePools(nodes ?? [], cluster.parity);
  // Order by health severity, then by pool number — so when we truncate
  // the visible list, the worst pools are always shown.
  const poolsByPriority = [...poolSummaries].sort((a, b) => {
    const r = POOL_RANK[a.health] - POOL_RANK[b.health];
    return r !== 0 ? r : a.pool - b.pool;
  });
  const poolsOverflow = poolsByPriority.length > POOLS_LIMIT;
  const visiblePools =
    poolsOverflow && !poolsExpanded
      ? poolsByPriority.slice(0, POOLS_LIMIT)
      : poolsByPriority;

  const hostSelectionReady = selected.size > 0;
  const localManualHint =
    "Systemd/SSH actions are not available for local macOS or Windows single-node deployments.";
  const hostActionsHint =
    localManualCluster
      ? localManualHint
      : !sshConfigured
        ? "SSH is not configured for this cluster. Set it up in Settings to enable host actions."
        : selected.size === 0
          ? "Select one or more hosts below to enable actions."
          : null;

  return (
    <section className="cdetail">
      {/* ── header ──────────────────────────────────────────────────── */}
      <header className="cdetail__header">
        {/* breadcrumb spans full width above the title row */}
        <div
          className="subtle cdetail__breadcrumb"
        >
          <Link to="/clusters">Clusters</Link>
          <span> / </span>
          <span>{cluster.name}</span>
        </div>
        {/* title row: name+meta on the left, action buttons on the right */}
        <div className="cdetail__header-row">
          <div>
            <h1 className="cdetail__name" data-testid="cluster-title">
              {cluster.name}
              {clusterHealthPill(cluster)}
              <div className="cdetail__gear-wrap" ref={settingsMenuRef}>
                <button
                  type="button"
                  className="cdetail__gear-btn"
                  data-testid="cluster-settings-toggle"
                  onClick={() => setSettingsOpen((v) => !v)}
                  aria-haspopup="menu"
                  aria-expanded={settingsOpen}
                  title="Cluster settings"
                >
                  <span className="gear-icon">⚙</span> settings
                </button>
                {settingsOpen && (
                  <div className="cdetail__menu" role="menu">
                    {renderSettingsMenu(cluster, (op) => {
                      setSettingsOpen(false);
                      if (op.flavor === "navigate" && op.navigateTo) {
                        navigate(op.navigateTo(cluster));
                        return;
                      }
                      setActiveOp({ op });
                    })}
                  </div>
                )}
              </div>
            </h1>
            <p className="muted cdetail__meta">
              <span data-testid="cluster-meta">
                Version: {cluster.version} · {cluster.nodeCount} nodes · {cluster.poolCount} pool ·
              </span>
              EC:{cluster.parity}
              {cluster.migratedFrom &&
                ` · migrated from MinIO ${cluster.migratedFrom.version}`}
            </p>
          </div>
          <div className="hstack">
          {cluster.consoleUrl && (
            <a
              className="btn"
              href={cluster.consoleUrl}
              target="_blank"
              rel="noreferrer"
            >
              ↗ Open {cluster.engine === "minio" ? "MinIO" : "Buckit"} console
            </a>
          )}
          {cluster.engine === "minio" && (
            <button
              className="btn btn--primary"
              data-testid="cluster-migrate-link"
              onClick={() => navigate(`/clusters/migrate?from=${cluster.id}`)}
            >
              Migrate to Buckit
            </button>
          )}
          <div className="cdetail__menu-wrap" ref={actionsMenuRef}>
            <button
              className={
                cluster.engine === "minio" ? "btn" : "btn btn--primary"
              }
              data-testid="cluster-actions-toggle"
              onClick={() => setActionsOpen((v) => !v)}
              aria-haspopup="menu"
              aria-expanded={actionsOpen}
            >
              Cluster Actions ▾
            </button>
            {actionsOpen && (
              <div className="cdetail__menu cdetail__menu--wide" role="menu">
                {renderActionsMenu(
                  cluster,
                  localManualCluster ? localManualHint : undefined,
                  (op) => {
                    setActionsOpen(false);
                    if (clusterOpRequiresSSH(op) && !sshConfigured) {
                      setShowSshRequired(true);
                      return;
                    }
                    if (op.flavor === "navigate" && op.navigateTo) {
                      navigate(op.navigateTo(cluster));
                      return;
                    }
                    setActiveOp({ op });
                  },
                )}
              </div>
            )}
          </div>
        </div>
      </div>
      </header>

      {/* ── summary cards ──────────────────────────────────────────── */}
      <div className="cdetail__cards">
        <div className="card cdetail__card">
          <div className="cdetail__card-title">Health</div>
          <div className="cdetail__card-value">
            {clusterHealthPill(cluster)}
          </div>
          {summary && (
            <>
              <div className="cdetail__card-sub">
                {summary.nodes.online}/{summary.nodes.total} nodes online
                {summary.nodes.degraded > 0 && ` · ${summary.nodes.degraded} degraded`}
                {summary.nodes.offline > 0 && ` · ${summary.nodes.offline} offline`}
              </div>
              <div className="cdetail__card-sub">
                {summary.drives.ready}/{summary.drives.total} drives ready
                {summary.drives.healing > 0 && ` · ${summary.drives.healing} healing`}
                {summary.drives.failed > 0 && ` · ${summary.drives.failed} failed`}
              </div>
            </>
          )}
        </div>

        <div className="card cdetail__card">
          <div className="cdetail__card-title">Capacity</div>
          <div className="cdetail__card-value">
            {formatBytes(cluster.usedBytes)}{" "}
            <span className="subtle" style={{ fontSize: "var(--fs-md)" }}>
              / {formatBytes(cluster.usableBytes)}
            </span>
          </div>
          <div className="progress">
            <div className="progress__bar" style={{ width: `${usedPct}%` }} />
          </div>
          <div className="cdetail__card-sub">{usedPct}% used</div>
        </div>

        <div className="card cdetail__card">
          <div className="cdetail__card-title">Pools</div>
          {visiblePools.length === 0 ? (
            <div className="cdetail__card-sub">No pools — cluster has no nodes yet.</div>
          ) : (
            <>
              {visiblePools.map((p) => (
                <div key={p.pool} className="cdetail__pool-row">
                  <div className="cdetail__pool-head">
                    <span className="cdetail__pool-num">Pool {p.pool}</span>
                    {poolHealthPill(p.health)}
                  </div>
                  <div className="cdetail__card-sub">
                    {p.nodes.online}/{p.nodes.total} nodes
                    {p.nodes.degraded > 0 && ` · ${p.nodes.degraded} degraded`}
                    {p.nodes.offline > 0 && ` · ${p.nodes.offline} offline`}
                  </div>
                  <div className="cdetail__card-sub">
                    {p.drives.ready}/{p.drives.total} drives
                    {p.drives.healing > 0 && ` · ${p.drives.healing} healing`}
                    {p.drives.failed > 0 && ` · ${p.drives.failed} failed`}
                  </div>
                </div>
              ))}
              {poolsOverflow && (
                <button
                  type="button"
                  className="cdetail__pool-toggle"
                  onClick={() => setPoolsExpanded((v) => !v)}
                  aria-expanded={poolsExpanded}
                >
                  {poolsExpanded
                    ? "Show fewer pools ▲"
                    : `Show ${poolsByPriority.length - POOLS_LIMIT} more ${
                        poolsByPriority.length - POOLS_LIMIT === 1 ? "pool" : "pools"
                      } ▼`}
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {/* ── nodes table ─────────────────────────────────────────────── */}
      <div className="cdetail__nodes">
        <div className="cdetail__nodes-bar">
          <h2 className="cdetail__nodes-title">
            Nodes{" "}
            <span className="subtle">
              ({filtered.length} of {nodes?.length ?? 0})
            </span>
          </h2>
        </div>

        {/* always-visible bulk action bar */}
        <div
          className={
            "cdetail__bulkbar" + (hostSelectionReady ? " is-enabled" : " is-disabled")
          }
          role="region"
          aria-label="Host actions"
        >
          <span className="cdetail__bulkbar-count">
            {selected.size > 0
              ? `${selected.size} ${selected.size === 1 ? "host" : "hosts"} selected`
              : "Host actions"}
          </span>
          {selected.size > 0 && (
            <button
              className="btn btn--ghost btn--sm"
              onClick={() => setSelected(new Set())}
              aria-label="Clear selection"
            >
              ✕ Clear
            </button>
          )}
          {selected.size === 0 && hostActionsHint && (
            <span className="cdetail__bulkbar-hint subtle">
              {hostActionsHint}
            </span>
          )}
          <div className="grow" />
          <div className="hstack" style={{ flexWrap: "wrap" }}>
            {BULK_HOST_OPERATIONS.map((op) => {
              const engineDisabled = opDisabledForEngine(op, cluster);
              const disabled = localManualCluster || !hostSelectionReady || engineDisabled;
              const title = engineDisabled
                ? BUCKIT_ONLY_HINT
                : hostActionsHint ?? undefined;
              return (
                <button
                  key={op.id}
                  className={"btn btn--sm" + (op.danger ? " btn--danger-ghost" : "")}
                  onClick={() => runHostAction(op)}
                  disabled={disabled}
                  title={title}
                >
                  {op.label.replace(/…$/, "")}
                </button>
              );
            })}
          </div>
        </div>

        <div className="card card--table">
          <table className="table cdetail__table">
            <thead>
              <tr>
                <th style={{ width: 36 }}>
                  <input
                    ref={headerRef}
                    type="checkbox"
                    checked={
                      filtered.length > 0 && visibleSelected === filtered.length
                    }
                    onChange={toggleAllVisible}
                    aria-label="Select all visible"
                  />
                </th>
                <th>
                  <SortHeader
                    label="Host"
                    active={sortActive("host")}
                    dir={sortDir}
                    onClick={() => sortBy("host")}
                  />
                </th>
                <th className="num">
                  <SortHeader
                    label="Pool"
                    active={sortActive("pool")}
                    dir={sortDir}
                    align="right"
                    onClick={() => sortBy("pool")}
                  />
                </th>
                <th>
                  <SortHeader
                    label="State"
                    active={sortActive("state")}
                    dir={sortDir}
                    onClick={() => sortBy("state")}
                  />
                </th>
                <th>
                  <SortHeader
                    label="Version"
                    active={sortActive("version")}
                    dir={sortDir}
                    onClick={() => sortBy("version")}
                  />
                </th>
                <th className="probe-col">
                  <SortHeader
                    label="Ping"
                    title="ICMP / TCP reachable"
                    active={sortActive("ping")}
                    dir={sortDir}
                    align="center"
                    onClick={() => sortBy("ping")}
                  />
                </th>
                <th className="probe-col">
                  <SortHeader
                    label="SSH"
                    title="SSH handshake + auth"
                    active={sortActive("ssh")}
                    dir={sortDir}
                    align="center"
                    onClick={() => sortBy("ssh")}
                  />
                </th>
                <th className="probe-col">
                  <SortHeader
                    label="S3 API"
                    title="S3 API on :9000"
                    active={sortActive("api")}
                    dir={sortDir}
                    align="center"
                    onClick={() => sortBy("api")}
                  />
                </th>
                <th className="probe-col">
                  <SortHeader
                    label="Console"
                    title="Buckit web console on :9001"
                    active={sortActive("console")}
                    dir={sortDir}
                    align="center"
                    onClick={() => sortBy("console")}
                  />
                </th>
                <th>
                  <SortHeader
                    label="Kernel"
                    active={sortActive("kernel")}
                    dir={sortDir}
                    onClick={() => sortBy("kernel")}
                  />
                </th>
                <th>
                  <SortHeader
                    label="Uptime"
                    active={sortActive("uptime")}
                    dir={sortDir}
                    onClick={() => sortBy("uptime")}
                  />
                </th>
              </tr>
              <tr className="cdetail__filter-row">
                <th></th>
                <th>
                  <input
                    className="input input--xs"
                    placeholder="Filter hosts…"
                    value={hostFilter}
                    onChange={(e) => setHostFilter(e.target.value)}
                  />
                </th>
                <th>
                  <select
                    className="select input--xs"
                    value={poolFilter === "all" ? "all" : String(poolFilter)}
                    onChange={(e) =>
                      setPoolFilter(
                        e.target.value === "all"
                          ? "all"
                          : parseInt(e.target.value, 10),
                      )
                    }
                  >
                    <option value="all">All</option>
                    {distinctPools.map((p) => (
                      <option key={p} value={p}>{p}</option>
                    ))}
                  </select>
                </th>
                <th>
                  <select
                    className="select input--xs"
                    value={stateFilter}
                    onChange={(e) =>
                      setStateFilter(e.target.value as typeof stateFilter)
                    }
                  >
                    <option value="all">All</option>
                    <option value="online">Online</option>
                    <option value="degraded">Degraded</option>
                    <option value="offline">Offline</option>
                    <option value="unknown">Unknown</option>
                  </select>
                </th>
                <th>
                  <select
                    className="select input--xs"
                    value={versionFilter}
                    onChange={(e) => setVersionFilter(e.target.value)}
                  >
                    <option value="all">All</option>
                    {distinctVersions.map((v) => (
                      <option key={v} value={v}>{v}</option>
                    ))}
                  </select>
                </th>
                <th></th>
                <th></th>
                <th></th>
                <th></th>
                <th>
                  <input
                    className="input input--xs"
                    placeholder="Filter…"
                    value={kernelFilter}
                    onChange={(e) => setKernelFilter(e.target.value)}
                  />
                </th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {filtered.length === 0 ? (
                <tr>
                  <td colSpan={11} className="muted" style={{ padding: "var(--s-4)" }}>
                    No nodes match this view.
                  </td>
                </tr>
              ) : (
                filtered.map((n) => (
                  <tr key={n.id} className={selected.has(n.id) ? "is-selected" : ""}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selected.has(n.id)}
                        onChange={() => toggleNode(n.id)}
                        aria-label={`Select ${n.hostname}`}
                      />
                    </td>
                    <td>
                      <Link
                        to={`/clusters/${cluster.id}/nodes/${n.id}`}
                        className="cdetail__hostname"
                      >
                        {n.hostname}
                      </Link>
                    </td>
                    <td className="num subtle">{n.pool}</td>
                    <td>{nodeStatePill(n)}</td>
                    <td className="mono subtle">{n.version ?? "—"}</td>
                    <td className="probe-col">{probeDot(n.pingable, "Ping")}</td>
                    <td className="probe-col">{probeDot(n.sshable, "SSH")}</td>
                    <td className="probe-col">{probeDot(n.apiAccessible, "S3 API")}</td>
                    <td className="probe-col">{probeDot(n.consoleAccessible, "Console")}</td>
                    <td className="mono subtle">
                      {n.kernel ? n.kernel : <span className="subtle">—</span>}
                    </td>
                    <td className="subtle">
                      {n.uptimeSec ? formatUptime(n.uptimeSec) : "—"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {activeOp && (
        <OperationModal
          def={activeOp.op}
          cluster={cluster}
          targetHostIds={activeOp.targetHostIds}
          scopeLabel={activeOp.scopeLabel}
          onClose={(result) => {
            const finishedOp = activeOp.op;
            const wasBulk = !!activeOp.targetHostIds;
            setActiveOp(null);
            // Cluster state may have mutated (freeze, remove, etc.). Trigger a
            // real refresh so the latest cluster + probe state lands quickly.
            triggerRefresh();
            qc.invalidateQueries({ queryKey: ["history"] });
            // Clear the row selection after a successful bulk run so
            // the operator isn't sitting with stale checkboxes.
            if (wasBulk && result === "success") setSelected(new Set());
            // Remove-cluster mutates the list itself; navigate away if
            // this cluster no longer exists.
            if (finishedOp.id === "remove_cluster") {
              navigate("/clusters");
            }
          }}
        />
      )}
      {showSshRequired && (
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

// Renders the Cluster Actions menu, grouped by transport (admin / ssh).
// The "manager" group (settings, remove) has moved to the gear menu.
// Filters out items hidden by their `visible` predicate.
function renderActionsMenu(
  cluster: Cluster,
  sshDisabledHint: string | undefined,
  onPick: (op: OperationDef) => void,
) {
  const visible = CLUSTER_OPERATIONS.filter(
    (o) => o.visible?.(cluster) ?? true,
  );
  const groups: OperationDef["group"][] = ["admin", "ssh"];
  return groups.flatMap((g) => {
    const items = visible.filter((o) => o.group === g);
    if (items.length === 0) return [];
    return [
      <div key={`g-${g}`} className="cdetail__menu-group">
        {GROUP_LABELS[g]}
      </div>,
      ...items.map((op) => {
        const engineDisabled = opDisabledForEngine(op, cluster);
        const sshDisabled = clusterOpRequiresSSH(op) && !!sshDisabledHint;
        const disabled = engineDisabled || sshDisabled;
        const title = engineDisabled
          ? BUCKIT_ONLY_HINT
          : sshDisabled
            ? sshDisabledHint
            : undefined;
        return (
          <button
            key={op.id}
            className={
              "cdetail__menu-item cdetail__menu-item--stacked" +
              (op.danger ? " is-danger" : "")
            }
            data-testid={`cluster-action-${op.id}`}
            onClick={() => onPick(op)}
            disabled={disabled}
            title={title}
            role="menuitem"
          >
            <div>{op.dynamicLabel ? op.dynamicLabel(cluster) : op.label}</div>
            <div
              className="subtle"
              style={{ fontSize: "var(--fs-xs)", fontWeight: 400 }}
            >
              {op.description}
            </div>
          </button>
        );
      }),
    ];
  });
}

// Renders the gear / settings menu: Configure admin creds, Configure SSH,
// and Remove cluster definition. These are "manager" group items that live
// near the cluster label rather than in the main Cluster Actions dropdown.
function renderSettingsMenu(
  cluster: Cluster,
  onPick: (op: OperationDef) => void,
) {
  const items = CLUSTER_OPERATIONS.filter(
    (o) => o.group === "manager" && (o.visible?.(cluster) ?? true),
  );
  return items.map((op) => (
    <button
      key={op.id}
      className={
        "cdetail__menu-item cdetail__menu-item--stacked" +
        (op.danger ? " is-danger" : "")
      }
      data-testid={`cluster-setting-${op.id}`}
      onClick={() => onPick(op)}
      role="menuitem"
    >
      <div>{op.dynamicLabel ? op.dynamicLabel(cluster) : op.label}</div>
      <div
        className="subtle"
        style={{ fontSize: "var(--fs-xs)", fontWeight: 400 }}
      >
        {op.description}
      </div>
    </button>
  ));
}

function formatUptime(sec: number): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  if (d > 0) return `${d}d ${h}h`;
  const m = Math.floor((sec % 3600) / 60);
  return `${h}h ${m}m`;
}
