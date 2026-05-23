package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/credentials"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

const (
	buckitPrimaryEnvFile   = "/etc/default/minio"
	buckitSecondaryEnvFile = "/etc/minio/config.env"
)

var rotateRootCredsPostRestartWait = WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}
var rotateRootCredsRestartRequestTimeout = 2 * time.Minute

type rotateRootCredsParams struct {
	NewPassword string `json:"newPassword"`
}

type rotateRootCredsExecutor struct{ deps Deps }

func (e *rotateRootCredsExecutor) Validate(req tasks.DispatchRequest) error {
	var p rotateRootCredsParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return fmt.Errorf("rotate_root_creds: invalid params: %w", err)
	}
	if err := validateRotateRootCredsParams(p); err != nil {
		return err
	}
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpRotateRootCreds)
}

func (e *rotateRootCredsExecutor) Execute(ctx context.Context, run *tasks.Run) (retErr error) {
	defer func() {
		if retErr == nil {
			return
		}
		finalizePendingRotateHosts(run, "aborted after an earlier failure")
	}()

	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpRotateRootCreds); err != nil {
		return err
	}

	var p rotateRootCredsParams
	if err := json.Unmarshal(run.Params, &p); err != nil {
		return fmt.Errorf("rotate_root_creds: invalid params: %w", err)
	}
	if err := validateRotateRootCredsParams(p); err != nil {
		return err
	}

	start := time.Now()
	seedHostStatuses(run, rc.nodes)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Checking cluster health"
	})
	run.LogInfo("checking cluster health before rotating root password")
	info, err := rc.admin.ServerInfo(ctx)
	if err != nil {
		return fmt.Errorf("preflight health: %w", err)
	}
	if !allOnline(info) {
		run.LogError("cluster is not healthy; refusing to rotate root password")
		return errors.New("preflight health: cluster must be healthy before rotating root password")
	}
	run.LogOK("cluster healthy; proceeding with root password rotation")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Resolving root credential files"
	})

	plans := make([]rotateRootCredsTarget, 0, len(rc.nodes))
	for i, n := range rc.nodes {
		target, err := resolveRotateRootCredsTarget(ctx, e.deps, rc, n, run.TaskID)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s preflight: %w", n.Hostname, err)
		}
		plans = append(plans, target)
		setHostState(run, i, n, tasks.HostPending, "staged")
		run.LogInfo("%s: target root env file %s", n.Hostname, target.SecondaryPath)
	}
	if len(plans) > 0 {
		sameAsCurrent := true
		for _, target := range plans {
			if p.NewPassword != target.CurrentPassword {
				sameAsCurrent = false
				break
			}
		}
		if sameAsCurrent {
			return errors.New("rotate_root_creds: selected password is unchanged")
		}
	}

	normalizePlans := make([]rotateRootCredsTarget, 0, len(plans))
	for _, target := range plans {
		if target.NeedsNormalization {
			normalizePlans = append(normalizePlans, target)
		}
	}
	if len(normalizePlans) > 0 {
		run.LogInfo("normalizing legacy nodes into MINIO_CONFIG_ENV_FILE")
		run.MutateState(func(s *tasks.OperationProgress) {
			s.Detail = "Normalizing legacy nodes to secondary env files"
		})
		appliedNormalize := make([]rotateRootCredsTarget, 0, len(normalizePlans))
		for _, target := range normalizePlans {
			idx := indexOfNode(plans, target.Node.ID)
			if err := ctx.Err(); err != nil {
				return err
			}
			setHostState(run, idx, target.Node, tasks.HostRunning, "writing normalization")
			normalizeTarget := target.withPassword(target.CurrentPassword)
			if err := applyRotateRootCredsTarget(ctx, e.deps, rc, normalizeTarget); err != nil {
				setHostState(run, idx, target.Node, tasks.HostFailed, err.Error())
				run.LogError("%s: normalization write failed, rolling back prior nodes", target.Node.Hostname)
				rollbackRotateRootCredTargets(ctx, run, e.deps, rc, append(appliedNormalize, normalizeTarget))
				return fmt.Errorf("%s normalization write: %w", target.Node.Hostname, err)
			}
			appliedNormalize = append(appliedNormalize, normalizeTarget)
			for i := range plans {
				if plans[i].Node.ID != normalizeTarget.Node.ID {
					continue
				}
				plans[i].PrimaryText = normalizeTarget.NewPrimaryText
				plans[i].SecondaryText = normalizeTarget.NewSecondaryText
				plans[i].SecondaryExists = true
				plans[i].NeedsNormalization = false
				break
			}
			setHostState(run, idx, target.Node, tasks.HostPending, "normalized")
			run.LogOK("%s: normalization written", target.Node.Hostname)
		}

		run.LogInfo("rolling systemctl restart to load MINIO_CONFIG_ENV_FILE")
		run.MutateState(func(s *tasks.OperationProgress) {
			s.Detail = "Rolling restart to load secondary env files"
		})
		restartedTargets, err := rollingRestartTargets(ctx, run, e.deps, rc, normalizePlans)
		if err != nil {
			run.LogError("normalization restart failed; restoring original env files on already-normalized nodes")
			rollbackTargets := append([]rotateRootCredsTarget(nil), restartedTargets...)
			rollbackTargets = append(rollbackTargets, normalizePlans[len(restartedTargets)])
			if rbErr := rollbackRotateRootCredTargetsWithRestart(ctx, run, e.deps, rc, rollbackTargets); rbErr != nil {
				return fmt.Errorf("%v; rollback recovery incomplete: %w", err, rbErr)
			}
			return err
		}
	}

	run.LogInfo("writing updated root password to all nodes")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Writing root password to all nodes"
	})
	applied := make([]rotateRootCredsTarget, 0, len(plans))
	for i, target := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, target.Node, tasks.HostRunning, "writing env files")
		passwordTarget := target.withPassword(p.NewPassword)
		if err := applyRotateRootCredsTarget(ctx, e.deps, rc, passwordTarget); err != nil {
			setHostState(run, i, target.Node, tasks.HostFailed, err.Error())
			run.LogError("%s: write failed, rolling back prior nodes", target.Node.Hostname)
			rollbackRotateRootCredTargets(ctx, run, e.deps, rc, append(applied, passwordTarget))
			return fmt.Errorf("%s write: %w", target.Node.Hostname, err)
		}
		applied = append(applied, passwordTarget)
		setHostState(run, i, target.Node, tasks.HostPending, "written")
		run.LogOK("%s: env files updated", target.Node.Hostname)
	}

	newCreds := domain.AdminCreds{
		URL:       rc.adminCreds.URL,
		AccessKey: rc.adminCreds.AccessKey,
		SecretKey: p.NewPassword,
		Insecure:  rc.adminCreds.Insecure,
	}

	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Requesting in-place restart"
	})
	run.LogInfo("calling admin ServiceRestart")
	run.LogInfo("waiting for restart request to complete; the admin connection may drop while buckit re-execs")
	restartCtx, cancelRestart := context.WithTimeout(ctx, rotateRootCredsRestartRequestTimeout)
	err = rc.admin.ServiceRestart(restartCtx)
	cancelRestart()
	if err != nil {
		run.LogError("in-place restart request failed; env files contain the new password")
		return fmt.Errorf("admin restart: %w; env files were updated but the restart request failed. Retry with Restart cluster or Rolling restart from the action menu, then verify cluster health", err)
	}

	if err := persistRotatedAdminCreds(ctx, e.deps, rc.cluster.ID, newCreds); err != nil {
		run.LogError("failed to persist rotated admin credentials after restart")
		return fmt.Errorf("persist new admin creds: %w; the cluster is now running on the new password but bm could not save it. Update the stored credentials via the \"Configure admin credentials\" menu action", err)
	}
	if e.deps.AdminPool != nil {
		e.deps.AdminPool.Drop(rc.cluster.ID)
	}
	newAdmin, err := freshAdminClient(e.deps, rc.cluster.ID, newCreds)
	if err != nil {
		return fmt.Errorf("admin client refresh: %w", err)
	}

	run.LogInfo("restart request accepted; waiting for cluster healthy with rotated credentials")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Waiting for cluster healthy with rotated credentials"
	})
	if err := waitClusterHealthy(ctx, newAdmin, rotateRootCredsPostRestartWait); err != nil {
		run.LogError("cluster did not become healthy with rotated credentials within 2m")
		return fmt.Errorf("post-rotation health: %w; new admin credentials were saved. Use Restart cluster or Rolling restart from the action menu, then verify cluster health", err)
	}
	run.LogOK("cluster healthy with rotated credentials")
	for i, target := range plans {
		setHostState(run, i, target.Node, tasks.HostSucceeded, "healthy")
	}

	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Root password rotated"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Root user", Value: rc.adminCreds.AccessKey},
			{Label: "Elapsed", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, newAdmin)
	return nil
}

func validateRotateRootCredsParams(p rotateRootCredsParams) error {
	if err := credentials.ValidateRootPassword(p.NewPassword); err != nil {
		return fmt.Errorf("rotate_root_creds: %w", err)
	}
	return nil
}

type rotateRootCredsTarget struct {
	Node               domain.Node
	PrimaryPath        string
	SecondaryPath      string
	PrimaryText        string
	PrimaryExists      bool
	SecondaryText      string
	SecondaryExists    bool
	PrimaryBackup      string
	SecondaryBackup    string
	NewPrimaryText     string
	NewSecondaryText   string
	CurrentUser        string
	CurrentPassword    string
	NeedsNormalization bool
	ServiceUser        string
	ServiceGroup       string
}

func resolveRotateRootCredsTarget(
	ctx context.Context,
	deps Deps,
	rc *runCtx,
	n domain.Node,
	taskID string,
) (rotateRootCredsTarget, error) {
	primaryText, primaryExists, err := readRemoteOptionalFile(ctx, deps, rc, n, buckitPrimaryEnvFile)
	if err != nil {
		return rotateRootCredsTarget{}, err
	}
	if !primaryExists {
		return rotateRootCredsTarget{}, fmt.Errorf("%s not found", buckitPrimaryEnvFile)
	}
	primaryEnv, err := parseEnvFile(primaryText)
	if err != nil {
		return rotateRootCredsTarget{}, fmt.Errorf("parse %s: %w", buckitPrimaryEnvFile, err)
	}
	if primaryEnv.get("MINIO_ROOT_USER_FILE") != "" || primaryEnv.get("MINIO_ROOT_PASSWORD_FILE") != "" {
		return rotateRootCredsTarget{}, fmt.Errorf("%s uses MINIO_ROOT_*_FILE indirection, which rotate_root_creds does not support", buckitPrimaryEnvFile)
	}

	configPath := strings.TrimSpace(primaryEnv.get("MINIO_CONFIG_ENV_FILE"))
	hasRuntimeConfigPath := configPath != ""
	if configPath == "" {
		configPath = buckitSecondaryEnvFile
	}
	serviceUser, serviceGroup, err := detectServiceUserGroup(ctx, deps, rc, n)
	if err != nil {
		return rotateRootCredsTarget{}, fmt.Errorf("detect service user/group: %w", err)
	}
	secondaryText, secondaryExists, err := readRemoteOptionalFile(ctx, deps, rc, n, configPath)
	if err != nil {
		return rotateRootCredsTarget{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	secondaryEnv := newEnvFile()
	if secondaryExists {
		secondaryEnv, err = parseEnvFile(secondaryText)
		if err != nil {
			return rotateRootCredsTarget{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
		if secondaryEnv.get("MINIO_ROOT_USER_FILE") != "" || secondaryEnv.get("MINIO_ROOT_PASSWORD_FILE") != "" {
			return rotateRootCredsTarget{}, fmt.Errorf("%s uses MINIO_ROOT_*_FILE indirection, which rotate_root_creds does not support", configPath)
		}
	}

	rootUser, rootPassword, err := resolveCurrentRootCreds(primaryEnv, secondaryEnv, hasRuntimeConfigPath)
	if err != nil {
		return rotateRootCredsTarget{}, err
	}
	if rootUser != rc.adminCreds.AccessKey {
		return rotateRootCredsTarget{}, fmt.Errorf("root user %q does not match stored admin username %q", rootUser, rc.adminCreds.AccessKey)
	}

	nextPrimary := primaryEnv.
		set("MINIO_CONFIG_ENV_FILE", configPath).
		remove("MINIO_ROOT_USER").
		remove("MINIO_ROOT_PASSWORD").
		remove("MINIO_ROOT_USER_OLD").
		remove("MINIO_ROOT_PASSWORD_OLD")

	return rotateRootCredsTarget{
		Node:               n,
		PrimaryPath:        buckitPrimaryEnvFile,
		SecondaryPath:      configPath,
		PrimaryText:        primaryText,
		PrimaryExists:      primaryExists,
		SecondaryText:      secondaryText,
		SecondaryExists:    secondaryExists,
		PrimaryBackup:      buckitPrimaryEnvFile + ".bm-" + taskID + ".bak",
		SecondaryBackup:    configPath + ".bm-" + taskID + ".bak",
		NewPrimaryText:     nextPrimary.render(),
		CurrentUser:        rootUser,
		CurrentPassword:    rootPassword,
		NeedsNormalization: !hasRuntimeConfigPath,
		ServiceUser:        serviceUser,
		ServiceGroup:       serviceGroup,
	}, nil
}

func resolveCurrentRootCreds(primary, secondary envFile, useSecondary bool) (string, string, error) {
	if useSecondary {
		if user, pass, ok := secondary.rootCreds(); ok {
			return user, pass, nil
		}
	}
	if user, pass, ok := primary.rootCreds(); ok {
		return user, pass, nil
	}
	if !useSecondary {
		return "", "", fmt.Errorf("root credentials not found in %s", buckitPrimaryEnvFile)
	}
	if user, pass, ok := secondary.rootCreds(); ok {
		return user, pass, nil
	}
	return "", "", fmt.Errorf("root credentials not found in %s or %s", buckitPrimaryEnvFile, buckitSecondaryEnvFile)
}

func (t rotateRootCredsTarget) withPassword(password string) rotateRootCredsTarget {
	t.NewSecondaryText = newEnvFileFromText(t.SecondaryText).
		set("MINIO_ROOT_USER", t.CurrentUser).
		set("MINIO_ROOT_PASSWORD", password).
		remove("MINIO_ROOT_USER_OLD").
		remove("MINIO_ROOT_PASSWORD_OLD").
		render()
	return t
}

func applyRotateRootCredsTarget(ctx context.Context, deps Deps, rc *runCtx, target rotateRootCredsTarget) error {
	if target.PrimaryExists {
		if err := backupRemoteFile(ctx, deps, rc, target.Node, target.PrimaryPath, target.PrimaryBackup); err != nil {
			return fmt.Errorf("backup %s: %w", target.PrimaryPath, err)
		}
	}
	if target.SecondaryExists {
		if err := backupRemoteFile(ctx, deps, rc, target.Node, target.SecondaryPath, target.SecondaryBackup); err != nil {
			return fmt.Errorf("backup %s: %w", target.SecondaryPath, err)
		}
	}
	if err := writeRemoteFile(ctx, deps, rc, target.Node, target.SecondaryPath, target.NewSecondaryText, target.SecondaryExists, "600"); err != nil {
		return fmt.Errorf("write %s: %w", target.SecondaryPath, err)
	}
	if err := setRemoteFileOwnership(ctx, deps, rc, target.Node, target.SecondaryPath, target.ServiceUser, target.ServiceGroup, "600"); err != nil {
		return fmt.Errorf("fix ownership %s: %w", target.SecondaryPath, err)
	}
	if err := writeRemoteFile(ctx, deps, rc, target.Node, target.PrimaryPath, target.NewPrimaryText, target.PrimaryExists, "644"); err != nil {
		return fmt.Errorf("write %s: %w", target.PrimaryPath, err)
	}
	return nil
}

func rollbackRotateRootCredTargets(ctx context.Context, run *tasks.Run, deps Deps, rc *runCtx, targets []rotateRootCredsTarget) {
	for _, target := range targets {
		if err := restoreRotateRootCredTarget(ctx, deps, rc, target); err != nil {
			run.LogError("%s: rollback failed: %v", target.Node.Hostname, err)
			continue
		}
		run.LogWarn("%s: reverted env file changes", target.Node.Hostname)
	}
}

func restoreRotateRootCredTarget(ctx context.Context, deps Deps, rc *runCtx, target rotateRootCredsTarget) error {
	if err := restoreRemoteFile(ctx, deps, rc, target.Node, target.PrimaryPath, target.PrimaryText, target.PrimaryExists, "644"); err != nil {
		return err
	}
	if err := restoreRemoteFile(ctx, deps, rc, target.Node, target.SecondaryPath, target.SecondaryText, target.SecondaryExists, "600"); err != nil {
		return err
	}
	if target.SecondaryExists {
		if err := setRemoteFileOwnership(ctx, deps, rc, target.Node, target.SecondaryPath, target.ServiceUser, target.ServiceGroup, "600"); err != nil {
			return err
		}
	}
	return nil
}

func rollingRestartTargets(ctx context.Context, run *tasks.Run, deps Deps, rc *runCtx, targets []rotateRootCredsTarget) ([]rotateRootCredsTarget, error) {
	unit := unitName(rc.cluster.Engine)
	completed := make([]rotateRootCredsTarget, 0, len(targets))
	for _, target := range targets {
		idx := indexOfClusterNode(rc.nodes, target.Node.ID)
		if idx < 0 {
			idx = 0
		}
		if err := ctx.Err(); err != nil {
			return completed, err
		}
		setHostState(run, idx, target.Node, tasks.HostRunning, "systemctl restart")
		run.LogInfo("%s: systemctl restart %s", target.Node.Hostname, unit)
		cmd := sudoSystemctl(rc.sshCreds.User, "restart "+unit)
		if _, err := runHostStep(ctx, deps, rc, target.Node, cmd); err != nil {
			setHostState(run, idx, target.Node, tasks.HostFailed, err.Error())
			return completed, fmt.Errorf("%s restart: %w", target.Node.Hostname, err)
		}
		if err := waitHostHealthy(ctx, deps, rc, target.Node, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, idx, target.Node, tasks.HostFailed, err.Error())
			return completed, fmt.Errorf("%s health: %w", target.Node.Hostname, err)
		}
		setHostState(run, idx, target.Node, tasks.HostPending, "normalized")
		run.LogOK("%s: healthy", target.Node.Hostname)
		completed = append(completed, target)
	}
	return completed, nil
}

func rollbackRotateRootCredTargetsWithRestart(ctx context.Context, run *tasks.Run, deps Deps, rc *runCtx, targets []rotateRootCredsTarget) error {
	unit := unitName(rc.cluster.Engine)
	for i := len(targets) - 1; i >= 0; i-- {
		target := targets[i]
		idx := indexOfClusterNode(rc.nodes, target.Node.ID)
		if idx < 0 {
			idx = 0
		}
		run.LogWarn("%s: restoring original env files", target.Node.Hostname)
		if err := restoreRotateRootCredTarget(ctx, deps, rc, target); err != nil {
			setHostState(run, idx, target.Node, tasks.HostFailed, "rollback file restore failed")
			return fmt.Errorf("%s rollback restore: %w", target.Node.Hostname, err)
		}
		setHostState(run, idx, target.Node, tasks.HostRunning, "rollback systemctl restart")
		run.LogWarn("%s: systemctl restart %s to reload original env layout", target.Node.Hostname, unit)
		if _, err := runHostStep(ctx, deps, rc, target.Node, sudoSystemctl(rc.sshCreds.User, "restart "+unit)); err != nil {
			setHostState(run, idx, target.Node, tasks.HostFailed, "rollback restart failed")
			return fmt.Errorf("%s rollback restart: %w", target.Node.Hostname, err)
		}
		if err := waitHostHealthy(ctx, deps, rc, target.Node, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, idx, target.Node, tasks.HostFailed, "rollback health failed")
			return fmt.Errorf("%s rollback health: %w", target.Node.Hostname, err)
		}
		setHostState(run, idx, target.Node, tasks.HostPending, "rolled back")
		run.LogOK("%s: restored original env layout", target.Node.Hostname)
	}
	return nil
}

func finalizePendingRotateHosts(run *tasks.Run, detail string) {
	run.MutateState(func(s *tasks.OperationProgress) {
		for i := range s.HostStatuses {
			if s.HostStatuses[i].State == tasks.HostPending {
				s.HostStatuses[i].State = tasks.HostFailed
				s.HostStatuses[i].Detail = detail
			}
		}
	})
}

func indexOfNode(plans []rotateRootCredsTarget, nodeID string) int {
	for i, target := range plans {
		if target.Node.ID == nodeID {
			return i
		}
	}
	return -1
}

func indexOfClusterNode(nodes []domain.Node, nodeID string) int {
	for i, n := range nodes {
		if n.ID == nodeID {
			return i
		}
	}
	return -1
}

var rotateRootCredsPersistAttempts = 5
var rotateRootCredsPersistBackoff = 500 * time.Millisecond

func persistRotatedAdminCreds(ctx context.Context, deps Deps, clusterID string, creds domain.AdminCreds) error {
	var lastErr error
	for attempt := 1; attempt <= rotateRootCredsPersistAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := deps.ClusterAdmin.Put(ctx, clusterID, creds); err != nil {
			lastErr = err
			if attempt == rotateRootCredsPersistAttempts {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(rotateRootCredsPersistBackoff * time.Duration(attempt)):
			}
			continue
		}
		return nil
	}
	return lastErr
}

func freshAdminClient(deps Deps, clusterID string, creds domain.AdminCreds) (*admin.Client, error) {
	if deps.AdminPool != nil {
		return deps.AdminPool.Get(clusterID, creds)
	}
	return admin.New(creds)
}

func backupRemoteFile(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, src, dst string) error {
	cmd := fmt.Sprintf("cp %s %s", shellQuote(src), shellQuote(dst))
	_, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, cmd))
	return err
}

func setRemoteFileOwnership(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, filePath, user, group, mode string) error {
	cmd := fmt.Sprintf("chown %s:%s %s && chmod %s %s",
		shellQuote(user), shellQuote(group), shellQuote(filePath), shellQuote(mode), shellQuote(filePath))
	_, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, cmd))
	return err
}

func detectServiceUserGroup(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) (string, string, error) {
	unit := unitName(rc.cluster.Engine)
	cmd := fmt.Sprintf("systemctl show %s -p User -p Group", shellQuote(unit))
	out, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, cmd))
	if err != nil {
		return "", "", err
	}
	user := "root"
	group := "root"
	for _, line := range strings.Split(out, "\n") {
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

func readRemoteOptionalFile(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, filePath string) (string, bool, error) {
	const (
		existsMarker  = "__BM_EXISTS__"
		missingMarker = "__BM_MISSING__"
	)
	cmd := fmt.Sprintf("if [ -f %s ]; then printf '%s\\n'; cat %s; else printf '%s\\n'; fi",
		shellQuote(filePath), existsMarker, shellQuote(filePath), missingMarker)
	out, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, cmd))
	if err != nil {
		return "", false, err
	}
	if strings.HasPrefix(out, existsMarker+"\n") {
		return strings.TrimPrefix(out, existsMarker+"\n"), true, nil
	}
	if strings.TrimSpace(out) == missingMarker {
		return "", false, nil
	}
	return "", false, fmt.Errorf("unexpected file probe output for %s", filePath)
}

func writeRemoteFile(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, filePath, body string, existed bool, createMode string) error {
	dir := path.Dir(filePath)
	var cmd strings.Builder
	cmd.WriteString("install -d ")
	cmd.WriteString(shellQuote(dir))
	if !existed {
		cmd.WriteString(" && install -m ")
		cmd.WriteString(createMode)
		cmd.WriteString(" /dev/null ")
		cmd.WriteString(shellQuote(filePath))
	}
	cmd.WriteString(" && tee ")
	cmd.WriteString(shellQuote(filePath))
	cmd.WriteString(" > /dev/null <<'BMCFG'\n")
	cmd.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		cmd.WriteByte('\n')
	}
	cmd.WriteString("BMCFG\n")
	_, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, cmd.String()))
	return err
}

func restoreRemoteFile(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, filePath, body string, existed bool, createMode string) error {
	if !existed {
		_, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, "rm -f "+shellQuote(filePath)))
		return err
	}
	return writeRemoteFile(ctx, deps, rc, n, filePath, body, true, createMode)
}

type envFile struct {
	lines []envLine
}

type envLine struct {
	raw   string
	key   string
	value string
	kind  envLineKind
}

type envLineKind int

const (
	envLineRaw envLineKind = iota
	envLineAssign
)

var envAssignPattern = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

func newEnvFile() envFile {
	return envFile{lines: nil}
}

func newEnvFileFromText(text string) envFile {
	if strings.TrimSpace(text) == "" {
		return newEnvFile()
	}
	f, err := parseEnvFile(text)
	if err != nil {
		return newEnvFile()
	}
	return f
}

func parseEnvFile(text string) (envFile, error) {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	out := envFile{lines: make([]envLine, 0, len(lines))}
	for _, raw := range lines {
		m := envAssignPattern.FindStringSubmatch(raw)
		if len(m) != 3 {
			out.lines = append(out.lines, envLine{raw: raw, kind: envLineRaw})
			continue
		}
		out.lines = append(out.lines, envLine{
			raw:   raw,
			key:   m[1],
			value: parseEnvValue(strings.TrimSpace(m[2])),
			kind:  envLineAssign,
		})
	}
	return out, nil
}

func parseEnvValue(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = strings.TrimPrefix(strings.TrimSuffix(v, `"`), `"`)
		v = strings.ReplaceAll(v, `\"`, `"`)
		v = strings.ReplaceAll(v, `\\`, `\`)
		return v
	}
	if len(v) >= 2 && strings.HasPrefix(v, `'`) && strings.HasSuffix(v, `'`) {
		return strings.TrimPrefix(strings.TrimSuffix(v, `'`), `'`)
	}
	return strings.TrimSpace(v)
}

func (f envFile) get(key string) string {
	for i := len(f.lines) - 1; i >= 0; i-- {
		line := f.lines[i]
		if line.kind == envLineAssign && line.key == key {
			return line.value
		}
	}
	return ""
}

func (f envFile) rootCreds() (string, string, bool) {
	user := strings.TrimSpace(f.get("MINIO_ROOT_USER"))
	password := strings.TrimSpace(f.get("MINIO_ROOT_PASSWORD"))
	if user == "" || password == "" {
		return "", "", false
	}
	return user, password, true
}

func (f envFile) set(key, value string) envFile {
	next := envFile{lines: make([]envLine, 0, len(f.lines)+1)}
	replaced := false
	for _, line := range f.lines {
		if line.kind == envLineAssign && line.key == key {
			if !replaced {
				next.lines = append(next.lines, envLine{
					raw:   formatEnvAssignment(key, value),
					key:   key,
					value: value,
					kind:  envLineAssign,
				})
				replaced = true
			}
			continue
		}
		next.lines = append(next.lines, line)
	}
	if !replaced {
		next.lines = append(next.lines, envLine{
			raw:   formatEnvAssignment(key, value),
			key:   key,
			value: value,
			kind:  envLineAssign,
		})
	}
	return next
}

func (f envFile) remove(key string) envFile {
	next := envFile{lines: make([]envLine, 0, len(f.lines))}
	for _, line := range f.lines {
		if line.kind == envLineAssign && line.key == key {
			continue
		}
		next.lines = append(next.lines, line)
	}
	return next
}

func (f envFile) render() string {
	if len(f.lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, line := range f.lines {
		b.WriteString(line.raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func formatEnvAssignment(key, value string) string {
	return key + "=\"" + escapeEnvDoubleQuoted(value) + `"`
}

func escapeEnvDoubleQuoted(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
