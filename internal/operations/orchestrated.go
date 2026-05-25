package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// ---- start_cluster: parallel systemctl start across all hosts ----

type startClusterExecutor struct{ deps Deps }

func (e *startClusterExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *startClusterExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	start := time.Now()
	unit := unitName(rc.cluster.Engine)
	seedHostStatuses(run, rc.nodes)

	var (
		mu      sync.Mutex
		failed  int
		failErr error
		wg      sync.WaitGroup
	)
	for i, n := range rc.nodes {
		wg.Add(1)
		go func(i int, n domain.Node) {
			defer wg.Done()
			setHostState(run, i, n, tasks.HostRunning, "systemctl start")
			cmd := sudoSystemctl(rc.sshCreds.User, "start "+unit)
			if _, err := runHostStep(ctx, e.deps, rc, n, cmd); err != nil {
				setHostState(run, i, n, tasks.HostFailed, err.Error())
				run.LogError("%s: %v", n.Hostname, err)
				mu.Lock()
				failed++
				if failErr == nil {
					failErr = err
				}
				mu.Unlock()
				return
			}
			setHostState(run, i, n, tasks.HostSucceeded, "started")
			run.LogOK("%s: started", n.Hostname)
		}(i, n)
	}
	wg.Wait()

	if failed > 0 {
		return fmt.Errorf("%d/%d hosts failed to start: %w", failed, len(rc.nodes), failErr)
	}
	// Cluster-wide health wait — admin API only works once quorum is up.
	run.LogInfo("waiting for cluster healthy")
	if err := waitClusterHealthy(ctx, rc.admin, WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}); err != nil {
		return fmt.Errorf("post-start health: %w", err)
	}
	duration := time.Since(start)
	run.LogOK("Cluster healthy after %s", formatDuration(duration))
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster started"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Healthy after", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- rolling_restart: sequential systemctl restart with health-wait between hosts ----

type rollingRestartExecutor struct{ deps Deps }

func (e *rollingRestartExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *rollingRestartExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	start := time.Now()
	unit := unitName(rc.cluster.Engine)
	seedHostStatuses(run, rc.nodes)

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		setHostState(run, i, n, tasks.HostRunning, "systemctl restart")
		run.LogInfo("%s: systemctl restart %s", n.Hostname, unit)
		cmd := sudoSystemctl(rc.sshCreds.User, "restart "+unit)
		if _, err := runHostStep(ctx, e.deps, rc, n, cmd); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		// Wait for THIS host's service before moving on.
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "healthy")
		run.LogOK("%s: healthy", n.Hostname)
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Rolling restart complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Sequential time", Value: formatDuration(duration)},
			{Label: "Failed", Value: "0"},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- cluster_upgrade_by_admin_update: native admin update with cluster-wide restart ----

type clusterUpgradeByAdminUpdateExecutor struct{ deps Deps }

func (e *clusterUpgradeByAdminUpdateExecutor) Validate(req tasks.DispatchRequest) error {
	if len(req.Params) > 0 {
		var p rollingUpgradeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("cluster_upgrade_by_admin_update: invalid params: %w", err)
		}
	}
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpClusterUpgradeByAdminUpdate)
}

func (e *clusterUpgradeByAdminUpdateExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpClusterUpgradeByAdminUpdate); err != nil {
		return err
	}
	var p rollingUpgradeParams
	if len(run.Params) > 0 {
		_ = json.Unmarshal(run.Params, &p)
	}
	targetReleaseTime, hasTargetReleaseTime := parseComparableVersionTime(p.Version)
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("admin update preflight: server info: %w", err)
		}
		if err := ensureVersionIsNewer(p.Version, targetReleaseTime, info); err != nil {
			return fmt.Errorf("admin update preflight: %w", err)
		}
	}
	start := time.Now()
	updateURL, err := resolveBinaryUpdateURL(ctx, rc, p.Version)
	if err != nil {
		return err
	}
	run.LogInfo("calling admin ServerUpdate")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Requesting cluster update"
	})
	status, err := rc.admin.ServerUpdate(ctx, updateURL)
	if err != nil {
		return fmt.Errorf("admin update: %w", err)
	}
	if err := applyServerUpdateStatus(run, status); err != nil {
		return err
	}
	run.LogOK("Update request accepted, polling for healthy")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Waiting for cluster healthy"
	})
	if err := waitClusterHealthy(ctx, rc.admin, WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}); err != nil {
		return fmt.Errorf("post-update health: %w", err)
	}
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("post-update version check: server info: %w", err)
		}
		if err := ensureVersionReachedTarget(targetReleaseTime, p.Version, info); err != nil {
			return fmt.Errorf("post-update version check: %w", err)
		}
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster upgrade complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "From", Value: rc.cluster.Version},
			{Label: "To", Value: p.Version},
			{Label: "Upgrade time", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

func applyServerUpdateStatus(run *tasks.Run, status admin.ServerUpdateStatus) error {
	hostStatuses := make([]tasks.HostOpStatus, 0, len(status.Results))
	failures := make([]string, 0)
	for _, r := range status.Results {
		detail := strings.TrimSpace(r.UpdatedVersion)
		state := tasks.HostSucceeded
		if strings.TrimSpace(r.Error) != "" {
			state = tasks.HostFailed
			detail = strings.TrimSpace(r.Error)
			failures = append(failures, fmt.Sprintf("%s: %s", r.Host, r.Error))
		} else if detail == "" {
			detail = "updated"
		}
		hostStatuses = append(hostStatuses, tasks.HostOpStatus{
			Hostname: r.Host,
			State:    state,
			Detail:   detail,
		})
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = hostStatuses
	})
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("admin update reported host failures: %s", strings.Join(failures, "; "))
}

func ensureVersionIsNewer(targetLabel string, targetTime time.Time, info *domain.ServerInfo) error {
	if info == nil || len(info.Servers) == 0 {
		return errors.New("no servers reported by admin API")
	}
	for _, s := range info.Servers {
		currentTime, ok := parseComparableVersionTime(s.Version)
		if !ok {
			continue
		}
		if !targetTime.After(currentTime) {
			return fmt.Errorf(
				"selected version %s is not newer than %s on %s",
				targetLabel,
				s.Version,
				s.Endpoint,
			)
		}
	}
	return nil
}

func ensureVersionReachedTarget(targetTime time.Time, targetLabel string, info *domain.ServerInfo) error {
	if info == nil || len(info.Servers) == 0 {
		return errors.New("no servers reported by admin API")
	}
	for _, s := range info.Servers {
		currentTime, ok := parseComparableVersionTime(s.Version)
		if !ok {
			return fmt.Errorf("server %s reported an uncomparable version %q", s.Endpoint, s.Version)
		}
		if currentTime.Before(targetTime) {
			return fmt.Errorf("server %s still reports %s after update to %s", s.Endpoint, s.Version, targetLabel)
		}
	}
	return nil
}

func parseComparableVersionTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts.UTC(), true
	}
	for _, prefix := range []string{"RELEASE.", "DEVELOPMENT."} {
		if strings.HasPrefix(v, prefix) {
			if ts, err := time.Parse("2006-01-02T15-04-05Z", strings.TrimPrefix(v, prefix)); err == nil {
				return ts.UTC(), true
			}
		}
	}
	if ts, err := time.Parse("2006-01-02T15-04-05Z", v); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

// ---- cluster_upgrade_by_systemctl: stage upgrade on each host, then restart cluster once ----

type rollingUpgradeParams struct {
	Version   string `json:"version,omitempty"`
	CustomURL string `json:"customUrl,omitempty"`
}

type clusterUpgradeBySystemctlExecutor struct{ deps Deps }

func (e *clusterUpgradeBySystemctlExecutor) Validate(req tasks.DispatchRequest) error {
	if len(req.Params) > 0 {
		var p rollingUpgradeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("cluster_upgrade_by_systemctl: invalid params: %w", err)
		}
	}
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpClusterUpgradeBySystemctl)
}

func (e *clusterUpgradeBySystemctlExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpKind("cluster_upgrade_by_systemctl")); err != nil {
		return err
	}
	var p rollingUpgradeParams
	if len(run.Params) > 0 {
		_ = json.Unmarshal(run.Params, &p)
	}
	targetReleaseTime, hasTargetReleaseTime := parseComparableVersionTime(p.Version)
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("systemctl upgrade preflight: server info: %w", err)
		}
		if err := ensureVersionIsNewer(p.Version, targetReleaseTime, info); err != nil {
			return fmt.Errorf("systemctl upgrade preflight: %w", err)
		}
	}
	fromVersion := rc.cluster.Version
	toVersion := p.Version
	if toVersion == "" || toVersion == "custom" {
		toVersion = "custom URL"
	}

	start := time.Now()
	seedHostStatuses(run, rc.nodes)
	stagedHosts := 0
	skippedHosts := 0

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		hasUnit, err := hostHasBuckitSystemdUnit(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s unit probe: %w", n.Hostname, err)
		}
		if !hasUnit {
			skippedHosts++
			setHostState(run, i, n, tasks.HostSucceeded, "skipped: buckit.service not installed")
			run.LogInfo("%s: skipping host without buckit.service", n.Hostname)
			continue
		}
		pkgMgr, err := pkgManagerForHost(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s detect package manager: %w", n.Hostname, err)
		}
		artifact, err := resolveArtifactForNode(ctx, e.deps, rc, n, p.Version, p.CustomURL, pkgMgr.Kind())
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s version resolve: %w", n.Hostname, err)
		}
		expectedSHA256, err := deploy.FetchChecksum(ctx, artifact)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s checksum resolve: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "downloading")
		run.LogInfo("%s: downloading %s", n.Hostname, artifact.URL)
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.DownloadCommand(artifact.URL)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s download: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "verifying checksum")
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s checksum: %w", n.Hostname, err)
		}
		packageAction, err := inspectInstallAction(ctx, e.deps, rc, n, pkgMgr)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s package inspect: %w", n.Hostname, err)
		}
		if packageAction == deploy.InstallActionDowngrade {
			const msg = "cluster_upgrade_by_systemctl: selected version is older than the version currently installed; downgrade is not supported"
			setHostState(run, i, n, tasks.HostFailed, msg)
			return fmt.Errorf("%s: %s", n.Hostname, msg)
		}
		actionLabel := pkgMgr.Kind() + " " + string(packageAction)
		actionCommand := pkgMgr.InstallCommand(packageAction)
		setHostState(run, i, n, tasks.HostRunning, actionLabel)
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, actionCommand)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s %s: %w", n.Hostname, packageAction, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "systemctl daemon-reload")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "daemon-reload")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s daemon-reload: %w", n.Hostname, err)
		}
		stagedHosts++
		setHostState(run, i, n, tasks.HostSucceeded, "staged")
		run.LogOK("%s: upgraded and staged for cluster restart", n.Hostname)
	}
	if stagedHosts == 0 {
		return errors.New("cluster upgrade by systemctl: no hosts with buckit.service installed")
	}

	run.LogInfo("calling admin ServiceRestart")
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Restarting cluster"
	})
	if err := rc.admin.ServiceRestart(ctx); err != nil {
		return fmt.Errorf("admin restart: %w", err)
	}
	run.LogOK("Restart request accepted, polling for healthy")
	if err := waitClusterHealthy(ctx, rc.admin, WaitOptions{Timeout: 2 * time.Minute, Tick: 3 * time.Second}); err != nil {
		return fmt.Errorf("post-upgrade restart health: %w", err)
	}
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("post-upgrade version check: server info: %w", err)
		}
		if err := ensureVersionReachedTarget(targetReleaseTime, p.Version, info); err != nil {
			return fmt.Errorf("post-upgrade version check: %w", err)
		}
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster upgrade complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Updated hosts", Value: fmt.Sprintf("%d", stagedHosts)},
			{Label: "Skipped", Value: fmt.Sprintf("%d", skippedHosts)},
			{Label: "From", Value: fromVersion},
			{Label: "To", Value: toVersion},
			{Label: "Upgrade time", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- redeploy_software: Buckit-only. stop -> reinstall -> start per host ----

type redeployExecutor struct{ deps Deps }

func (e *redeployExecutor) Validate(req tasks.DispatchRequest) error {
	if len(req.Params) > 0 {
		var p rollingUpgradeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fmt.Errorf("redeploy_software: invalid params: %w", err)
		}
	}
	return validateEngineAtDispatch(e.deps, req.ClusterID, tasks.OpRedeploySoftware)
}

func (e *redeployExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	rc, err := load(ctx, e.deps, run.ClusterID)
	if err != nil {
		return err
	}
	if err := validateEngineCompat(rc.cluster, tasks.OpKind("redeploy_software")); err != nil {
		return err
	}
	var p rollingUpgradeParams
	if len(run.Params) > 0 {
		if err := json.Unmarshal(run.Params, &p); err != nil {
			return fmt.Errorf("redeploy_software: invalid params: %w", err)
		}
	}
	if p.Version == "" {
		return errors.New("redeploy_software: version required")
	}
	start := time.Now()
	seedHostStatuses(run, rc.nodes)

	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkgMgr, err := pkgManagerForHost(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s detect package manager: %w", n.Hostname, err)
		}
		// Bootstrap missing /etc/default/minio + /etc/minio/config.env
		// (+ certs) from a peer *before* we touch anything
		// irreversible. Failing here leaves the target untouched —
		// no download, no half-installed rpm, no stranded
		// /tmp/buckit.rpm — so the operator can fix the situation
		// and retry cleanly.
		if err := bootstrapBuckitConfigFromPeer(ctx, e.deps, rc, n); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s config bootstrap: %w", n.Hostname, err)
		}
		artifact, err := resolveArtifactForNode(ctx, e.deps, rc, n, p.Version, p.CustomURL, pkgMgr.Kind())
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s version resolve: %w", n.Hostname, err)
		}
		expectedSHA256, err := deploy.FetchChecksum(ctx, artifact)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s checksum resolve: %w", n.Hostname, err)
		}
		// Download + verify + inspect the candidate package *before*
		// stopping the service: that way a downgrade rejection (or any
		// earlier failure) leaves the node serving traffic. The actual
		// stop → install → start only runs once we know the candidate
		// is acceptable.
		setHostState(run, i, n, tasks.HostRunning, "downloading")
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.DownloadCommand(artifact.URL)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s download: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "verifying checksum")
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s checksum: %w", n.Hostname, err)
		}
		packageAction, err := inspectInstallAction(ctx, e.deps, rc, n, pkgMgr)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s package inspect: %w", n.Hostname, err)
		}
		if packageAction == deploy.InstallActionDowngrade {
			const msg = "redeploy_software: selected version is older than the version currently installed; downgrade is not supported"
			setHostState(run, i, n, tasks.HostFailed, msg)
			return fmt.Errorf("%s: %s", n.Hostname, msg)
		}
		actionLabel := pkgMgr.Kind() + " " + string(packageAction)
		actionCommand := pkgMgr.InstallCommand(packageAction)
		setHostState(run, i, n, tasks.HostRunning, "stop")
		// Tolerate "Unit not loaded" / "inactive" — a redeploy that
		// follows a previous uninstall (or runs on a host where the
		// unit was never installed) should still reach the install +
		// start steps below. Mirror the `|| true` pattern the
		// migration cutover uses for `systemctl stop minio.service`.
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "stop buckit.service || true")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s stop: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, actionLabel)
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, actionCommand)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s %s: %w", n.Hostname, packageAction, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "start")
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "start buckit.service")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s start: %w", n.Hostname, err)
		}
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "redeployed")
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Redeploy complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Duration", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// bootstrapBuckitConfigFromPeer makes sure target has the runtime
// config files buckit.service reads (/etc/default/minio and
// /etc/minio/config.env, plus the TLS material under /etc/minio/certs
// when present). If the target already has /etc/default/minio it's a
// no-op. Otherwise the function picks any peer node that does have the
// file, pulls each artefact as base64-over-stdout, and writes it onto
// the target with the same mode. Single-node clusters with no peer to
// copy from are surfaced as a clear error so the operator knows to use
// the new-cluster wizard for cold bootstraps.
func bootstrapBuckitConfigFromPeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) error {
	primary := "/etc/default/minio"
	secondary := "/etc/minio/config.env"
	certsDir := "/etc/minio/certs"

	have, err := remoteFileExists(ctx, deps, rc, target, primary)
	if err != nil {
		return fmt.Errorf("probe %s on %s: %w", primary, target.Hostname, err)
	}
	if have {
		return nil
	}
	peer, ok := pickConfigSourcePeer(ctx, deps, rc, target, primary)
	if !ok {
		return fmt.Errorf(
			"%s is missing %s and no other node in this cluster has it — for a fresh single-node cluster use the new-cluster wizard, not redeploy",
			target.Hostname, primary,
		)
	}
	if err := copyRemoteFile(ctx, deps, rc, peer, target, primary, "600"); err != nil {
		return fmt.Errorf("copy %s from %s: %w", primary, peer.Hostname, err)
	}
	if secondaryOnPeer, _ := remoteFileExists(ctx, deps, rc, peer, secondary); secondaryOnPeer {
		if err := copyRemoteFile(ctx, deps, rc, peer, target, secondary, "600"); err != nil {
			return fmt.Errorf("copy %s from %s: %w", secondary, peer.Hostname, err)
		}
	}
	if certsOnPeer, _ := remoteFileExists(ctx, deps, rc, peer, certsDir); certsOnPeer {
		if err := copyRemoteTarball(ctx, deps, rc, peer, target, "/etc/minio", "certs"); err != nil {
			return fmt.Errorf("copy %s from %s: %w", certsDir, peer.Hostname, err)
		}
	}
	return nil
}

// pickConfigSourcePeer chooses a node to copy bootstrap config from.
// Online peers come first (the admin API said so as of the last
// refresh), with any other reachable peer that still has the marker
// file as a fallback. Falling back matters when the cluster admin
// state is stale or the only file-holders happen to be in a degraded
// state but are still SSH-reachable — we'd rather copy stale config
// than refuse to redeploy.
func pickConfigSourcePeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, marker string) (domain.Node, bool) {
	var fallback domain.Node
	var haveFallback bool
	for _, n := range rc.nodes {
		if n.ID == target.ID {
			continue
		}
		ok, _ := remoteFileExists(ctx, deps, rc, n, marker)
		if !ok {
			continue
		}
		if n.State == domain.NodeOnline {
			return n, true
		}
		if !haveFallback {
			fallback = n
			haveFallback = true
		}
	}
	return fallback, haveFallback
}

func remoteFileExists(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, path string) (bool, error) {
	cmd := sudoBash(rc.sshCreds.User, "test -e "+shellQuote(path))
	if _, err := runHostStep(ctx, deps, rc, n, cmd); err != nil {
		// runHostStep returns an error on non-zero exit too; we
		// interpret that as "file is absent" rather than a transport
		// failure. A real transport break is rare and will surface
		// from the next caller-visible step.
		return false, nil
	}
	return true, nil
}

func copyRemoteFile(ctx context.Context, deps Deps, rc *runCtx, source, target domain.Node, path, mode string) error {
	read := sudoBash(rc.sshCreds.User, "base64 -w0 "+shellQuote(path))
	out, err := runHostStep(ctx, deps, rc, source, read)
	if err != nil {
		return err
	}
	encoded := strings.TrimSpace(out)
	dir := pathDir(path)
	write := sudoBash(rc.sshCreds.User, fmt.Sprintf(
		"mkdir -p %s && printf '%%s' %s | base64 -d > %s && chmod %s %s",
		shellQuote(dir),
		shellQuote(encoded),
		shellQuote(path),
		mode,
		shellQuote(path),
	))
	_, err = runHostStep(ctx, deps, rc, target, write)
	return err
}

// copyRemoteTarball streams `entry` under `baseDir` from source to
// target via `tar | base64`. Used for /etc/minio/certs so directory
// modes, multiple files, and CAs/ live transferred together.
func copyRemoteTarball(ctx context.Context, deps Deps, rc *runCtx, source, target domain.Node, baseDir, entry string) error {
	read := sudoBash(rc.sshCreds.User, fmt.Sprintf(
		"tar -C %s -cf - %s | base64 -w0",
		shellQuote(baseDir), shellQuote(entry),
	))
	out, err := runHostStep(ctx, deps, rc, source, read)
	if err != nil {
		return err
	}
	encoded := strings.TrimSpace(out)
	write := sudoBash(rc.sshCreds.User, fmt.Sprintf(
		"mkdir -p %s && printf '%%s' %s | base64 -d | tar -C %s -xf -",
		shellQuote(baseDir),
		shellQuote(encoded),
		shellQuote(baseDir),
	))
	_, err = runHostStep(ctx, deps, rc, target, write)
	return err
}

func pathDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		if i == 0 {
			return "/"
		}
		return p[:i]
	}
	return "."
}

// ---- shared helpers for the orchestrated flavor ----

func seedHostStatuses(run *tasks.Run, ns []domain.Node) {
	statuses := make([]tasks.HostOpStatus, len(ns))
	for i, n := range ns {
		statuses[i] = tasks.HostOpStatus{HostID: n.ID, Hostname: n.Hostname, State: tasks.HostPending}
	}
	run.MutateState(func(s *tasks.OperationProgress) {
		s.HostStatuses = statuses
		total := len(ns)
		zero := 0
		s.Total = &total
		s.Current = &zero
	})
}

func setHostState(run *tasks.Run, i int, _ domain.Node, state tasks.HostOpState, detail string) {
	run.MutateState(func(s *tasks.OperationProgress) {
		if i < len(s.HostStatuses) {
			s.HostStatuses[i].State = state
			s.HostStatuses[i].Detail = detail
		}
		if state == tasks.HostSucceeded || state == tasks.HostFailed {
			cur := 0
			if s.Current != nil {
				cur = *s.Current
			}
			cur++
			s.Current = &cur
		}
	})
}

func sudoSystemctl(user, args string) string {
	cmd := "systemctl " + args + " && systemctl daemon-reload"
	if user == "root" {
		return cmd
	}
	return "sudo -n bash -c " + shellQuote(cmd)
}

func sudoBash(user, cmd string) string {
	if user == "root" {
		return cmd
	}
	return "sudo -n bash -c " + shellQuote(cmd)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + replaceSingleQuotes(s) + "'"
}

func replaceSingleQuotes(s string) string {
	out := ""
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
		} else {
			out += string(r)
		}
	}
	return out
}

// resolveArtifactForNode resolves the Artifact (URL + sha256 sidecar
// URLs) for the version/customURL pair, honouring the host's detected
// package format. version="custom" plus customURL takes a .rpm-vs-.deb
// auto-detect via CustomArtifactFromURL — that catches mismatches up
// front rather than mid-install.
func resolveArtifactForNode(
	ctx context.Context,
	deps Deps,
	rc *runCtx,
	n domain.Node,
	version string,
	customURL string,
	kind string,
) (deploy.Artifact, error) {
	if version == "custom" {
		if customURL == "" {
			return deploy.Artifact{}, errors.New("customUrl required when version=custom")
		}
		art, err := deploy.CustomArtifactFromURL(customURL)
		if err != nil {
			return deploy.Artifact{}, err
		}
		if kind != "" && art.Kind != kind {
			return deploy.Artifact{}, fmt.Errorf(
				"custom URL is a %s artifact but host uses %s",
				art.Kind, kind,
			)
		}
		return art, nil
	}
	arch, err := detectNodeArch(ctx, deps, rc, n)
	if err != nil {
		return deploy.Artifact{}, err
	}
	return deploy.ResolveArtifact(version, kind, arch)
}

func resolveBinaryUpdateURL(ctx context.Context, rc *runCtx, version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return "", errors.New("cluster upgrade by admin update: version is required")
	}
	if rc.admin == nil {
		return "", errors.New("cluster upgrade by admin update: admin client not available")
	}
	info, err := rc.admin.ServerInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("cluster upgrade by admin update: server info: %w", err)
	}
	if info == nil || len(info.Servers) == 0 {
		return "", errors.New("cluster upgrade by admin update: no servers reported by admin API")
	}
	var detectedOS, detectedArch string
	for _, s := range info.Servers {
		osName := strings.TrimSpace(strings.ToLower(s.OS))
		arch := strings.TrimSpace(strings.ToLower(s.Arch))
		if osName == "" || arch == "" {
			return "", fmt.Errorf("cluster upgrade by admin update: server %s did not report os/arch", s.Endpoint)
		}
		if detectedOS == "" {
			detectedOS = osName
			detectedArch = arch
			continue
		}
		if detectedOS != osName || detectedArch != arch {
			return "", fmt.Errorf(
				"cluster upgrade by admin update: mixed platform inventory detected (%s/%s, %s/%s)",
				detectedOS, detectedArch, osName, arch,
			)
		}
	}
	url, err := deploy.ResolveBinaryURL(version, detectedOS, detectedArch)
	if err != nil {
		return "", fmt.Errorf("cluster upgrade by admin update: %w", err)
	}
	return url, nil
}

// inspectInstallAction inspects the buckit package currently installed
// on n (if any) and the candidate staged at mgr.LocalFile(), then
// reports which install verb the caller should use:
//
//   - InstallActionReinstall: same EVR is already on disk (or both EVRs
//     differ only in epoch — covered by V-R vercmp returning 0).
//   - InstallActionUpgrade:   nothing installed, or installed < candidate.
//   - InstallActionDowngrade: installed > candidate. Callers refuse this —
//     dnf would reject the local RPM anyway, and downgrade isn't a
//     supported bm flow.
//
// The script that produces the three-line probe output lives on the
// PackageManager (so rpmManager / debManager normalize their respective
// vercmp algorithms), but the Go-side decision is shared.
func inspectInstallAction(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, mgr deploy.PackageManager) (deploy.InstallAction, error) {
	out, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, mgr.InspectScript()))
	if err != nil {
		return "", err
	}
	return deploy.ParseInspectOutput(out)
}

func detectNodeArch(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) (string, error) {
	out, err := runHostStep(ctx, deps, rc, n, "uname -m")
	if err != nil {
		return "", fmt.Errorf("detect arch: %w", err)
	}
	arch := strings.TrimSpace(out)
	if arch == "" {
		return "", errors.New("detect arch: empty uname -m output")
	}
	switch arch {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	default:
		return strings.ToLower(arch), nil
	}
}

func hostHasBuckitSystemdUnit(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) (bool, error) {
	cmd := `state=$(systemctl show -p LoadState --value buckit.service 2>/dev/null || true); printf "%s" "$state"`
	out, err := runHostStep(ctx, deps, rc, n, cmd)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "loaded", nil
}
