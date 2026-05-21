// Migration draft state. Mirrors new-cluster shape where applicable, plus
// MinIO-specific snapshot, plan, cutover, and verify substates.

import {
  DiscoveredDrive,
  DiscoveryResult,
  HostRow,
  PreflightResult,
  SshCreds,
  SshOverrides,
} from "../new/state";

export type {
  DiscoveredDrive,
  DiscoveryResult,
  HostRow,
  PreflightResult,
  SshCreds,
  SshOverrides,
};

export interface MigrationDraft {
  // The imported MinIO cluster being migrated. The wizard is entered
  // via /clusters/migrate?from=<id> from the cluster detail page; this
  // is the id of that source record. Name/hosts are seeded from the
  // same record at mount time.
  sourceClusterId: string;
  name: string;
  description: string;
  // Buckit version to install. Chosen on the Plan step.
  version: string;

  hosts: HostRow[];
  ssh: SshCreds;

  discovery: Record<string, DiscoveryResult>;
  minioDetection: Record<string, MinioNodeInfo>;

  snapshot: MinioSnapshot | null;
  // On-disk path the backend wrote the snapshot file to. Captured by
  // POST /clusters/:id/migrate/snapshot in the Review step; consumed by
  // POST /clusters/:id/migrate/cutover in the Migrate step.
  snapshotPath: string | null;
  plan: MigrationPlan;
  preflight: PreflightResult[];
  cutover: CutoverState;
  verify: VerifyResult | null;
}

export interface MinioNodeInfo {
  binaryPath: string;
  binaryVersion: string;
  serviceUnit: string;
  serviceActive: boolean;
  envFile: string;
  minioVolumes: string;
  minioOpts: string;
  detectedPools: string;
}

export interface MinioSnapshot {
  buckets: number;
  largestBucket: { name: string; size: string };
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

export interface MigrationPlan {
  installMethod: "dnf" | "apt" | "apk" | "tarball";
  estimatedDowntimeSec: number;
  estimatedTotalSec: number;
  ack: boolean;
}

export interface CutoverState {
  perNode: Record<string, CutoverNodeState>;
  overallNodesDone: number;
  paused: boolean;
}

export interface CutoverNodeState {
  state:
    | "pending"
    | "stopping_minio"
    | "uploading_pkg"
    | "installing"
    | "switching_unit"
    | "waiting_health"
    | "waiting_cluster"
    | "done"
    | "rolled_back"
    | "failed";
  cutoverStartedAt?: string;
  durationSec?: number;
}

export interface VerifyResult {
  clusterHealthy: boolean;
  nodesReporting: { ok: number; total: number };
  bucketsOk: { ok: number; total: number };
  objectsSampled: { ok: number; total: number };
  users: { ok: number; total: number };
  groups: { ok: number; total: number };
  policies: { ok: number; total: number };
  serviceAccounts: { ok: number; total: number };
  bucketPolicies: { ok: number; total: number };
  lifecycle: { ok: number; total: number };
  notifications: { ok: number; total: number };
  smokeOk: boolean;
}

export function emptyMigration(
  source: { id: string; name: string; description?: string; hosts: HostRow[] },
): MigrationDraft {
  return {
    sourceClusterId: source.id,
    name: source.name,
    description: source.description ?? "",
    version: "v1.0.0",
    hosts: source.hosts,
    ssh: { authMethod: "agent", user: "", sudo: true },
    discovery: {},
    minioDetection: {},
    snapshot: null,
    snapshotPath: null,
    plan: {
      installMethod: "dnf",
      estimatedDowntimeSec: 60,
      estimatedTotalSec: 720,
      ack: false,
    },
    preflight: [],
    cutover: { perNode: {}, overallNodesDone: 0, paused: false },
    verify: null,
  };
}

export const STEPS = [
  { id: "ssh", label: "SSH credentials" },
  { id: "review", label: "Preflight Check" },
  { id: "migrate", label: "Migrate" },
];
