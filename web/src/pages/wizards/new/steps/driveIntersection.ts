// Computes the intersection of per-host drive mountpoints so the
// Topology step can auto-select MINIO_VOLUMES drives.
//
// Eligibility rules:
//   - Boot drives are excluded (isBoot === true).
//   - Unformatted drives are excluded — they have no mountpoint to
//     intersect on. The operator must prepare them first (case C).
//   - System mounts (/, /boot, /home, /var, /etc, /tmp, /usr) are
//     excluded by default so accidental selections are caught. When the
//     operator sets a specific data volume root, mounts under that root
//     are allowed even if they live under a system prefix.
//
// Result shape:
//   common         — sorted list of mountpoints present on EVERY host
//   extrasByHost   — per-host mountpoints that didn't make the cut
//                    (eligible but not present on at least one other host)
//   eligibleByHost — per-host count of eligible data drives (for case
//                    detection: if any host has 0, we're in case C)
//
// Case detection by the caller:
//   common.length > 0 AND no extras → case A (full overlap)
//   common.length > 0 AND extras    → case B (partial overlap)
//   common.length === 0              → case C (no common mounts)

import { DiscoveredDrive } from "../state";

const SYSTEM_MOUNT_PREFIXES = [
  "/",
  "/boot",
  "/home",
  "/var",
  "/etc",
  "/tmp",
  "/usr",
  "/root",
];

export function isEligibleMount(d: DiscoveredDrive): boolean {
  return isEligibleMountUnderRoot(d, "");
}

export function isEligibleMountUnderRoot(
  d: DiscoveredDrive,
  preferredRoot: string,
): boolean {
  if (d.isBoot) return false;
  if (!d.mount) return false;
  // A drive with a mountpoint is clearly formatted even if fsType wasn't
  // detected (e.g. loop devices where lsblk doesn't report fstype).
  if (hasSpecificRoot(preferredRoot)) {
    return mountUnderRoot(d.mount, preferredRoot);
  }
  if (d.isSystem) return false;
  if (SYSTEM_MOUNT_PREFIXES.includes(d.mount)) return false;
  return true;
}

export interface IntersectionResult {
  common: string[];
  extrasByHost: Record<string, string[]>;
  eligibleByHost: Record<string, number>;
}

export function intersectMounts(
  perHost: Record<string, DiscoveredDrive[]>,
  preferredRoot = "",
): IntersectionResult {
  const hostIds = Object.keys(perHost);
  if (hostIds.length === 0) {
    return { common: [], extrasByHost: {}, eligibleByHost: {} };
  }

  const eligibleSets: Record<string, Set<string>> = {};
  const eligibleByHost: Record<string, number> = {};
  for (const hid of hostIds) {
    const mounts = (perHost[hid] ?? [])
      .filter((d) => isEligibleMountUnderRoot(d, preferredRoot))
      .map((d) => d.mount);
    eligibleSets[hid] = new Set(mounts);
    eligibleByHost[hid] = mounts.length;
  }

  // Intersection of all sets.
  const first = eligibleSets[hostIds[0]];
  const common = new Set<string>();
  for (const m of first) {
    if (hostIds.every((hid) => eligibleSets[hid].has(m))) common.add(m);
  }

  const extrasByHost: Record<string, string[]> = {};
  for (const hid of hostIds) {
    const extras = [...eligibleSets[hid]]
      .filter((m) => !common.has(m))
      .sort(byNaturalOrder);
    if (extras.length > 0) extrasByHost[hid] = extras;
  }

  return {
    common: [...common].sort(byNaturalOrder),
    extrasByHost,
    eligibleByHost,
  };
}

function mountUnderRoot(mount: string, root: string): boolean {
  const trimmedMount = trimSlash(mount);
  const trimmedRoot = trimSlash(root);
  return trimmedMount === trimmedRoot || trimmedMount.startsWith(trimmedRoot + "/");
}

function hasSpecificRoot(root: string): boolean {
  const trimmedRoot = trimSlash(root);
  return trimmedRoot !== "" && trimmedRoot !== "/";
}

function trimSlash(value: string): string {
  const trimmed = value.trim();
  if (trimmed === "/" || trimmed === "") return trimmed;
  return trimmed.replace(/\/+$/, "");
}

// Natural sort so /data/disk2 < /data/disk10 (avoids the default
// lexicographic "/data/disk10" < "/data/disk2" surprise).
function byNaturalOrder(a: string, b: string): number {
  const ax = a.split(/(\d+)/).map((x) => (/^\d+$/.test(x) ? parseInt(x, 10) : x));
  const bx = b.split(/(\d+)/).map((x) => (/^\d+$/.test(x) ? parseInt(x, 10) : x));
  for (let i = 0; i < Math.min(ax.length, bx.length); i++) {
    if (ax[i] === bx[i]) continue;
    if (typeof ax[i] === "number" && typeof bx[i] === "number") {
      return (ax[i] as number) - (bx[i] as number);
    }
    return String(ax[i]) < String(bx[i]) ? -1 : 1;
  }
  return ax.length - bx.length;
}
