import { NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useAppVersion, useClusters } from "../api/hooks";
import "./AppShell.css";

export function AppShell() {
  const { data: clusters } = useClusters();
  const { data: appVersion } = useAppVersion();
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
      </header>

      <div className="shell__body">
        <nav className="shell__sidebar">
          <NavLink to="/clusters" className="shell__nav">Clusters</NavLink>
          <NavLink to="/history" className="shell__nav">History</NavLink>
          <NavLink to="/settings" className="shell__nav">Settings</NavLink>
          <div className="grow" />
          <a className="shell__nav shell__nav--subtle" href="https://github.com/buckit-io/buckit" target="_blank" rel="noreferrer">
            Docs ↗
          </a>
          <span className="shell__version">{appVersion ?? "—"}</span>
        </nav>

        <main className="shell__main">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
