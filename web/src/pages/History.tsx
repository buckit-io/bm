import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useClearHistory, useClusters, useHistory } from "../api/hooks";
import { Pill, PillTone } from "../components/Pill";
import { formatDuration, formatRelative, HistoryEntry } from "../mock/data";
import "./History.css";

type KindFilter = "all" | "cli" | "ui_action";

const KIND_FILTERS: { id: KindFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "cli", label: "CLI" },
  { id: "ui_action", label: "UI" },
];

function statusPill(s: HistoryEntry["status"]) {
  const map: Record<HistoryEntry["status"], { tone: PillTone; icon: string; label: string }> = {
    succeeded: { tone: "success", icon: "✓", label: "Succeeded" },
    failed: { tone: "danger", icon: "✗", label: "Failed" },
    running: { tone: "info", icon: "⟳", label: "Running" },
  };
  const m = map[s];
  return <Pill tone={m.tone} icon={m.icon}>{m.label}</Pill>;
}

export function History() {
  const { data: rows, isLoading } = useHistory();
  const { data: clusters } = useClusters();
  const clear = useClearHistory();

  const [kind, setKind] = useState<KindFilter>("all");
  const [target, setTarget] = useState<string>("all");
  const [search, setSearch] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (rows ?? []).filter((r) => {
      if (kind !== "all" && r.kind !== kind) return false;
      if (target !== "all" && r.target !== target) return false;
      if (q && !r.display.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [rows, kind, target, search]);

  const copyCmd = (text: string) => {
    navigator.clipboard?.writeText(text).catch(() => {});
  };

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
        Recent CLI commands and web UI actions on this machine. Read-only
        commands and background fetches are not recorded.
      </p>

      <div className="history__toolbar">
        <div className="clusters__filters" role="tablist">
          {KIND_FILTERS.map((f) => (
            <button
              key={f.id}
              role="tab"
              aria-selected={kind === f.id}
              className={"chip" + (kind === f.id ? " is-active" : "")}
              onClick={() => setKind(f.id)}
            >
              {f.label}
            </button>
          ))}
        </div>
        <select
          className="select history__select"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        >
          <option value="all">All targets</option>
          {(clusters ?? []).map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </select>
        <input
          className="input history__search"
          placeholder="Search…"
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
                <th style={{ width: 110 }}>Time</th>
                <th style={{ width: 60 }}>Source</th>
                <th>Action</th>
                <th style={{ width: 140 }}>Target</th>
                <th style={{ width: 120 }}>Status</th>
                <th style={{ width: 90 }}>Duration</th>
                <th style={{ width: 110 }}></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={r.id}>
                  <td className="subtle" title={new Date(r.at).toLocaleString()}>
                    {formatRelative(r.at)}
                  </td>
                  <td>
                    <span
                      className={
                        "history__source history__source--" + r.kind
                      }
                      title={r.kind === "cli" ? "CLI command" : "Web UI action"}
                    >
                      {r.kind === "cli" ? "❯_" : "✦"}
                    </span>
                  </td>
                  <td>
                    <span className={r.kind === "cli" ? "mono history__cmd" : ""}>
                      {r.display}
                    </span>
                  </td>
                  <td>
                    {r.target ? (
                      <Link to={`/clusters/${r.target}`}>{r.target}</Link>
                    ) : (
                      <span className="subtle">—</span>
                    )}
                  </td>
                  <td>
                    {statusPill(r.status)}
                    {r.exitCode !== undefined && r.kind === "cli" && (
                      <span className="subtle" style={{ fontSize: "var(--fs-xs)", marginLeft: 6 }}>
                        exit {r.exitCode}
                      </span>
                    )}
                  </td>
                  <td className="subtle">{formatDuration(r.durationSec)}</td>
                  <td className="history__actions">
                    {r.kind === "cli" && (
                      <button
                        className="btn btn--ghost btn--sm"
                        onClick={() => copyCmd(r.display)}
                      >
                        Copy
                      </button>
                    )}
                    {r.taskId && (
                      <Link
                        to={`/tasks/${r.taskId}`}
                        className="btn btn--ghost btn--sm"
                      >
                        View task
                      </Link>
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
              machine. Tasks and cluster state are not affected.
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
    </section>
  );
}
