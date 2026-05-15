import { NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useTasks, useClusters } from "../api/hooks";
import "./AppShell.css";

export function AppShell() {
  const { data: clusters } = useClusters();
  const { data: tasks } = useTasks();
  const runningTasks = (tasks ?? []).filter((t) => t.state === "running");
  const location = useLocation();
  const params = useParams();

  const inCluster = location.pathname.startsWith("/clusters/") && params.clusterId;
  const activeCluster = inCluster
    ? (clusters ?? []).find((c) => c.id === params.clusterId)
    : null;

  return (
    <div className="shell">
      <header className="shell__topbar">
        <NavLink to="/clusters" className="shell__brand">
          <span className="shell__brand-mark">B</span>
          <span>Buckit Manager</span>
        </NavLink>

        {activeCluster && (
          <div className="shell__crumb">
            <span className="subtle">Cluster:</span>
            <span className="shell__crumb-name">{activeCluster.name}</span>
          </div>
        )}

        <div className="grow" />

        <NavLink to="/tasks" className="shell__tasks">
          <span className={runningTasks.length ? "shell__tasks-dot is-active" : "shell__tasks-dot"} />
          {runningTasks.length} task{runningTasks.length === 1 ? "" : "s"}
        </NavLink>
      </header>

      <div className="shell__body">
        <nav className="shell__sidebar">
          <NavLink to="/clusters" className="shell__nav">Clusters</NavLink>
          <NavLink to="/tasks" className="shell__nav">Tasks</NavLink>
          <NavLink to="/history" className="shell__nav">History</NavLink>
          <NavLink to="/settings" className="shell__nav">Settings</NavLink>
          <div className="grow" />
          <a className="shell__nav shell__nav--subtle" href="https://github.com/buckit-io/buckit" target="_blank" rel="noreferrer">
            Docs ↗
          </a>
          <span className="shell__version">v0.1.0</span>
        </nav>

        <main className="shell__main">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
