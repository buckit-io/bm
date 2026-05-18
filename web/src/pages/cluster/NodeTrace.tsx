// S3 API trace viewer for a single node. Real backend proxies
// /minio/admin/v3/trace and streams to the browser via SSE. Mock uses
// the existing TaskLogStream (it doesn't care that the content shape is
// different).

import { Link, useParams } from "react-router-dom";
import { useCluster, useNode } from "../../api/hooks";
import { TaskLogStream } from "../../components/TaskLogStream";

export function NodeTrace() {
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
          S3 API trace
        </h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Streaming admin trace events from{" "}
          <span className="mono">{node.hostname}</span>. Each line is one
          S3 API request as it hits this node.
        </p>
      </header>
      <TaskLogStream taskId={`node-trace:${node.id}`} maxLines={400} />
    </div>
  );
}
