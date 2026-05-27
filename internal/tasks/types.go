// Package tasks runs the operation-dispatch pipeline used by the UI's unified
// operation modal: validate → per-cluster lock → executor → live progress over
// a hub → terminal snapshot persisted to the bbolt `history` bucket.
//
// Domain types here mirror web/src/mock/data.ts and web/src/mock/api.ts
// field-for-field — the wire contract is whatever the UI already consumes.
package tasks

import (
	"encoding/json"
	"time"
)

// OpKind enumerates every operation the UI knows about. Only `noop` is
// registered with an executor in M2; the rest land in M3/M4/M7/M8 alongside
// their owning subsystems.
type OpKind string

const (
	OpNoop          OpKind = "noop"
	OpSshProbe      OpKind = "ssh_probe"
	OpClusterDeploy OpKind = "cluster_deploy"

	// Cluster-wide (M7)
	OpRestartCluster              OpKind = "restart_cluster"
	OpStopCluster                 OpKind = "stop_cluster"
	OpFreezeAPI                   OpKind = "freeze_api"
	OpUnfreezeAPI                 OpKind = "unfreeze_api"
	OpStartHeal                   OpKind = "start_heal"
	OpRollingRestart              OpKind = "rolling_restart"
	OpClusterUpgradeBySystemctl   OpKind = "cluster_upgrade_by_systemctl"
	OpClusterUpgradeByAdminUpdate OpKind = "cluster_upgrade_by_admin_update"
	OpStartCluster                OpKind = "start_cluster"
	OpStopClusterBySystemctl      OpKind = "stop_cluster_by_systemctl"
	OpRotateRootCreds             OpKind = "rotate_root_creds"
	OpAddPool                     OpKind = "add_pool"

	// Host-scoped (M3/M7)
	OpSystemctlRestart OpKind = "systemctl_restart"
	OpSystemctlStop    OpKind = "systemctl_stop"
	OpSystemctlStart   OpKind = "systemctl_start"
	OpRedeploySoftware OpKind = "redeploy_software"
	OpRebootHost       OpKind = "reboot_host"
	OpShutdownHost     OpKind = "shutdown_host"

	// Migration (M8)
	OpMigrateCutover  OpKind = "migrate_cutover"
	OpMigrateRollback OpKind = "migrate_rollback"
)

// OperationState matches web/src/mock/api.ts.OperationProgress.state.
type OperationState string

const (
	StateRunning   OperationState = "running"
	StateSucceeded OperationState = "succeeded"
	StateFailed    OperationState = "failed"
	StateCanceled  OperationState = "canceled"
)

// IsTerminal reports whether s is a finished state.
func (s OperationState) IsTerminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCanceled
}

// HostOpState mirrors web/src/mock/data.ts.HostOpState.
type HostOpState string

const (
	HostPending   HostOpState = "pending"
	HostRunning   HostOpState = "running"
	HostSucceeded HostOpState = "succeeded"
	HostFailed    HostOpState = "failed"
	// HostSkipped: the executor intentionally skipped this host (e.g.
	// it was already offline at the start of a migration cutover).
	// Distinct from Failed so the UI can render a neutral "skipped"
	// pill rather than a danger-colored failure.
	HostSkipped HostOpState = "skipped"
)

// HostOpStatus mirrors web/src/mock/data.ts.HostOpStatus.
type HostOpStatus struct {
	HostID      string      `json:"hostId"`
	Hostname    string      `json:"hostname"`
	State       HostOpState `json:"state"`
	Detail      string      `json:"detail,omitempty"`
	DurationSec *float64    `json:"durationSec,omitempty"`
}

// OpEvent mirrors web/src/mock/api.ts.OpEvent.
type OpEvent struct {
	TS    time.Time `json:"ts"`
	Level string    `json:"level,omitempty"` // "info" | "ok" | "warn" | "error"
	Text  string    `json:"text"`
}

// SummaryItem mirrors the {label, value} entries in OperationProgress.summary.
type SummaryItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// OperationProgress mirrors web/src/mock/api.ts.OperationProgress.
type OperationProgress struct {
	TaskID       string         `json:"taskId"`
	State        OperationState `json:"state"`
	CurrentStep  string         `json:"currentStep,omitempty"`
	Current      *int           `json:"current,omitempty"`
	Total        *int           `json:"total,omitempty"`
	Detail       string         `json:"detail,omitempty"`
	FailureNote  string         `json:"failureNote,omitempty"`
	HostStatuses []HostOpStatus `json:"hostStatuses,omitempty"`
	Summary      []SummaryItem  `json:"summary,omitempty"`
	Events       []OpEvent      `json:"events,omitempty"`
}

// OperationResult mirrors web/src/mock/data.ts.OperationResult — the frozen
// snapshot embedded in history rows. Live-only fields (events, counters) are
// intentionally omitted.
type OperationResult struct {
	State        OperationState `json:"state"`
	Detail       string         `json:"detail,omitempty"`
	Summary      []SummaryItem  `json:"summary,omitempty"`
	HostStatuses []HostOpStatus `json:"hostStatuses,omitempty"`
	FailureNote  string         `json:"failureNote,omitempty"`
}

// HostScope mirrors web/src/mock/data.ts.HistoryEntry.hostScope.
type HostScope struct {
	Hostnames []string `json:"hostnames"`
	Count     int      `json:"count"`
}

// HistoryEntry mirrors web/src/mock/data.ts.HistoryEntry. The id is a ULID so
// reverse-iteration of the bbolt bucket is naturally newest-first.
type HistoryEntry struct {
	ID          string           `json:"id"`
	At          time.Time        `json:"at"`
	OpKind      OpKind           `json:"opKind"`
	OpLabel     string           `json:"opLabel"`
	ClusterID   string           `json:"clusterId"`
	ClusterName string           `json:"clusterName"`
	HostScope   *HostScope       `json:"hostScope,omitempty"`
	Status      OperationState   `json:"status"` // running | succeeded | failed | canceled
	DurationSec *float64         `json:"durationSec,omitempty"`
	FailureNote string           `json:"failureNote,omitempty"`
	Result      *OperationResult `json:"result,omitempty"`
}

// DispatchRequest is the JSON body for POST /api/v1/operations.
type DispatchRequest struct {
	ClusterID     string          `json:"clusterId"`
	Kind          OpKind          `json:"kind"`
	Params        json.RawMessage `json:"params,omitempty"`
	TargetHostIDs []string        `json:"targetHostIds,omitempty"`
	// Optional UI-supplied context. Persisted directly into the history row
	// so the page doesn't have to reverse-derive these from the params.
	ClusterName string     `json:"clusterName,omitempty"`
	OpLabel     string     `json:"opLabel,omitempty"`
	HostScope   *HostScope `json:"hostScope,omitempty"`
}

// DispatchResponse is the JSON body returned from POST /api/v1/operations.
type DispatchResponse struct {
	TaskID string `json:"taskId"`
}
