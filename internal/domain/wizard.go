package domain

import "time"

// BuckitVersion is one selectable entry on the new-cluster wizard's Basics
// step. Returned by GET /artifacts/versions.
type BuckitVersion struct {
	Tag         string `json:"tag"`
	Label       string `json:"label"`
	RpmURL      string `json:"rpmUrl,omitempty"`
	RpmURLAmd64 string `json:"rpmUrlAmd64,omitempty"`
	RpmURLArm64 string `json:"rpmUrlArm64,omitempty"`
	DebURL      string `json:"debUrl,omitempty"`
	SHA256URL   string `json:"sha256Url,omitempty"`
}

// CustomUrlCheckState matches web/src/pages/wizards/new/state.ts:CustomUrlCheck.state.
type CustomUrlCheckState string

const (
	CustomUrlIdle     CustomUrlCheckState = "idle"
	CustomUrlChecking CustomUrlCheckState = "checking"
	CustomUrlValid    CustomUrlCheckState = "valid"
	CustomUrlWarn     CustomUrlCheckState = "warn"
	CustomUrlError    CustomUrlCheckState = "error"
)

// CustomUrlCheck is the response from POST /artifacts/validate.
type CustomUrlCheck struct {
	State     CustomUrlCheckState `json:"state"`
	Message   string              `json:"message,omitempty"`
	SizeBytes int64               `json:"sizeBytes,omitempty"`
	SHA256    string              `json:"sha256,omitempty"`
}

// HostProbeState matches HostRow.probe in the TS state.
type HostProbeState string

const (
	HostProbeIdle       HostProbeState = "idle"
	HostProbeProbing    HostProbeState = "probing"
	HostProbeReachable  HostProbeState = "reachable"
	HostProbeAuthFailed HostProbeState = "auth_failed"
	HostProbeTimeout    HostProbeState = "timeout"
)

// HostRow matches web/src/pages/wizards/new/state.ts:HostRow.
type HostRow struct {
	ID          string         `json:"id"`
	Hostname    string         `json:"hostname"`
	Port        int            `json:"port"`
	Probe       HostProbeState `json:"probe"`
	SSHOverride *SshOverrides  `json:"sshOverride,omitempty"`
}

// Topology matches state.ts:Topology.
type Topology struct {
	SetSize        int      `json:"setSize"`
	Parity         int      `json:"parity"`
	SelectedMounts []string `json:"selectedMounts"`
}

// DiscoveredDrive matches state.ts:DiscoveredDrive.
type DiscoveredDrive struct {
	Device    string `json:"device"`
	Mount     string `json:"mount"`
	SizeBytes int64  `json:"sizeBytes"`
	FsType    string `json:"fsType,omitempty"`
	IsBoot    bool   `json:"isBoot,omitempty"`
}

// WizardDiscoveryState matches state.ts:DiscoveryResult.state.
type WizardDiscoveryState string

const (
	WizardDiscoveryPending WizardDiscoveryState = "pending"
	WizardDiscoveryRunning WizardDiscoveryState = "running"
	WizardDiscoveryDone    WizardDiscoveryState = "done"
	WizardDiscoveryFailed  WizardDiscoveryState = "failed"
)

// WizardDiscoveryResult matches web/src/pages/wizards/new/state.ts:DiscoveryResult.
// Distinct from ImportCandidate (M4); this is the per-host SSH probe payload
// the new-cluster wizard's Discover step renders.
type WizardDiscoveryResult struct {
	State           WizardDiscoveryState `json:"state"`
	OS              string               `json:"os,omitempty"`
	Arch            string               `json:"arch,omitempty"`
	Kernel          string               `json:"kernel,omitempty"`
	Cores           *int                 `json:"cores,omitempty"`
	RamGiB          *int                 `json:"ramGiB,omitempty"`
	Drives          []DiscoveredDrive    `json:"drives,omitempty"`
	NIC             string               `json:"nic,omitempty"`
	ExistingService string               `json:"existingService,omitempty"` // "buckit" | "minio"
	SudoOk          *bool                `json:"sudoOk,omitempty"`
	Error           string               `json:"error,omitempty"`
}

// PreflightSeverity matches state.ts:PreflightResult.severity.
type PreflightSeverity string

const (
	PreflightBlocking PreflightSeverity = "blocking"
	PreflightAdvisory PreflightSeverity = "advisory"
)

// PreflightStatus matches state.ts:PreflightResult.result.
type PreflightStatus string

const (
	PreflightPass    PreflightStatus = "pass"
	PreflightWarn    PreflightStatus = "warn"
	PreflightFail    PreflightStatus = "fail"
	PreflightSkipped PreflightStatus = "skipped"
)

// PreflightHostStatus matches state.ts:PreflightHostStatus.
type PreflightHostStatus struct {
	HostID   string          `json:"hostId"`
	Hostname string          `json:"hostname"`
	Status   PreflightStatus `json:"status"` // pass | warn | fail (skipped only at outer level)
	Message  string          `json:"message,omitempty"`
}

// PreflightResult matches state.ts:PreflightResult.
type PreflightResult struct {
	ID           string                `json:"id"`
	Label        string                `json:"label"`
	Severity     PreflightSeverity     `json:"severity"`
	Result       PreflightStatus       `json:"result"`
	HostStatuses []PreflightHostStatus `json:"hostStatuses,omitempty"`
	Detail       string                `json:"detail,omitempty"`
}

// Credentials is the operator-supplied root user/password for a new cluster.
type Credentials struct {
	RootUser     string `json:"rootUser"`
	RootPassword string `json:"rootPassword"`
}

// APIPorts captures the S3 + console listen ports the deploy writes into
// MINIO_OPTS.
type APIPorts struct {
	Port        int `json:"port"`
	ConsolePort int `json:"consolePort"`
}

// NewClusterDraft matches web/src/pages/wizards/new/state.ts:NewClusterDraft.
//
// The TS interface also carries deploy + done sub-states (DeployState /
// DoneState) but those live entirely in the UI — bm tracks the equivalent via
// the operation history row, so they're omitted here.
type NewClusterDraft struct {
	Name           string                           `json:"name"`
	Description    string                           `json:"description,omitempty"`
	Version        string                           `json:"version"`
	CustomURL      string                           `json:"customUrl,omitempty"`
	CustomURLCheck *CustomUrlCheck                  `json:"customUrlCheck,omitempty"`
	Credentials    Credentials                      `json:"credentials"`
	API            APIPorts                         `json:"api"`
	Region         string                           `json:"region"`
	ServerURL      string                           `json:"serverUrl,omitempty"`
	Hosts          []HostRow                        `json:"hosts"`
	SSH            SshCreds                         `json:"ssh"`
	Discovery      map[string]WizardDiscoveryResult `json:"discovery,omitempty"`
	Topology       Topology                         `json:"topology"`
}

// ---- Migration snapshot ----

// BucketSnapshot is the slice of a bucket's metadata the snapshot captures.
type BucketSnapshot struct {
	Name         string    `json:"name"`
	CreationDate time.Time `json:"creationDate"`
	SizeBytes    int64     `json:"sizeBytes,omitempty"`
	Versioning   string    `json:"versioning,omitempty"` // Enabled / Suspended / off
	ObjectLock   bool      `json:"objectLock,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
}

// UserSnapshot captures an admin-defined user — name + policies + access key
// metadata. The secret key is NOT captured (it can't be retrieved from the
// admin API anyway); operators rotate creds during migration.
type UserSnapshot struct {
	AccessKey string   `json:"accessKey"`
	Status    string   `json:"status,omitempty"` // enabled / disabled
	Policies  []string `json:"policies,omitempty"`
}

// PolicySnapshot captures an IAM-style policy document by name + JSON body.
type PolicySnapshot struct {
	Name   string `json:"name"`
	Policy string `json:"policy"` // JSON-encoded policy document
}

// LifecycleRule captures one bucket-lifecycle rule.
type LifecycleRule struct {
	BucketName string `json:"bucketName"`
	RuleID     string `json:"ruleId"`
	RuleXML    string `json:"ruleXml"`
}

// NotificationTarget captures bucket event notification destinations.
type NotificationTarget struct {
	BucketName string   `json:"bucketName"`
	ARN        string   `json:"arn"`
	Events     []string `json:"events,omitempty"`
}

// GroupSnapshot captures an admin-defined group: name + members + attached
// policy.
type GroupSnapshot struct {
	Name    string   `json:"name"`
	Status  string   `json:"status,omitempty"` // enabled / disabled
	Members []string `json:"members,omitempty"`
	Policy  string   `json:"policy,omitempty"`
}

// ServiceAccountSnapshot captures one minio service account. Only the
// metadata is recoverable from the admin API — the secret key is opaque.
type ServiceAccountSnapshot struct {
	AccessKey   string `json:"accessKey"`
	ParentUser  string `json:"parentUser,omitempty"`
	Status      string `json:"status,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// MinioSnapshot is the rollback-safety capture written before a migration
// cutover. Persisted to ~/.config/bm/snapshots/<clusterId>-<ts>.json.
//
// The on-disk format is wire-stable across bm versions — the cutover and
// rollback executors read it back unchanged. Add fields with caution and
// keep them optional.
type MinioSnapshot struct {
	CapturedAt      time.Time                `json:"capturedAt"`
	ClusterID       string                   `json:"clusterId"`
	Version         string                   `json:"version"`
	Buckets         []BucketSnapshot         `json:"buckets"`
	Users           []UserSnapshot           `json:"users,omitempty"`
	Groups          []GroupSnapshot          `json:"groups,omitempty"`
	Policies        []PolicySnapshot         `json:"policies,omitempty"`
	ServiceAccounts []ServiceAccountSnapshot `json:"serviceAccounts,omitempty"`
	Lifecycle       []LifecycleRule          `json:"lifecycle,omitempty"`
	Notifications   []NotificationTarget     `json:"notifications,omitempty"`
	// Warnings flag soft per-bucket fetch failures (e.g. a single bucket's
	// lifecycle endpoint 404'd). The cutover proceeds — operators see them in
	// the Review step of the wizard.
	Warnings []string `json:"warnings,omitempty"`
}

// MinioSnapshotSummary is the counts-and-warnings shape the migrate wizard's
// Review step renders. Derived from MinioSnapshot at the snapshot endpoint
// so the UI doesn't have to enumerate each list.
//
// Mirrors web/src/pages/wizards/migrate/state.ts:MinioSnapshot.
type MinioSnapshotSummary struct {
	Buckets            int             `json:"buckets"`
	LargestBucket      *BucketHeadline `json:"largestBucket,omitempty"`
	Versioning         int             `json:"versioning"`
	Lifecycle          int             `json:"lifecycle"`
	ObjectLock         int             `json:"objectLock"`
	Users              int             `json:"users"`
	Groups             int             `json:"groups"`
	CustomPolicies     int             `json:"customPolicies"`
	ServiceAccounts    int             `json:"serviceAccounts"`
	Policies           int             `json:"policies"`
	Notifications      int             `json:"notifications"`
	ReplicationTargets int             `json:"replicationTargets"`
	Warnings           []string        `json:"warnings"`
}

// BucketHeadline is the {name, size} pair the wizard renders as
// `largestBucket`. Size is a human-readable string ("12.4 GiB") so the UI
// can render it verbatim.
type BucketHeadline struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// VerifyCheck is one ok/total pair in the post-cutover audit.
type VerifyCheck struct {
	OK    int `json:"ok"`
	Total int `json:"total"`
}

// MigrationVerifyResult is the post-cutover audit emitted onto the cutover
// task's OperationResult. Mirrors web/src/pages/wizards/migrate/state.ts
// :VerifyResult — the wizard's Migrate step renders the same fields.
type MigrationVerifyResult struct {
	ClusterHealthy  bool        `json:"clusterHealthy"`
	NodesReporting  VerifyCheck `json:"nodesReporting"`
	BucketsOK       VerifyCheck `json:"bucketsOk"`
	ObjectsSampled  VerifyCheck `json:"objectsSampled"`
	Users           VerifyCheck `json:"users"`
	Groups          VerifyCheck `json:"groups"`
	Policies        VerifyCheck `json:"policies"`
	ServiceAccounts VerifyCheck `json:"serviceAccounts"`
	BucketPolicies  VerifyCheck `json:"bucketPolicies"`
	Lifecycle       VerifyCheck `json:"lifecycle"`
	Notifications   VerifyCheck `json:"notifications"`
	SmokeOK         bool        `json:"smokeOk"`
	// FailureNote is non-empty when the verify pass surfaced a problem the
	// operator should look at. The cutover does NOT auto-rollback — the note
	// is informational.
	FailureNote string `json:"failureNote,omitempty"`
}
