import { useState } from "react";
import { NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { shutdownManager } from "../api/client";
import { useClusters } from "../api/hooks";
import "./AppShell.css";

type ShutdownState = "idle" | "shutting_down" | "stopped";

export function AppShell() {
  const { data: clusters } = useClusters();
  const location = useLocation();
  const params = useParams();
  const [shutdownState, setShutdownState] = useState<ShutdownState>("idle");
  const [shutdownError, setShutdownError] = useState<string | null>(null);

  const inCluster = location.pathname.startsWith("/clusters/") && params.clusterId;
  const activeCluster = inCluster
    ? (clusters ?? []).find((c) => c.id === params.clusterId)
    : null;

  const onExit = async () => {
    if (shutdownState !== "idle") return;
    const confirmed = window.confirm(
      "Stop Buckit Manager?\n\nThis stops the local bm web server. Running operations will be interrupted.",
    );
    if (!confirmed) return;

    setShutdownState("shutting_down");
    setShutdownError(null);
    try {
      await shutdownManager();
      setShutdownState("stopped");
      window.setTimeout(() => window.close(), 250);
    } catch (err) {
      setShutdownState("idle");
      setShutdownError(err instanceof Error ? err.message : "Failed to stop Buckit Manager");
    }
  };

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

        {shutdownError && (
          <span className="shell__shutdown-error" role="alert">
            {shutdownError}
          </span>
        )}
        <div className="shell__utilities">
          <a
            className="btn btn--ghost btn--sm shell__utility"
            href="https://buckit.sh/docs/administration/buckit-manager"
            target="_blank"
            rel="noreferrer"
          >
            Docs ↗
          </a>
          <button
            className="btn btn--ghost btn--sm shell__exit"
            type="button"
            onClick={onExit}
            disabled={shutdownState !== "idle"}
          >
            {shutdownState === "shutting_down" ? "Exiting..." : "Exit"}
          </button>
        </div>
      </header>

      <div className="shell__body">
        <nav className="shell__sidebar">
          <NavLink to="/clusters" className="shell__nav">Clusters</NavLink>
          <NavLink to="/welcome" className="shell__nav">Wizards</NavLink>
          <NavLink to="/history" className="shell__nav">History</NavLink>
          <NavLink to="/settings" className="shell__nav">Settings</NavLink>
        </nav>

        <main className="shell__main">
          <Outlet />
        </main>
      </div>

      {shutdownState === "stopped" && (
        <div className="shell__stopped" role="dialog" aria-modal="true" aria-labelledby="shell-stopped-title">
          <div className="shell__stopped-panel">
            <h1 id="shell-stopped-title">Buckit Manager has stopped</h1>
            <p>The local bm web server has exited. You can close this tab.</p>
            <button className="btn btn--primary" type="button" onClick={() => window.close()}>
              Close tab
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
