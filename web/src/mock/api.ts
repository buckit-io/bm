// Mock API client. Returns Promises with simulated latency. Mirrors
// what the real backend endpoints will return, so swapping the real
// client in later is a one-file change.

import {
  auditEvents,
  Cluster,
  clusters,
  computeHealth,
  computeHealthSummary,
  history,
  HistoryEntry,
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

// ---- clusters ----

export function listClusters(): Promise<Cluster[]> {
  return delay(clusters);
}

export function getCluster(id: string): Promise<Cluster | null> {
  return delay(clusters.find((c) => c.id === id) ?? null);
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
