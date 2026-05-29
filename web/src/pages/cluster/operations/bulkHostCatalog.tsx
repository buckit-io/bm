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
  REBOOT_HOST,
  SHUTDOWN_HOST,
];
