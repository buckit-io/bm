import { useState } from "react";
import { Link } from "react-router-dom";
import { useTasks } from "../api/hooks";
import { TaskStatePill } from "../components/TaskStateIcon";
import { formatDuration, formatRelative, TaskState } from "../mock/data";

const FILTERS: Array<{ id: TaskState | "all"; label: string }> = [
  { id: "all", label: "All" },
  { id: "running", label: "Running" },
  { id: "succeeded", label: "Success" },
  { id: "failed", label: "Failed" },
  { id: "canceled", label: "Canceled" },
];

export function Tasks() {
  const { data: tasks, isLoading } = useTasks();
  const [filter, setFilter] = useState<TaskState | "all">("all");

  const filtered = (tasks ?? []).filter(
    (t) => filter === "all" || t.state === filter,
  );

  return (
    <section className="clusters">
      <header className="clusters__header">
        <h1>Tasks</h1>
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
          <p className="muted" style={{ padding: "var(--s-4)" }}>
            No tasks in this view.
          </p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>State</th>
                <th>Name</th>
                <th>Cluster</th>
                <th>Started</th>
                <th>Duration</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((t) => (
                <tr key={t.id}>
                  <td><TaskStatePill state={t.state} /></td>
                  <td>
                    <Link to={`/tasks/${t.id}`} className="clusters__name">
                      {t.name}
                    </Link>
                    {t.failureNote && (
                      <div className="subtle" style={{ marginTop: 2 }}>
                        ↳ {t.failureNote}
                      </div>
                    )}
                  </td>
                  <td>
                    {t.clusterId ? (
                      <Link to={`/clusters/${t.clusterId}`}>{t.clusterName}</Link>
                    ) : (
                      <span className="subtle">—</span>
                    )}
                  </td>
                  <td className="subtle">{formatRelative(t.startedAt)}</td>
                  <td className="subtle">{formatDuration(t.durationSec)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}
