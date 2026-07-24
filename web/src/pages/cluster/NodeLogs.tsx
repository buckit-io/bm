// Per-node log viewer. One-shot journalctl fetch over SSH with a range
// dropdown, refresh button, and a client-side substring filter. Not a live
// tail — operators pick a range, hit refresh when they want fresher lines.

import { useMemo, useState } from "react";
import { Link, useLocation, useParams } from "react-router-dom";
import { ApiError } from "../../api/client";
import { useCluster, useNode, useNodeLogs } from "../../api/hooks";
import type { NodeLogsRange } from "../../api/types";
import type { NodeDetailNavigationState } from "./navigationState";
import "../../components/TaskLogStream.css";

const RANGE_OPTIONS: { value: NodeLogsRange; label: string }[] = [
  { value: "15m", label: "Last 15 minutes" },
  { value: "1h", label: "Last hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
];

export function NodeLogs() {
  const { clusterId = "", nodeId = "" } = useParams();
  const location = useLocation();
  const { data: cluster } = useCluster(clusterId);
  const { data: node, isLoading: nodeLoading } = useNode(clusterId, nodeId);
  const cameFromClusterDetail =
    (location.state as NodeDetailNavigationState | null)?.fromClusterDetail === true;

  const [since, setSince] = useState<NodeLogsRange>("1h");
  const [filter, setFilter] = useState("");

  const logs = useNodeLogs(clusterId, nodeId, since);

  const filtered = useMemo(() => {
    const lines = logs.data?.lines ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return lines;
    return lines.filter((l) => l.toLowerCase().includes(q));
  }, [logs.data?.lines, filter]);

  if (nodeLoading) return <p className="muted">Loading…</p>;
  if (!cluster || !node) return <p className="muted">Node not found.</p>;

  const unitLabel =
    logs.data?.unit ?? (cluster.engine === "minio" ? "minio.service" : "buckit.service");
  const errMsg = errorMessage(logs.error);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="vstack" style={{ gap: 4 }}>
        <Link
          to={`/clusters/${cluster.id}/nodes/${node.id}`}
          state={
            cameFromClusterDetail
              ? ({ fromClusterDetail: true } satisfies NodeDetailNavigationState)
              : undefined
          }
          className="subtle"
          style={{ fontSize: "var(--fs-sm)" }}
        >
          ← {node.hostname}
        </Link>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Logs</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          <span className="mono">journalctl -u {unitLabel}</span> from{" "}
          <span className="mono">{node.hostname}</span>.
        </p>
      </header>

      <div
        className="hstack"
        style={{
          gap: "var(--s-3)",
          flexWrap: "wrap",
          alignItems: "center",
        }}
      >
        <label className="hstack" style={{ gap: "var(--s-2)", alignItems: "center" }}>
          <span className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            Range
          </span>
          <select
            className="input"
            value={since}
            onChange={(e) => setSince(e.target.value as NodeLogsRange)}
            disabled={logs.isFetching}
            style={{ height: 32, width: "auto", paddingRight: "var(--s-4)" }}
          >
            {RANGE_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>

        <input
          type="search"
          className="input"
          placeholder="Filter lines…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{ flex: "1 1 240px", minWidth: 200, height: 32 }}
        />

        <button
          className="btn btn--sm"
          onClick={() => logs.refetch()}
          disabled={logs.isFetching}
          title="Re-fetch logs from the node"
        >
          {logs.isFetching ? "Loading…" : "Refresh"}
        </button>

        <span className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          {summaryText(logs.data, filtered.length, filter, errMsg)}
        </span>
      </div>

      <div className="logstream">
        <div className="logstream__body" style={{ height: 480 }}>
          {errMsg ? (
            <div className="subtle" style={{ padding: "var(--s-3)" }}>
              {errMsg}
            </div>
          ) : logs.isLoading ? (
            <div className="subtle" style={{ padding: "var(--s-3)" }}>
              Fetching…
            </div>
          ) : filtered.length === 0 ? (
            <div className="subtle" style={{ padding: "var(--s-3)" }}>
              {logs.data?.lines.length === 0
                ? "No log lines in this range."
                : "No lines match the filter."}
            </div>
          ) : (
            filtered.map((line, i) => (
              <div key={i} className={"logstream__line " + lineToneClass(line)}>
                <span className="logstream__text" style={{ gridColumn: "1 / -1" }}>
                  {line}
                </span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function summaryText(
  data: { lines: string[]; truncated: boolean; fetchedAt: string } | undefined,
  shownCount: number,
  filter: string,
  errMsg: string | null,
): string {
  if (errMsg) return "";
  if (!data) return "";
  const total = data.lines.length;
  const filterPart = filter.trim() ? ` of ${total} shown` : "";
  const truncatedPart = data.truncated ? " · output truncated" : "";
  const fetchedPart = ` · fetched ${new Date(data.fetchedAt).toLocaleTimeString()}`;
  return `${shownCount} lines${filterPart}${truncatedPart}${fetchedPart}`;
}

function errorMessage(err: unknown): string | null {
  if (!err) return null;
  if (err instanceof ApiError) {
    if (err.status === 424) {
      if (err.code === "ssh_not_configured") {
        return "SSH is not configured for this cluster — set it up to view logs.";
      }
      return err.message || "Cannot reach the node over SSH.";
    }
    if (err.status === 502) {
      return `Node is unreachable: ${err.message}`;
    }
    return `${err.code}: ${err.message}`;
  }
  return err instanceof Error ? err.message : "Failed to fetch logs.";
}

// lineToneClass colors the row by journalctl severity hints. Cheap heuristic
// against the raw line — full journal priority parsing isn't worth it here.
function lineToneClass(line: string): string {
  const lower = line.toLowerCase();
  if (/\b(error|err|fatal|panic|fail|failed)\b/.test(lower)) {
    return "logstream__line--error";
  }
  if (/\b(warn|warning)\b/.test(lower)) {
    return "logstream__line--warn";
  }
  return "";
}
