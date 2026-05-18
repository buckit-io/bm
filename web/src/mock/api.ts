// Mock API client. Returns Promises with simulated latency. Mirrors
// what the real backend endpoints will return, so swapping the real
// client in later is a one-file change.

import {
  auditEvents,
  Cluster,
  ClusterEngine,
  clusters,
  computeHealth,
  computeHealthSummary,
  history,
  HistoryEntry,
  makeNodes,
  Node,
  nodesByCluster,
  Task,
  tasks,
} from "./data";

const LATENCY_MS = 80;

function delay<T>(value: T, ms = LATENCY_MS): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms));
}

// ---- session ----

let session: { user: string; sessionId: string } | null = null;

export type LoginResult = { ok: true } | { ok: false; error: string };

export function login(username: string, password: string): Promise<LoginResult> {
  if (username === "admin" && password === "admin") {
    session = { user: "admin", sessionId: "mock-session" };
    return delay({ ok: true });
  }
  return delay({ ok: false, error: "Invalid username or password." });
}

export function logout() {
  session = null;
  return delay({ ok: true });
}

export function me() {
  return delay(session ? { user: session.user } : null);
}

// ---- custom artifact validation ----
//
// Real backend: HEAD the URL, parse Content-Length, look for a
// `<url>.sha256` companion file (and verify on download). The mock
// fabricates a result after ~500ms based on URL heuristics so the
// Basics step can exercise the valid / warn / error branches.

export interface ArtifactCheck {
  state: "valid" | "warn" | "error";
  message?: string;
  sizeBytes?: number;
  sha256?: string;
}

export async function validateCustomArtifact(
  rawUrl: string,
): Promise<ArtifactCheck> {
  await new Promise((r) => setTimeout(r, 500));
  const url = rawUrl.trim();
  if (!url) {
    return { state: "error", message: "URL is required." };
  }
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return { state: "error", message: "Not a valid URL." };
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return {
      state: "error",
      message: `Unsupported scheme: ${parsed.protocol}`,
    };
  }
  // Mock unreachable cases for demo purposes.
  if (/(broken|404|unreachable)/i.test(url)) {
    return { state: "error", message: "Could not reach URL (HTTP 404)." };
  }
  // Mock sha256 detection: only assume a companion file is published for
  // .rpm / .deb / .tar.gz artifacts. Everything else is reachable but
  // unverifiable.
  const hasCompanion = /\.(rpm|deb|tar\.gz)$/i.test(parsed.pathname);
  const sizeBytes = 87 * 1024 * 1024;
  if (hasCompanion) {
    return {
      state: "valid",
      message: "Reachable and sha256 verified.",
      sizeBytes,
      sha256: "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
    };
  }
  return {
    state: "warn",
    message: "Reachable, but no sha256 published — integrity not verified.",
    sizeBytes,
  };
}

// ---- clusters ----

export function listClusters(): Promise<Cluster[]> {
  return delay(clusters);
}

export function getCluster(id: string): Promise<Cluster | null> {
  return delay(clusters.find((c) => c.id === id) ?? null);
}

// Cluster import is a two-step flow:
//   1. discoverCluster(params, onProgress) — non-persisting. Calls
//      /minio/admin/v3/info on the real backend, streams progress lines
//      via the callback, returns an ImportCandidate the UI can preview.
//   2. commitImport(candidate, name) — persists the candidate to bbolt
//      under the operator-chosen name and returns the final cluster id.
// The mock fabricates plausible data after staged delays.

export type ImportError =
  | { kind: "unreachable"; message: string }
  | { kind: "auth"; message: string }
  | { kind: "invalid_url"; message: string };

export type DiscoveryResult =
  | { ok: true; candidate: ImportCandidate }
  | { ok: false; error: ImportError };

export interface ImportCandidate {
  // Default name suggestion (URL host slug). The modal lets the
  // operator override before commit.
  suggestedName: string;
  url: string;
  username: string;
  password: string;
  engine: ClusterEngine;
  version: string;
  // Pre-built node fixture, not yet keyed into nodesByCluster.
  nodes: Node[];
  poolCount: number;
  driveCount: number;
  rawBytes: number;
  usedBytes: number;
  usableBytes: number;
  parity: number;
}

export interface DiscoveryProgress {
  ts: string;
  level: "info" | "ok" | "error";
  text: string;
}

let importSeq = 1;

const PROGRESS_STEP_MS = 220;

function progressLine(
  text: string,
  level: DiscoveryProgress["level"] = "info",
): DiscoveryProgress {
  return { ts: new Date().toISOString(), level, text };
}

export async function discoverCluster(
  params: { url: string; username: string; password: string },
  onProgress: (line: DiscoveryProgress) => void,
): Promise<DiscoveryResult> {
  const url = params.url.trim();
  if (!url) {
    onProgress(progressLine("URL is required", "error"));
    return { ok: false, error: { kind: "invalid_url", message: "URL is required." } };
  }
  let host: string;
  try {
    host = new URL(url).hostname || "imported";
  } catch {
    onProgress(progressLine(`Could not parse URL: ${url}`, "error"));
    return { ok: false, error: { kind: "invalid_url", message: "Could not parse URL." } };
  }
  if (!params.username.trim() || !params.password.trim()) {
    onProgress(progressLine("Access key and secret key are required", "error"));
    return {
      ok: false,
      error: { kind: "auth", message: "Access key and secret key are required." },
    };
  }

  const step = (ms = PROGRESS_STEP_MS) =>
    new Promise((r) => setTimeout(r, ms));

  // Engine detection heuristic for the prototype — real backend reads
  // the version string returned by /minio/admin/v3/info.
  const looksMinio = /minio/i.test(url);
  const engine: ClusterEngine = looksMinio ? "minio" : "buckit";
  const version = looksMinio ? "RELEASE.2024-10-13T13-34-11Z" : "v1.0.0";

  // Stage 1: connect + auth + admin info.
  onProgress(progressLine(`Connecting to ${url}`));
  await step();
  onProgress(progressLine("TCP handshake successful", "ok"));
  await step();
  onProgress(progressLine(`Authenticating as ${params.username}`));
  await step();
  onProgress(progressLine("Authentication accepted", "ok"));
  await step();
  onProgress(progressLine("GET /minio/admin/v3/info"));
  await step();
  onProgress(
    progressLine(
      `Server reports ${engine === "minio" ? "MinIO" : "Buckit"} ${version}`,
      "ok",
    ),
  );
  await step();

  // Stage 2: fabricate nodes + per-node detail lines.
  const tempId = `__import-${Date.now()}`;
  const nodes = makeNodes(tempId, [4], host.split(".")[0] || "node");
  onProgress(
    progressLine(`Found ${nodes.length} nodes across 1 pool`, "ok"),
  );
  await step();
  for (const node of nodes) {
    onProgress(progressLine(`Discovering ${node.hostname}`));
    await step(140);
    const dataDrives = node.drives.filter((d) => !d.isBoot);
    onProgress(
      progressLine(
        `  └─ ${dataDrives.length} data drives, ${
          dataDrives[0] ? dataDrives[0].sizeBytes / 1024 ** 4 : 0
        } TiB each`,
      ),
    );
    await step(80);
    onProgress(
      progressLine(
        `  └─ Kernel ${node.kernel ?? "unknown"}, ${
          node.ramBytes ? Math.round(node.ramBytes / 1024 ** 3) : "?"
        } GB RAM`,
      ),
    );
    await step(80);
  }

  // Stage 3: derived totals.
  const driveCount = nodes.reduce(
    (sum, n) => sum + n.drives.filter((d) => !d.isBoot).length,
    0,
  );
  const rawBytes = nodes.reduce(
    (sum, n) =>
      sum + n.drives.filter((d) => !d.isBoot).reduce((s, d) => s + d.sizeBytes, 0),
    0,
  );
  const usedBytes = Math.floor(rawBytes * 0.18);
  const parity = 4;
  const usableBytes = Math.floor(
    rawBytes * ((nodes.length * 4 - parity) / (nodes.length * 4)),
  );

  onProgress(progressLine("Computing topology", "info"));
  await step();
  onProgress(progressLine("Discovery complete", "ok"));

  return {
    ok: true,
    candidate: {
      suggestedName: slugifyHost(host),
      url,
      username: params.username,
      password: params.password,
      engine,
      version,
      nodes,
      poolCount: 1,
      driveCount,
      rawBytes,
      usedBytes,
      usableBytes,
      parity,
    },
  };
}

export async function commitImport(
  candidate: ImportCandidate,
  chosenName: string,
): Promise<{ clusterId: string }> {
  await new Promise((r) => setTimeout(r, 250));
  const name = chosenName.trim();
  const id = makeImportedId(name);
  const nowIso = new Date().toISOString();

  // Re-key the node objects from the temporary id used during discovery
  // to the final committed cluster id.
  const nodes: Node[] = candidate.nodes.map((n) => ({
    ...n,
    id: n.id.replace(/^__import-\d+/, id),
    clusterId: id,
  }));

  const cluster: Cluster = {
    id,
    name,
    description: `Imported from ${candidate.url}`,
    engine: candidate.engine,
    version: candidate.version,
    health: "unknown",
    healthSummary: null,
    lastFetchedAt: nowIso,
    unreachableSince: null,
    sshConfigured: false,
    apiFrozen: false,
    nodeCount: nodes.length,
    poolCount: candidate.poolCount,
    driveCount: candidate.driveCount,
    parity: candidate.parity,
    usableBytes: candidate.usableBytes,
    rawBytes: candidate.rawBytes,
    usedBytes: candidate.usedBytes,
    lastActivityAt: nowIso,
    createdAt: nowIso,
  };
  cluster.healthSummary = computeHealthSummary(cluster, nodes, tasks);
  cluster.health = computeHealth(cluster, cluster.healthSummary);

  clusters.push(cluster);
  nodesByCluster[id] = nodes;
  return { clusterId: id };
}

function slugifyHost(host: string): string {
  return (
    host
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 32) || "imported"
  );
}

function makeImportedId(name: string): string {
  const slug = slugifyHost(name);
  if (!clusters.some((c) => c.id === slug)) return slug;
  while (clusters.some((c) => c.id === `${slug}-${importSeq}`)) importSeq++;
  return `${slug}-${importSeq++}`;
}

// Simulates POST /api/v1/clusters/refresh. Synchronous re-fetch of
// each cluster's /minio/admin/v3/info plus SSH facts. The real backend
// runs this in parallel and returns the updated list; the mock just
// waits ~1.2s then mutates the fixture in place.
export async function refreshAllClusters(): Promise<Cluster[]> {
  await new Promise((r) => setTimeout(r, 1200));
  const nowIso = new Date().toISOString();
  for (const c of clusters) {
    c.lastFetchedAt = nowIso;
    c.unreachableSince = null;
    const nodes = nodesByCluster[c.id] ?? [];
    c.healthSummary = computeHealthSummary(c, nodes, tasks);
    c.health = computeHealth(c, c.healthSummary);
  }
  return clusters;
}

// ---- cluster SSH config ----
//
// Per-cluster SSH credentials store. Real backend: bbolt bucket
// "cluster_ssh" keyed by clusterId, encrypted via BM_DATA_KEY.
// Reachable via GET/PUT /api/v1/clusters/:id/ssh.

import type { SshCreds, SshOverrides } from "../pages/wizards/new/state";

export interface ClusterSshConfig {
  ssh: SshCreds;
  // Per-host SSH overrides, keyed by host id.
  overrides: Record<string, SshOverrides>;
}

const clusterSshStore = new Map<string, ClusterSshConfig>();

// Seed: clusters that report sshConfigured=true get a plausible
// existing config so the page demonstrates loading.
for (const c of clusters) {
  if (c.sshConfigured) {
    clusterSshStore.set(c.id, {
      ssh: {
        authMethod: "key",
        user: "buckit",
        keyPath: "~/.ssh/id_ed25519",
        sudo: true,
      },
      overrides: {},
    });
  }
}

export function getClusterSshConfig(
  clusterId: string,
): Promise<ClusterSshConfig | null> {
  return delay(clusterSshStore.get(clusterId) ?? null);
}

export function saveClusterSshConfig(
  clusterId: string,
  config: ClusterSshConfig,
): Promise<{ ok: true }> {
  clusterSshStore.set(clusterId, {
    ssh: { ...config.ssh },
    overrides: Object.fromEntries(
      Object.entries(config.overrides).map(([k, v]) => [k, { ...v }]),
    ),
  });
  // Flip the cluster's summary flag so dashboards/menus reflect that
  // SSH is now configured.
  const cluster = clusters.find((c) => c.id === clusterId);
  if (cluster) cluster.sshConfigured = true;
  return delay({ ok: true });
}

// ---- nodes ----

export function listNodes(clusterId: string): Promise<Node[]> {
  return delay(nodesByCluster[clusterId] ?? []);
}

export function getNode(clusterId: string, nodeId: string): Promise<Node | null> {
  const list = nodesByCluster[clusterId] ?? [];
  return delay(list.find((n) => n.id === nodeId) ?? null);
}

// ---- tasks ----

export function listTasks(opts?: {
  clusterId?: string;
  state?: Task["state"];
}): Promise<Task[]> {
  let result = tasks;
  if (opts?.clusterId) result = result.filter((t) => t.clusterId === opts.clusterId);
  if (opts?.state) result = result.filter((t) => t.state === opts.state);
  return delay(result);
}

export function getTask(id: string): Promise<Task | null> {
  return delay(tasks.find((t) => t.id === id) ?? null);
}

// ---- audit ----

export function listAudit() {
  return delay(auditEvents);
}

// ---- history ----

export function listHistory(): Promise<HistoryEntry[]> {
  // Return newest-first.
  return delay([...history].sort((a, b) => (a.at < b.at ? 1 : -1)));
}

export function clearHistory(): Promise<{ ok: true }> {
  history.splice(0, history.length);
  return delay({ ok: true });
}

let historySeq = 1000;
// recordHistory appends a row in-process. The real backend would route
// every state-changing handler through this same path. The prototype
// calls it from action buttons (Rolling restart, Tear down, etc.) so
// the History tab updates feel real.
export function recordHistory(
  entry: Omit<HistoryEntry, "id" | "at">,
): HistoryEntry {
  const row: HistoryEntry = {
    ...entry,
    id: "h-" + historySeq++,
    at: new Date().toISOString(),
  };
  history.push(row);
  return row;
}

// ---- cluster operation dispatch ----
//
// Single entry point for every cluster Action menu item. Real backend
// routes by `kind` to the right admin API call or SSH orchestration.
// The mock fabricates a Task record + history row, then advances state
// asynchronously for orchestrated ops so the OperationModal has
// something to subscribe to.

export type HostOpState = "pending" | "running" | "succeeded" | "failed";

export interface HostOpStatus {
  hostId: string;
  hostname: string;
  state: HostOpState;
  detail?: string;        // current sub-step on this node
  durationSec?: number;   // populated when state is terminal
}

export interface OpEvent {
  ts: string;
  level?: "info" | "ok" | "warn" | "error";
  text: string;
}

export interface OperationProgress {
  taskId: string;
  state: "running" | "succeeded" | "failed" | "canceled";
  currentStep?: string;
  current?: number;   // e.g., nodes done
  total?: number;     // e.g., total nodes
  detail?: string;    // free-text status line
  failureNote?: string;
  // Per-host progression for orchestrated ops. Undefined for signal
  // ops or pre-dispatch.
  hostStatuses?: HostOpStatus[];
  // Optional structured key/value pairs surfaced in the modal's
  // terminal phase. Used by ops that return rich result data —
  // e.g., restart_cluster's "4 online, 0 offline, 0 hung" + total
  // restart time.
  summary?: { label: string; value: string }[];
  // Streaming events surfaced in the modal's running / terminal
  // phases. Used by ops that don't have per-host progression but do
  // stream activity — e.g., start_heal.
  events?: OpEvent[];
}

interface OperationStore {
  [taskId: string]: OperationProgress;
}

const operations: OperationStore = {};
let opSeq = 1;

// Per-op-kind handlers. Each handler receives the taskId and is
// responsible for advancing the operation's state. Returns the kind
// label used in History entries.
export type OpKind =
  // Cluster-wide
  | "restart_cluster"
  | "stop_cluster"
  | "freeze_api"
  | "unfreeze_api"
  | "start_heal"
  | "rolling_restart"
  | "rolling_upgrade"
  | "start_cluster"
  | "rotate_root_creds"
  | "add_pool"
  | "remove_cluster"
  // Host-scoped (bulk selection or single node)
  | "systemctl_restart"
  | "systemctl_stop"
  | "systemctl_start"
  | "redeploy_software"
  | "reboot_host"
  | "shutdown_host";

export interface DispatchResult {
  taskId: string;
}

export function getOperationProgress(taskId: string): OperationProgress | null {
  return operations[taskId] ?? null;
}

// Request cancellation. The orchestration loop checks for this state
// between hosts; the in-flight host finishes naturally before the
// loop halts.
export function cancelOperation(taskId: string) {
  const cur = operations[taskId];
  if (!cur || cur.state !== "running") return;
  // Heal-style ops have an event stream rather than a host list. For
  // those, "cancel" means "stop following" — the server-side scan
  // continues. Mark the op succeeded with an explanatory note instead
  // of leaving it as canceled.
  const isFollowOnly = !!cur.events && !cur.hostStatuses;
  if (isFollowOnly) {
    const events = [
      ...(cur.events ?? []),
      {
        ts: new Date().toISOString(),
        level: "info" as const,
        text: "Stopped following. The heal scan continues on the server.",
      },
    ];
    operations[taskId] = {
      ...cur,
      state: "succeeded",
      events,
      detail: "Stopped following. Heal continues on the server.",
    };
    return;
  }
  operations[taskId] = { ...cur, state: "canceled", detail: "Cancel requested. Finishing current host…" };
}

export interface DispatchOptions {
  // Restrict the orchestrated host loop to a subset of cluster nodes.
  // Undefined = all cluster nodes (cluster-wide ops). Used by the
  // selected-host bulk bar (N hosts) and per-node Actions menu (1 host).
  targetHostIds?: string[];
}

export async function dispatchOperation(
  clusterId: string,
  kind: OpKind,
  params: Record<string, unknown> = {},
  opts: DispatchOptions = {},
): Promise<DispatchResult> {
  // Tiny dispatch delay to feel like a real network call.
  await new Promise((r) => setTimeout(r, 200));
  const taskId = `op-${opSeq++}`;
  const cluster = clusters.find((c) => c.id === clusterId);
  const clusterName = cluster?.name ?? clusterId;
  const allClusterHosts = cluster ? nodesByCluster[cluster.id] ?? [] : [];
  const targetHosts = opts.targetHostIds
    ? allClusterHosts.filter((h) => opts.targetHostIds!.includes(h.id))
    : allClusterHosts;

  operations[taskId] = { taskId, state: "running" };
  recordHistory({
    kind: "ui_action",
    display: humanLabel(kind, params, targetHosts) + ` on ${clusterName}`,
    target: clusterId,
    status: "running",
  });

  // Admin API ops with rich progress: bm sends one admin API call,
  // then polls the cluster until results are ready. From the UI's
  // perspective these look orchestrated (running phase with per-host
  // progression for restart/stop, or an event stream for heal).
  if (kind === "restart_cluster") {
    startRestartProgression(taskId, cluster);
    return { taskId };
  }
  if (kind === "stop_cluster") {
    startStopProgression(taskId, cluster);
    return { taskId };
  }
  if (kind === "start_heal") {
    startHealProgression(taskId, cluster);
    return { taskId };
  }

  // Signal-flavor ops: complete immediately. The modal will flip to
  // terminal phase right away.
  if (isSignal(kind)) {
    applySignalSideEffect(kind, cluster);
    operations[taskId] = {
      taskId,
      state: "succeeded",
      detail: signalSuccessDetail(kind),
    };
    return { taskId };
  }

  // Orchestrated ops: advance through per-node stages asynchronously.
  // Scope to targetHosts when provided (bulk-host bar or single-node
  // Actions menu); otherwise iterate all cluster nodes.
  startOrchestratedProgression(taskId, kind, cluster, params, targetHosts);
  return { taskId };
}

// Polls the cluster back to healthy after a restart signal is sent.
// Mock: each node comes back online over ~2 s with some staggering.
function startRestartProgression(taskId: string, cluster: Cluster | undefined) {
  if (!cluster) return;
  const hosts = nodesByCluster[cluster.id] ?? [];
  const hostStatuses: HostOpStatus[] = hosts.map((h) => ({
    hostId: h.id,
    hostname: h.hostname,
    state: "running",
    detail: "Waiting for restart…",
  }));

  operations[taskId] = {
    taskId,
    state: "running",
    current: 0,
    total: hosts.length,
    detail: "Restart signal sent. Polling for cluster to come back…",
    hostStatuses,
  };

  const start = Date.now();
  hosts.forEach((h, i) => {
    setTimeout(
      () => {
        const hs = hostStatuses.find((x) => x.hostId === h.id);
        if (hs) {
          hs.state = "succeeded";
          hs.durationSec =
            Math.round((Date.now() - start) / 100) / 10; // seconds, 1 dp
        }
        const succeededCount = hostStatuses.filter(
          (x) => x.state === "succeeded",
        ).length;
        const last = i === hosts.length - 1;
        if (last) {
          const durationMs = Date.now() - start;
          operations[taskId] = {
            ...operations[taskId]!,
            state: "succeeded",
            current: succeededCount,
            hostStatuses: [...hostStatuses],
            summary: [
              {
                label: "Servers",
                value: `${succeededCount} online, ${hosts.length - succeededCount} offline, 0 hung`,
              },
              {
                label: "Restart time",
                value: `${(durationMs / 1000).toFixed(2)} s`,
              },
            ],
          };
        } else {
          operations[taskId] = {
            ...operations[taskId]!,
            current: succeededCount,
            hostStatuses: [...hostStatuses],
          };
        }
      },
      400 + i * 350,
    );
  });
}

// mc admin service stop: signal is sent immediately, bm polls until
// every node confirms it's down. Mock: hosts go offline over ~1.5 s.
function startStopProgression(taskId: string, cluster: Cluster | undefined) {
  if (!cluster) return;
  const hosts = nodesByCluster[cluster.id] ?? [];
  const hostStatuses: HostOpStatus[] = hosts.map((h) => ({
    hostId: h.id,
    hostname: h.hostname,
    state: "running",
    detail: "Stopping…",
  }));

  operations[taskId] = {
    taskId,
    state: "running",
    current: 0,
    total: hosts.length,
    detail: "Stop signal sent. Polling for nodes to go down…",
    hostStatuses,
  };

  const start = Date.now();
  hosts.forEach((h, i) => {
    setTimeout(
      () => {
        const hs = hostStatuses.find((x) => x.hostId === h.id);
        if (hs) {
          hs.state = "succeeded";
          hs.detail = "Stopped";
          hs.durationSec = Math.round((Date.now() - start) / 100) / 10;
        }
        const stoppedCount = hostStatuses.filter(
          (x) => x.state === "succeeded",
        ).length;
        const last = i === hosts.length - 1;
        if (last) {
          const durationMs = Date.now() - start;
          operations[taskId] = {
            ...operations[taskId]!,
            state: "succeeded",
            current: stoppedCount,
            hostStatuses: [...hostStatuses],
            summary: [
              {
                label: "Servers",
                value: `0 online, ${stoppedCount} offline, 0 hung`,
              },
              {
                label: "Stop time",
                value: `${(durationMs / 1000).toFixed(2)} s`,
              },
            ],
          };
        } else {
          operations[taskId] = {
            ...operations[taskId]!,
            current: stoppedCount,
            hostStatuses: [...hostStatuses],
          };
        }
      },
      300 + i * 280,
    );
  });
}

// mc admin heal: signal is sent, then bm follows the server-side event
// stream (healed/scanned/failed items). Not tied to any one node.
// The modal exposes a "Stop following" button that halts the local
// stream without canceling the server-side scan.
function startHealProgression(taskId: string, cluster: Cluster | undefined) {
  if (!cluster) return;
  const start = Date.now();
  const events: OpEvent[] = [];
  let scanned = 0;
  let healed = 0;
  let failed = 0;

  const push = (text: string, level: OpEvent["level"] = "info") => {
    events.push({ ts: new Date().toISOString(), level, text });
  };

  const writeProgress = (extra: Partial<OperationProgress> = {}) => {
    operations[taskId] = {
      ...operations[taskId]!,
      events: [...events],
      detail: `Scanned ${scanned.toLocaleString()} items · ${healed} healed · ${failed} failed`,
      ...extra,
    };
  };

  operations[taskId] = {
    taskId,
    state: "running",
    detail: "Heal scan started…",
    events: [],
  };

  push("Heal scan started across all buckets and pools", "ok");
  push("Scanning bucket data…");
  writeProgress();

  const buckets = ["assets-prod", "logs-2026", "media", "backups"];
  const objects = ["object.dat", "image.jpg", "report.pdf", "video.mp4", "archive.tar"];
  let i = 0;
  const TOTAL = 28;

  const step = () => {
    const cur = operations[taskId];
    if (!cur || cur.state !== "running") return;
    if (i >= TOTAL) {
      const durationMs = Date.now() - start;
      push(
        `Heal scan complete: ${scanned.toLocaleString()} items scanned, ${healed} healed, ${failed} failed`,
        "ok",
      );
      operations[taskId] = {
        ...operations[taskId]!,
        state: "succeeded",
        events: [...events],
        summary: [
          { label: "Items scanned", value: scanned.toLocaleString() },
          { label: "Items healed", value: `${healed}` },
          { label: "Items failed", value: `${failed}` },
          { label: "Duration", value: `${(durationMs / 1000).toFixed(1)} s` },
        ],
      };
      return;
    }
    const bucket = buckets[i % buckets.length];
    const obj = objects[i % objects.length];
    const path = `${bucket}/${i.toString(16).padStart(3, "0")}/${obj}`;

    if (i % 5 === 4) {
      scanned += 200 + Math.floor(Math.random() * 120);
      push(`Crawled ${scanned.toLocaleString()} items so far`);
    } else if (i % 9 === 8) {
      failed++;
      scanned++;
      push(`Heal failed: ${path} (parity unavailable)`, "warn");
    } else {
      healed++;
      scanned++;
      push(`Healed: ${path}`, "ok");
    }
    writeProgress();
    i++;
    setTimeout(step, 350 + Math.random() * 300);
  };
  setTimeout(step, 250);
}

function isSignal(kind: OpKind): boolean {
  return (
    kind === "freeze_api" ||
    kind === "unfreeze_api" ||
    kind === "remove_cluster"
  );
}

function applySignalSideEffect(kind: OpKind, cluster?: Cluster) {
  if (!cluster) return;
  if (kind === "freeze_api") cluster.apiFrozen = true;
  if (kind === "unfreeze_api") cluster.apiFrozen = false;
  if (kind === "remove_cluster") {
    const idx = clusters.findIndex((c) => c.id === cluster.id);
    if (idx >= 0) clusters.splice(idx, 1);
    delete nodesByCluster[cluster.id];
  }
}

function signalSuccessDetail(kind: OpKind): string {
  switch (kind) {
    case "restart_cluster": return "Cluster restart signal sent. Cluster will be back up in a few seconds.";
    case "stop_cluster":    return "Cluster stop signal sent.";
    case "freeze_api":   return "S3 API frozen. All reads and writes are blocked.";
    case "unfreeze_api": return "S3 API unfrozen. Reads and writes resume.";
    case "start_heal":      return "Heal scan started. The scan runs server-side and may take hours on a large deployment.";
    case "remove_cluster":  return "Cluster definition removed.";
    default: return "Done.";
  }
}

function humanLabel(
  kind: OpKind,
  params: Record<string, unknown>,
  targetHosts: Node[] = [],
): string {
  // For host-scoped ops, suffix with which host(s) — "(node1)" for a
  // single host, "(3 hosts)" for a bulk selection. Cluster-wide ops
  // ignore this since the cluster name is already appended by the
  // caller.
  const hostSuffix =
    targetHosts.length === 1
      ? ` (${targetHosts[0].hostname})`
      : targetHosts.length > 1
        ? ` (${targetHosts.length} hosts)`
        : "";
  switch (kind) {
    case "restart_cluster":   return "Restart cluster";
    case "stop_cluster":      return "Stop cluster";
    case "freeze_api":     return "Froze S3 API";
    case "unfreeze_api":   return "Unfroze S3 API";
    case "start_heal":        return "Started heal";
    case "rolling_restart":   return "Rolling restart";
    case "rolling_upgrade":   return `Rolling upgrade to ${params.version ?? "?"}`;
    case "start_cluster":     return "Start cluster";
    case "rotate_root_creds": return "Rotated root credentials";
    case "add_pool":          return "Added pool";
    case "remove_cluster":    return "Removed cluster definition";
    case "systemctl_restart": return "Systemctl restart service" + hostSuffix;
    case "systemctl_stop":    return "Systemctl stop service" + hostSuffix;
    case "systemctl_start":   return "Systemctl start service" + hostSuffix;
    case "redeploy_software": return `Redeploy ${params.version ?? "?"}` + hostSuffix;
    case "reboot_host":       return "Reboot host" + hostSuffix;
    case "shutdown_host":     return "Shut down host" + hostSuffix;
  }
}

function startOrchestratedProgression(
  taskId: string,
  kind: OpKind,
  cluster: Cluster | undefined,
  _params: Record<string, unknown>,
  targetHosts?: Node[],
) {
  if (!cluster) return;
  const hosts = targetHosts ?? nodesByCluster[cluster.id] ?? [];
  const total = Math.max(hosts.length, 1);
  // Initialize all hosts as pending. As the orchestration ticks
  // through, each one moves through running → succeeded.
  const hostStatuses: HostOpStatus[] = hosts.map((h) => ({
    hostId: h.id,
    hostname: h.hostname,
    state: "pending",
  }));

  // Seed progress so the modal renders the host list immediately.
  operations[taskId] = {
    taskId,
    state: "running",
    current: 0,
    total,
    detail: `Starting ${humanLabel(kind, {})}…`,
    hostStatuses,
  };

  let idx = 0;
  const step = () => {
    if (operations[taskId]?.state !== "running") return;
    if (idx >= total) return;

    // Move the current host to "running".
    const cur = hostStatuses[idx];
    if (cur) cur.state = "running";
    operations[taskId] = {
      ...operations[taskId]!,
      current: idx,
      total,
      detail: cur ? `Working on ${cur.hostname}…` : undefined,
      hostStatuses: [...hostStatuses],
    };

    // Finish this host after a delay, then advance — or halt if a
    // cancel came in during the wait.
    setTimeout(() => {
      const wasCanceled = operations[taskId]?.state === "canceled";
      if (cur) {
        cur.state = "succeeded";
        cur.durationSec = 5 + Math.round(Math.random() * 6);
      }
      idx++;
      const done = idx >= total;
      if (wasCanceled) {
        operations[taskId] = {
          ...operations[taskId]!,
          state: "canceled",
          current: idx,
          total,
          detail: `${humanLabel(kind, {})} canceled after ${idx} of ${total} hosts.`,
          hostStatuses: [...hostStatuses],
        };
        return;
      }
      operations[taskId] = {
        ...operations[taskId]!,
        state: done ? "succeeded" : "running",
        current: idx,
        total,
        detail: done ? `${humanLabel(kind, {})} complete.` : undefined,
        hostStatuses: [...hostStatuses],
      };
      if (!done) setTimeout(step, 300);
    }, 1100);
  };
  setTimeout(step, 600);
}

// ---- live log stream ----
//
// Real backend will be SSE; here we return an unsubscribe function and
// invoke the callback every ~700ms with a synthetic log line.

const LOG_SOURCES = [
  "node1", "node2", "node3", "node4", "node5", "node6", "node7", "node8",
];
const LOG_TEMPLATES = [
  "$ systemctl daemon-reload",
  "$ scp buckit-1.0.0.rpm /tmp/",
  "✓ uploaded (45 MiB)",
  "$ dnf install -y /tmp/buckit-1.0.0.rpm",
  "✓ installed buckit-1.0.0",
  "$ systemctl disable minio",
  "$ systemctl enable --now buckit",
  "✓ buckit.service active",
  "i reading EnvironmentFile=/etc/default/minio",
  "✓ health probe /minio/health/live",
  "✓ cluster-healthy probe (8/8 nodes ready)",
];

export interface LogLine {
  ts: string;
  source: string;
  text: string;
}

export function subscribeTaskLog(
  _taskId: string,
  onLine: (line: LogLine) => void,
): () => void {
  let cancelled = false;
  let i = 0;
  const tick = () => {
    if (cancelled) return;
    const src = LOG_SOURCES[i % LOG_SOURCES.length];
    const text = LOG_TEMPLATES[i % LOG_TEMPLATES.length];
    onLine({ ts: new Date().toISOString(), source: src, text });
    i++;
    setTimeout(tick, 600 + Math.random() * 600);
  };
  setTimeout(tick, 200);
  return () => {
    cancelled = true;
  };
}
