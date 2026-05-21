import { useEffect, useRef, useState } from "react";
import { Link, Navigate } from "react-router-dom";
import { Pill } from "../components/Pill";
import { useClusters, useRefreshClusters } from "../api/hooks";
import { Cluster } from "../api/types";
import { formatBytes } from "../lib/format";
import "./Clusters.css";

function pickMostStale(clusters: Cluster[]): string | null {
  const stamps = clusters
    .map((c) => c.lastFetchedAt)
    .filter((t): t is string => !!t);
  if (stamps.length === 0) return null;
  // The OLDEST timestamp is the most stale — that's what we surface.
  return stamps.reduce((a, b) => (new Date(a) < new Date(b) ? a : b));
}

function ageSeconds(iso: string): number {
  return Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
}

function formatAge(sec: number): string {
  if (sec < 60) return `${sec}s ago`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  if (m < 60) return s ? `${m}m ${s}s ago` : `${m}m ago`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m ago`;
}

const autoRefreshStaleAfterSec = 30 * 60;

function healthPill(c: Cluster) {
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

// Renders "online / total" or "ready / total" with a subtle red on the
// numerator when it lags the denominator — so the eye catches "71/72" the
// same way it catches the Degraded pill.
function ratioCell(ok: number, total: number) {
  if (total === 0) return <span className="subtle">—</span>;
  const short = ok < total;
  return (
    <span className="mono" style={{ fontVariantNumeric: "tabular-nums" }}>
      <span style={short ? { color: "var(--c-warning)", fontWeight: 600 } : undefined}>
        {ok}
      </span>
      <span className="subtle">/{total}</span>
    </span>
  );
}

export function Clusters() {
  const { data: clusters, isLoading } = useClusters();
  const refresh = useRefreshClusters();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const autoRefreshStarted = useRef(false);

  // Re-render every second so "Fetched Ns ago" actually ticks while the
  // operator stares at the page. Cheap — one setState per second.
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  // Close the +New menu on outside-click.
  useEffect(() => {
    if (!menuOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as globalThis.Node | null;
      if (menuRef.current && target && !menuRef.current.contains(target)) {
        setMenuOpen(false);
      }
    };
    window.addEventListener("mousedown", onMouseDown);
    return () => window.removeEventListener("mousedown", onMouseDown);
  }, [menuOpen]);

  // Redirect to /welcome when the backend has no clusters yet (first run).
  if (!isLoading && clusters && clusters.length === 0) {
    return <Navigate to="/welcome" replace />;
  }

  const filtered = clusters ?? [];
  const mostStale = pickMostStale(clusters ?? []);
  const stale = mostStale ? ageSeconds(mostStale) : null;

  useEffect(() => {
    if (autoRefreshStarted.current) return;
    if (isLoading || !clusters || clusters.length === 0) return;
    if (stale !== null && stale < autoRefreshStaleAfterSec) return;
    autoRefreshStarted.current = true;
    refresh.mutate();
  }, [clusters, isLoading, refresh, stale]);

  return (
    <section className="clusters">
      <header className="clusters__header">
        <h1>Clusters</h1>
        <div className="hstack">
          <span
            className="clusters__stale"
            title={
              mostStale
                ? `Oldest fetch across visible clusters: ${new Date(mostStale).toLocaleString()}`
                : "No clusters have been fetched yet"
            }
          >
            {stale === null ? "Not fetched yet" : `Fetched ${formatAge(stale)}`}
          </span>
          <button
            className="btn"
            onClick={() => refresh.mutate()}
            disabled={refresh.isPending}
            aria-label="Refresh all clusters"
          >
            <span
              className={"clusters__sync-icon" + (refresh.isPending ? " is-spinning" : "")}
              aria-hidden
            >
              ↻
            </span>
            {refresh.isPending ? "Refreshing…" : "Refresh"}
          </button>
          <div className="clusters__new" ref={menuRef}>
            <button
              className="btn btn--primary"
              onClick={() => setMenuOpen((v) => !v)}
              aria-haspopup="menu"
              aria-expanded={menuOpen}
            >
              + New ▾
            </button>
            {menuOpen && (
              <div className="clusters__menu" role="menu">
                <Link
                  to="/clusters/new"
                  className="clusters__menu-item"
                  onClick={() => setMenuOpen(false)}
                >
                  Deploy new cluster
                </Link>
                <Link
                  to="/clusters/import"
                  className="clusters__menu-item"
                  onClick={() => setMenuOpen(false)}
                >
                  Import existing Buckit or MinIO cluster
                </Link>
              </div>
            )}
          </div>
        </div>
      </header>

      <div className="card clusters__table-wrap">
        {isLoading ? (
          <p className="muted" style={{ padding: "var(--s-4)" }}>Loading…</p>
        ) : filtered.length === 0 ? (
          <p className="muted" style={{ padding: "var(--s-4)" }}>No clusters in this view.</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th className="num">Pools</th>
                <th className="num" title="Online / total nodes">Nodes (Online/Total)</th>
                <th className="num" title="Ready / total drives">Drives (Ready/Total)</th>
                <th>Version</th>
                <th>Health</th>
                <th>Used</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((c) => {
                const s = c.healthSummary;
                const nodeTotal = s?.nodes.total && s.nodes.total > 0 ? s.nodes.total : c.nodeCount;
                const nodeReady = s?.nodes.total && s.nodes.total > 0 ? s.nodes.online : c.nodeCount;
                const driveTotal = s?.drives.total && s.drives.total > 0 ? s.drives.total : c.driveCount;
                const driveReady = s?.drives.total && s.drives.total > 0 ? s.drives.ready : c.driveCount;
                return (
                  <tr key={c.id}>
                    <td>
                      <div className="hstack" style={{ gap: "var(--s-2)", alignItems: "center" }}>
                        <Link to={`/clusters/${c.id}`} className="clusters__name">
                          {c.name}
                        </Link>
                        {c.engine === "minio" && (
                          <Pill tone="warning">MinIO</Pill>
                        )}
                      </div>
                      {c.description && (
                        <div className="subtle clusters__desc">{c.description}</div>
                      )}
                    </td>
                    <td className="num">
                      {c.poolCount === 0 ? (
                        <span className="subtle">—</span>
                      ) : (
                        <span className="mono" style={{ fontVariantNumeric: "tabular-nums" }}>
                          {c.poolCount}
                        </span>
                      )}
                    </td>
                    <td className="num">
                      {ratioCell(nodeReady, nodeTotal)}
                    </td>
                    <td className="num">
                      {ratioCell(driveReady, driveTotal)}
                    </td>
                    <td>{c.version}</td>
                    <td>{healthPill(c)}</td>
                    <td>
                      {c.usableBytes === 0 ? (
                        "—"
                      ) : (
                        <span className="subtle">
                          {formatBytes(c.usedBytes)} / {formatBytes(c.usableBytes)}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}
