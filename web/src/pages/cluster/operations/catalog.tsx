// The concrete operation definitions, one per Cluster Actions menu
// item. Grouped by transport (admin / ssh / manager) for menu display.

import { OperationDef } from "./defs";

// Simple one-line confirmation step shared by ops that just need
// "are you sure".
function confirmStep(message: string, nextLabel: string, danger?: boolean) {
  return {
    id: "confirm",
    render: () => <p style={{ fontSize: "var(--fs-sm)" }}>{message}</p>,
    canAdvance: () => true,
    nextLabel,
    danger,
  };
}

// ── Admin API ─────────────────────────────────────────────────────

const RESTART_CLUSTER: OperationDef<Record<string, never>> = {
  id: "restart_cluster",
  group: "admin",
  label: "Restart cluster",
  description: "Fast — every node re-execs in place. ~3 s of cluster unavailability.",
  flavor: "orchestrated",
  opKind: "restart_cluster",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "Sends a restart signal to the cluster via the admin API. Every node re-execs in place, causing roughly 3 seconds of cluster unavailability.",
      "Restart cluster",
    ),
  ],
};

const STOP_CLUSTER: OperationDef<Record<string, never>> = {
  id: "stop_cluster",
  group: "admin",
  label: "Stop cluster",
  description: "Stops every node. S3 API and console unreachable until started.",
  flavor: "orchestrated",
  opKind: "stop_cluster",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "Stops buckit.service on every node via the admin API. The S3 API and console will be unreachable until you start the cluster again. Data and config are not affected.",
      "Stop cluster",
      true,
    ),
  ],
};

const FREEZE_API: OperationDef<Record<string, never>> = {
  id: "freeze_api",
  group: "admin",
  label: "Freeze S3 API",
  description: "Blocks all S3 API calls (reads and writes). Use only during maintenance.",
  flavor: "signal",
  opKind: "freeze_api",
  initialParams: {},
  visible: (c) => !c.apiFrozen,
  inputSteps: [
    confirmStep(
      "Freezes the S3 API via the admin API. The cluster will reject all S3 API requests — reads AND writes — until you unfreeze. The admin API still works (you need it to unfreeze). Note: this action is normally hidden from standard tooling because it's disruptive.",
      "Freeze S3 API",
      true,
    ),
  ],
};

const UNFREEZE_API: OperationDef<Record<string, never>> = {
  id: "unfreeze_api",
  group: "admin",
  label: "Unfreeze S3 API",
  description: "Resume accepting S3 API calls.",
  flavor: "signal",
  opKind: "unfreeze_api",
  initialParams: {},
  visible: (c) => c.apiFrozen,
  inputSteps: [
    confirmStep(
      "Unfreezes the S3 API via the admin API. The cluster will resume accepting all S3 API requests.",
      "Unfreeze S3 API",
    ),
  ],
};

const START_HEAL: OperationDef<Record<string, never>> = {
  id: "start_heal",
  group: "admin",
  label: "Start heal",
  description: "Trigger a heal scan. Runs server-side; may take hours.",
  flavor: "orchestrated",
  opKind: "start_heal",
  cancellable: true,
  cancelLabel: "Stop following",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "Starts a heal scan via the admin API. The scan runs server-side and may take hours on a large deployment. bm follows the event stream live. Stopping the follow does not stop the scan.",
      "Start heal",
    ),
  ],
};

// ── SSH ───────────────────────────────────────────────────────────

const ROLLING_RESTART: OperationDef<Record<string, never>> = {
  id: "rolling_restart",
  group: "ssh",
  label: "Rolling restart",
  description: "One node at a time, health-wait between. No cluster downtime.",
  flavor: "orchestrated",
  opKind: "rolling_restart",
  cancellable: true,
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm restarts buckit.service one node at a time, waiting for each node to report healthy before moving to the next. Per-node downtime ~5–10 s; total ~nodes × per-node.",
      "Start rolling restart",
    ),
  ],
};

const ROLLING_UPGRADE: OperationDef<{ version: string }> = {
  id: "rolling_upgrade",
  group: "ssh",
  label: "Rolling upgrade…",
  description: "Upgrade Buckit version one node at a time.",
  flavor: "orchestrated",
  opKind: "rolling_upgrade",
  initialParams: { version: "v1.0.0" },
  inputSteps: [
    {
      id: "version",
      render: ({ params, setParams }) => (
        <>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            bm will scp the new package to each node, install it, then
            rolling-restart with health-wait between nodes.
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
      nextLabel: "Start upgrade",
    },
  ],
};

const START_CLUSTER: OperationDef<Record<string, never>> = {
  id: "start_cluster",
  group: "ssh",
  label: "Start cluster",
  description: "Start a stopped cluster. Per-node systemctl start over SSH.",
  flavor: "orchestrated",
  opKind: "start_cluster",
  cancellable: true,
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm starts buckit.service on every node via SSH. The admin API can't be used here because there's no process to receive it.",
      "Start cluster",
    ),
  ],
};

const ROTATE_ROOT_CREDS: OperationDef<{
  newUser: string;
  newPassword: string;
  typedName: string;
  showPass: boolean;
}> = {
  id: "rotate_root_creds",
  group: "ssh",
  label: "Rotate root credentials…",
  description: "Set a new root user / password across all nodes.",
  flavor: "orchestrated",
  opKind: "rotate_root_creds",
  initialParams: {
    newUser: "",
    newPassword: "",
    typedName: "",
    showPass: false,
  },
  inputSteps: [
    {
      id: "form",
      render: ({ params, setParams, cluster }) => (
        <div className="vstack" style={{ gap: "var(--s-3)" }}>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            bm rewrites <span className="mono">/etc/default/minio</span>{" "}
            on every node and runs a two-pass rolling restart so the
            cluster transitions from the old credentials to the new
            without losing quorum.
          </p>
          <div className="field">
            <label className="field-label">New root user</label>
            <input
              className="input"
              value={params.newUser}
              onChange={(e) =>
                setParams({
                  ...params,
                  newUser: e.target.value.replace(/[^a-zA-Z0-9_-]/g, ""),
                })
              }
              placeholder="admin"
            />
          </div>
          <div className="field">
            <label className="field-label">New root password</label>
            <div className="hstack" style={{ gap: "var(--s-2)" }}>
              <input
                type={params.showPass ? "text" : "password"}
                className="input"
                value={params.newPassword}
                onChange={(e) =>
                  setParams({ ...params, newPassword: e.target.value })
                }
                style={{ flex: 1 }}
              />
              <button
                type="button"
                className="btn btn--sm"
                onClick={() =>
                  setParams({ ...params, showPass: !params.showPass })
                }
              >
                {params.showPass ? "Hide" : "Show"}
              </button>
            </div>
            <span className="field-hint">Min 8 characters.</span>
          </div>
          <div className="banner banner--warning">
            <span>⚠</span>
            <span>
              Existing S3 clients, applications, and CLI sessions using
              the old credentials will stop working at cutover. Update
              them before clients reconnect.
            </span>
          </div>
          <div className="field">
            <label className="field-label">
              Type the cluster name to confirm
            </label>
            <input
              className="input"
              value={params.typedName}
              onChange={(e) =>
                setParams({ ...params, typedName: e.target.value })
              }
              placeholder={cluster.name}
            />
          </div>
        </div>
      ),
      canAdvance: (p, c) =>
        p.newUser.length >= 3 &&
        p.newPassword.length >= 8 &&
        p.typedName === c.name,
      nextLabel: "Rotate",
      danger: true,
    },
  ],
};

const ADD_POOL: OperationDef<Record<string, never>> = {
  id: "add_pool",
  group: "ssh",
  label: "Add new pool…",
  description: "Expand the cluster with another pool.",
  flavor: "orchestrated",
  opKind: "add_pool",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "Adding a new pool requires updating MINIO_VOLUMES on every node and rolling-restarting the cluster. The full pool-add wizard hasn't shipped yet.",
      "Continue",
    ),
  ],
};

// ── Manager ───────────────────────────────────────────────────────

const CONFIGURE_SSH: OperationDef<Record<string, never>> = {
  id: "configure_ssh",
  group: "manager",
  label: "Configure SSH credentials",
  description: "Set the SSH credentials bm uses to reach this cluster.",
  flavor: "navigate",
  initialParams: {},
  inputSteps: [],
  navigateTo: (c) => `/clusters/${c.id}/ssh`,
};

const REMOVE_CLUSTER: OperationDef<{ typedName: string }> = {
  id: "remove_cluster",
  group: "manager",
  label: "Remove cluster definition",
  description: "Drop the cluster from this manager. Hosts and data untouched.",
  flavor: "signal",
  opKind: "remove_cluster",
  danger: true,
  initialParams: { typedName: "" },
  inputSteps: [
    {
      id: "typed-confirm",
      render: ({ params, setParams, cluster }) => (
        <>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            Removes <b>{cluster.name}</b> from this manager. You can
            re-import via <span className="mono">/clusters/import</span>{" "}
            later.
          </p>
          <div className="field">
            <label className="field-label">
              Type the cluster name to confirm
            </label>
            <input
              className="input"
              value={params.typedName}
              onChange={(e) =>
                setParams({ typedName: e.target.value })
              }
              placeholder={cluster.name}
            />
          </div>
        </>
      ),
      canAdvance: (p, c) => p.typedName === c.name,
      nextLabel: "Remove",
      danger: true,
    },
  ],
};

export const CLUSTER_OPERATIONS: OperationDef[] = [
  // Admin API
  RESTART_CLUSTER,
  STOP_CLUSTER,
  FREEZE_API,
  UNFREEZE_API,
  START_HEAL,
  // SSH
  ROLLING_RESTART,
  ROLLING_UPGRADE,
  START_CLUSTER,
  ROTATE_ROOT_CREDS,
  ADD_POOL,
  // Manager
  CONFIGURE_SSH,
  REMOVE_CLUSTER,
];

export const GROUP_LABELS: Record<string, string> = {
  admin: "Admin API",
  ssh: "SSH",
  manager: "Manager",
};
