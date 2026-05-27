// Package domain holds the wire types shared by api, ssh, nodes, sshconfig
// and tasks. The shapes are the contract the UI consumes via web/src/mock/*;
// JSON tags here are the single source of truth.
package domain

// AuthMethod is the SSH credential flavour.
type AuthMethod string

const (
	AuthKey      AuthMethod = "key"
	AuthPassword AuthMethod = "password"
	AuthAgent    AuthMethod = "agent"
)

// SshCreds mirrors web/src/pages/wizards/new/state.ts:SshCreds.
type SshCreds struct {
	AuthMethod    AuthMethod `json:"authMethod"`
	User          string     `json:"user"`
	Port          int        `json:"port,omitempty"`
	KeyPath       string     `json:"keyPath,omitempty"`
	KeyPassphrase string     `json:"keyPassphrase,omitempty"`
	Password      string     `json:"password,omitempty"`
	Sudo          bool       `json:"sudo"`
}

// SshOverrides mirrors web/src/pages/wizards/new/state.ts:SshOverrides — every
// field is optional and inherits from the cluster default when unset.
type SshOverrides struct {
	AuthMethod    *AuthMethod `json:"authMethod,omitempty"`
	User          *string     `json:"user,omitempty"`
	KeyPath       *string     `json:"keyPath,omitempty"`
	KeyPassphrase *string     `json:"keyPassphrase,omitempty"`
	Password      *string     `json:"password,omitempty"`
	Sudo          *bool       `json:"sudo,omitempty"`
}

// ClusterSshConfig mirrors web/src/mock/api.ts:ClusterSshConfig.
type ClusterSshConfig struct {
	SSH       SshCreds                `json:"ssh"`
	Overrides map[string]SshOverrides `json:"overrides"`
}

// DriveState mirrors web/src/mock/data.ts:Drive.state.
type DriveState string

const (
	DriveReady   DriveState = "ready"
	DriveHealing DriveState = "healing"
	DriveFailed  DriveState = "failed"
	DriveUnknown DriveState = "unknown"
)

// Drive mirrors web/src/mock/data.ts:Drive.
type Drive struct {
	Mount      string     `json:"mount"`
	Device     string     `json:"device"`
	SizeBytes  int64      `json:"sizeBytes"`
	UsedBytes  int64      `json:"usedBytes"`
	State      DriveState `json:"state"`
	HealingPct *float64   `json:"healingPct,omitempty"`
	IsBoot     bool       `json:"isBoot,omitempty"`
}

// NIC mirrors the nic struct embedded in web/src/mock/data.ts:Node.
type NIC struct {
	Iface     string `json:"iface"`
	SpeedMbps int    `json:"speedMbps"`
}

// NodeState mirrors web/src/mock/data.ts:Node.state.
type NodeState string

const (
	NodeOnline      NodeState = "online"
	NodeOffline     NodeState = "offline"
	NodeDegraded    NodeState = "degraded"
	NodeUnknown     NodeState = "unknown"
	NodeUnreachable NodeState = "unreachable"
)

// Node mirrors web/src/mock/data.ts:Node.
type Node struct {
	ID                string    `json:"id"`
	ClusterID         string    `json:"clusterId"`
	Hostname          string    `json:"hostname"`
	SSHPort           int       `json:"sshPort"`
	APIPort           int       `json:"apiPort,omitempty"`
	Label             string    `json:"label,omitempty"`
	State             NodeState `json:"state"`
	Version           string    `json:"version,omitempty"`
	UptimeSec         *float64  `json:"uptimeSec,omitempty"`
	OS                string    `json:"os,omitempty"`
	Arch              string    `json:"arch,omitempty"`
	Kernel            string    `json:"kernel,omitempty"`
	CPUModel          string    `json:"cpuModel,omitempty"`
	CPUCores          *int      `json:"cpuCores,omitempty"`
	CPUThreads        *int      `json:"cpuThreads,omitempty"`
	CPUMaxMHz         *int      `json:"cpuMaxMHz,omitempty"`
	RAMBytes          *int64    `json:"ramBytes,omitempty"`
	NIC               *NIC      `json:"nic,omitempty"`
	Drives            []Drive   `json:"drives"`
	ExistingService   string    `json:"existingService,omitempty"` // "buckit" | "minio"
	Pool              int       `json:"pool"`
	Pingable          bool      `json:"pingable"`
	Sshable           bool      `json:"sshable"`
	APIAccessible     bool      `json:"apiAccessible"`
	ConsoleAccessible bool      `json:"consoleAccessible"`
}

// HostRef is a minimal host descriptor used by the connection-test endpoint
// and by orchestrated executors. Distinct from Node so callers don't have to
// fabricate a full Node just to dial.
type HostRef struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port,omitempty"`
}

// ManagerSettings mirrors the minimal settings payload the UI reads today.
type ManagerSettings struct {
	RemoteAccess RemoteAccessSettings `json:"remoteAccess"`
	VersionPin   *string              `json:"versionPin"`
	Version      string               `json:"version"`
}

type RemoteAccessSettings struct {
	Enabled bool `json:"enabled"`
}
