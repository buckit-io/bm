// Migration draft state. Mirrors new-cluster shape where applicable, plus
// MinIO-specific snapshot, plan, cutover, verify and finalize substates.

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
  name: string;
  description: string;
  version: string;

  importAlias?: string;
  hosts: HostRow[];
  ssh: SshCreds;

  discovery: Record<string, DiscoveryResult>;
  minioDetection: Record<string, MinioNodeInfo>;

  snapshot: MinioSnapshot | null;
  plan: MigrationPlan;
  preflight: PreflightResult[];
  cutover: CutoverState;
  verify: VerifyResult | null;
  finalized: boolean;
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
  concurrency: "sequential" | "two_at_a_time";
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

export function emptyMigration(): MigrationDraft {
  return {
    name: "legacy-east",
    description: "",
    version: "v1.0.0",
    hosts: [
      { id: "m1", hostname: "", port: 22, probe: "idle" },
      { id: "m2", hostname: "", port: 22, probe: "idle" },
    ],
    ssh: { authMethod: "agent", user: "buckit", sudo: true },
    discovery: {},
    minioDetection: {},
    snapshot: null,
    plan: {
      installMethod: "dnf",
      concurrency: "sequential",
      estimatedDowntimeSec: 60,
      estimatedTotalSec: 720,
      ack: false,
    },
    preflight: [],
    cutover: { perNode: {}, overallNodesDone: 0, paused: false },
    verify: null,
    finalized: false,
  };
}

export const STEPS = [
  { id: "basics", label: "Basics" },
  { id: "nodes", label: "Nodes" },
  { id: "discover", label: "Discover" },
  { id: "snapshot", label: "Snapshot" },
  { id: "plan", label: "Plan" },
  { id: "preflight", label: "Preflight" },
  { id: "cutover", label: "Cutover" },
  { id: "verify", label: "Verify" },
  { id: "finalize", label: "Finalize" },
];
