// Live log viewer for a single node, reached from the per-node Actions
// menu's "View live logs" item. Real backend tails journalctl over the
// admin API and streams to the browser via SSE. Mock uses the existing
// TaskLogStream which emits canned lines.

import { Link, useParams } from "react-router-dom";
import { useCluster, useNode } from "../../api/hooks";
import { TaskLogStream } from "../../components/TaskLogStream";

export function NodeLogs() {
  const { clusterId = "", nodeId = "" } = useParams();
  const { data: cluster } = useCluster(clusterId);
  const { data: node, isLoading } = useNode(clusterId, nodeId);

  if (isLoading) return <p className="muted">Loading…</p>;
  if (!cluster || !node) return <p className="muted">Node not found.</p>;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="vstack" style={{ gap: 4 }}>
        <Link
          to={`/clusters/${cluster.id}/nodes/${node.id}`}
          className="subtle"
          style={{ fontSize: "var(--fs-sm)" }}
        >
          ← {node.hostname}
        </Link>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          Live logs
        </h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Streaming <span className="mono">journalctl -u buckit -f</span> from{" "}
          <span className="mono">{node.hostname}</span>.
        </p>
      </header>
      <TaskLogStream taskId={`node-logs:${node.id}`} maxLines={400} />
    </div>
  );
}
