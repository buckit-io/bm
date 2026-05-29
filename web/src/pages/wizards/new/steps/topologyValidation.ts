// Validates that the chosen topology forms a deployable storage layout.
// Returns an array of error messages; empty array means OK.
//
// Rules enforced:
//   - At least one drive must be selected.
//   - One total drive is allowed as a standalone deployment.
//   - Total drives must be divisible by at least one supported set size
//     (2–16) for erasure-coded deployments. If not, the operator must
//     add/remove drives.
//   - Set size must divide the total drive count cleanly in erasure mode.
//   - Parity must be between 1 and setSize / 2 in erasure mode.

import { NewClusterDraft } from "../state";
import { parseCustomDataVolumes } from "./customDataVolumes";
import { deriveDeploymentMode } from "./deploymentMode";

const VALID_SET_SIZES = [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];

export function topologyErrors(draft: NewClusterDraft): string[] {
  const errors: string[] = [];
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const nodes = validHosts.length;
  const drivesPerNode = draft.topology.selectedMounts.length;
  const totalDrives = nodes * drivesPerNode;
  const { setSize, parity } = draft.topology;
  const deploymentMode = deriveDeploymentMode(totalDrives);

  if (draft.topology.dataVolumeMode === "custom") {
    const parsed = parseCustomDataVolumes(draft.topology.customDataVolumePaths ?? "");
    if (parsed.error) {
      errors.push(parsed.error);
      return errors;
    }
  }

  if (drivesPerNode === 0) {
    errors.push("Select at least one drive to deploy.");
    return errors;
  }

  if (deploymentMode === "standalone") {
    return errors;
  }

  // Check if any valid set size divides the total.
  const validDivisors = VALID_SET_SIZES.filter((s) => totalDrives % s === 0);
  if (validDivisors.length === 0) {
    errors.push(
      `Total drives (${totalDrives}) is not divisible by any supported erasure set size (2–16). Add or remove drives so the total is divisible by at least one of: ${VALID_SET_SIZES.join(", ")}.`,
    );
    return errors;
  }

  if (setSize < 2) {
    errors.push("Erasure-coded deployments require a set size of at least 2.");
    return errors;
  }

  if (totalDrives % setSize !== 0) {
    errors.push(
      `Total drives (${totalDrives}) is not divisible by set size (${setSize}). Valid set sizes for ${totalDrives} drives: ${validDivisors.join(", ")}.`,
    );
  }

  if (parity < 1) {
    errors.push("Erasure-coded deployments require parity of at least 1.");
  }

  if (parity > Math.floor(setSize / 2)) {
    errors.push(
      `Parity (${parity}) exceeds maximum for set size ${setSize}. Max parity: ${Math.floor(setSize / 2)}.`,
    );
  }

  return errors;
}
