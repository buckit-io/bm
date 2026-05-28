// Operations exposed by the Actions menu on /clusters/:id/nodes/:id.
// Each entry is scoped to a single host at dispatch time via
// OperationModal's targetHostIds prop (the one node being viewed).
//
// Diagnostics (logs) are navigate-flavor: the viewer page owns the
// fetch + controls, not a locked-until-done modal.

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

const REDEPLOY_SOFTWARE: OperationDef<Record<string, never>> = {
  id: "node_redeploy_software",
  group: "software",
  label: "Redeploy replacement node…",
  description: "Bootstrap a clean host into the cluster from a same-pool peer.",
  flavor: "orchestrated",
  opKind: "redeploy_software",
  buckitOnly: true,
  initialParams: {},
  inputSteps: [
    {
      id: "confirm",
      render: ({ cluster }) => (
        <div className="vstack" style={{ gap: "var(--s-3)" }}>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            Brings a clean replacement host online as a full member of
            this cluster. bm mirrors the configuration of a same-pool
            peer onto the target and installs Buckit{" "}
            <span className="mono">
              {cluster.version || "(unknown)"}
            </span>
            . To change cluster versions, use the cluster-wide upgrade
            flows instead.
          </p>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            <b>What bm will do:</b>
          </p>
          <ol
            style={{
              fontSize: "var(--fs-sm)",
              marginTop: 0,
              paddingLeft: "var(--s-4)",
            }}
          >
            <li>
              Verify the host is clean — no leftover config files,
              systemd drop-ins, or data on the data drives.
            </li>
            <li>
              Copy <span className="mono">/etc/default/minio</span>,{" "}
              <span className="mono">/etc/minio/config.env</span>,{" "}
              TLS certs, and any{" "}
              <span className="mono">
                /etc/systemd/system/buckit.service.d/
              </span>{" "}
              drop-ins from a same-pool peer.
            </li>
            <li>
              Create the Buckit service user/group and the
              MINIO_VOLUMES data directories owned by that user.
            </li>
            <li>
              Install the Buckit package, start{" "}
              <span className="mono">buckit.service</span>, and wait
              for the node to report healthy.
            </li>
          </ol>
          <div className="banner banner--warning">
            <span>⚠</span>
            <span>
              <b>Prerequisites.</b> The target must be a clean host:
              no existing buckit / minio config files, no operator
              drop-ins under{" "}
              <span className="mono">/etc/systemd/system/</span>, and
              every data drive mounted at its intended mount point
              with the buckit subdir empty or absent. At least one
              healthy peer in the same pool must already be in the
              cluster — bm reads the unit identity, env files, certs,
              and volume layout from it.
            </span>
          </div>
        </div>
      ),
      canAdvance: () => true,
      nextLabel: "Start redeploy",
    },
  ],
};

// ── Diagnostics ──────────────────────────────────────────────────

const VIEW_LIVE_LOGS: OperationDef<Record<string, never>> = {
  id: "node_view_live_logs",
  group: "diagnostics",
  label: "View service log",
  description: "Fetch recent journalctl output from this node.",
  flavor: "navigate",
  initialParams: {},
  inputSteps: [],
  // The cluster id is the cluster currently in view; the node id is
  // appended by NodeDetail when constructing the modal mount.
  navigateTo: (c, ctx) => `/clusters/${c.id}/nodes/${ctx?.nodeId ?? ""}/logs`,
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
