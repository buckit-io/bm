// Validates that the chosen topology forms a deployable erasure-coded
// pool. Returns an array of error messages; empty array means OK.
//
// Erasure-coding rules enforced:
//   - At least one drive must be selected.
//   - Set size must divide the total drive count cleanly (every erasure
//     set has exactly `setSize` drives — leftover drives can't be used).
//   - Parity must be strictly less than set size (parity == setSize
//     leaves zero data shards).
//
// Things deliberately NOT enforced here (deferred to M5 preflight):
//   - Uniform drive sizes across hosts (the real backend reads
//     discovery; the wizard doesn't have ground truth without M4).
//   - Time skew, sudo presence, package-manager availability, etc.

import { NewClusterDraft } from "../state";

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

  if (parity >= setSize) {
    errors.push(
      `Parity (${parity}) must be less than set size (${setSize}). Lower the parity or raise the set size.`,
    );
  }

  if (totalDrives % setSize !== 0) {
    errors.push(
      `Total drives across all hosts (${totalDrives}) is not divisible by set size (${setSize}). Pick a different set size or adjust the drive selection.`,
    );
  }

  return errors;
}
