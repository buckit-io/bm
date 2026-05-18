// Operations exposed by the Actions menu on /clusters/:id/nodes/:id.
// Each entry is scoped to a single host at dispatch time via
// OperationModal's targetHostIds prop (the one node being viewed).
//
// Diagnostics (logs/trace) are navigate-flavor: streaming viewers
// belong on their own page, not in a locked-until-done modal.

import { OperationDef } from "./defs";

function confirmStep(message: string, nextLabel: string, danger?: boolean) {
  return {
    id: "confirm",
    render: () => <p style={{ fontSize: "var(--fs-sm)" }}>{message}</p>,
    canAdvance: () => true,
    nextLabel,
    danger,
  };
}

// ── Service ──────────────────────────────────────────────────────

const SYSTEMCTL_RESTART: OperationDef<Record<string, never>> = {
  id: "node_systemctl_restart",
  group: "service",
  label: "Systemctl restart service",
  description: "Restart buckit.service on this node. ~5–10 s downtime.",
  flavor: "orchestrated",
  opKind: "systemctl_restart",
  cancellable: true,
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm runs systemctl restart buckit.service on this host and waits for the node to report healthy.",
      "Restart service",
    ),
  ],
};

const SYSTEMCTL_STOP: OperationDef<Record<string, never>> = {
  id: "node_systemctl_stop",
  group: "service",
  label: "Systemctl stop service",
  description: "Stop buckit.service on this node. Pool serves through other nodes.",
  flavor: "orchestrated",
  opKind: "systemctl_stop",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm runs systemctl stop buckit.service on this host. The pool continues serving through the other nodes, but you'll be running below quorum margin until you start the service again.",
      "Stop service",
      true,
    ),
  ],
};

const SYSTEMCTL_START: OperationDef<Record<string, never>> = {
  id: "node_systemctl_start",
  group: "service",
  label: "Systemctl start service",
  description: "Start buckit.service on this node.",
  flavor: "orchestrated",
  opKind: "systemctl_start",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm runs systemctl start buckit.service on this host and waits for the node to report healthy.",
      "Start service",
    ),
  ],
};

// ── Software ─────────────────────────────────────────────────────

const REDEPLOY_SOFTWARE: OperationDef<{ version: string }> = {
  id: "node_redeploy_software",
  group: "software",
  label: "Redeploy software…",
  description: "Reinstall Buckit on this node.",
  flavor: "orchestrated",
  opKind: "redeploy_software",
  initialParams: { version: "v1.0.0" },
  inputSteps: [
    {
      id: "version",
      render: ({ params, setParams }) => (
        <>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            bm scp's the package to this host, installs it, then restarts
            buckit.service.
          </p>
          <div className="field">
            <label className="field-label">Target version</label>
            <select
              className="select"
              value={params.version}
              onChange={(e) => setParams({ version: e.target.value })}
            >
              <option value="v1.0.1">v1.0.1 (latest stable)</option>
              <option value="v1.0.0">v1.0.0</option>
              <option value="v0.99.0">v0.99.0</option>
            </select>
          </div>
        </>
      ),
      canAdvance: (p) => p.version.length > 0,
      nextLabel: "Start redeploy",
    },
  ],
};

// ── Diagnostics ──────────────────────────────────────────────────

const VIEW_LIVE_LOGS: OperationDef<Record<string, never>> = {
  id: "node_view_live_logs",
  group: "diagnostics",
  label: "View live logs",
  description: "Tail journalctl on this node in a live viewer.",
  flavor: "navigate",
  initialParams: {},
  inputSteps: [],
  // The cluster id is the cluster currently in view; the node id is
  // appended by NodeDetail when constructing the modal mount.
  navigateTo: (c, ctx) => `/clusters/${c.id}/nodes/${ctx?.nodeId ?? ""}/logs`,
};

const RUN_TRACE: OperationDef<Record<string, never>> = {
  id: "node_run_trace",
  group: "diagnostics",
  label: "Run trace",
  description: "Stream the S3 API trace for this node.",
  flavor: "navigate",
  initialParams: {},
  inputSteps: [],
  navigateTo: (c, ctx) => `/clusters/${c.id}/nodes/${ctx?.nodeId ?? ""}/trace`,
};

// ── Host ─────────────────────────────────────────────────────────

const REBOOT_HOST: OperationDef<{ typed: string }> = {
  id: "node_reboot_host",
  group: "host",
  label: "Reboot host…",
  description: "Reboot this host. ~30–90 s downtime.",
  flavor: "orchestrated",
  opKind: "reboot_host",
  danger: true,
  cancellable: true,
  initialParams: { typed: "" },
  inputSteps: [
    {
      id: "typed-confirm",
      render: ({ params, setParams }) => (
        <>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            bm reboots this host via systemctl reboot, then waits for
            the node to come back and rejoin the cluster. Per-host
            downtime ~30–90 s.
          </p>
          <div className="field">
            <label className="field-label">
              Type <span className="mono">REBOOT</span> to confirm
            </label>
            <input
              className="input"
              value={params.typed}
              onChange={(e) => setParams({ typed: e.target.value })}
              placeholder="REBOOT"
            />
          </div>
        </>
      ),
      canAdvance: (p) => p.typed === "REBOOT",
      nextLabel: "Reboot",
      danger: true,
    },
  ],
};

const SHUTDOWN_HOST: OperationDef<{ typed: string }> = {
  id: "node_shutdown_host",
  group: "host",
  label: "Shut down host…",
  description: "Power off this host. Stays down until manually started.",
  flavor: "orchestrated",
  opKind: "shutdown_host",
  danger: true,
  initialParams: { typed: "" },
  inputSteps: [
    {
      id: "typed-confirm",
      render: ({ params, setParams }) => (
        <>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            bm powers off this host via systemctl poweroff. The host
            stays down until you power it on manually.
          </p>
          <div className="field">
            <label className="field-label">
              Type <span className="mono">SHUTDOWN</span> to confirm
            </label>
            <input
              className="input"
              value={params.typed}
              onChange={(e) => setParams({ typed: e.target.value })}
              placeholder="SHUTDOWN"
            />
          </div>
        </>
      ),
      canAdvance: (p) => p.typed === "SHUTDOWN",
      nextLabel: "Shut down",
      danger: true,
    },
  ],
};

export const NODE_OPERATIONS: OperationDef[] = [
  // Service
  SYSTEMCTL_RESTART,
  SYSTEMCTL_STOP,
  SYSTEMCTL_START,
  // Software
  REDEPLOY_SOFTWARE,
  // Diagnostics
  VIEW_LIVE_LOGS,
  RUN_TRACE,
  // Host
  REBOOT_HOST,
  SHUTDOWN_HOST,
];

export const NODE_GROUP_LABELS: Record<string, string> = {
  service: "Service",
  software: "Software",
  diagnostics: "Diagnostics",
  host: "Host",
};
