// Operations exposed by the bulk-selection bar on /clusters/:id.
// Each entry is identical in shape to the cluster-wide catalog, but
// the host scope is supplied at dispatch time via OperationModal's
// targetHostIds prop (set from the row checkboxes).

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

function typedConfirmStep(
  message: string,
  expected: string,
  nextLabel: string,
) {
  return {
    id: "typed-confirm",
    render: ({ params, setParams }: { params: { typed: string }; setParams: (p: { typed: string }) => void }) => (
      <>
        <p style={{ fontSize: "var(--fs-sm)" }}>{message}</p>
        <div className="field">
          <label className="field-label">
            Type <span className="mono">{expected}</span> to confirm
          </label>
          <input
            className="input"
            value={params.typed}
            onChange={(e) => setParams({ typed: e.target.value })}
            placeholder={expected}
          />
        </div>
      </>
    ),
    canAdvance: (p: { typed: string }) => p.typed === expected,
    nextLabel,
    danger: true,
  };
}

const SYSTEMCTL_RESTART: OperationDef<Record<string, never>> = {
  id: "bulk_systemctl_restart",
  group: "ssh",
  label: "Systemctl restart service",
  description: "Restart buckit.service on the selected hosts, one at a time.",
  flavor: "orchestrated",
  opKind: "systemctl_restart",
  cancellable: true,
  initialParams: {},
  inputSteps: [
    confirmStep(
      "bm runs systemctl restart buckit.service on each selected host, waiting for the node to report healthy before moving to the next. Per-host downtime ~5–10 s.",
      "Start restart",
    ),
  ],
};

const REDEPLOY_SOFTWARE: OperationDef<Record<string, never>> = {
  id: "bulk_redeploy_software",
  group: "ssh",
  label: "Redeploy replacement node…",
  description: "Bootstrap clean hosts into the cluster from same-pool peers.",
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
            Brings each selected clean replacement host online as a
            full member of this cluster. bm mirrors the configuration
            of a same-pool peer onto each target and installs Buckit{" "}
            <span className="mono">
              {cluster.version || "(unknown)"}
            </span>
            . Hosts are processed one at a time, waiting for each to
            report healthy before moving to the next. To change
            cluster versions, use the cluster-wide upgrade flows
            instead.
          </p>
          <p style={{ fontSize: "var(--fs-sm)" }}>
            <b>What bm will do on each host:</b>
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
              for the host to report healthy.
            </li>
          </ol>
          <div className="banner banner--warning">
            <span>⚠</span>
            <span>
              <b>Prerequisites.</b> Each target must be a clean host:
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

const REBOOT_HOST: OperationDef<{ typed: string }> = {
  id: "bulk_reboot_host",
  group: "ssh",
  label: "Reboot host…",
  description: "Reboot the selected hosts one at a time.",
  flavor: "orchestrated",
  opKind: "reboot_host",
  danger: true,
  cancellable: true,
  initialParams: { typed: "" },
  inputSteps: [
    typedConfirmStep(
      "bm reboots each selected host via systemctl reboot, then waits for the node to come back and rejoin the cluster before moving on. Per-host downtime ~30–90 s.",
      "REBOOT",
      "Reboot hosts",
    ),
  ],
};

const SHUTDOWN_HOST: OperationDef<{ typed: string }> = {
  id: "bulk_shutdown_host",
  group: "ssh",
  label: "Shut down host…",
  description: "Power off the selected hosts one at a time.",
  flavor: "orchestrated",
  opKind: "shutdown_host",
  danger: true,
  initialParams: { typed: "" },
  inputSteps: [
    typedConfirmStep(
      "bm powers off each selected host via systemctl poweroff. The hosts stay down until you power them on manually. Don't shut down so many at once that you lose quorum.",
      "SHUTDOWN",
      "Shut down hosts",
    ),
  ],
};

export const BULK_HOST_OPERATIONS: OperationDef[] = [
  SYSTEMCTL_RESTART,
  REDEPLOY_SOFTWARE,
  REBOOT_HOST,
  SHUTDOWN_HOST,
];
