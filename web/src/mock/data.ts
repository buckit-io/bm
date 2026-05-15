// Mock domain data driving the prototype. Everything is in-memory and
// matches the domain shapes the backend will eventually serve. The
// types defined here become the contract derived in UI-P7.

export type ClusterStatus = "active" | "draft" | "migrating" | "failed";
export type Health = "healthy" | "degraded" | "critical" | "unknown";
export type TaskState =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled";

// HealthSummary is the rolled-up signal data the manager uses to compute
// `Cluster.health`. The backend computes both fields server-side and returns
// them together on every cluster list/detail response, so the UI never has
// to fan out per-cluster fetches to render a status pill.
export interface HealthSummary {
  nodes: { online: number; degraded: number; offline: number; total: number };
  drives: { ready: number; healing: number; failed: number; total: number };
  // Names of in-flight long-running ops on this cluster (cutover, rolling
  // restart, deploy, healing job, etc.). Presence flags the cluster as
  // "Degraded" even when nodes and drives all read clean.
  activeOps: string[];
}

export interface Cluster {
  id: string;
  name: string;
  description?: string;
  intendedUse: "production" | "staging" | "dev";
  version: string;
  status: ClusterStatus;
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

export interface Task {
  id: string;
  name: string;
  kind:
    | "discovery"
    | "preflight"
    | "deploy"
    | "snapshot"
    | "cutover"
    | "verify"
    | "rollback"
    | "finalize"
    | "rolling_restart"
    | "health_probe";
  clusterId?: string;
  clusterName?: string;
  state: TaskState;
  triggeredBy: string;
  startedAt: string;
  durationSec?: number;
  steps: TaskStep[];
  failureNote?: string;
  retryable?: boolean;
}

export interface TaskStep {
  id: string;
  name: string;
  state: TaskState;
  durationSec?: number;
  children?: TaskStep[];
}

export interface AuditEvent {
  id: string;
  at: string;
  actor: string;
  action: string;
  target?: string;
}

// HistoryEntry is the row backing the History tab. Two flavours, both
// stored in the same bucket on the backend: literal CLI invocations and
// human-readable UI action descriptions. See ui-architecture.md for the
// vocabulary.
export interface HistoryEntry {
  id: string;
  at: string;
  kind: "cli" | "ui_action";
  display: string;
  target?: string;
  status: "succeeded" | "failed" | "running";
  durationSec?: number;
  taskId?: string;
  exitCode?: number;
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
function makeNodes(
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
    intendedUse: "production",
    version: "v1.0.0",
    status: "active",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(14),
    unreachableSince: null,
    sshConfigured: true,
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
    intendedUse: "staging",
    version: "v1.0.0",
    status: "active",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(26),
    unreachableSince: null,
    sshConfigured: true,
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
    id: "prod-west-new",
    name: "prod-west-new",
    intendedUse: "production",
    version: "—",
    status: "draft",
    health: "unknown",
    healthSummary: null,
    lastFetchedAt: null,
    unreachableSince: null,
    sshConfigured: true,
    nodeCount: 0,
    poolCount: 0,
    driveCount: 0,
    parity: 0,
    usableBytes: 0,
    rawBytes: 0,
    usedBytes: 0,
    lastActivityAt: minutesAgo(5),
    createdAt: minutesAgo(20),
  },
  {
    id: "legacy-migrate",
    name: "legacy-migrate",
    intendedUse: "production",
    version: "v1.0.0",
    status: "migrating",
    health: "unknown", // overwritten by computeAndApplyHealth below
    healthSummary: null,
    lastFetchedAt: secondsAgo(8),
    unreachableSince: null,
    sshConfigured: true,
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
];

export const nodesByCluster: Record<string, Node[]> = {
  "prod-east": makeNodes("prod-east", [6, 4, 4]),
  staging: makeNodes("staging", [4]),
  "prod-west-new": [],
  "legacy-migrate": makeNodes("legacy-migrate", [8], "legacy-node"),
};

function step(
  id: string,
  name: string,
  state: TaskState,
  durationSec?: number,
  children?: TaskStep[],
): TaskStep {
  return { id, name, state, durationSec, children };
}

export const tasks: Task[] = [
  {
    id: "task-migrate-legacy-1",
    name: "Migrate legacy-east",
    kind: "cutover",
    clusterId: "legacy-migrate",
    clusterName: "legacy-migrate",
    state: "running",
    triggeredBy: "admin",
    startedAt: minutesAgo(3),
    steps: [
      step("snapshot", "Snapshot MinIO state", "succeeded", 18),
      step("preflight", "Preflight", "succeeded", 22),
      step("cutover", "Cutover (3/8 nodes)", "running", 152, [
        step("c-node1", "node1", "succeeded", 58),
        step("c-node2", "node2", "succeeded", 61),
        step("c-node3", "node3", "running", 33),
        step("c-node4", "node4", "pending"),
        step("c-node5", "node5", "pending"),
        step("c-node6", "node6", "pending"),
        step("c-node7", "node7", "pending"),
        step("c-node8", "node8", "pending"),
      ]),
      step("verify", "Verify", "pending"),
      step("finalize", "Finalize", "pending"),
    ],
    retryable: false,
  },
  {
    id: "task-probe-prod-1",
    name: "Health probe",
    kind: "health_probe",
    clusterId: "prod-east",
    clusterName: "prod-east",
    state: "running",
    triggeredBy: "system",
    startedAt: minutesAgo(0),
    steps: [step("probe", "Probe all nodes", "running", 20)],
  },
  {
    id: "task-restart-prod-1",
    name: "Rolling restart",
    kind: "rolling_restart",
    clusterId: "prod-east",
    clusterName: "prod-east",
    state: "succeeded",
    triggeredBy: "admin",
    startedAt: hoursAgo(2),
    durationSec: 252,
    steps: [
      step("plan", "Plan order", "succeeded", 2),
      step("restart", "Rolling restart (6 nodes)", "succeeded", 240),
    ],
  },
  {
    id: "task-deploy-staging-1",
    name: "Deploy v1.0.0",
    kind: "deploy",
    clusterId: "staging",
    clusterName: "staging",
    state: "failed",
    triggeredBy: "admin",
    startedAt: daysAgo(1),
    durationSec: 720,
    steps: [
      step("fetch", "Fetch package", "succeeded", 12),
      step("install", "Install on nodes", "failed", 708, [
        step("i-node1", "node1", "succeeded", 110),
        step("i-node2", "node2", "succeeded", 112),
        step("i-node3", "node3", "failed", 600),
        step("i-node4", "node4", "canceled"),
      ]),
    ],
    failureNote: "node3: SSH timeout",
    retryable: true,
  },
  {
    id: "task-discover-1",
    name: "Discover nodes",
    kind: "discovery",
    clusterId: "prod-west-new",
    clusterName: "prod-west-new",
    state: "canceled",
    triggeredBy: "admin",
    startedAt: daysAgo(1),
    durationSec: 45,
    steps: [step("probe", "SSH probe", "canceled", 45)],
  },
];

export const auditEvents: AuditEvent[] = [
  { id: "a1", at: minutesAgo(0), actor: "admin", action: "task.start", target: "task-migrate-legacy-1" },
  { id: "a2", at: hoursAgo(2), actor: "admin", action: "cluster.restart", target: "prod-east" },
  { id: "a3", at: daysAgo(1), actor: "admin", action: "task.start", target: "task-deploy-staging-1" },
  { id: "a4", at: daysAgo(3), actor: "admin", action: "cluster.deploy", target: "prod-east" },
];

// History is the user-facing log surfaced in the History tab. Phase 1
// records UI action descriptions; future phases will add literal CLI
// command rows from terminal invocations.
export const history: HistoryEntry[] = [
  {
    id: "h1",
    at: hoursAgo(2),
    kind: "ui_action",
    display: "Rolling restart on prod-east",
    target: "prod-east",
    status: "succeeded",
    durationSec: 252,
    taskId: "task-restart-prod-1",
  },
  {
    id: "h2",
    at: hoursAgo(5),
    kind: "cli",
    display: "bm cluster ls",
    status: "succeeded",
    exitCode: 0,
  },
  {
    id: "h3",
    at: daysAgo(1),
    kind: "ui_action",
    display: "Deployed cluster staging v1.0.0",
    target: "staging",
    status: "failed",
    durationSec: 720,
    taskId: "task-deploy-staging-1",
  },
  {
    id: "h4",
    at: daysAgo(2),
    kind: "ui_action",
    display: "Rotated SSH credentials for prod-east",
    target: "prod-east",
    status: "succeeded",
    durationSec: 3,
  },
  {
    id: "h5",
    at: daysAgo(3),
    kind: "ui_action",
    display: "Deployed cluster prod-east v1.0.0",
    target: "prod-east",
    status: "succeeded",
    durationSec: 574,
    taskId: "task-deploy-prod-east-initial",
  },
];

// ---- health computation ----

// Kinds of tasks that count as "active operations" affecting cluster health.
// health_probe is excluded — it's just monitoring noise.
const ACTIVE_OP_KINDS: Task["kind"][] = [
  "deploy",
  "cutover",
  "rolling_restart",
  "rollback",
  "finalize",
];

export function computeHealthSummary(
  cluster: Cluster,
  nodes: Node[],
  allTasks: Task[],
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

  const activeOps = allTasks
    .filter(
      (t) =>
        t.state === "running" &&
        t.clusterId === cluster.id &&
        ACTIVE_OP_KINDS.includes(t.kind),
    )
    .map((t) => t.name);

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
    activeOps,
  };
}

// computeHealth rolls a summary into a single status string. The rule:
//   - draft clusters or clusters we can't reach are "unknown"
//   - "critical" means parity is exhausted (cluster cannot serve writes):
//       drive failures exceed the per-set parity tolerance, or node
//       failures exceed parity
//   - "degraded" means cluster still serves traffic but is not clean:
//       any node not fully online, any drive not ready, or any
//       long-running op in flight
//   - everything else is "healthy"
export function computeHealth(cluster: Cluster, s: HealthSummary): Health {
  if (cluster.status === "draft") return "unknown";
  if (s.nodes.total === 0) return "unknown";
  if (s.drives.failed > cluster.parity) return "critical";
  if (s.nodes.offline > cluster.parity) return "critical";
  if (s.nodes.offline > 0 || s.nodes.degraded > 0) return "degraded";
  if (s.drives.healing > 0 || s.drives.failed > 0) return "degraded";
  if (s.activeOps.length > 0) return "degraded";
  return "healthy";
}

// One-shot init: populate every cluster's health + healthSummary from the
// fixture data. The real backend does this server-side on every probe tick;
// here we do it once on module load so the mock API returns ready-to-render
// records.
clusters.forEach((c) => {
  const nodes = nodesByCluster[c.id] ?? [];
  c.healthSummary = computeHealthSummary(c, nodes, tasks);
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
