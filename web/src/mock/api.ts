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
    status: "active",
    health: "unknown",
    healthSummary: null,
    lastFetchedAt: nowIso,
    unreachableSince: null,
    sshConfigured: false,
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
    if (c.status === "draft") continue;
    c.lastFetchedAt = nowIso;
    c.unreachableSince = null;
    const nodes = nodesByCluster[c.id] ?? [];
    c.healthSummary = computeHealthSummary(c, nodes, tasks);
    c.health = computeHealth(c, c.healthSummary);
  }
  return clusters;
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
