// Mock domain data driving the prototype. Everything is in-memory and
// matches the domain shapes the backend will eventually serve. The
// types defined here become the contract derived in UI-P7.

export type Health = "healthy" | "degraded" | "critical" | "unknown";

// HealthSummary is the rolled-up signal data the manager uses to compute
// `Cluster.health`. The backend computes both fields server-side and returns
// them together on every cluster list/detail response, so the UI never has
// to fan out per-cluster fetches to render a status pill.
export interface HealthSummary {
  nodes: { online: number; degraded: number; offline: number; total: number };
  drives: { ready: number; healing: number; failed: number; total: number };
}

// Which server software is running on the cluster's nodes.
//   "buckit" — Buckit (active or draft); the default for clusters bm
//              deploys or imports from a healthy Buckit endpoint.
//   "minio"  — MinIO Community Edition; imported clusters that haven't
//              been migrated yet. Surfaces the "Migrate to Buckit"
//              button on the cluster detail page.
export type ClusterEngine = "buckit" | "minio";

export interface Cluster {
  id: string;
  name: string;
  description?: string;
  engine: ClusterEngine;
  version: string;
  health: Health;
  healthSummary: HealthSummary | null;
  nodeCount: number;
  poolCount: number;
  driveCount: number;
  parity: number;
  usableBytes: number;
  rawBytes: number;
  usedBytes: number;
  // Wall-clock time of the manager's last successful probe for this cluster.
  // null for drafts and clusters that have never been reached.
  lastFetchedAt: string | null;
  // Set when two consecutive probes have failed. While set, `health` is
  // forced to "unknown" and cached counts are shown greyed in the UI.
  unreachableSince: string | null;
  // Whether SSH credentials have been set up for this cluster. Host-level
  // actions (reboot, redeploy, restart unit) are only enabled when true.
  sshConfigured: boolean;
  // Whether the cluster is currently in `mc admin service freeze` state.
  // Reads continue; writes are blocked at the S3 API.
  apiFrozen: boolean;
  lastActivityAt: string;
  createdAt: string;
  migratedFrom?: { product: "minio"; version: string; finalizedAt: string };
}

export interface Node {
  id: string;
  clusterId: string;
  hostname: string;
  sshPort: number;
  label?: string;
  state: "online" | "offline" | "degraded" | "unknown";
  version?: string;
  uptimeSec?: number;
  os?: string;
  // Host facts come from the admin API's HostInfo block (post-Buckit
  // extension; see ui-architecture.md). Empty when the operator is
  // pointing bm at an older Buckit build that doesn't return the block.
  kernel?: string;
  cpuModel?: string;
  cpuCores?: number;     // physical cores
  cpuThreads?: number;   // logical (SMT-aware)
  cpuMaxMHz?: number;
  ramBytes?: number;     // total host RAM
  nic?: { iface: string; speedMbps: number };
  drives: Drive[];
  existingService?: "buckit" | "minio";
  // Which pool inside the cluster this node belongs to (1-indexed).
  pool: number;
  // Connectivity probes, populated on each fetch. The four are independent:
  //   pingable          — TCP/ICMP reachable
  //   sshable           — SSH handshake + auth succeed
  //   apiAccessible     — S3 API responds at :9000/minio/health/live
  //   consoleAccessible — Buckit web console on :9001 reachable
  pingable: boolean;
  sshable: boolean;
  apiAccessible: boolean;
  consoleAccessible: boolean;
}

export interface Drive {
  mount: string;
  device: string;
  sizeBytes: number;
  usedBytes: number;
  state: "ready" | "healing" | "failed";
  healingPct?: number;
  isBoot?: boolean;
}

export interface AuditEvent {
  id: string;
  at: string;
  actor: string;
  action: string;
  target?: string;
}

// Per-host outcome for orchestrated ops. Persisted as part of an
// OperationResult so the History tab's View modal can show the same
// per-host breakdown the live OperationModal showed.
export type HostOpState = "pending" | "running" | "succeeded" | "failed";

export interface HostOpStatus {
  hostId: string;
  hostname: string;
  state: HostOpState;
  detail?: string;
  durationSec?: number;
}

// Snapshot of an operation's terminal state, persisted on the history
// row. Renders into the History tab's View modal. Live-only fields
// (events stream, progress counters) are intentionally omitted.
export interface OperationResult {
  state: "succeeded" | "failed" | "canceled";
  // Signal ops one-liner, e.g. "S3 API frozen."
  detail?: string;
  // Admin API result table for ops that have one (restart/stop/heal).
  summary?: { label: string; value: string }[];
  // Per-host outcome for SSH-orchestrated ops.
  hostStatuses?: HostOpStatus[];
  failureNote?: string;
}

// HistoryEntry is the row backing the History tab. Every entry is a
// mutable action initiated from the web UI — cluster Actions, bulk-
// host actions, or per-node Actions. Read-only fetches and CLI
// invocations are not recorded.
export interface HistoryEntry {
  id: string;
  at: string;
  // Operation identifier — matches the OpKind union in mock/api.ts.
  // The page renders the human-friendly label from `opLabel`.
  opKind: string;
  opLabel: string;
  // Target cluster. Always present — every mutable op has a cluster.
  clusterId: string;
  clusterName: string;
  // Host scope. Undefined for cluster-wide ops (Restart cluster,
  // Rolling restart, etc.); set when the op was scoped to a subset
  // of hosts via the bulk-selection bar or per-node Actions menu.
  hostScope?: {
    hostnames: string[];
    count: number;
  };
  status: "succeeded" | "failed" | "running" | "canceled";
  durationSec?: number;
  failureNote?: string;
  // Persisted snapshot of the op's terminal state. Undefined while
  // status is "running". Rendered by the History page's View modal.
  result?: OperationResult;
}

// ---- fixture data ----

const now = new Date();
const minutesAgo = (m: number) =>
  new Date(now.getTime() - m * 60_000).toISOString();
const hoursAgo = (h: number) => minutesAgo(h * 60);
const daysAgo = (d: number) => hoursAgo(d * 24);
const secondsAgo = (s: number) =>
  new Date(now.getTime() - s * 1000).toISOString();

const TiB = 1024 ** 4;
const GiB = 1024 ** 3;

function makeDrives(count: number, sizeTiB: number, usedPct = 0.5): Drive[] {
  const drives: Drive[] = [];
  for (let i = 0; i < count; i++) {
    drives.push({
      mount: `/data/disk${i + 1}`,
      device: `/dev/sd${String.fromCharCode(97 + i)}`,
      sizeBytes: sizeTiB * TiB,
      usedBytes: sizeTiB * TiB * usedPct,
      state: "ready",
    });
  }
  drives.push({
    mount: "/",
    device: `/dev/sd${String.fromCharCode(97 + count)}`,
    sizeBytes: 256 * GiB,
    usedBytes: 50 * GiB,
    state: "ready",
    isBoot: true,
  });
  return drives;
}

// makeNodes builds the node fixture for a cluster. `poolSizes` is the
// per-pool node count; pass `[6, 4]` for 10 nodes across two pools, or
// `[8]` for a single 8-node pool. A global node index is used for
// hostnames so node1..nodeN are unique across pools.
export function makeNodes(
  clusterId: string,
  poolSizes: number[],
  base = "node",
): Node[] {
  const nodes: Node[] = [];
  let i = 0;
  for (let p = 0; p < poolSizes.length; p++) {
    const pool = p + 1;
    for (let k = 0; k < poolSizes[p]; k++) {
      i++;
      const drives = makeDrives(12, 16);
      if (clusterId === "prod-east" && i === 5) {
        drives[7].state = "healing";
        drives[7].healingPct = 37;
      }

      // Default: every probe green. A few interesting cases per cluster
      // demonstrate partial connectivity failures the operator needs to
      // see in the list view.
      let pingable = true;
      let sshable = true;
      let apiAccessible = true;
      let consoleAccessible = true;
      let state: Node["state"] =
        clusterId === "prod-east" && i === 5 ? "degraded" : "online";
      let kernel: string | undefined = "6.8.0-31-generic";

      // prod-east pool 2: one node on an older kernel (rolling reboot
      // in progress) — exercises the Kernel filter + sort.
      if (clusterId === "prod-east" && i === 9) {
        kernel = "6.5.0-44-generic";
      }

      if (clusterId === "legacy-migrate" && i === 3) {
        // Mid-cutover: process is between binaries; admin API is briefly down.
        apiAccessible = false;
        consoleAccessible = false;
        state = "degraded";
      }
      if (clusterId === "legacy-migrate" && i === 7) {
        // SSH key was rotated upstream; we're still using the old one.
        // Kernel still shows because admin API returns it independently
        // of SSH.
        sshable = false;
      }

      nodes.push({
        id: `${clusterId}-node${i}`,
        clusterId,
        hostname: `${base}${i}.example.com`,
        sshPort: 22,
        state,
        version: "v1.0.0",
        uptimeSec: 3 * 86400 + i * 17,
        os: "Ubuntu 24.04 LTS",
        kernel,
        cpuModel: "Intel Xeon Gold 6248R",
        cpuCores: 24,
        cpuThreads: 48,
        cpuMaxMHz: 3000,
        ramBytes: 256 * GiB,
        nic: { iface: "eno1", speedMbps: 25_000 },
        drives,
        pool,
        pingable,
        sshable,
        apiAccessible,
        consoleAccessible,
      });
    }
  }
  return nodes;
}

export const clusters: Cluster[] = [
  {
    id: "prod-east",
    name: "prod-east",
    description: "Customer-facing production",
    engine: "buckit",
    version: "v1.0.0",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(14),
    unreachableSince: null,
    sshConfigured: true,
    apiFrozen: false,
    nodeCount: 14,
    poolCount: 3,
    driveCount: 168,
    parity: 4,
    usableBytes: 2016 * TiB,
    rawBytes: 2688 * TiB,
    usedBytes: 980 * TiB,
    lastActivityAt: minutesAgo(12),
    createdAt: daysAgo(60),
  },
  {
    id: "staging",
    name: "staging",
    engine: "buckit",
    version: "v1.0.0",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(26),
    unreachableSince: null,
    sshConfigured: true,
    apiFrozen: false,
    nodeCount: 4,
    poolCount: 1,
    driveCount: 48,
    parity: 4,
    usableBytes: 576 * TiB,
    rawBytes: 768 * TiB,
    usedBytes: 87 * TiB,
    lastActivityAt: hoursAgo(2),
    createdAt: daysAgo(30),
  },
  {
    id: "legacy-migrate",
    name: "legacy-migrate",
    engine: "buckit",
    version: "v1.0.0",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(8),
    unreachableSince: null,
    sshConfigured: true,
    apiFrozen: false,
    nodeCount: 8,
    poolCount: 1,
    driveCount: 96,
    parity: 4,
    usableBytes: 1152 * TiB,
    rawBytes: 1536 * TiB,
    usedBytes: 920 * TiB,
    lastActivityAt: minutesAgo(0),
    createdAt: daysAgo(2),
    migratedFrom: {
      product: "minio",
      version: "v2024-12-01",
      finalizedAt: minutesAgo(0),
    },
  },
  {
    id: "legacy-east",
    name: "legacy-east",
    description: "Imported MinIO cluster, awaiting migration",
    engine: "minio",
    version: "RELEASE.2024-10-13T13-34-11Z",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(45),
    unreachableSince: null,
    sshConfigured: false,
    apiFrozen: false,
    nodeCount: 4,
    poolCount: 1,
    driveCount: 16,
    parity: 4,
    usableBytes: 192 * TiB,
    rawBytes: 256 * TiB,
    usedBytes: 72 * TiB,
    lastActivityAt: minutesAgo(8),
    createdAt: daysAgo(1),
  },
];

export const nodesByCluster: Record<string, Node[]> = {
  "prod-east": makeNodes("prod-east", [6, 4, 4]),
  staging: makeNodes("staging", [4]),
  "legacy-migrate": makeNodes("legacy-migrate", [8], "legacy-node"),
  "legacy-east": makeNodes("legacy-east", [4], "legacy-east"),
};

export const auditEvents: AuditEvent[] = [
  { id: "a1", at: minutesAgo(0), actor: "admin", action: "cluster.migrate", target: "legacy-migrate" },
  { id: "a2", at: hoursAgo(2), actor: "admin", action: "cluster.restart", target: "prod-east" },
  { id: "a3", at: daysAgo(1), actor: "admin", action: "cluster.deploy", target: "staging" },
  { id: "a4", at: daysAgo(3), actor: "admin", action: "cluster.deploy", target: "prod-east" },
];

// History is the user-facing log surfaced in the History tab. Every
// row is a mutable action initiated from the web UI.
export const history: HistoryEntry[] = [
  {
    id: "h1",
    at: hoursAgo(2),
    opKind: "rolling_restart",
    opLabel: "Rolling restart",
    clusterId: "prod-east",
    clusterName: "prod-east",
    status: "succeeded",
    durationSec: 252,
    result: {
      state: "succeeded",
      hostStatuses: [
        { hostId: "prod-east-node1", hostname: "prod-east-node1", state: "succeeded", durationSec: 19 },
        { hostId: "prod-east-node2", hostname: "prod-east-node2", state: "succeeded", durationSec: 18 },
        { hostId: "prod-east-node3", hostname: "prod-east-node3", state: "succeeded", durationSec: 21 },
        { hostId: "prod-east-node4", hostname: "prod-east-node4", state: "succeeded", durationSec: 17 },
      ],
    },
  },
  {
    id: "h2",
    at: hoursAgo(5),
    opKind: "systemctl_restart",
    opLabel: "Systemctl restart service",
    clusterId: "prod-east",
    clusterName: "prod-east",
    hostScope: {
      hostnames: ["prod-east-node3", "prod-east-node4"],
      count: 2,
    },
    status: "succeeded",
    durationSec: 18,
    result: {
      state: "succeeded",
      hostStatuses: [
        { hostId: "prod-east-node3", hostname: "prod-east-node3", state: "succeeded", durationSec: 9 },
        { hostId: "prod-east-node4", hostname: "prod-east-node4", state: "succeeded", durationSec: 9 },
      ],
    },
  },
  {
    id: "h3",
    at: daysAgo(1),
    opKind: "deploy_cluster",
    opLabel: "Deploy cluster staging v1.0.0",
    clusterId: "staging",
    clusterName: "staging",
    status: "failed",
    durationSec: 720,
    failureNote: "node3: SSH timeout during package install",
    result: {
      state: "failed",
      failureNote: "node3: SSH timeout during package install",
      hostStatuses: [
        { hostId: "staging-node1", hostname: "staging-node1", state: "succeeded", durationSec: 110 },
        { hostId: "staging-node2", hostname: "staging-node2", state: "succeeded", durationSec: 112 },
        { hostId: "staging-node3", hostname: "staging-node3", state: "failed", durationSec: 600 },
        { hostId: "staging-node4", hostname: "staging-node4", state: "pending" },
      ],
    },
  },
  {
    id: "h4",
    at: daysAgo(2),
    opKind: "rotate_root_creds",
    opLabel: "Rotate root credentials",
    clusterId: "prod-east",
    clusterName: "prod-east",
    status: "succeeded",
    durationSec: 47,
    result: {
      state: "succeeded",
      detail: "Root credentials rotated and the cluster restarted with the new key.",
    },
  },
  {
    id: "h5",
    at: daysAgo(2),
    opKind: "reboot_host",
    opLabel: "Reboot host",
    clusterId: "prod-east",
    clusterName: "prod-east",
    hostScope: {
      hostnames: ["prod-east-node1"],
      count: 1,
    },
    status: "succeeded",
    durationSec: 64,
    result: {
      state: "succeeded",
      hostStatuses: [
        { hostId: "prod-east-node1", hostname: "prod-east-node1", state: "succeeded", durationSec: 64 },
      ],
    },
  },
  {
    id: "h6",
    at: daysAgo(3),
    opKind: "deploy_cluster",
    opLabel: "Deploy cluster prod-east v1.0.0",
    clusterId: "prod-east",
    clusterName: "prod-east",
    status: "succeeded",
    durationSec: 574,
    result: {
      state: "succeeded",
      detail: "Cluster deployed across 4 hosts (pool 1).",
    },
  },
];

// ---- health computation ----

export function computeHealthSummary(
  _cluster: Cluster,
  nodes: Node[],
): HealthSummary {
  let nOnline = 0;
  let nDegraded = 0;
  let nOffline = 0;
  for (const n of nodes) {
    if (n.state === "online") nOnline++;
    else if (n.state === "degraded") nDegraded++;
    else nOffline++; // "offline" and "unknown" both count as not-serving
  }

  let dReady = 0;
  let dHealing = 0;
  let dFailed = 0;
  for (const n of nodes) {
    for (const d of n.drives) {
      if (d.isBoot) continue;
      if (d.state === "ready") dReady++;
      else if (d.state === "healing") dHealing++;
      else dFailed++;
    }
  }

  return {
    nodes: {
      online: nOnline,
      degraded: nDegraded,
      offline: nOffline,
      total: nodes.length,
    },
    drives: {
      ready: dReady,
      healing: dHealing,
      failed: dFailed,
      total: dReady + dHealing + dFailed,
    },
  };
}

// computeHealth rolls a summary into a single status string. The rule:
//   - clusters we can't reach are "unknown"
//   - "critical" means parity is exhausted (cluster cannot serve writes):
//       drive failures exceed the per-set parity tolerance, or node
//       failures exceed parity
//   - "degraded" means cluster still serves traffic but is not clean:
//       any node not fully online, or any drive not ready
//   - everything else is "healthy"
export function computeHealth(cluster: Cluster, s: HealthSummary): Health {
  if (s.nodes.total === 0) return "unknown";
  if (s.drives.failed > cluster.parity) return "critical";
  if (s.nodes.offline > cluster.parity) return "critical";
  if (s.nodes.offline > 0 || s.nodes.degraded > 0) return "degraded";
  if (s.drives.healing > 0 || s.drives.failed > 0) return "degraded";
  return "healthy";
}

// One-shot init: populate every cluster's health + healthSummary from the
// fixture data. The real backend does this server-side on every probe tick;
// here we do it once on module load so the mock API returns ready-to-render
// records.
clusters.forEach((c) => {
  const nodes = nodesByCluster[c.id] ?? [];
  c.healthSummary = computeHealthSummary(c, nodes);
  c.health = computeHealth(c, c.healthSummary);
});

// ---- helpers ----

export function formatBytes(b: number): string {
  if (b === 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let v = b;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 10 || i <= 1 ? 0 : 1)} ${units[i]}`;
}

export function formatRelative(iso: string): string {
  const t = new Date(iso).getTime();
  const diff = Math.max(0, Date.now() - t);
  const sec = Math.floor(diff / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export function formatDuration(sec?: number): string {
  if (sec === undefined) return "—";
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  if (m < 60) return `${m}m${s ? ` ${s.toString().padStart(2, "0")}s` : ""}`;
  const h = Math.floor(m / 60);
  return `${h}h ${(m % 60).toString().padStart(2, "0")}m`;
}
