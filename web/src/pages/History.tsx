// History tab — log of every mutable action initiated from the web UI.
// Read-only fetches and background polls are not recorded. CLI
// invocations are out of scope (bm doesn't intercept terminal calls).

import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useClearHistory, useClusters, useHistory } from "../api/hooks";
import { Pill, PillTone } from "../components/Pill";
import { formatDuration, formatRelative, HistoryEntry } from "../mock/data";
import { HistoryResultModal } from "./HistoryResultModal";
import "./History.css";

type StatusFilter = "all" | HistoryEntry["status"];

const STATUS_FILTERS: { id: StatusFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "succeeded", label: "Succeeded" },
  { id: "failed", label: "Failed" },
  { id: "running", label: "Running" },
  { id: "canceled", label: "Canceled" },
];

function statusPill(s: HistoryEntry["status"]) {
  const map: Record<
    HistoryEntry["status"],
    { tone: PillTone; icon: string; label: string }
  > = {
    succeeded: { tone: "success", icon: "✓", label: "Succeeded" },
    failed: { tone: "danger", icon: "✗", label: "Failed" },
    running: { tone: "info", icon: "⟳", label: "Running" },
    canceled: { tone: "warning", icon: "⊘", label: "Canceled" },
  };
  const m = map[s];
  return <Pill tone={m.tone} icon={m.icon}>{m.label}</Pill>;
}

// Render the host scope for a history row. Single host shows the
// hostname; bulk shows the count with a tooltip listing the names.
function renderScope(r: HistoryEntry) {
  if (!r.hostScope) {
    return <span className="subtle">cluster-wide</span>;
  }
  if (r.hostScope.count === 1) {
    return <span className="mono">{r.hostScope.hostnames[0]}</span>;
  }
  return (
    <span title={r.hostScope.hostnames.join("\n")}>
      {r.hostScope.count} hosts
    </span>
  );
}

export function History() {
  const { data: rows, isLoading } = useHistory();
  const { data: clusters } = useClusters();
  const clear = useClearHistory();

  const [status, setStatus] = useState<StatusFilter>("all");
  const [clusterFilter, setClusterFilter] = useState<string>("all");
  const [search, setSearch] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [viewEntry, setViewEntry] = useState<HistoryEntry | null>(null);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (rows ?? []).filter((r) => {
      if (status !== "all" && r.status !== status) return false;
      if (clusterFilter !== "all" && r.clusterId !== clusterFilter) return false;
      if (q) {
        const hay = `${r.opLabel} ${r.clusterName} ${
          r.hostScope?.hostnames.join(" ") ?? ""
        }`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [rows, status, clusterFilter, search]);

  return (
    <section className="history">
      <header className="history__header">
        <h1>History</h1>
        <button
          className="btn btn--sm"
          onClick={() => setConfirmOpen(true)}
          disabled={(rows ?? []).length === 0}
        >
          Clear history…
        </button>
      </header>

      <p className="muted history__intro">
        Actions performed from this manager.
      </p>

      <div className="history__toolbar">
        <div className="clusters__filters" role="tablist">
          {STATUS_FILTERS.map((f) => (
            <button
              key={f.id}
              role="tab"
              aria-selected={status === f.id}
              className={"chip" + (status === f.id ? " is-active" : "")}
              onClick={() => setStatus(f.id)}
            >
              {f.label}
            </button>
          ))}
        </div>
        <select
          className="select history__select"
          value={clusterFilter}
          onChange={(e) => setClusterFilter(e.target.value)}
        >
          <option value="all">All clusters</option>
          {(clusters ?? []).map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </select>
        <input
          className="input history__search"
          placeholder="Search operation, cluster, or hostname…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="card card--table">
        {isLoading ? (
          <p className="muted" style={{ padding: "var(--s-4)" }}>Loading…</p>
        ) : filtered.length === 0 ? (
          <p className="muted" style={{ padding: "var(--s-4)" }}>
            No history matching this view.
          </p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 170 }}>Time</th>
                <th>Operation</th>
                <th style={{ width: 160 }}>Cluster</th>
                <th style={{ width: 160 }}>Scope</th>
                <th style={{ width: 130 }}>Status</th>
                <th style={{ width: 90 }}>Duration</th>
                <th style={{ width: 110 }}></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={r.id}>
                  <td>
                    <span className="history__time-rel">
                      {formatRelative(r.at)}
                    </span>
                    <span className="history__time-abs">
                      {new Date(r.at).toLocaleString()}
                    </span>
                  </td>
                  <td>
                    <div>{r.opLabel}</div>
                    {r.failureNote && (
                      <div
                        className="subtle"
                        style={{
                          fontSize: "var(--fs-xs)",
                          color: "var(--c-danger)",
                          marginTop: 2,
                        }}
                      >
                        {r.failureNote}
                      </div>
                    )}
                  </td>
                  <td>
                    <Link to={`/clusters/${r.clusterId}`}>{r.clusterName}</Link>
                  </td>
                  <td>{renderScope(r)}</td>
                  <td>{statusPill(r.status)}</td>
                  <td className="subtle">{formatDuration(r.durationSec)}</td>
                  <td className="history__actions">
                    {r.status !== "running" && (
                      <button
                        className="btn btn--ghost btn--sm"
                        onClick={() => setViewEntry(r)}
                      >
                        View
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {confirmOpen && (
        <div className="modal-backdrop" onClick={() => setConfirmOpen(false)}>
          <div className="card modal" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
              Clear history?
            </h3>
            <p style={{ fontSize: "var(--fs-sm)" }}>
              All {(rows ?? []).length} entries will be removed from this
              machine. Cluster state is not affected.
            </p>
            <div className="hstack" style={{ justifyContent: "flex-end" }}>
              <button className="btn" onClick={() => setConfirmOpen(false)}>Cancel</button>
              <button
                className="btn btn--danger"
                onClick={() => {
                  clear.mutate();
                  setConfirmOpen(false);
                }}
              >
                Clear all
              </button>
            </div>
          </div>
        </div>
      )}

      {viewEntry && (
        <HistoryResultModal
          entry={viewEntry}
          onClose={() => setViewEntry(null)}
        />
      )}
    </section>
  );
}
