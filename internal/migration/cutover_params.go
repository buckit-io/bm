package migration

import (
	"errors"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
)

// CutoverParams is the executor-friendly subset of MigrationDraft (web/src/
// pages/wizards/migrate/state.ts:MigrationDraft). The wizard POSTs the full
// draft; this struct picks out the fields the cutover pipeline reads.
type CutoverParams struct {
	// SourceClusterID is the bbolt key of the imported MinIO cluster being
	// migrated. The Cluster row's Engine flips from minio → buckit on
	// success.
	SourceClusterID string `json:"sourceClusterId"`

	// Name + Description are persisted into the post-cutover Cluster row
	// when the engine flips. Default to the existing values when empty.
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`

	// TargetVersion is the Buckit release tag to install. Resolved through
	// deploy.VersionByTag so the artifact URL is consistent with new-cluster.
	TargetVersion string `json:"targetVersion"`

	// API ports for the new Buckit service. When zero, defaults match the
	// new-cluster wizard (9000 / 9001) — but most migrations preserve the
	// existing minio listeners, which the installer detects from the
	// existing /etc/default/minio.
	API domain.APIPorts `json:"api,omitempty"`

	// Region is the value written into MINIO_REGION on the new
	// /etc/default/minio drop-in. Default us-east-1 when empty.
	Region string `json:"region,omitempty"`

	// ServerURL is the public URL the cluster reports for itself. Optional —
	// the installer falls back to http://<host>:<port>.
	ServerURL string `json:"serverUrl,omitempty"`

	// Hosts is the per-host SSH target list. Order is significant — cutover
	// is sequential and processes hosts in this order.
	Hosts []domain.HostRow `json:"hosts"`

	// SSH is the cluster-default SSH credential pack. Per-host overrides on
	// individual HostRow.SSHOverride values are merged in by the installer.
	SSH domain.SshCreds `json:"ssh"`

	// SnapshotPath is the absolute path to the MinIO snapshot file written
	// by /migrate/snapshot. The cutover refuses to run when this points at
	// a non-existent file.
	SnapshotPath string `json:"snapshotPath"`

	// HealthyTimeout caps the wait for /minio/health/live after each host's
	// service swap. Default 90s.
	HealthyTimeout time.Duration `json:"healthyTimeoutSec,omitempty"`

	// ClusterHealthyTimeout caps the per-host wait for cluster-wide health
	// (admin info reports every server `online`). Default 120s.
	ClusterHealthyTimeout time.Duration `json:"clusterHealthyTimeoutSec,omitempty"`

	// AdminCreds is the persisted admin URL+keys for the source cluster.
	// Populated by the API handler before dispatch — not part of the
	// wizard's draft payload.
	AdminCreds domain.AdminCreds `json:"-"`
}

// Validate enforces invariants the cutover pipeline depends on.
func (p CutoverParams) Validate() error {
	if p.SourceClusterID == "" {
		return errors.New("cutover: sourceClusterId required")
	}
	if p.TargetVersion == "" {
		return errors.New("cutover: targetVersion required")
	}
	if v := deploy.VersionByTag(p.TargetVersion); v == nil {
		return errors.New("cutover: unsupported targetVersion " + p.TargetVersion)
	}
	if len(p.Hosts) == 0 {
		return errors.New("cutover: at least one host required")
	}
	if p.SSH.User == "" {
		return errors.New("cutover: ssh user required")
	}
	if p.SnapshotPath == "" {
		return errors.New("cutover: snapshotPath required (run /migrate/snapshot first)")
	}
	return nil
}

// ArtifactForKind resolves the Buckit Artifact for the host's detected
// package format. kind is "rpm" or "deb".
func (p CutoverParams) ArtifactForKind(kind string) (deploy.Artifact, error) {
	art, err := deploy.ResolveArtifact(p.TargetVersion, kind, "")
	if err != nil {
		return deploy.Artifact{}, errors.New("cutover: " + err.Error())
	}
	return art, nil
}

// FromMigrationBody decodes the wire payload the API handler hands the
// dispatcher. The body is permissive — fields the wizard adds for UI-only
// reasons are ignored. Hosts with empty hostnames are dropped.
func FromMigrationBody(body MigrationBody) CutoverParams {
	hosts := make([]domain.HostRow, 0, len(body.Hosts))
	for _, h := range body.Hosts {
		if strings.TrimSpace(h.Hostname) == "" {
			continue
		}
		if h.Port == 0 {
			h.Port = 22
		}
		hosts = append(hosts, h)
	}
	healthy := time.Duration(body.HealthyTimeoutSec) * time.Second
	if healthy <= 0 {
		healthy = 90 * time.Second
	}
	clusterHealthy := time.Duration(body.ClusterHealthyTimeoutSec) * time.Second
	if clusterHealthy <= 0 {
		clusterHealthy = 120 * time.Second
	}
	return CutoverParams{
		SourceClusterID:       body.SourceClusterID,
		Name:                  body.Name,
		Description:           body.Description,
		TargetVersion:         body.TargetVersion,
		API:                   body.API,
		Region:                body.Region,
		ServerURL:             body.ServerURL,
		Hosts:                 hosts,
		SSH:                   body.SSH,
		SnapshotPath:          body.SnapshotPath,
		HealthyTimeout:        healthy,
		ClusterHealthyTimeout: clusterHealthy,
	}
}

// MigrationBody is the JSON shape the API handler accepts. It deliberately
// uses int seconds for timeouts (rather than time.Duration) so the wire
// format stays operator-friendly.
type MigrationBody struct {
	SourceClusterID          string             `json:"sourceClusterId"`
	Name                     string             `json:"name,omitempty"`
	Description              string             `json:"description,omitempty"`
	TargetVersion            string             `json:"targetVersion"`
	API                      domain.APIPorts    `json:"api,omitempty"`
	Region                   string             `json:"region,omitempty"`
	ServerURL                string             `json:"serverUrl,omitempty"`
	Hosts                    []domain.HostRow   `json:"hosts"`
	SSH                      domain.SshCreds    `json:"ssh"`
	SnapshotPath             string             `json:"snapshotPath"`
	HealthyTimeoutSec        int                `json:"healthyTimeoutSec,omitempty"`
	ClusterHealthyTimeoutSec int                `json:"clusterHealthyTimeoutSec,omitempty"`
}

// ---- per-host stage enum ----

// Stage names mirror web/src/pages/wizards/migrate/state.ts:CutoverNodeState.
// Keep the strings byte-identical to the UI enum so the Migrate step's
// stage-pill renderer maps without translation.
type Stage string

const (
	StagePending        Stage = "pending"
	StageStoppingMinio  Stage = "stopping_minio"
	StageUploadingPkg   Stage = "uploading_pkg"
	StageInstalling     Stage = "installing"
	StageSwitchingUnit  Stage = "switching_unit"
	StageWaitingHealth  Stage = "waiting_health"
	StageWaitingCluster Stage = "waiting_cluster"
	StageDone           Stage = "done"
	StageRolledBack     Stage = "rolled_back"
	StageFailed         Stage = "failed"
)

// IsTerminal reports whether the stage is one of the final per-host outcomes.
// The cutover loop uses this to decide when to advance to the next host.
func (s Stage) IsTerminal() bool {
	switch s {
	case StageDone, StageFailed, StageRolledBack:
		return true
	}
	return false
}

// StepEvent is the per-host progress signal the cutover pipeline emits.
// Mirrors deploy.StepEvent so the executor wraps both with the same
// progress-update plumbing.
type StepEvent struct {
	HostID   string
	Hostname string
	Stage    Stage
	Detail   string
	Err      error
}
