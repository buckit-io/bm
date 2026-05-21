// Validates that the chosen topology forms a deployable erasure-coded
// pool. Returns an array of error messages; empty array means OK.
//
// Erasure-coding rules enforced:
//   - At least one drive must be selected.
//   - Total drives must be divisible by at least one supported set size
//     (2–16). If not, the operator must add/remove drives.
//   - Set size must divide the total drive count cleanly.
//   - Parity must be ≤ setSize / 2.

import { NewClusterDraft } from "../state";

const VALID_SET_SIZES = [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];

export function topologyErrors(draft: NewClusterDraft): string[] {
  const errors: string[] = [];
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const nodes = validHosts.length;
  const drivesPerNode = draft.topology.selectedMounts.length;
  const totalDrives = nodes * drivesPerNode;
  const { setSize, parity } = draft.topology;

  if (drivesPerNode === 0) {
    errors.push("Select at least one drive to deploy.");
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

  if (totalDrives % setSize !== 0) {
    errors.push(
      `Total drives (${totalDrives}) is not divisible by set size (${setSize}). Valid set sizes for ${totalDrives} drives: ${validDivisors.join(", ")}.`,
    );
  }

  if (parity > Math.floor(setSize / 2)) {
    errors.push(
      `Parity (${parity}) exceeds maximum for set size ${setSize}. Max parity: ${Math.floor(setSize / 2)}.`,
    );
  }

  return errors;
}
