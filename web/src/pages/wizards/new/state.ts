// Draft state for the New Cluster wizard. Lives client-side during the
// prototype; in the real product the backend owns this via the cluster
// draft record (PATCH /api/v1/clusters/:id).

// Sentinel that means "the operator will provide a URL instead of
// picking a tagged release." When `version === CUSTOM_VERSION`, look at
// `customUrl` for the actual artifact path.
export const CUSTOM_VERSION = "custom";

export const BUCKIT_VERSIONS: { tag: string; label: string }[] = [
  { tag: "v1.0.0", label: "v1.0.0 (latest stable)" },
  { tag: "v0.99.0", label: "v0.99.0" },
  { tag: "v0.98.0", label: "v0.98.0" },
];

// CustomUrlCheck is the validation result for a custom artifact URL.
//   idle    — no URL entered yet (or version isn't "custom")
//   checking— validate request in flight
//   valid   — reachable AND sha256 detected; safe to use
//   warn    — reachable but sha256 unavailable; user can still proceed
//             (the deploy will succeed but artifact integrity is on the
//             operator)
//   error   — unreachable, unparseable, or known-bad; Next is blocked
export interface CustomUrlCheck {
  state: "idle" | "checking" | "valid" | "warn" | "error";
  message?: string;
  sizeBytes?: number;
  sha256?: string;
}

export interface NewClusterDraft {
  name: string;
  description: string;
  // One of BUCKIT_VERSIONS[].tag, or CUSTOM_VERSION.
  version: string;
  // Only meaningful when version === CUSTOM_VERSION. The URL the
  // operator pastes into the Basics step.
  customUrl: string;
  // Result of the last validate call against customUrl. Updated by the
  // debounced check in the Basics step.
  customUrlCheck: CustomUrlCheck;

  // Root credentials the operator chooses on the Basics step. The
  // deploy step writes these verbatim into MINIO_ROOT_USER and
  // MINIO_ROOT_PASSWORD on every node. No auto-generation — operators
  // can pre-stage in a password manager.
  credentials: { rootUser: string; rootPassword: string };
  // Listen ports for the S3 API and the console. Default 9000/9001;
  // change when ports collide or corp policy requires specific values.
  api: { port: number; consolePort: number };
  // S3 region label. Returned in headers and used by some clients for
  // signature validation. Default us-east-1.
  region: string;
  // External URL the server advertises in pre-signed URLs and admin
  // responses. Blank → derived at deploy from the first host.
  serverUrl: string;

  hosts: HostRow[];
  ssh: SshCreds;

  discovery: Record<string, DiscoveryResult>;
  topology: Topology;
  preflight: PreflightResult[];
  deploy: DeployState;
  done: DoneState;
}

// Per-host override for SSH credentials. Any field left undefined falls
// back to the cluster-wide `SshCreds` set in the SSH credentials card.
// When the operator clears the "override" checkbox the whole object is
// removed (sshOverride becomes undefined).
export interface SshOverrides {
  authMethod?: "agent" | "key" | "password";
  user?: string;
  keyPath?: string;
  keyPassphrase?: string;
  password?: string;
  // Override the cluster-wide passwordless-sudo setting. Undefined =
  // inherit; true/false = explicit override.
  sudo?: boolean;
}

export interface HostRow {
  id: string;
  hostname: string;
  port: number;
  probe: "idle" | "probing" | "reachable" | "auth_failed" | "timeout";
  // When present, overrides the cluster-wide SSH config for this host
  // only. Used for mixed environments (one host on a different OS user,
  // a single legacy host on password auth, etc.).
  sshOverride?: SshOverrides;
}

export interface SshCreds {
  authMethod: "agent" | "key" | "password";
  user: string;
  // Used when authMethod === "key": absolute or `~`-relative path to a
  // private key file the bm process can read. Stored as a path (not the
  // key bytes) because bm runs as the operator's process and shares the
  // operator's filesystem.
  keyPath?: string;
  // Optional passphrase for encrypted keys. Encrypted at rest via
  // BM_DATA_KEY when persisted.
  keyPassphrase?: string;
  // Used when authMethod === "password". Also encrypted at rest.
  password?: string;
  sudo: boolean;
}

export interface DiscoveryResult {
  state: "pending" | "running" | "done" | "failed";
  os?: string;
  // Machine architecture as reported by `uname -m`. Normalized: "amd64"
  // for x86_64, "arm64" for aarch64, etc. MinIO requires every host in
  // a cluster to share an architecture (binary is per-arch).
  arch?: string;
  kernel?: string;
  cores?: number;
  ramGiB?: number;
  // Per-drive detail returned by M4 discovery. The Topology step reads
  // these across all hosts to compute the common-mount intersection.
  drives?: DiscoveredDrive[];
  nic?: string;
  existingService?: "buckit" | "minio";
  sudoOk?: boolean;
}

export interface DiscoveredDrive {
  device: string;        // /dev/sdb, /dev/nvme0n1, etc.
  mount: string;         // "/data/disk1"; "" when unmounted
  sizeBytes: number;
  fsType?: string;       // "xfs" | "ext4" | undefined (unformatted)
  isBoot?: boolean;      // host's root/boot disk; never eligible
}

export interface Topology {
  setSize: number;
  parity: 2 | 3 | 4 | 6 | 8;
  // The drive mountpoints selected for MINIO_VOLUMES. Computed
  // automatically as the intersection of per-host mountpoints once
  // discovery completes; empty when discovery hasn't run or no common
  // mounts exist (case C).
  selectedMounts: string[];
}

export interface PreflightResult {
  id: string;
  label: string;
  // blocking — Next is disabled until this passes.
  // advisory — warn-on-fail but operator can proceed.
  severity: "blocking" | "advisory";
  // pass    — check passed on every applicable host.
  // warn    — non-blocking issue (advisory severity only).
  // fail    — check failed on at least one host.
  // skipped — not applicable (e.g., sudo when ssh.user === "root").
  result: "pass" | "warn" | "fail" | "skipped";
  // Per-host outcomes when the check is host-scoped (most are). Empty
  // when the check is overall (e.g., hostname pattern fit).
  hostStatuses?: PreflightHostStatus[];
  // General detail not tied to a specific host (e.g., the detected
  // hostname pattern, the package URL that failed to fetch, etc.).
  detail?: string;
}

export interface PreflightHostStatus {
  hostId: string;
  hostname: string;
  status: "pass" | "warn" | "fail";
  message?: string;
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
  nodesHealthy: number;
  poolsOnline: number;
  smokeTestPassed: boolean;
  credsRevealed: boolean;
}

export function emptyDraft(): NewClusterDraft {
  return {
    name: "",
    description: "",
    version: "v1.0.0",
    customUrl: "",
    customUrlCheck: { state: "idle" },
    credentials: { rootUser: "", rootPassword: "" },
    api: { port: 9000, consolePort: 9001 },
    region: "us-east-1",
    serverUrl: "",
    hosts: [
      { id: "h1", hostname: "", port: 22, probe: "idle" },
    ],
    ssh: { authMethod: "agent", user: "buckit", sudo: true },
    discovery: {},
    topology: {
      setSize: 16,
      parity: 4,
      selectedMounts: [],
    },
    preflight: [],
    deploy: { perNode: {}, overallPct: 0, canceled: false },
    done: {
      consoleUrl: "",
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
