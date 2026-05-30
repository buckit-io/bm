// The concrete operation definitions, one per Cluster Actions menu
// item. Grouped by transport (admin / ssh / manager) for menu display.

import { useEffect, useState } from "react";
import { listVersions } from "../../../api/client";
import type { BuckitVersion } from "../../../api/types";
import {
  ROOT_PASSWORD_HINT,
  validateRootPassword,
} from "../../../lib/credentials";
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

export function VersionSelectStep({
  params,
  setParams,
  intro,
}: {
  params: { version: string };
  setParams: (next: { version: string }) => void;
  intro: string;
}) {
  const [versions, setVersions] = useState<BuckitVersion[]>([]);

  useEffect(() => {
    listVersions().then(setVersions).catch(() => {});
  }, []);

  useEffect(() => {
    if (versions.length === 0) return;
    if (versions.some((v) => v.tag === params.version)) return;
    setParams({ version: versions[0].tag });
  }, [versions, params.version, setParams]);

  return (
    <>
      <p style={{ fontSize: "var(--fs-sm)" }}>{intro}</p>
      <div className="field">
        <label className="field-label">Target version</label>
        <select
          className="select"
          data-testid="cluster-operation-version"
          value={params.version}
          onChange={(e) => setParams({ version: e.target.value })}
        >
          {versions.map((v) => (
            <option key={v.tag} value={v.tag}>
              {v.label}
            </option>
          ))}
        </select>
      </div>
    </>
  );
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
    {
      id: "confirm",
      render: () => (
        <div className="vstack" style={{ gap: "var(--s-3)" }}>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            Stops buckit.service on every node via the admin API. The
            S3 API and console will be unreachable until you start the
            cluster again. Data and config are not affected.
          </p>
          <div className="banner banner--warning">
            <span>⚠</span>
            <span>
              If the cluster is running under a systemd service,
              systemd will respawn the process immediately after this
              stop. Use <b>Stop cluster (systemctl)</b> instead to
              leave the cluster down.
            </span>
          </div>
        </div>
      ),
      canAdvance: () => true,
      nextLabel: "Stop cluster",
      danger: true,
    },
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

const CLUSTER_UPGRADE_BY_ADMIN_UPDATE: OperationDef<{ version: string }> = {
  id: "cluster_upgrade_by_admin_update",
  group: "admin",
  label: "Upgrade cluster via Admin API",
  description: "Native self-update path. Downloads the new binary and restarts all nodes together.",
  flavor: "orchestrated",
  opKind: "cluster_upgrade_by_admin_update",
  buckitOnly: true,
  initialParams: { version: "v1.0.0" },
  inputSteps: [
    {
      id: "version",
      render: ({ params, setParams }) => (
        <div className="vstack" style={{ gap: "var(--s-3)" }}>
          <VersionSelectStep
            params={params}
            setParams={setParams}
            intro="Calls the Buckit Admin API update flow. Use this for clusters not managed by systemd. The selected release binary is applied cluster-wide and every node restarts together."
          />
          <div className="banner banner--warning">
            <span>⚠</span>
            <span>
              If the cluster is running under a systemd service, use{" "}
              <b>Upgrade Buckit systemd service</b> instead. The Admin
              API self-update only replaces the running binary, so the
              installed package version will end up out of sync with
              what's actually running.
            </span>
          </div>
        </div>
      ),
      canAdvance: (p) => p.version.length > 0,
      nextLabel: "Start upgrade",
    },
  ],
};

// ── SSH ───────────────────────────────────────────────────────────

const ROLLING_RESTART: OperationDef<Record<string, never>> = {
  id: "rolling_restart",
  group: "ssh",
  label: "Rolling restart (systemctl)",
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

const CLUSTER_UPGRADE_BY_SYSTEMCTL: OperationDef<{ version: string }> = {
  id: "cluster_upgrade_by_systemctl",
  group: "ssh",
  label: "Upgrade Buckit systemd service…",
  description: "Stage the upgrade on all nodes, then restart the cluster once.",
  flavor: "orchestrated",
  opKind: "cluster_upgrade_by_systemctl",
  buckitOnly: true,
  initialParams: { version: "v1.0.0" },
  inputSteps: [
    {
      id: "version",
      render: ({ params, setParams }) => (
        <VersionSelectStep
          params={params}
          setParams={setParams}
          intro="Installs the new package on every node without restarting, then restarts the cluster once via the Admin API after all nodes are updated."
        />
      ),
      canAdvance: (p) => p.version.length > 0,
      nextLabel: "Start upgrade",
    },
  ],
};

const START_CLUSTER: OperationDef<Record<string, never>> = {
  id: "start_cluster",
  group: "ssh",
  label: "Start cluster (systemctl)",
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

const STOP_CLUSTER_BY_SYSTEMCTL: OperationDef<Record<string, never>> = {
  id: "stop_cluster_by_systemctl",
  group: "ssh",
  label: "Stop cluster (systemctl)",
  description: "Per-node systemctl stop over SSH. Cluster stays down.",
  flavor: "orchestrated",
  opKind: "stop_cluster_by_systemctl",
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm stops buckit.service on every node via SSH. Unlike \"Stop cluster\" (admin API), the systemd unit ends up inactive, so the Restart=always policy will not respawn it. The S3 API and console will be unreachable until you start the cluster again.",
      "Stop cluster",
      true,
    ),
  ],
};

const ROTATE_ROOT_CREDS: OperationDef<{
  newPassword: string;
  typedName: string;
  showPass: boolean;
}> = {
  id: "rotate_root_creds",
  group: "ssh",
  label: "Rotate root password…",
  description: "Rewrite the cluster root password on every node, then restart the cluster.",
  flavor: "orchestrated",
  opKind: "rotate_root_creds",
  buckitOnly: true,
  initialParams: {
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
            1. Moves current password into reloadable env file{" "}
            <span className="mono">/etc/minio/config.env</span> if it's
            not there on each node, does a rolling systemctl restart.
            <br />
            2. Writes the new password to the env file on each node.
            <br />
            3. Restarts the cluster through the admin API.
            <br />
            4. Waits for the cluster to become healthy.
            <br />
            5. Rolls back the changes if a step fails.
          </p>
          <div className="field">
            <label className="field-label">New root password</label>
            <div className="hstack" style={{ gap: "var(--s-2)" }}>
              <input
                data-testid="rotate-root-password-input"
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
            <span className="field-hint">{ROOT_PASSWORD_HINT}</span>
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
              data-testid="rotate-root-password-confirm-name"
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
        validateRootPassword(p.newPassword).ok && p.typedName === c.name,
      nextLabel: "Rotate password",
      danger: true,
    },
  ],
};

// Hidden until the pool-add wizard ships.
// const ADD_POOL: OperationDef<Record<string, never>> = {
//   id: "add_pool",
//   group: "ssh",
//   label: "Add new pool…",
//   description: "Expand the cluster with another pool.",
//   flavor: "orchestrated",
//   opKind: "add_pool",
//   initialParams: {},
//   inputSteps: [
//     confirmStep(
//       "Adding a new pool requires updating MINIO_VOLUMES on every node and rolling-restarting the cluster. The full pool-add wizard hasn't shipped yet.",
//       "Continue",
//     ),
//   ],
// };

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

const CONFIGURE_ADMIN_CREDS: OperationDef<Record<string, never>> = {
  id: "configure_admin_creds",
  group: "manager",
  label: "Set admin credentials",
  description: "Set the root credentials bm uses for talking to cluster nodes.",
  flavor: "navigate",
  initialParams: {},
  inputSteps: [],
  navigateTo: (c) => `/clusters/${c.id}/settings`,
};

const REMOVE_CLUSTER: OperationDef<{ typedName: string }> = {
  id: "remove_cluster",
  group: "manager",
  label: "Remove cluster definition",
  description: "Drop the cluster from this manager. Hosts and data untouched.",
  // Backend: DELETE /clusters/:id. Not an OpKind — the design doc
  // dropped remove_cluster from the dispatch path in favor of REST.
  flavor: "delete",
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
  CLUSTER_UPGRADE_BY_ADMIN_UPDATE,
  // SSH
  ROLLING_RESTART,
  START_CLUSTER,
  STOP_CLUSTER_BY_SYSTEMCTL,
  CLUSTER_UPGRADE_BY_SYSTEMCTL,
  ROTATE_ROOT_CREDS,
  // ADD_POOL,  // hidden — pool-add wizard not yet shipped
  // Manager
  CONFIGURE_SSH,
  CONFIGURE_ADMIN_CREDS,
  REMOVE_CLUSTER,
];

export const GROUP_LABELS: Record<string, string> = {
  admin: "Admin API",
  ssh: "SSH",
  manager: "Manager",
};
