import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Pill, PillTone } from "../components/Pill";
import { useClusters, useRefreshClusters } from "../api/hooks";
import { Cluster, formatBytes } from "../mock/data";
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

type Filter = "all" | "active" | "draft" | "migrating" | "failed";
const FILTERS: { id: Filter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "active", label: "Active" },
  { id: "draft", label: "Draft" },
  { id: "migrating", label: "Migrating" },
  { id: "failed", label: "Failed" },
];

function healthPill(c: Cluster) {
  if (c.status === "draft")
    return <Pill tone="neutral" icon="○">—</Pill>;
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
function ratioCell(ok: number, total: number, draft: boolean) {
  if (draft || total === 0) return <span className="subtle">—</span>;
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

function statusPill(c: Cluster) {
  const map: Record<Cluster["status"], PillTone> = {
    active: "success",
    draft: "neutral",
    migrating: "warning",
    failed: "danger",
  };
  const label = c.status[0].toUpperCase() + c.status.slice(1);
  return <Pill tone={map[c.status]}>{label}</Pill>;
}

export function Clusters() {
  const { data: clusters, isLoading } = useClusters();
  const refresh = useRefreshClusters();
  const [filter, setFilter] = useState<Filter>("all");
  const [menuOpen, setMenuOpen] = useState(false);

  // Re-render every second so "Fetched Ns ago" actually ticks while the
  // operator stares at the page. Cheap — one setState per second.
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  const filtered = (clusters ?? []).filter(
    (c) => filter === "all" || c.status === filter,
  );
  const mostStale = pickMostStale(clusters ?? []);
  const stale = mostStale ? ageSeconds(mostStale) : null;

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
          <div className="clusters__new">
            <button
              className="btn btn--primary"
              onClick={() => setMenuOpen((v) => !v)}
            >
              + New ▾
            </button>
            {menuOpen && (
              <div className="clusters__menu" role="menu">
                <Link to="/clusters/new" className="clusters__menu-item">
                  Deploy new cluster
                </Link>
                <Link to="/clusters/import" className="clusters__menu-item">
                  Import existing cluster
                </Link>
              </div>
            )}
          </div>
        </div>
      </header>

      <div className="clusters__filters" role="tablist">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            role="tab"
            aria-selected={filter === f.id}
            className={"chip" + (filter === f.id ? " is-active" : "")}
            onClick={() => setFilter(f.id)}
          >
            {f.label}
          </button>
        ))}
      </div>

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
                <th className="num">Nodes</th>
                <th className="num">Drives</th>
                <th>Version</th>
                <th>Health</th>
                <th>Used</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((c) => {
                const draft = c.status === "draft";
                const s = c.healthSummary;
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
                      {draft || c.poolCount === 0 ? (
                        <span className="subtle">—</span>
                      ) : (
                        <span className="mono" style={{ fontVariantNumeric: "tabular-nums" }}>
                          {c.poolCount}
                        </span>
                      )}
                    </td>
                    <td className="num">
                      {ratioCell(s?.nodes.online ?? 0, s?.nodes.total ?? 0, draft)}
                    </td>
                    <td className="num">
                      {ratioCell(s?.drives.ready ?? 0, s?.drives.total ?? 0, draft)}
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
                    <td>{statusPill(c)}</td>
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
