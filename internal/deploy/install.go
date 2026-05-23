package deploy

import (
	"context"
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
	// HealthyTimeout caps how long we wait for the local /minio/health/live
	// probe after starting the service. Default 60s.
	HealthyTimeout time.Duration
}

// NewInstaller returns an Installer using pool. nil-safe (returns nil-friendly
// Installer that fails the first dial with a clear error).
func NewInstaller(pool *bmssh.Pool) *Installer {
	return &Installer{Pool: pool, HealthyTimeout: 60 * time.Second}
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

	url, err := params.ArtifactURL()
	if err != nil {
		reportErr(StageFailed, err)
		return err
	}
	artifact := CustomRPMArtifact(url)
	if params.Version != "custom" {
		arch, err := params.clusterArch()
		if err != nil {
			reportErr(StageFailed, err)
			return err
		}
		artifact, err = ResolveRPMArtifact(params.Version, arch)
		if err != nil {
			reportErr(StageFailed, fmt.Errorf("deploy: %w", err))
			return err
		}
	}
	expectedSHA256, err := FetchRPMChecksum(ctx, artifact)
	if err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	report(StageDownloading, fmt.Sprintf("Fetching %s", url))
	if err := runStep(ctx, client, DownloadRPMCommand(url)); err != nil {
		reportErr(StageFailed, fmt.Errorf("download: %w", err))
		return err
	}
	report(StageDownloading, "Verifying /tmp/buckit.rpm sha256")
	if err := runStep(ctx, client, VerifyRPMChecksumCommand(expectedSHA256)); err != nil {
		reportErr(StageFailed, fmt.Errorf("checksum: %w", err))
		return err
	}

	report(StageInstalling, "Installing /tmp/buckit.rpm")
	pkgCmd := pickInstallCmd(ctx, client)
	if err := runStep(ctx, client, pkgCmd); err != nil {
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
	if err := runStep(ctx, client, sudoWrap(creds, "systemctl daemon-reload && systemctl enable --now buckit.service")); err != nil {
		reportErr(StageFailed, fmt.Errorf("systemctl: %w", err))
		return err
	}

	report(StageHealthy, "Waiting for /minio/health/live")
	if err := in.waitHealthy(ctx, client, params.API.Port); err != nil {
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

func (in *Installer) waitHealthy(ctx context.Context, client *ssh.Client, port int) error {
	if port == 0 {
		port = 9000
	}
	deadline := time.Now().Add(in.HealthyTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := bmssh.Run(ctx, client, fmt.Sprintf("curl -fsS --max-time 5 http://127.0.0.1:%d/minio/health/live", port))
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

// pickInstallCmd picks the right package manager by probing for one.
func pickInstallCmd(ctx context.Context, client *ssh.Client) string {
	return PickInstallCmd(ctx, client)
}

// PickInstallCmd is the exported variant the migration package reuses for
// the cutover install step. Probes for dnf / yum / apt-get and returns the
// command that installs /tmp/buckit.rpm.
func PickInstallCmd(ctx context.Context, client *ssh.Client) string {
	for _, mgr := range []string{"dnf", "yum", "apt-get"} {
		r, err := bmssh.Run(ctx, client, "command -v "+mgr)
		if err == nil && r.ExitCode == 0 {
			switch mgr {
			case "dnf", "yum":
				return mgr + " install -y /tmp/buckit.rpm"
			case "apt-get":
				return "apt-get install -y /tmp/buckit.rpm"
			}
		}
	}
	// Fall back to dnf — the preflight rejects hosts that lack it.
	return "dnf install -y /tmp/buckit.rpm"
}

// secondaryEnvFile is the canonical path for the secondary env file that
// holds MINIO_ROOT_USER / MINIO_ROOT_PASSWORD. Kept in sync with
// buckitSecondaryEnvFile in internal/operations/rotate_root_creds.go so that
// rotate's NeedsNormalization check returns false for freshly deployed
// clusters.
const secondaryEnvFile = "/etc/minio/config.env"

// renderConfigEnv writes the /etc/default/minio body with the path to the
// secondary env file (which carries the root credentials), region, listen
// ports, and the selected MINIO_VOLUMES.
func renderConfigEnv(p DeployParams, host domain.HostRow) string {
	volumes := renderVolumes(p)
	serverURL := strings.TrimSpace(p.ServerURL)
	if serverURL == "" {
		serverURL = fmt.Sprintf("http://%s:%d", host.Hostname, p.API.Port)
	}
	var b strings.Builder
	b.WriteString(formatEnv("MINIO_CONFIG_ENV_FILE", secondaryEnvFile))
	b.WriteString(formatEnv("MINIO_VOLUMES", volumes))
	b.WriteString(formatEnv("MINIO_REGION", p.Region))
	b.WriteString(formatEnv("MINIO_OPTS", fmt.Sprintf("--address :%d --console-address :%d", p.API.Port, p.API.ConsolePort)))
	b.WriteString(formatEnv("MINIO_SERVER_URL", serverURL))
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
