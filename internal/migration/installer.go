package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	bmssh "github.com/buckit-io/bm/internal/ssh"
)

// minioEnvPath is the canonical /etc/default/minio location both minio.service
// and the new buckit.service drop-ins read.
const minioEnvPath = "/etc/default/minio"

// minioEnvBackup is the per-host on-disk copy of the original env file. Kept
// next to the original so rollback is a simple `cp` away.
const minioEnvBackup = "/etc/default/minio.bm-bak"

// Installer drives a single host through the cutover pipeline. Reuses the
// SSH pool so multiple sequential hosts amortize handshake cost.
//
// Stage progression mirrors web/src/pages/wizards/migrate/state.ts
// :CutoverNodeState.state (StoppingMinio → UploadingPkg → Installing →
// SwitchingUnit → WaitingHealth → Done).
type Installer struct {
	Pool *bmssh.Pool

	// HealthyTimeout caps the wait for /minio/health/live after the new
	// service starts. Default 90s.
	HealthyTimeout time.Duration
}

// NewInstaller returns an Installer using pool. nil-safe (returns an Installer
// that fails the first dial with a clear error).
func NewInstaller(pool *bmssh.Pool) *Installer {
	return &Installer{Pool: pool, HealthyTimeout: 90 * time.Second}
}

// Install walks one host through the full cutover pipeline. emit is called
// on every stage transition; the executor wraps those into UI events.
// Returns the first error that aborted the run, or nil on Done.
func (in *Installer) Install(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}
	if in == nil || in.Pool == nil {
		return errors.New("cutover: nil installer / pool")
	}

	report(StagePending, "Queued")
	if err := ctx.Err(); err != nil {
		return err
	}

	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
		return err
	}

	// 1. Capture the existing minio env into a backup file. Doing this
	//    before stopping minio means a transport failure here aborts the
	//    cutover safely — minio is still running.
	report(StageStoppingMinio, "Backing up "+minioEnvPath)
	if err := backupMinioEnv(ctx, client, creds); err != nil {
		reportErr(StageFailed, fmt.Errorf("backup env: %w", err))
		return err
	}

	// 2. Stop the existing minio.service. If the unit doesn't exist (e.g.
	//    the operator already manually disabled it), a non-zero exit is
	//    OK — log and continue.
	report(StageStoppingMinio, "systemctl stop minio.service")
	stopCmd := deploy.SudoWrap(creds, "systemctl stop minio.service || true")
	if err := deploy.RunStep(ctx, client, stopCmd); err != nil {
		reportErr(StageFailed, fmt.Errorf("stop minio: %w", err))
		return err
	}

	// 3. Detect the host's package manager, then resolve+fetch the
	//    matching Buckit artifact.
	mgr, err := deploy.DetectPackageManagerForClient(ctx, client)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("detect package manager: %w", err))
		return err
	}
	artifact, err := params.ArtifactForKind(mgr.Kind())
	if err != nil {
		reportErr(StageFailed, err)
		return err
	}
	expectedSHA256, err := deploy.FetchChecksum(ctx, artifact)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}
	report(StageUploadingPkg, "Fetching "+artifact.URL)
	if err := deploy.RunStep(ctx, client, mgr.DownloadCommand(artifact.URL)); err != nil {
		reportErr(StageFailed, fmt.Errorf("download: %w", err))
		return err
	}
	report(StageUploadingPkg, fmt.Sprintf("Verifying %s sha256", mgr.LocalFile()))
	if err := deploy.RunStep(ctx, client, mgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	// 4. Install the package.
	report(StageInstalling, fmt.Sprintf("Installing %s", mgr.LocalFile()))
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, mgr.InstallCommand(deploy.InstallActionFresh))); err != nil {
		reportErr(StageFailed, fmt.Errorf("install: %w", err))
		return err
	}

	// 5. Swap the systemd unit: disable minio.service, enable buckit.service.
	//    /etc/default/minio remains the env source — Buckit is wire-compatible.
	report(StageSwitchingUnit, "systemctl swap minio.service → buckit.service")
	swap := strings.Join([]string{
		"systemctl disable minio.service || true",
		"systemctl daemon-reload",
		"systemctl enable --now buckit.service",
	}, " && ")
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, swap)); err != nil {
		reportErr(StageFailed, fmt.Errorf("swap unit: %w", err))
		return err
	}

	// 6. Wait for the new service to report healthy locally.
	report(StageWaitingHealth, "Waiting for /minio/health/live")
	if err := in.waitHealthy(ctx, client, params); err != nil {
		reportErr(StageFailed, fmt.Errorf("local health: %w", err))
		return err
	}

	report(StageDone, "Buckit healthy")
	return nil
}

// backupMinioEnv copies /etc/default/minio to /etc/default/minio.bm-bak when
// the original exists and the backup doesn't. Idempotent — re-running cutover
// on a host that's already been backed up does not overwrite the original.
func backupMinioEnv(ctx context.Context, client *ssh.Client, creds bmssh.Resolved) error {
	cmd := deploy.SudoWrap(creds, fmt.Sprintf(
		"if [ -f %s ] && [ ! -f %s ]; then cp -p %s %s; fi",
		minioEnvPath, minioEnvBackup, minioEnvPath, minioEnvBackup,
	))
	return deploy.RunStep(ctx, client, cmd)
}

// waitHealthy polls /minio/health/live until 200 or the deadline passes.
// The cluster-wide health-wait happens at the executor level, between hosts.
func (in *Installer) waitHealthy(ctx context.Context, client *ssh.Client, params CutoverParams) error {
	port := params.API.Port
	if port == 0 {
		// Buckit reads MINIO_OPTS from /etc/default/minio for the listen
		// address; we don't know it definitively without re-parsing the env.
		// Default to the minio default 9000 — operators with non-default
		// ports set params.API.Port via the wizard.
		port = 9000
	}
	timeout := in.HealthyTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := bmssh.Run(ctx, client, fmt.Sprintf("curl -fsS --max-time 5 http://127.0.0.1:%d/minio/health/live", port))
		if err == nil && r.ExitCode == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Rollback reverts one host from Buckit back to MinIO. Runs only on hosts
// whose cutover state reached StageDone; the rollback executor decides which
// hosts qualify.
//
// Steps: disable buckit.service, restore the env-file backup, daemon-reload,
// enable --now minio.service. Idempotent — running on a host that's already
// rolled back is a no-op (systemctl emits "already" warnings, exit 0).
func (in *Installer) Rollback(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}

	if in == nil || in.Pool == nil {
		return errors.New("rollback: nil installer / pool")
	}

	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
		return err
	}

	report(StageRolledBack, "Stopping buckit.service")
	stop := strings.Join([]string{
		"systemctl disable --now buckit.service || true",
	}, " && ")
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, stop)); err != nil {
		reportErr(StageFailed, fmt.Errorf("stop buckit: %w", err))
		return err
	}

	report(StageRolledBack, "Restoring "+minioEnvPath)
	restore := fmt.Sprintf("if [ -f %s ]; then cp -p %s %s; fi", minioEnvBackup, minioEnvBackup, minioEnvPath)
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, restore)); err != nil {
		reportErr(StageFailed, fmt.Errorf("restore env: %w", err))
		return err
	}

	report(StageRolledBack, "systemctl enable --now minio.service")
	startMinio := strings.Join([]string{
		"systemctl daemon-reload",
		"systemctl enable --now minio.service",
	}, " && ")
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, startMinio)); err != nil {
		reportErr(StageFailed, fmt.Errorf("start minio: %w", err))
		return err
	}

	// Wait for minio to report healthy at the same port the cutover ran
	// against. Any waitHealthy failure is treated as a hard rollback fail.
	report(StageRolledBack, "Waiting for minio /health/live")
	if err := in.waitHealthy(ctx, client, params); err != nil {
		reportErr(StageFailed, fmt.Errorf("minio health: %w", err))
		return err
	}

	report(StageRolledBack, "MinIO restored")
	return nil
}
