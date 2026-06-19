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
  // deploy step writes these into MINIO_ROOT_USER and MINIO_ROOT_PASSWORD
  // inside /etc/minio/config.env on every node (the primary
  // /etc/default/minio just points at that secondary file via
  // MINIO_CONFIG_ENV_FILE, so future rotations don't have to rewrite
  // cluster config). No auto-generation — operators can pre-stage in a
  // password manager.
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
  // When true, the deploy commit step also persists ssh + overrides
  // to the cluster_ssh bucket so post-deploy ops can reuse them
  // without re-prompting. Default true; bound to a checkbox on the
  // AddNodes step.
  persistSsh: boolean;

  // Transport security for the deployed cluster. "off" leaves MinIO on
  // HTTP. "byo" ships the operator-supplied cert + key into
  // /etc/minio/certs on every node and flips MINIO_VOLUMES to https.
  // Per the MinIO TLS guide, one cert with SANs covering every
  // hostname is the standard layout — backend preflight enforces that.
  tls: TlsConfig;

  discovery: Record<string, DiscoveryResult>;
  topology: Topology;
  preflight: PreflightResult[];
  deploy: DeployState;
  done: DoneState;
}

export type TlsMode = "off" | "byo";

// TlsConfig holds the operator's TLS choice for the cluster. PEMs are
// pasted (or loaded from a file in the browser) and only live in the
// wizard's in-memory draft — there is no persistence layer for drafts,
// so no at-rest encryption concern. The backend re-validates everything
// in DeployParams.Validate before any of this lands on a host.
export interface TlsConfig {
  mode: TlsMode;
  // PEM-encoded leaf cert (optionally followed by intermediates).
  certPem: string;
  // PEM-encoded private key (PKCS#1, PKCS#8, or SEC1 EC).
  keyPem: string;
  // Optional concatenated CA bundle. Only needed when intermediates
  // aren't already chained into certPem and the chain isn't reachable
  // via the system trust store.
  caBundlePem: string;
  // Derived from certPem by the Basics step; surfaced in the UI for
  // confidence and in the Review summary. Not sent to the server.
  parsed?: ParsedCert;
  // Set when certPem/keyPem failed to parse or didn't pair. Blocks Next.
  parseError?: string;
}

export interface ParsedCert {
  commonName: string;
  sans: string[];      // DNS SANs + IP SANs, mixed.
  notBefore: string;   // ISO 8601.
  notAfter: string;    // ISO 8601.
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
  // Cluster-wide SSH port. Saving the cluster SSH config propagates this
  // to every node's `sshPort`, so the periodic TCP probe and per-host
  // SSH ops pick it up. Omitted/zero means "leave node ports alone".
  port?: number;
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
  isSystem?: boolean;    // system mount (/, /var, /usr, etc.); hidden by default
}

export interface Topology {
  setSize: number;
  parity: number;
  dataVolumeMode: "auto" | "custom";
  // The mountpoints selected on the Topology step. Deploy uses a
  // managed `buckit/` subdirectory under each selected mount rather
  // than writing directly at the mount root.
  selectedMounts: string[];
  // Operator-entered path list or brace pattern used when dataVolumeMode
  // is custom. Expanded into selectedMounts before deploy.
  customDataVolumePaths: string;
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
  clusterId: string;
  aliasSaved: boolean;
  aliasWarning?: string;
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
    ssh: { authMethod: "agent", user: "", sudo: true },
    persistSsh: false,
    tls: { mode: "off", certPem: "", keyPem: "", caBundlePem: "" },
    discovery: {},
    topology: {
      setSize: 16,
      parity: 4,
      dataVolumeMode: "auto",
      selectedMounts: [],
      customDataVolumePaths: "",
    },
    preflight: [],
    deploy: { perNode: {}, overallPct: 0, canceled: false },
    done: {
      clusterId: "",
      aliasSaved: false,
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
