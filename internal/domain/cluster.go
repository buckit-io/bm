package domain

import "time"

// HealthState matches web/src/mock/data.ts:Health.
type HealthState string

const (
	HealthHealthy  HealthState = "healthy"
	HealthDegraded HealthState = "degraded"
	HealthCritical HealthState = "critical"
	HealthUnknown  HealthState = "unknown"
)

// ClusterEngine matches web/src/mock/data.ts:ClusterEngine.
type ClusterEngine string

const (
	EngineBuckit ClusterEngine = "buckit"
	EngineMinio  ClusterEngine = "minio"
)

// NodeRollup is the per-state counter block embedded in HealthSummary.
type NodeRollup struct {
	Online   int `json:"online"`
	Degraded int `json:"degraded"`
	Offline  int `json:"offline"`
	Total    int `json:"total"`
}

// DriveRollup is the per-state counter block for drives.
type DriveRollup struct {
	Ready   int `json:"ready"`
	Healing int `json:"healing"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

// HealthSummary matches web/src/mock/data.ts:HealthSummary.
type HealthSummary struct {
	Nodes  NodeRollup  `json:"nodes"`
	Drives DriveRollup `json:"drives"`
}

// MigratedFrom matches web/src/mock/data.ts:Cluster.migratedFrom.
type MigratedFrom struct {
	Product     string    `json:"product"` // "minio"
	Version     string    `json:"version"`
	FinalizedAt time.Time `json:"finalizedAt"`
}

// Cluster matches web/src/mock/data.ts:Cluster.
type Cluster struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	Engine           ClusterEngine  `json:"engine"`
	Version          string         `json:"version"`
	Health           HealthState    `json:"health"`
	HealthSummary    *HealthSummary `json:"healthSummary"`
	NodeCount        int            `json:"nodeCount"`
	PoolCount        int            `json:"poolCount"`
	DriveCount       int            `json:"driveCount"`
	Parity           int            `json:"parity"`
	UsableBytes      int64          `json:"usableBytes"`
	RawBytes         int64          `json:"rawBytes"`
	UsedBytes        int64          `json:"usedBytes"`
	LastFetchedAt    *time.Time     `json:"lastFetchedAt"`
	UnreachableSince *time.Time     `json:"unreachableSince"`
	SSHConfigured    bool           `json:"sshConfigured"`
	APIFrozen        bool           `json:"apiFrozen"`
	LastActivityAt   time.Time      `json:"lastActivityAt"`
	CreatedAt        time.Time      `json:"createdAt"`
	MigratedFrom     *MigratedFrom  `json:"migratedFrom,omitempty"`
	// ConsoleURL is the Buckit web console deep-link target. Empty when the
	// server doesn't advertise one — the UI hides the "Open console" button.
	ConsoleURL string `json:"consoleUrl,omitempty"`
}

// AdminCreds is the persisted admin API access pack for one cluster.
type AdminCreds struct {
	URL       string `json:"url"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	// Insecure skips TLS verification — required for self-signed admin
	// endpoints. Default false.
	Insecure bool `json:"insecure"`
}

// ServerInfo is the slice of madmin's ServerInfoMsg we actually consume. Kept
// in domain so the rest of the code doesn't import madmin directly.
type ServerInfo struct {
	Mode       string             `json:"mode"`
	Version    string             `json:"version"`
	DeployedAt time.Time          `json:"deployedAt"`
	ConsoleURL string             `json:"consoleUrl,omitempty"`
	Servers    []ServerInfoServer `json:"servers"`
	Pools      int                `json:"pools"`
	Parity     int                `json:"parity"`
	Raw        int64              `json:"raw"`
	Used       int64              `json:"used"`
	Usable     int64              `json:"usable"`
}

// ServerInfoServer is one host in a ServerInfo response.
type ServerInfoServer struct {
	Endpoint    string                  `json:"endpoint"`
	State       NodeState               `json:"state"`
	PoolNumber  int                     `json:"poolNumber"`
	Uptime      *float64                `json:"uptime,omitempty"`
	Version     string                  `json:"version,omitempty"`
	Kernel      string                  `json:"kernel,omitempty"`
	OS          string                  `json:"os,omitempty"`
	CPUModel    string                  `json:"cpuModel,omitempty"`
	CPUCores    *int                    `json:"cpuCores,omitempty"`
	CPUThreads  *int                    `json:"cpuThreads,omitempty"`
	CPUMaxMHz   *int                    `json:"cpuMaxMHz,omitempty"`
	RAMBytes    *int64                  `json:"ramBytes,omitempty"`
	NIC         *NIC                    `json:"nic,omitempty"`
	Drives      []ServerInfoDrive       `json:"drives"`
}

// ServerInfoDrive is one drive on a discovered host.
type ServerInfoDrive struct {
	Mount      string     `json:"mount"`
	Device     string     `json:"device"`
	SizeBytes  int64      `json:"sizeBytes"`
	UsedBytes  int64      `json:"usedBytes"`
	State      DriveState `json:"state"`
	HealingPct *float64   `json:"healingPct,omitempty"`
	IsBoot     bool       `json:"isBoot,omitempty"`
}

// AccountInfo is the slice of madmin's AccountInfo we consume.
type AccountInfo struct {
	AccountName string   `json:"accountName"`
	Buckets     []string `json:"buckets"`
}

// ImportCandidate matches web/src/mock/api.ts:ImportCandidate.
type ImportCandidate struct {
	SuggestedName string        `json:"suggestedName"`
	URL           string        `json:"url"`
	Username      string        `json:"username"`
	Password      string        `json:"password"`
	Engine        ClusterEngine `json:"engine"`
	Version       string        `json:"version"`
	Nodes         []Node        `json:"nodes"`
	PoolCount     int           `json:"poolCount"`
	DriveCount    int           `json:"driveCount"`
	RawBytes      int64         `json:"rawBytes"`
	UsedBytes     int64         `json:"usedBytes"`
	UsableBytes   int64         `json:"usableBytes"`
	Parity        int           `json:"parity"`
	ConsoleURL    string        `json:"consoleUrl,omitempty"`
}

// DiscoveryProgress matches web/src/mock/api.ts:DiscoveryProgress — one
// line in the SSE stream of an in-flight discover call.
type DiscoveryProgress struct {
	TS    time.Time `json:"ts"`
	Level string    `json:"level"` // "info" | "ok" | "error"
	Text  string    `json:"text"`
}

// ImportErrorKind matches the discriminator in web/src/mock/api.ts:ImportError.
type ImportErrorKind string

const (
	ImportErrUnreachable ImportErrorKind = "unreachable"
	ImportErrAuth        ImportErrorKind = "auth"
	ImportErrInvalidURL  ImportErrorKind = "invalid_url"
)

// ImportError is the typed error returned by Discover. Kept as a struct (not a
// Go error) so the HTTP layer can encode it directly into the final SSE
// `event: result` frame as `{ok:false, error}`.
type ImportError struct {
	Kind    ImportErrorKind `json:"kind"`
	Message string          `json:"message"`
}
