package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/domain"
	bmssh "github.com/buckit-io/bm/internal/ssh"
)

// Stage names match web/src/pages/wizards/new/state.ts:DeployNodeState.state.
type Stage string

const (
	StagePending       Stage = "pending"
	StageDownloading   Stage = "downloading"
	StageInstalling    Stage = "installing"
	StageWritingConfig Stage = "writing_config"
	StageStarting      Stage = "starting"
	StageHealthy       Stage = "healthy"
	StageFailed        Stage = "failed"
)

// StepEvent is the per-host progress signal the install pipeline emits.
// The executor wraps these into OperationProgress mutations.
type StepEvent struct {
	HostID   string
	Hostname string
	Stage    Stage
	Detail   string
	Err      error
}

// Installer drives a single host through the install pipeline. Reuses the
// SSH pool so multiple sequential hosts amortize handshake cost.
type Installer struct {
	Pool *bmssh.Pool
	// StartTimeout caps how long we wait for systemctl enable --now to return.
	// Default 180s.
	StartTimeout time.Duration
	// HealthyTimeout caps how long we wait for the local /minio/health/live
	// probe after starting the service. Default 60s.
	HealthyTimeout time.Duration
}

// NewInstaller returns an Installer using pool. nil-safe (returns nil-friendly
// Installer that fails the first dial with a clear error).
func NewInstaller(pool *bmssh.Pool) *Installer {
	return &Installer{
		Pool:           pool,
		StartTimeout:   180 * time.Second,
		HealthyTimeout: 60 * time.Second,
	}
}

// Install walks one host through the full deploy pipeline. emit is called on
// every stage transition; callers translate those into UI events. Returns
// the first error that aborted the run, or nil on healthy.
func (in *Installer) Install(ctx context.Context, host domain.HostRow, params DeployParams, emit func(StepEvent)) error {
	creds := bmssh.Merge(params.SSH, host.SSHOverride)
	report := func(stage Stage, detail string) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Detail: detail})
	}
	reportErr := func(stage Stage, err error) {
		emit(StepEvent{HostID: host.ID, Hostname: host.Hostname, Stage: stage, Err: err, Detail: err.Error()})
	}

	if in == nil || in.Pool == nil {
		return errors.New("deploy: nil installer / pool")
	}

	report(StagePending, "Queued")
	if err := ctx.Err(); err != nil {
		return err
	}

	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	client, err := in.Pool.Get(ctx, deployClusterIDPlaceholder(params.Name), ref, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("dial: %w", err))
		return err
	}

	mgr, err := DetectPackageManager(ctx, func(probeCtx context.Context, cmd string) (string, error) {
		r, runErr := bmssh.Run(probeCtx, client, cmd)
		if runErr != nil {
			return r.Stdout, runErr
		}
		return r.Stdout, nil
	})
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("detect package manager: %w", err))
		return err
	}

	artifact, err := params.ArtifactForKind(mgr.Kind())
	if err != nil {
		reportErr(StageFailed, err)
		return err
	}
	expectedSHA256, err := FetchChecksum(ctx, artifact)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	report(StageDownloading, fmt.Sprintf("Fetching %s", artifact.URL))
	if err := runStep(ctx, client, mgr.DownloadCommand(artifact.URL)); err != nil {
		reportErr(StageFailed, fmt.Errorf("download: %w", err))
		return err
	}
	report(StageDownloading, fmt.Sprintf("Verifying %s sha256", mgr.LocalFile()))
	if err := runStep(ctx, client, mgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	report(StageInstalling, fmt.Sprintf("Installing %s", mgr.LocalFile()))
	if err := runStep(ctx, client, mgr.InstallCommand(InstallActionFresh)); err != nil {
		reportErr(StageFailed, fmt.Errorf("install: %w", err))
		return err
	}

	serviceUser, serviceGroup, err := detectServiceUserGroup(ctx, client, creds)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("detect service user/group: %w", err))
		return err
	}

	report(StageWritingConfig, "Preparing storage directories")
	if err := runStep(ctx, client, sudoWrap(creds, prepareStorageCmd(params.Topology.SelectedMounts, serviceUser, serviceGroup))); err != nil {
		reportErr(StageFailed, fmt.Errorf("storage permissions: %w", err))
		return err
	}

	if params.TLS.Enabled() {
		report(StageWritingConfig, "Installing TLS certificate to "+CertsDir)
		certCmd := writeCertsCmd(params.TLS, serviceUser, serviceGroup)
		if err := runStep(ctx, client, sudoWrap(creds, certCmd)); err != nil {
			reportErr(StageFailed, fmt.Errorf("tls certs: %w", err))
			return err
		}
	}

	// Write the secondary env file first so that if its write fails the
	// primary file still has no MINIO_CONFIG_ENV_FILE pointing at a missing
	// target. Mirrors applyRotateRootCredsTarget ordering.
	report(StageWritingConfig, "Writing "+secondaryEnvFile)
	secondaryBody := renderSecondaryConfigEnv(params)
	secondaryCmd := fmt.Sprintf(
		"install -d /etc/minio"+
			" && install -o %s -g %s -m 600 /dev/null %s"+
			" && tee %s > /dev/null <<'BMCFG'\n%sBMCFG",
		ShellEscape(serviceUser),
		ShellEscape(serviceGroup),
		ShellEscape(secondaryEnvFile),
		ShellEscape(secondaryEnvFile),
		secondaryBody,
	)
	if err := runStep(ctx, client, sudoWrap(creds, secondaryCmd)); err != nil {
		reportErr(StageFailed, fmt.Errorf("config secondary: %w", err))
		return err
	}

	report(StageWritingConfig, "Writing /etc/default/minio")
	configBody := renderConfigEnv(params, host)
	cfgCmd := fmt.Sprintf("tee /etc/default/minio > /dev/null <<'BMCFG'\n%s\nBMCFG\nchmod 600 /etc/default/minio", configBody)
	if err := runStep(ctx, client, sudoWrap(creds, cfgCmd)); err != nil {
		reportErr(StageFailed, fmt.Errorf("config: %w", err))
		return err
	}

	report(StageStarting, "systemctl enable --now buckit.service")
	if err := in.runStartStep(ctx, client, host, params, creds, report); err != nil {
		reportErr(StageFailed, fmt.Errorf("systemctl: %w", err))
		return err
	}

	report(StageHealthy, "Waiting for /minio/health/live")
	if err := in.waitHealthy(ctx, client, params.API.Port, params.TLS.Enabled()); err != nil {
		reportErr(StageFailed, fmt.Errorf("health: %w", err))
		return err
	}
	report(StageHealthy, "Service healthy")
	return nil
}

// runStep wraps ssh.Run with the exit-code-is-failure semantic the install
// pipeline assumes.
func runStep(ctx context.Context, client *ssh.Client, cmd string) error {
	return RunStep(ctx, client, cmd)
}

// RunStep wraps ssh.Run with exit-code-is-failure semantics. Exported for
// reuse by the migration installer; nonzero exit is wrapped in a plain error
// containing stderr.
func RunStep(ctx context.Context, client *ssh.Client, cmd string) error {
	r, err := bmssh.Run(ctx, client, cmd)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		stderr := strings.TrimSpace(r.Stderr)
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", r.ExitCode)
		}
		return fmt.Errorf("%s", stderr)
	}
	return nil
}

func (in *Installer) runStartStep(ctx context.Context, client *ssh.Client, host domain.HostRow, params DeployParams, creds bmssh.Resolved, report func(Stage, string)) error {
	timeout := in.StartTimeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := sudoWrap(creds, "systemctl daemon-reload && systemctl enable --now buckit.service")
	err := runStep(startCtx, client, cmd)
	if err == nil {
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	report(StageStarting, fmt.Sprintf("Timed out after %s; dropping SSH client and reconnecting for diagnostics", timeout))
	diagClient := client
	if fresh, reacquireErr := in.reconnectHost(ctx, host, params, creds); reacquireErr == nil && fresh != nil {
		report(StageStarting, "Reconnected SSH client for timeout diagnostics")
		diagClient = fresh
	} else if reacquireErr != nil {
		report(StageStarting, "Reconnect for timeout diagnostics failed: "+reacquireErr.Error())
	}

	report(StageStarting, "Collecting systemd status and journal after start timeout")
	diag := in.collectStartDiagnostics(ctx, diagClient, creds)
	report(StageStarting, "Stopping buckit.service after timeout")
	_ = stopServiceBestEffort(ctx, diagClient, creds)
	if diag != "" {
		return fmt.Errorf("timed out after %s waiting for buckit.service to start\n%s", timeout, diag)
	}
	return fmt.Errorf("timed out after %s waiting for buckit.service to start", timeout)
}

func (in *Installer) reconnectHost(ctx context.Context, host domain.HostRow, params DeployParams, creds bmssh.Resolved) (*ssh.Client, error) {
	if in == nil || in.Pool == nil {
		return nil, errors.New("deploy: nil installer / pool")
	}
	clusterID := deployClusterIDPlaceholder(params.Name)
	in.Pool.Drop(clusterID, host.ID)
	reconnectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ref := domain.HostRef{ID: host.ID, Hostname: host.Hostname, Port: host.Port}
	return in.Pool.Get(reconnectCtx, clusterID, ref, creds)
}

func (in *Installer) collectStartDiagnostics(ctx context.Context, client *ssh.Client, creds bmssh.Resolved) string {
	diagCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	parts := make([]string, 0, 2)
	if status, err := captureCommand(diagCtx, client, sudoWrap(creds, "systemctl status buckit.service --no-pager -l || true")); err == nil && status != "" {
		parts = append(parts, "systemctl status buckit.service:\n"+status)
	}
	if journal, err := captureCommand(diagCtx, client, sudoWrap(creds, "journalctl -u buckit.service -n 100 --no-pager || true")); err == nil && journal != "" {
		parts = append(parts, "journalctl -u buckit.service -n 100:\n"+journal)
	}
	return strings.Join(parts, "\n\n")
}

func stopServiceBestEffort(ctx context.Context, client *ssh.Client, creds bmssh.Resolved) error {
	stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return runStep(stopCtx, client, sudoWrap(creds, "systemctl stop buckit.service || true"))
}

func captureCommand(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	r, err := bmssh.Run(ctx, client, cmd)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(r.Stdout)
	if out == "" {
		out = strings.TrimSpace(r.Stderr)
	}
	return out, nil
}

func (in *Installer) waitHealthy(ctx context.Context, client *ssh.Client, port int, tls bool) error {
	if port == 0 {
		port = 9000
	}
	// 127.0.0.1 is rarely in the cert SANs, so skip verification on this
	// loopback liveness probe. Real verification happens against the
	// cluster's external URL via admin creds elsewhere.
	curlOpts := "-fsS --max-time 5"
	scheme := "http"
	if tls {
		curlOpts += " -k"
		scheme = "https"
	}
	probe := fmt.Sprintf("curl %s %s://127.0.0.1:%d/minio/health/live", curlOpts, scheme, port)
	deadline := time.Now().Add(in.HealthyTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := bmssh.Run(ctx, client, probe)
		if err == nil && r.ExitCode == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy after %s", in.HealthyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// detectServiceUserGroup reads User= and Group= from the installed
// buckit.service unit via `systemctl show`. This mirrors the probe in
// internal/operations/rotate_root_creds.go and avoids hardcoding
// buckit:buckit in the deploy flow, so any future RPM that renames the
// service user is honoured. systemd's own default of root:root applies when
// the unit specifies neither field; we mirror that fallback here.
func detectServiceUserGroup(ctx context.Context, client *ssh.Client, creds bmssh.Resolved) (string, string, error) {
	cmd := "systemctl show buckit.service -p User -p Group"
	r, err := bmssh.Run(ctx, client, sudoWrap(creds, cmd))
	if err != nil {
		return "", "", err
	}
	if r.ExitCode != 0 {
		stderr := strings.TrimSpace(r.Stderr)
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", r.ExitCode)
		}
		return "", "", fmt.Errorf("systemctl show: %s", stderr)
	}
	user, group := "root", "root"
	for _, line := range strings.Split(r.Stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "User="):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "User=")); v != "" {
				user = v
			}
		case strings.HasPrefix(line, "Group="):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "Group=")); v != "" {
				group = v
			}
		}
	}
	return user, group, nil
}

// DetectPackageManagerForClient runs DetectPackageManager against an
// already-open *ssh.Client. Convenience for the migration installer.
func DetectPackageManagerForClient(ctx context.Context, client *ssh.Client) (PackageManager, error) {
	return DetectPackageManager(ctx, func(probeCtx context.Context, cmd string) (string, error) {
		r, err := bmssh.Run(probeCtx, client, cmd)
		if err != nil {
			return r.Stdout, err
		}
		return r.Stdout, nil
	})
}

// secondaryEnvFile is the canonical path for the secondary env file that
// holds MINIO_ROOT_USER / MINIO_ROOT_PASSWORD. Kept in sync with
// buckitSecondaryEnvFile in internal/operations/rotate_root_creds.go so that
// rotate's NeedsNormalization check returns false for freshly deployed
// clusters.
const secondaryEnvFile = "/etc/minio/config.env"

// renderConfigEnv writes the /etc/default/minio body with the path to the
// secondary env file (which carries the root credentials), region, listen
// ports, selected MINIO_VOLUMES, and the erasure storage class. When TLS is
// enabled the rendered scheme flips to https and MINIO_OPTS gains --certs-dir.
// MINIO_SERVER_URL is only written when the operator explicitly sets a
// cluster-wide server URL; deriving a per-host default makes distributed nodes
// reject each other due to mismatched MINIO_* environment values.
func renderConfigEnv(p DeployParams, _ domain.HostRow) string {
	opts := fmt.Sprintf("--address :%d --console-address :%d", p.API.Port, p.API.ConsolePort)
	if p.TLS.Enabled() {
		opts = fmt.Sprintf("%s --certs-dir %s", opts, CertsDir)
	}
	volumes := renderVolumes(p)
	serverURL := strings.TrimSpace(p.ServerURL)
	var b strings.Builder
	b.WriteString(formatEnv("MINIO_CONFIG_ENV_FILE", secondaryEnvFile))
	b.WriteString(formatEnv("MINIO_VOLUMES", volumes))
	b.WriteString(formatEnv("MINIO_REGION", p.Region))
	b.WriteString(formatEnv("MINIO_OPTS", opts))
	if p.totalDriveCount() > 1 {
		setSize := p.resolvedSetSize()
		parity := p.effectiveParity()
		if setSize != p.defaultSetSize() {
			b.WriteString(formatEnv("MINIO_ERASURE_SET_DRIVE_COUNT", fmt.Sprintf("%d", setSize)))
		}
		if parity != defaultParityBlocks(setSize) {
			b.WriteString(formatEnv("MINIO_STORAGE_CLASS_STANDARD", fmt.Sprintf("EC:%d", parity)))
		}
	}
	if serverURL != "" {
		b.WriteString(formatEnv("MINIO_SERVER_URL", serverURL))
	}
	return b.String()
}

// renderSecondaryConfigEnv writes the /etc/minio/config.env body. This file
// holds the root credentials so they can be rotated in place without
// rewriting /etc/default/minio. The two-file layout mirrors what the
// rotate_root_creds operation expects.
func renderSecondaryConfigEnv(p DeployParams) string {
	var b strings.Builder
	b.WriteString(formatEnv("MINIO_ROOT_USER", p.Credentials.RootUser))
	b.WriteString(formatEnv("MINIO_ROOT_PASSWORD", p.Credentials.RootPassword))
	return b.String()
}

func formatEnv(k, v string) string { return fmt.Sprintf("%s=%q\n", k, v) }

// writeCertsCmd builds the shell pipeline that lays down the TLS material
// MinIO reads on startup. Files land under CertsDir; the directory is mode
// 0700 and the cert files are mode 0600 owned by the service user. Uses
// `install` for atomic-create + mode/owner in one syscall and a heredoc per
// file. The same writer pattern that lays down /etc/minio/config.env.
func writeCertsCmd(tls domain.TLSConfig, user, group string) string {
	u, g := ShellEscape(user), ShellEscape(group)
	var b strings.Builder
	b.WriteString("set -e\n")
	// install -o takes only a username; group is a separate -g flag.
	b.WriteString("install -d -o " + u + " -g " + g + " -m 700 " + ShellEscape(CertsDir) + "\n")
	b.WriteString(installPemFile(CertPath, u, g, "600", tls.CertPEM))
	b.WriteString(installPemFile(KeyPath, u, g, "600", tls.KeyPEM))
	if strings.TrimSpace(tls.CABundlePEM) != "" {
		b.WriteString("install -d -o " + u + " -g " + g + " -m 755 " + ShellEscape(CertsDir+"/CAs") + "\n")
		b.WriteString(installPemFile(CABundlePath, u, g, "644", tls.CABundlePEM))
	}
	return b.String()
}

func installPemFile(path, user, group, mode, body string) string {
	quoted := ShellEscape(path)
	// Per-call random heredoc terminator: a fixed marker (e.g. "BMPEM") could
	// collide with operator-supplied PEM content and either truncate the write
	// or inject the remainder as shell. 16 hex chars = 64 bits of entropy;
	// collision with an arbitrary cert body is effectively impossible.
	marker := randomHeredocMarker()
	return fmt.Sprintf(
		"install -o %s -g %s -m %s /dev/null %s\n"+
			"tee %s > /dev/null <<'%s'\n%s%s\n",
		user, group, mode, quoted, quoted, marker, ensureTrailingNewline(body), marker,
	)
}

func randomHeredocMarker() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// rand.Read on a sane OS never errors; if it did, fall back to a
		// timestamp-derived marker — still unlikely to collide.
		return fmt.Sprintf("BMPEM%d", time.Now().UnixNano())
	}
	return "BMPEM_" + hex.EncodeToString(b)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func prepareStorageCmd(mounts []string, user, group string) string {
	owner := ShellEscape(user) + ":" + ShellEscape(group)
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, m := range mounts {
		quoted := ShellEscape(storagePathForMount(m))
		b.WriteString("mkdir -p " + quoted + "\n")
		b.WriteString("chown " + owner + " " + quoted + "\n")
		b.WriteString("chmod 755 " + quoted + "\n")
	}
	return b.String()
}

// sudoWrap prepends `sudo -n bash -c "..."` when ssh.user != root. Production
// requires passwordless sudo (validated by the preflight).
func sudoWrap(creds bmssh.Resolved, cmd string) string {
	return SudoWrap(creds, cmd)
}

// SudoWrap is the exported variant the migration package reuses. Wraps cmd
// in `sudo -n bash -c '...'` when the resolved creds aren't root and Sudo is
// set. Production requires passwordless sudo (validated by preflight).
func SudoWrap(creds bmssh.Resolved, cmd string) string {
	if creds.User == "root" {
		return cmd
	}
	if !creds.Sudo {
		return cmd
	}
	return "sudo -n bash -c " + ShellEscape(cmd)
}

func shellEscape(s string) string {
	return ShellEscape(s)
}

// ShellEscape single-quotes s for safe shell interpolation. Exported for
// reuse by the migration installer.
func ShellEscape(s string) string {
	if !strings.ContainsAny(s, " \t\"'$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// deployClusterIDPlaceholder is the synthetic clusterID used while the
// install is in flight — the real cluster row doesn't exist yet. Keeping
// per-host SSH clients keyed by a stable id lets the pool reuse the
// connection across the discover + preflight + install passes.
func deployClusterIDPlaceholder(name string) string { return "deploy-" + name }
