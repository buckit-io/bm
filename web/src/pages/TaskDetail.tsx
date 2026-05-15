import { Link, useParams } from "react-router-dom";
import { useTask } from "../api/hooks";
import { TaskStatePill } from "../components/TaskStateIcon";
import { TaskStepsTimeline } from "../components/TaskStepsTimeline";
import { TaskLogStream } from "../components/TaskLogStream";
import { formatDuration, formatRelative } from "../mock/data";
import "./TaskDetail.css";

export function TaskDetail() {
  const { taskId } = useParams();
  const { data: task, isLoading } = useTask(taskId);

  if (isLoading) return <p className="muted">Loading task…</p>;
  if (!task) return <p className="muted">Task not found.</p>;

  return (
    <section className="taskdetail">
      <header className="taskdetail__header">
        <div>
          <h1>{task.name}</h1>
          <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
            Started {formatRelative(task.startedAt)}
            {task.durationSec !== undefined
              ? ` · ${formatDuration(task.durationSec)}`
              : ""}
            {task.clusterId && (
              <>
                {" · "}Cluster{" "}
                <Link to={`/clusters/${task.clusterId}`}>{task.clusterName}</Link>
              </>
            )}
            {" · "}Triggered by {task.triggeredBy}
          </p>
        </div>
        <div className="hstack">
          <TaskStatePill state={task.state} />
          {task.state === "running" && (
            <>
              <button className="btn btn--sm">Pause after step</button>
              <button className="btn btn--sm">Cancel</button>
            </>
          )}
          {task.retryable && (
            <button className="btn btn--sm btn--primary">Retry</button>
          )}
          <button className="btn btn--sm">Download log ⤓</button>
        </div>
      </header>

      <div className="card taskdetail__steps">
        <h2 className="taskdetail__sectionTitle">Steps</h2>
        <TaskStepsTimeline steps={task.steps} />
      </div>

      <div className="taskdetail__log">
        <h2 className="taskdetail__sectionTitle">Live log</h2>
        <TaskLogStream taskId={task.id} />
      </div>
    </section>
  );
}
