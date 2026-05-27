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

// dropInPath is the per-host systemd drop-in bm writes to align
// buckit.service with the user / group / EnvironmentFile set the old
// minio.service was using. Removed by Rollback. The 10- prefix is the
// systemd-conventional lexical bucket for operator-supplied overrides
// (drop-ins are merged in lexical order; lower wins on scalar conflicts,
// so a future 90-operator.conf can still override us).
const (
	dropInDir  = "/etc/systemd/system/buckit.service.d"
	dropInPath = dropInDir + "/10-bm-migrated.conf"
)

// Conventional MinIO packaging values bm falls back to when minio.service
// doesn't declare User / Group / EnvironmentFile. The migration preflight
// (minioServiceComplete) verifies the corresponding host-side artifacts
// exist before emitting only-a-warning; a cutover dispatched without
// preflight assumes these values without verification.
const (
	fallbackMinioUser    = "minio-user"
	fallbackMinioGroup   = "minio-user"
	fallbackMinioEnvFile = "/etc/default/minio"
)

// Installer drives a single host through the cutover pipeline. Reuses the
// SSH pool so multiple sequential hosts amortize handshake cost.
//
// Stage progression mirrors web/src/pages/wizards/migrate/state.ts
// :CutoverNodeState.state (StoppingMinio → DownloadingPkg → Installing →
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

// PreStage performs every per-host step that does NOT require minio.service
// to be stopped: probe the live minio.service for User/Group/EnvironmentFile,
// detect the package manager, download + verify the Buckit artifact, install
// the package, and write the systemd drop-in. Runs concurrently across all
// targeted hosts; if any host fails, the cutover aborts before any host
// stops minio (zero-downtime abort).
//
// On success, the host has buckit installed but disabled, and a drop-in at
// dropInPath ready to be picked up the next time buckit.service is enabled.
// minio.service is still serving — the operator's cluster is untouched
// from a data-path perspective.
func (in *Installer) PreStage(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}
	if in == nil || in.Pool == nil {
		return errors.New("pre-stage: nil installer / pool")
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

	// 1. Capture minio.service's User/Group/EnvironmentFile while it's
	//    still running. The drop-in renderer uses these to point
	//    buckit.service at the same identity and env file.
	report(StageDownloadingPkg, "Probing minio.service")
	oldProps, err := deploy.LoadUnitProps(ctx, client, creds, "minio.service")
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("probe minio.service: %w", err))
		return err
	}

	// 2. Detect the host's package manager, then download + verify the
	//    Buckit artifact. Both run while minio.service is still serving,
	//    so any transport failure here aborts the cutover with zero
	//    impact on the cluster.
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
	report(StageDownloadingPkg, "Fetching "+artifact.URL)
	if err := deploy.RunStep(ctx, client, mgr.DownloadCommand(artifact.URL)); err != nil {
		reportErr(StageFailed, fmt.Errorf("download: %w", err))
		return err
	}
	report(StageDownloadingPkg, fmt.Sprintf("Verifying %s sha256", mgr.LocalFile()))
	if err := deploy.RunStep(ctx, client, mgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	// 3. Install the Buckit package. Buckit does not conflict with minio —
	//    different binary paths, different unit file paths, no shared env
	//    files — so this is safe to do while minio.service is still
	//    running. Verified against ../buckit/packaging/nfpm.yaml.
	report(StageInstalling, fmt.Sprintf("Installing %s", mgr.LocalFile()))
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, mgr.InstallCommand(deploy.InstallActionFresh))); err != nil {
		reportErr(StageFailed, fmt.Errorf("install: %w", err))
		return err
	}

	// 4. Probe buckit.service for its User/Group/EnvironmentFile and
	//    write a drop-in if those differ from minio.service's. The
	//    drop-in sits under /etc/systemd/system/buckit.service.d/ and
	//    has no effect until buckit.service is enabled in the Switch
	//    phase.
	newProps, err := deploy.LoadUnitProps(ctx, client, creds, "buckit.service")
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("probe buckit.service: %w", err))
		return err
	}
	if body := renderDropIn(oldProps, newProps); body != "" {
		report(StageSwitchingUnit, "Writing drop-in "+dropInPath)
		if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, writeDropInCmd(body))); err != nil {
			reportErr(StageFailed, fmt.Errorf("write drop-in: %w", err))
			return err
		}
	}

	report(StageDownloadingPkg, "Pre-staged")
	return nil
}

// Switch swaps minio.service → buckit.service atomically on one host:
// stop minio, daemon-reload, disable minio, enable --now buckit. Called
// inside the cluster-wide downtime window AFTER every host's PreStage
// has succeeded. Per-host health verification is the executor's job
// (cluster-wide verify phase); Switch only enables the unit.
func (in *Installer) Switch(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}
	if in == nil || in.Pool == nil {
		return errors.New("switch: nil installer / pool")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
		return err
	}

	// Stop minio.service — downtime begins on this host. A non-zero
	// exit is OK (operator may have manually disabled the unit).
	report(StageStoppingMinio, "systemctl stop minio.service")
	stopCmd := deploy.SudoWrap(creds, "systemctl stop minio.service || true")
	if err := deploy.RunStep(ctx, client, stopCmd); err != nil {
		reportErr(StageFailed, fmt.Errorf("stop minio: %w", err))
		return err
	}

	// Swap units in one shot: daemon-reload picks up the drop-in
	// written during PreStage; disable minio.service so it doesn't
	// fight buckit on the next reboot; enable --now buckit.service
	// to start it immediately.
	report(StageSwitchingUnit, "systemctl swap minio.service → buckit.service")
	swap := strings.Join([]string{
		"systemctl daemon-reload",
		"systemctl disable minio.service || true",
		"systemctl enable --now buckit.service",
	}, " && ")
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, swap)); err != nil {
		reportErr(StageFailed, fmt.Errorf("swap unit: %w", err))
		return err
	}

	report(StageWaitingHealth, "buckit.service started")
	return nil
}

// renderDropIn produces the drop-in body when the new buckit.service's
// User / Group / EnvironmentFile set differs from the old minio.service's.
// Returns "" when everything already matches — no drop-in, nothing for
// rollback to clean up.
//
// Defaults are *asymmetric* by design:
//   - OLD (minio.service) missing → conventional MinIO packaging value
//     (minio-user / minio-user / /etc/default/minio). The preflight
//     check minioServiceComplete verifies these exist on the host.
//   - NEW (buckit.service) missing → systemd's actual root fallback,
//     because that's what systemd will apply when buckit starts.
//
// For EnvironmentFile we emit the reset idiom (`EnvironmentFile=` on its
// own line) before re-adding the list — EnvironmentFile= is list-style
// additive in systemd, so a bare drop-in would append rather than replace.
func renderDropIn(old, new deploy.UnitProps) string {
	const systemdDefaultUser = "root"
	oldUser := orDefault(old.User, fallbackMinioUser)
	newUser := orDefault(new.User, systemdDefaultUser)
	oldGroup := orDefault(old.Group, fallbackMinioGroup)
	newGroup := orDefault(new.Group, systemdDefaultUser)
	oldEnv := old.EnvironmentFiles
	if len(oldEnv) == 0 {
		oldEnv = []string{fallbackMinioEnvFile}
	}

	var lines []string
	if oldUser != newUser {
		lines = append(lines, "User="+oldUser)
	}
	if oldGroup != newGroup {
		lines = append(lines, "Group="+oldGroup)
	}
	if !stringSliceEqual(oldEnv, new.EnvironmentFiles) {
		// Reset the inherited list first; without this the entries we
		// add below would *append* to whatever buckit.service ships.
		lines = append(lines, "EnvironmentFile=")
		for _, p := range oldEnv {
			// Preserve the `-` ignore-errors marker so a missing file on
			// the buckit side doesn't fail-start the unit (mirrors the
			// stock minio.service convention).
			lines = append(lines, "EnvironmentFile=-"+p)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "# Written by bm during MinIO → Buckit cutover. Removed on rollback.\n" +
		"[Service]\n" +
		strings.Join(lines, "\n") + "\n"
}

// writeDropInCmd returns a shell command that writes body to dropInPath.
// Uses a here-doc with a quoted sentinel so $variables in body are not
// expanded by the remote shell.
//
// The steps are joined with newlines (not " && ") because the heredoc
// terminator MUST be on a line by itself — joining with " && " puts the
// terminator on the same line as the chmod command and bash never sees
// the end of the heredoc, dumping the trailing `BMDROPIN && chmod …`
// text into the drop-in file. `set -e` preserves fail-fast behavior.
func writeDropInCmd(body string) string {
	return strings.Join([]string{
		"set -e",
		"mkdir -p " + dropInDir,
		"tee " + dropInPath + " > /dev/null <<'BMDROPIN'\n" + body + "BMDROPIN",
		"chmod 644 " + dropInPath,
	}, "\n")
}

// removeDropInCmd returns a shell command that removes the drop-in file
// and the parent directory if it's empty afterward. Safe to run when
// the drop-in was never written — rm -f swallows missing-file errors.
func removeDropInCmd() string {
	return strings.Join([]string{
		"rm -f " + dropInPath,
		"rmdir " + dropInDir + " 2>/dev/null || true",
	}, " && ")
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// waitHealthy polls /minio/health/live until 200 or the deadline passes.
// The cluster-wide health-wait happens at the executor level, between hosts.
func (in *Installer) waitHealthy(ctx context.Context, client *ssh.Client, params CutoverParams) error {
	port := params.API.Port
	if port == 0 {
		// Buckit reads MINIO_OPTS from the env file for the listen
		// address; we don't know it definitively without re-parsing.
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

// StopBuckit performs the buckit-side teardown on one host: disable buckit,
// uninstall the package, remove the drop-in directory. Safe to fan out
// across hosts in parallel — every step is local to its own host and
// idempotent against repeated invocations.
//
// Rollback must split into two phases (StopBuckit then StartMinio) so that
// MinIO's distributed bootstrap doesn't reject startup. If we ran
// stop+start back-to-back per host, the first host to start minio would
// dial peers still running buckit, see a binary-checksum mismatch, and
// refuse to join. Stopping ALL buckits first leaves no buckit running
// anywhere when minio comes up.
func (in *Installer) StopBuckit(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}
	if in == nil || in.Pool == nil {
		return errors.New("stop-buckit: nil installer / pool")
	}
	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
		return err
	}

	report(StageRolledBack, "Stopping buckit.service")
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, "systemctl disable --now buckit.service || true")); err != nil {
		reportErr(StageFailed, fmt.Errorf("stop buckit: %w", err))
		return err
	}

	// Uninstall the buckit package. Detect the manager fresh — Rollback
	// is a separate entry point that may run weeks later against a host
	// where the pkg mgr binary moved.
	report(StageRolledBack, "Uninstalling buckit package")
	mgr, mgrErr := deploy.DetectPackageManagerForClient(ctx, client)
	if mgrErr != nil {
		report(StageRolledBack, "Skipping uninstall (no package manager detected): "+mgrErr.Error())
	} else if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, mgr.UninstallCommand())); err != nil {
		reportErr(StageFailed, fmt.Errorf("uninstall buckit: %w", err))
		return err
	}

	// Clean up the drop-in directory left under /etc/systemd/system —
	// outside the package install paths, so uninstall doesn't touch it.
	report(StageRolledBack, "Removing drop-in "+dropInPath)
	if err := deploy.RunStep(ctx, client, deploy.SudoWrap(creds, removeDropInCmd())); err != nil {
		reportErr(StageFailed, fmt.Errorf("remove drop-in: %w", err))
		return err
	}

	return nil
}

// StartMinio re-enables minio.service on one host. Must run AFTER every
// attempted host has finished StopBuckit — otherwise the distributed
// bootstrap check rejects startup. Safe to fan out across hosts; each
// host's minio retries peer discovery until the rest come up.
func (in *Installer) StartMinio(ctx context.Context, host domain.HostRow, params CutoverParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}
	if in == nil || in.Pool == nil {
		return errors.New("start-minio: nil installer / pool")
	}
	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, "migrate-"+params.SourceClusterID, ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
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

	report(StageRolledBack, "Waiting for minio /health/live")
	if err := in.waitHealthy(ctx, client, params); err != nil {
		reportErr(StageFailed, fmt.Errorf("minio health: %w", err))
		return err
	}

	report(StageRolledBack, "MinIO restored")
	return nil
}
