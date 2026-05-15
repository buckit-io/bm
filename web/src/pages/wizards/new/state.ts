// Draft state for the New Cluster wizard. Lives client-side during the
// prototype; in the real product the backend owns this via the cluster
// draft record (PATCH /api/v1/clusters/:id).

export interface NewClusterDraft {
  name: string;
  description: string;
  intendedUse: "production" | "staging" | "dev";
  version: string;

  hosts: HostRow[];
  ssh: SshCreds;

  discovery: Record<string, DiscoveryResult>;
  topology: Topology;
  preflight: PreflightResult[];
  deploy: DeployState;
  done: DoneState;
}

export interface HostRow {
  id: string;
  hostname: string;
  port: number;
  label: string;
  probe: "idle" | "probing" | "reachable" | "auth_failed" | "timeout";
}

export interface SshCreds {
  authMethod: "agent" | "key" | "password";
  user: string;
  keyName?: string;
  password?: string;
  sudo: boolean;
}

export interface DiscoveryResult {
  state: "pending" | "running" | "done" | "failed";
  os?: string;
  kernel?: string;
  cores?: number;
  ramGiB?: number;
  drives?: number;
  driveSizeTiB?: number;
  nic?: string;
  existingService?: "buckit" | "minio";
}

export interface Topology {
  drivesPerNode: number;
  setSize: number;
  parity: 2 | 3 | 4 | 6 | 8;
  driveSizeTiB: number;
  excludedDrives: number; // count
}

export interface PreflightResult {
  id: string;
  label: string;
  scope?: string;
  result: "pass" | "warn" | "fail";
  detail?: string;
}

export interface DeployState {
  startedAt?: string;
  perNode: Record<string, DeployNodeState>;
  overallPct: number;
  canceled: boolean;
}

export interface DeployNodeState {
  state:
    | "pending"
    | "downloading"
    | "installing"
    | "writing_config"
    | "starting"
    | "healthy"
    | "failed";
  elapsedSec: number;
  lastEvent: string;
}

export interface DoneState {
  consoleUrl: string;
  rootUser: string;
  rootPass: string;
  nodesHealthy: number;
  poolsOnline: number;
  smokeTestPassed: boolean;
  credsRevealed: boolean;
}

export function emptyDraft(): NewClusterDraft {
  return {
    name: "",
    description: "",
    intendedUse: "production",
    version: "v1.0.0",
    hosts: [
      { id: "h1", hostname: "", port: 22, label: "", probe: "idle" },
      { id: "h2", hostname: "", port: 22, label: "", probe: "idle" },
    ],
    ssh: { authMethod: "agent", user: "buckit", sudo: true },
    discovery: {},
    topology: {
      drivesPerNode: 12,
      setSize: 16,
      parity: 4,
      driveSizeTiB: 16,
      excludedDrives: 0,
    },
    preflight: [],
    deploy: { perNode: {}, overallPct: 0, canceled: false },
    done: {
      consoleUrl: "",
      rootUser: "admin",
      rootPass: "",
      nodesHealthy: 0,
      poolsOnline: 0,
      smokeTestPassed: false,
      credsRevealed: false,
    },
  };
}

export const STEPS = [
  { id: "basics", label: "Basics" },
  { id: "nodes", label: "Nodes" },
  { id: "discover", label: "Discover" },
  { id: "topology", label: "Topology" },
  { id: "preflight", label: "Preflight" },
  { id: "review", label: "Review" },
  { id: "deploy", label: "Deploy" },
  { id: "done", label: "Done" },
];
