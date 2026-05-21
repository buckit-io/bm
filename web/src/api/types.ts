// Wire types shared by every fetch call into /api/v1/*. Mirrors the JSON
// shapes that internal/domain/*.go produces — keep the field names byte-
// identical to the Go struct tags. When a Go type adds a field, add it
// here too (and update the consuming page).
//
// Wizard-local UI state (NewClusterDraft, MigrationDraft, DeployState,
// CutoverState, etc.) lives next to the page that owns it, not here.

// ---- cluster + nodes ----

export type Health = "healthy" | "degraded" | "critical" | "unknown";

export type ClusterEngine = "buckit" | "minio";

export interface NodeRollup {
  online: number;
  degraded: number;
  offline: number;
  total: number;
}

export interface DriveRollup {
  ready: number;
  healing: number;
  failed: number;
  total: number;
}

export interface HealthSummary {
  nodes: NodeRollup;
  drives: DriveRollup;
}

export interface MigratedFrom {
  product: "minio";
  version: string;
  finalizedAt: string;
}

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
  lastFetchedAt: string | null;
  unreachableSince: string | null;
  sshConfigured: boolean;
  apiFrozen: boolean;
  lastActivityAt: string;
  createdAt: string;
  migratedFrom?: MigratedFrom;
  // Buckit web console deep-link target. Empty when the server doesn't
  // advertise one — the UI hides the "Open console" button.
  consoleUrl?: string;
}

export interface NIC {
  iface: string;
  speedMbps: number;
}

export type DriveState = "ready" | "healing" | "failed";

export interface Drive {
  mount: string;
  device: string;
  sizeBytes: number;
  usedBytes: number;
  state: DriveState;
  healingPct?: number;
  isBoot?: boolean;
}

export type NodeState = "online" | "offline" | "degraded" | "unknown";

export interface Node {
  id: string;
  clusterId: string;
  hostname: string;
  sshPort: number;
  label?: string;
  state: NodeState;
  version?: string;
  uptimeSec?: number;
  os?: string;
  kernel?: string;
  cpuModel?: string;
  cpuCores?: number;
  cpuThreads?: number;
  cpuMaxMHz?: number;
  ramBytes?: number;
  nic?: NIC;
  drives: Drive[];
  existingService?: "buckit" | "minio";
  pool: number;
  pingable: boolean;
  sshable: boolean;
  apiAccessible: boolean;
  consoleAccessible: boolean;
}

// ---- ssh credentials (per cluster + per host overrides) ----

export type AuthMethod = "key" | "password" | "agent";

export interface SshCreds {
  authMethod: AuthMethod;
  user: string;
  keyPath?: string;
  keyPassphrase?: string;
  password?: string;
  sudo: boolean;
}

export interface SshOverrides {
  authMethod?: AuthMethod;
  user?: string;
  keyPath?: string;
  keyPassphrase?: string;
  password?: string;
  sudo?: boolean;
}

export interface ClusterSshConfig {
  ssh: SshCreds;
  overrides: Record<string, SshOverrides>;
}

// ---- admin creds (loopback only — not currently surfaced to the UI) ----

export interface AdminCreds {
  url: string;
  accessKey: string;
  secretKey: string;
  insecure?: boolean;
}

// ---- import (discover + commit) ----

export type ImportErrorKind = "unreachable" | "auth" | "invalid_url";

export interface ImportError {
  kind: ImportErrorKind;
  message: string;
}

export interface DiscoveryProgress {
  ts: string;
  level: "info" | "ok" | "error";
  text: string;
}

export interface ImportCandidate {
  suggestedName: string;
  url: string;
  username: string;
  password: string;
  engine: ClusterEngine;
  version: string;
  nodes: Node[];
  poolCount: number;
  driveCount: number;
  rawBytes: number;
  usedBytes: number;
  usableBytes: number;
  parity: number;
  consoleUrl?: string;
}

// ---- artifact validation (Basics step custom URL) ----

export type ArtifactCheckState =
  | "idle"
  | "checking"
  | "valid"
  | "warn"
  | "error";

export interface ArtifactCheck {
  state: ArtifactCheckState;
  message?: string;
  sizeBytes?: number;
  sha256?: string;
}

export interface BuckitVersion {
  tag: string;
  label: string;
  rpmUrl?: string;
  rpmUrlAmd64?: string;
  rpmUrlArm64?: string;
  debUrl?: string;
  sha256Url?: string;
}

// ---- operations + history ----

// OpKind is the union of every operation the UI can dispatch. Matches the
// OpKind constants in internal/tasks/types.go.
export type OpKind =
  | "noop"
  | "ssh_probe"
  | "cluster_deploy"
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
  | "systemctl_restart"
  | "systemctl_stop"
  | "systemctl_start"
  | "redeploy_software"
  | "reboot_host"
  | "shutdown_host"
  | "migrate_cutover"
  | "migrate_rollback";

export type OperationState = "running" | "succeeded" | "failed" | "canceled";

export type HostOpState = "pending" | "running" | "succeeded" | "failed";

export interface HostOpStatus {
  hostId: string;
  hostname: string;
  state: HostOpState;
  detail?: string;
  durationSec?: number;
}

export interface OpEvent {
  ts: string;
  level?: "info" | "ok" | "warn" | "error";
  text: string;
}

export interface SummaryItem {
  label: string;
  value: string;
}

export interface OperationProgress {
  taskId: string;
  state: OperationState;
  currentStep?: string;
  current?: number;
  total?: number;
  detail?: string;
  failureNote?: string;
  hostStatuses?: HostOpStatus[];
  summary?: SummaryItem[];
  events?: OpEvent[];
}

export interface OperationResult {
  state: OperationState;
  detail?: string;
  summary?: SummaryItem[];
  hostStatuses?: HostOpStatus[];
  failureNote?: string;
}

export interface HostScope {
  hostnames: string[];
  count: number;
}

export interface HistoryEntry {
  id: string;
  at: string;
  opKind: OpKind | string;
  opLabel: string;
  clusterId: string;
  clusterName: string;
  hostScope?: HostScope;
  status: OperationState;
  durationSec?: number;
  failureNote?: string;
  result?: OperationResult;
}

// ---- dispatch (POST /operations body + response) ----

export interface DispatchRequest {
  clusterId: string;
  kind: OpKind;
  // Free-form per-op params; the orchestrator's Validate decodes this.
  params?: Record<string, unknown>;
  targetHostIds?: string[];
  clusterName?: string;
  opLabel?: string;
  hostScope?: HostScope;
}

export interface DispatchResponse {
  taskId: string;
}

// ---- migration snapshot (M8) ----

export interface BucketSnapshot {
  name: string;
  creationDate: string;
  sizeBytes?: number;
  versioning?: string;
  objectLock?: boolean;
  tags?: string[];
}

export interface UserSnapshot {
  accessKey: string;
  status?: string;
  policies?: string[];
}

export interface GroupSnapshot {
  name: string;
  status?: string;
  members?: string[];
  policy?: string;
}

export interface PolicySnapshot {
  name: string;
  policy: string;
}

export interface ServiceAccountSnapshot {
  accessKey: string;
  parentUser?: string;
  status?: string;
  name?: string;
  description?: string;
}

export interface LifecycleRule {
  bucketName: string;
  ruleId: string;
  ruleXml: string;
}

export interface NotificationTarget {
  bucketName: string;
  arn: string;
  events?: string[];
}

export interface MinioSnapshot {
  capturedAt: string;
  clusterId: string;
  version: string;
  buckets: BucketSnapshot[];
  users?: UserSnapshot[];
  groups?: GroupSnapshot[];
  policies?: PolicySnapshot[];
  serviceAccounts?: ServiceAccountSnapshot[];
  lifecycle?: LifecycleRule[];
  notifications?: NotificationTarget[];
  warnings?: string[];
}

export interface BucketHeadline {
  name: string;
  size: string;
}

export interface MinioSnapshotSummary {
  buckets: number;
  largestBucket?: BucketHeadline;
  versioning: number;
  lifecycle: number;
  objectLock: number;
  users: number;
  groups: number;
  customPolicies: number;
  serviceAccounts: number;
  policies: number;
  notifications: number;
  replicationTargets: number;
  warnings: string[];
}

export interface VerifyCheck {
  ok: number;
  total: number;
}

export interface MigrationVerifyResult {
  clusterHealthy: boolean;
  nodesReporting: VerifyCheck;
  bucketsOk: VerifyCheck;
  objectsSampled: VerifyCheck;
  users: VerifyCheck;
  groups: VerifyCheck;
  policies: VerifyCheck;
  serviceAccounts: VerifyCheck;
  bucketPolicies: VerifyCheck;
  lifecycle: VerifyCheck;
  notifications: VerifyCheck;
  smokeOk: boolean;
  failureNote?: string;
}

// ---- session + settings ----

export interface SessionMe {
  username: string;
}

export interface ManagerSettings {
  remoteAccess: { enabled: boolean };
  versionPin: string | null;
}

// ---- error envelope ----
//
// Backend errors come back as { error: code, message: human, milestone?: tag }.
// ApiError in client.ts wraps this so callers can switch on `code`.
export interface ApiErrorBody {
  error: string;
  message: string;
  milestone?: string;
}
