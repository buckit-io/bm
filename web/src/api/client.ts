// Real-backend HTTP client. Every UI fetch funnels through here so the
// error envelope, base path, and JSON encoding are handled in one place.
//
// The exported endpoint methods mirror what mock/api.ts used to expose;
// hooks.ts and the few pages that bypass hooks call these directly.
//
// SSE streaming endpoints live in api/sse.ts — fetch+ReadableStream there
// because EventSource doesn't support POST bodies (cluster import) or
// custom abort semantics (operation event subscription).

import type {
  ApiErrorBody,
  AdminCreds,
  AdminCredsView,
  ArtifactCheck,
  BuckitVersion,
  Cluster,
  ClusterSshConfig,
  DispatchRequest,
  DispatchResponse,
  HealthInfo,
  HistoryEntry,
  NodeLogsRange,
  NodeLogsResponse,
  ImportCandidate,
  ManagerSettings,
  MinioSnapshot,
  MinioSnapshotSummary,
  Node,
  OperationProgress,
  SessionMe,
  ManagerUpdateApplyResult,
  ManagerUpdateStatus,
} from "./types";

const BASE = "/api/v1";

// ApiError wraps a non-2xx response. The payload field surfaces the typed
// {error, message, milestone?} envelope when the backend returned JSON;
// it's null on transport errors (network drop, abort) and on plain-text
// 4xx/5xx responses where parsing the body failed.
export class ApiError extends Error {
  status: number;
  code: string;
  payload: ApiErrorBody | null;

  constructor(status: number, code: string, message: string, payload: ApiErrorBody | null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.payload = payload;
  }
}

interface RequestInitJSON extends Omit<RequestInit, "body"> {
  body?: unknown; // will be JSON-stringified
}

async function request<T>(
  method: string,
  path: string,
  init: RequestInitJSON = {},
): Promise<T> {
  const url = path.startsWith("http") ? path : BASE + path;
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...((init.headers as Record<string, string>) ?? {}),
  };
  let body: BodyInit | undefined;
  if (init.body !== undefined && init.body !== null) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(init.body);
  }
  const resp = await fetch(url, {
    method,
    headers,
    body,
    signal: init.signal,
    credentials: "same-origin",
  });
  if (resp.status === 204) {
    // No content — common for DELETE / PUT-success cases. Return undefined
    // typed as T (callers expecting void are fine; callers expecting a
    // value should not call paths that 204).
    return undefined as T;
  }
  const text = await resp.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      // Non-JSON body — leave parsed null and surface the raw text on error.
    }
  }
  if (!resp.ok) {
    const envelope = (parsed as ApiErrorBody | null) ?? null;
    const code = envelope?.error ?? `http_${resp.status}`;
    const message = envelope?.message ?? text ?? resp.statusText;
    throw new ApiError(resp.status, code, message, envelope);
  }
  return parsed as T;
}

const get = <T>(path: string, signal?: AbortSignal) =>
  request<T>("GET", path, { signal });
const post = <T>(path: string, body?: unknown, signal?: AbortSignal) =>
  request<T>("POST", path, { body, signal });
const put = <T>(path: string, body?: unknown, signal?: AbortSignal) =>
  request<T>("PUT", path, { body, signal });
const del = <T>(path: string, signal?: AbortSignal) =>
  request<T>("DELETE", path, { signal });

// ---- app version ----

export const getAppVersion = (signal?: AbortSignal) =>
  get<{ version: string }>("/healthz", signal);

// ---- session ----

export const me = (signal?: AbortSignal) => get<SessionMe>("/sessions/me", signal);

// ---- settings ----

export const getSettings = (signal?: AbortSignal) =>
  get<ManagerSettings>("/settings", signal);

export const getManagerUpdateStatus = (signal?: AbortSignal) =>
  get<ManagerUpdateStatus>("/manager/update", signal);

export const applyManagerUpdate = (signal?: AbortSignal) =>
  post<ManagerUpdateApplyResult>("/manager/update/apply", undefined, signal);

// ---- clusters ----

export const listClusters = (signal?: AbortSignal) =>
  get<Cluster[]>("/clusters", signal);

export async function getCluster(id: string, signal?: AbortSignal): Promise<Cluster | null> {
  try {
    return await get<Cluster>(`/clusters/${encodeURIComponent(id)}`, signal);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export const refreshAllClusters = (signal?: AbortSignal) =>
  post<Cluster[]>("/clusters/refresh", undefined, signal);

export const refreshOneCluster = (id: string, signal?: AbortSignal) =>
  post<Cluster>(`/clusters/${encodeURIComponent(id)}/refresh`, undefined, signal);

export const deleteCluster = (id: string, signal?: AbortSignal) =>
  del<void>(`/clusters/${encodeURIComponent(id)}`, signal);

export async function getClusterAdminCreds(
  clusterId: string,
  signal?: AbortSignal,
): Promise<AdminCredsView | null> {
  try {
    return await get<AdminCredsView>(
      `/clusters/${encodeURIComponent(clusterId)}/admin-creds`,
      signal,
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export const saveClusterAdminCreds = (
  clusterId: string,
  creds: AdminCreds,
  signal?: AbortSignal,
) => put<void>(`/clusters/${encodeURIComponent(clusterId)}/admin-creds`, creds, signal);

// getClusterHealthInfo fetches the admin /healthinfo bundle (kernel/CPU/RAM/
// NIC) for every host in the cluster. Returns null when the cluster has no
// admin creds configured (424) — callers render the cards as "—" in that
// case instead of treating it as an error.
export async function getClusterHealthInfo(
  clusterId: string,
  signal?: AbortSignal,
): Promise<HealthInfo | null> {
  try {
    return await get<HealthInfo>(
      `/clusters/${encodeURIComponent(clusterId)}/healthinfo`,
      signal,
    );
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 424)) {
      return null;
    }
    throw err;
  }
}

// commitImport — the second leg of the import flow. The streaming
// discover step is in api/sse.ts (POST + SSE on the same response).
export const commitImport = (
  body: { candidate: ImportCandidate; chosenName: string; insecure?: boolean },
  signal?: AbortSignal,
) => post<{ clusterId: string }>("/clusters/import/commit", body, signal);

// ---- nodes ----

export const listNodes = (clusterId: string, signal?: AbortSignal) =>
  get<Node[]>(`/clusters/${encodeURIComponent(clusterId)}/nodes`, signal);

export async function getNode(
  clusterId: string,
  nodeId: string,
  signal?: AbortSignal,
): Promise<Node | null> {
  try {
    return await get<Node>(
      `/clusters/${encodeURIComponent(clusterId)}/nodes/${encodeURIComponent(nodeId)}`,
      signal,
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

// getNodeLogs runs `journalctl -u <unit> --since=<range>` over SSH on the
// node and returns the captured lines. One-shot — callers re-invoke to
// refresh. `since` is a server-validated preset, not arbitrary text.
export const getNodeLogs = (
  clusterId: string,
  nodeId: string,
  params: { since: NodeLogsRange; unit?: string; limit?: number },
  signal?: AbortSignal,
) => {
  const qs = new URLSearchParams({ since: params.since });
  if (params.unit) qs.set("unit", params.unit);
  if (params.limit) qs.set("limit", String(params.limit));
  return get<NodeLogsResponse>(
    `/clusters/${encodeURIComponent(clusterId)}/nodes/${encodeURIComponent(nodeId)}/logs?${qs.toString()}`,
    signal,
  );
};

// ---- ssh config ----

export async function getClusterSshConfig(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterSshConfig | null> {
  try {
    return await get<ClusterSshConfig>(
      `/clusters/${encodeURIComponent(clusterId)}/ssh`,
      signal,
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

// saveClusterSshConfig writes SSH credentials for the cluster. When
// persist is false, the backend keeps them in process memory only
// (cleared on bm restart) — useful for one-off operations without
// committing creds to disk. Defaults to persistent.
//
// `hosts` (optional) carries per-node SSH ports {id, port}. Each entry
// overwrites the corresponding Node.SSHPort on disk regardless of the
// persist flag (the port is structural metadata, not a credential).
export const saveClusterSshConfig = (
  clusterId: string,
  config: ClusterSshConfig,
  options?: { persist?: boolean; hosts?: { id: string; port: number }[] },
  signal?: AbortSignal,
) => {
  const qs = options?.persist === false ? "?persist=false" : "";
  const body =
    options?.hosts && options.hosts.length > 0
      ? { ...config, hosts: options.hosts }
      : config;
  return put<void>(
    `/clusters/${encodeURIComponent(clusterId)}/ssh${qs}`,
    body,
    signal,
  );
};

// ---- artifacts (Basics step custom URL validation) ----

export const listVersions = (signal?: AbortSignal) =>
  get<BuckitVersion[]>("/artifacts/versions", signal);

export const validateCustomArtifact = (url: string, signal?: AbortSignal) =>
  post<ArtifactCheck>("/artifacts/validate", { url }, signal);

// ---- history ----

export const listHistory = (signal?: AbortSignal) =>
  get<HistoryEntry[]>("/history", signal);

export const clearHistory = (signal?: AbortSignal) =>
  del<{ deleted: number }>("/history", signal);

// ---- operations ----

export const dispatchOperation = (
  req: DispatchRequest,
  signal?: AbortSignal,
) => post<DispatchResponse>("/operations", req, signal);

export const getOperationProgress = (taskId: string, signal?: AbortSignal) =>
  get<OperationProgress>(`/operations/${encodeURIComponent(taskId)}`, signal);

export const cancelOperation = (taskId: string, signal?: AbortSignal) =>
  post<void>(`/operations/${encodeURIComponent(taskId)}/cancel`, undefined, signal);

// ---- new-cluster wizard ----

// newClusterDiscover probes each host over SSH for OS/CPU/RAM/drives/NIC/
// existing service. Body: { hosts, ssh }. Returns a map keyed by hostId
// with a domain.WizardDiscoveryResult (state, os, arch, kernel, cores,
// ramGiB, drives, nic, existingService, sudoOk, error).
export const newClusterDiscover = (
  body: { hosts: unknown[]; ssh: unknown },
  signal?: AbortSignal,
) => post<Record<string, unknown>>("/clusters/new/discover", body, signal);

// newClusterPreflight runs the new-cluster preflight catalog against the
// supplied draft. Returns the PreflightResult[] the wizard renders.
export const newClusterPreflight = (draft: unknown, signal?: AbortSignal) =>
  post<unknown[]>("/clusters/new/preflight", draft, signal);

export const newClusterDeploy = (draft: unknown, signal?: AbortSignal) =>
  post<DispatchResponse>("/clusters/new/deploy", draft, signal);

// ---- migration wizard ----

export const migrateSnapshot = (clusterId: string, signal?: AbortSignal) =>
  post<{ snapshot: MinioSnapshot; summary: MinioSnapshotSummary; path: string }>(
    `/clusters/${encodeURIComponent(clusterId)}/migrate/snapshot`,
    undefined,
    signal,
  );

export const migratePreflight = (
  clusterId: string,
  body?: unknown,
  signal?: AbortSignal,
) => post<unknown>(
  `/clusters/${encodeURIComponent(clusterId)}/migrate/preflight`,
  body,
  signal,
);

export const migrateCutover = (
  clusterId: string,
  body: unknown,
  signal?: AbortSignal,
) => post<DispatchResponse>(
  `/clusters/${encodeURIComponent(clusterId)}/migrate/cutover`,
  body,
  signal,
);

export const migrateRollback = (
  clusterId: string,
  body: unknown,
  signal?: AbortSignal,
) => post<DispatchResponse>(
  `/clusters/${encodeURIComponent(clusterId)}/migrate/rollback`,
  body,
  signal,
);
