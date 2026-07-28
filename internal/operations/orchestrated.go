package operations

import (
	"context"
	"encoding/base64"
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
		skipped int
		failErr error
		wg      sync.WaitGroup
	)
	for i, n := range rc.nodes {
		wg.Add(1)
		go func(i int, n domain.Node) {
			defer wg.Done()
			hasUnit, probeErr := hostHasSystemdUnit(ctx, e.deps, rc, n, unit)
			if probeErr != nil {
				setHostState(run, i, n, tasks.HostFailed, probeErr.Error())
				run.LogError("%s: %s probe: %v", n.Hostname, unit, probeErr)
				mu.Lock()
				failed++
				if failErr == nil {
					failErr = probeErr
				}
				mu.Unlock()
				return
			}
			if !hasUnit {
				setHostState(run, i, n, tasks.HostSucceeded, "skipped: "+unit+" not installed")
				run.LogInfo("%s: skipping host without %s", n.Hostname, unit)
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}
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
	if skipped == len(rc.nodes) {
		return fmt.Errorf("start_cluster: no hosts have %s installed", unit)
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
		summary := []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Started", Value: fmt.Sprintf("%d", len(rc.nodes)-skipped)},
		}
		if skipped > 0 {
			summary = append(summary, tasks.SummaryItem{Label: "Skipped", Value: fmt.Sprintf("%d", skipped)})
		}
		summary = append(summary, tasks.SummaryItem{Label: "Healthy after", Value: formatDuration(duration)})
		s.Summary = summary
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- stop_cluster_by_systemctl: parallel systemctl stop across all hosts ----
// Unlike OpStopCluster (admin API), this leaves the unit in `inactive` state
// so systemd's Restart= policy does NOT bring the process back up.

type stopClusterBySystemctlExecutor struct{ deps Deps }

func (e *stopClusterBySystemctlExecutor) Validate(req tasks.DispatchRequest) error { return nil }

func (e *stopClusterBySystemctlExecutor) Execute(ctx context.Context, run *tasks.Run) error {
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
		skipped int
		failErr error
		wg      sync.WaitGroup
	)
	for i, n := range rc.nodes {
		wg.Add(1)
		go func(i int, n domain.Node) {
			defer wg.Done()
			hasUnit, probeErr := hostHasSystemdUnit(ctx, e.deps, rc, n, unit)
			if probeErr != nil {
				setHostState(run, i, n, tasks.HostFailed, probeErr.Error())
				run.LogError("%s: %s probe: %v", n.Hostname, unit, probeErr)
				mu.Lock()
				failed++
				if failErr == nil {
					failErr = probeErr
				}
				mu.Unlock()
				return
			}
			if !hasUnit {
				setHostState(run, i, n, tasks.HostSucceeded, "skipped: "+unit+" not installed")
				run.LogInfo("%s: skipping host without %s", n.Hostname, unit)
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}
			setHostState(run, i, n, tasks.HostRunning, "systemctl stop")
			cmd := sudoSystemctl(rc.sshCreds.User, "stop "+unit)
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
			setHostState(run, i, n, tasks.HostSucceeded, "stopped")
			run.LogOK("%s: stopped", n.Hostname)
		}(i, n)
	}
	wg.Wait()

	if failed > 0 {
		return fmt.Errorf("%d/%d hosts failed to stop: %w", failed, len(rc.nodes), failErr)
	}
	if skipped == len(rc.nodes) {
		return fmt.Errorf("stop_cluster_by_systemctl: no hosts have %s installed", unit)
	}
	duration := time.Since(start)
	run.LogOK("Cluster stopped in %s", formatDuration(duration))
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Cluster stopped"
		summary := []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Stopped", Value: fmt.Sprintf("%d", len(rc.nodes)-skipped)},
		}
		if skipped > 0 {
			summary = append(summary, tasks.SummaryItem{Label: "Skipped", Value: fmt.Sprintf("%d", skipped)})
		}
		summary = append(summary, tasks.SummaryItem{Label: "Duration", Value: formatDuration(duration)})
		s.Summary = summary
	})
	// Admin API is intentionally down — don't refresh, just mark the cluster
	// row as unreachable so the UI reflects the operator's choice.
	markUnreachable(ctx, e.deps, run.ClusterID)
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

	skipped := 0
	for i, n := range rc.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		hasUnit, err := hostHasSystemdUnit(ctx, e.deps, rc, n, unit)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			return fmt.Errorf("%s %s probe: %w", n.Hostname, unit, err)
		}
		if !hasUnit {
			skipped++
			setHostState(run, i, n, tasks.HostSucceeded, "skipped: "+unit+" not installed")
			run.LogInfo("%s: skipping host without %s", n.Hostname, unit)
			continue
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
	if skipped == len(rc.nodes) {
		return fmt.Errorf("rolling_restart: no hosts have %s installed", unit)
	}
	duration := time.Since(start)
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Rolling restart complete"
		summary := []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(rc.nodes))},
			{Label: "Restarted", Value: fmt.Sprintf("%d", len(rc.nodes)-skipped)},
		}
		if skipped > 0 {
			summary = append(summary, tasks.SummaryItem{Label: "Skipped", Value: fmt.Sprintf("%d", skipped)})
		}
		summary = append(summary,
			tasks.SummaryItem{Label: "Sequential time", Value: formatDuration(duration)},
			tasks.SummaryItem{Label: "Failed", Value: "0"},
		)
		s.Summary = summary
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// ---- cluster_upgrade_by_admin_update: native admin update with cluster-wide restart ----

type clusterUpgradeByAdminUpdateExecutor struct{ deps Deps }

var clusterUpgradePostRestartWait = WaitOptions{
	Timeout: 2 * time.Minute,
	Tick:    3 * time.Second,
}

var errUncomparableServerVersion = errors.New("uncomparable server version")

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
	expectedServers := 0
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("admin update preflight: server info: %w", err)
		}
		expectedServers = len(info.Servers)
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
	postRestartWait := clusterUpgradePostRestartWait.withDefaults(2 * time.Minute)
	postRestartDeadline := time.Now().Add(postRestartWait.Timeout)
	if err := waitClusterHealthy(ctx, rc.admin, postRestartWait); err != nil {
		return fmt.Errorf("post-update health: %w", err)
	}
	if hasTargetReleaseTime {
		run.LogInfo("cluster healthy, waiting for all servers to report %s", p.Version)
		run.MutateState(func(s *tasks.OperationProgress) {
			s.Detail = "Waiting for version convergence"
		})
		remaining := time.Until(postRestartDeadline)
		if remaining < minimumVersionConvergenceBudget(postRestartWait.Timeout) {
			return fmt.Errorf(
				"post-update version check: cluster health took %s of the %s post-restart budget, leaving no time for version convergence",
				postRestartWait.Timeout-remaining,
				postRestartWait.Timeout,
			)
		}
		if err := waitServerVersionsReached(
			ctx,
			rc.admin.ServerInfo,
			targetReleaseTime,
			p.Version,
			expectedServers,
			WaitOptions{Timeout: remaining, Tick: postRestartWait.Tick},
			versionWaitProgressLogger(run),
		); err != nil {
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

func ensureVersionReachedTarget(targetTime time.Time, targetLabel string, expectedServers int, info *domain.ServerInfo) error {
	if info == nil || len(info.Servers) == 0 {
		return errors.New("no servers reported by admin API")
	}
	if expectedServers > 0 && len(info.Servers) < expectedServers {
		return fmt.Errorf("admin API reports %d of %d servers", len(info.Servers), expectedServers)
	}
	for _, s := range info.Servers {
		version := strings.TrimSpace(s.Version)
		if version != "" {
			if _, ok := parseComparableVersionTime(version); ok {
				continue
			}
			return fmt.Errorf("%w: server %s reported %q", errUncomparableServerVersion, s.Endpoint, s.Version)
		}
	}
	for _, s := range info.Servers {
		currentTime, ok := parseComparableVersionTime(s.Version)
		if !ok {
			return fmt.Errorf("server %s has not reported a version yet", s.Endpoint)
		}
		if currentTime.Before(targetTime) {
			return fmt.Errorf("server %s still reports %s after update to %s", s.Endpoint, s.Version, targetLabel)
		}
	}
	return nil
}

// waitServerVersionsReached allows the admin API's view of each server to
// converge after an asynchronous cluster restart. A cluster can be healthy
// while a just-restarted peer still briefly reports its previous version.
func waitServerVersionsReached(
	ctx context.Context,
	fetch func(context.Context) (*domain.ServerInfo, error),
	targetTime time.Time,
	targetLabel string,
	expectedServers int,
	opts WaitOptions,
	onRetry func(error),
) error {
	opts = opts.withDefaults(90 * time.Second)
	loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	var (
		lastFetchErr   error
		lastVersionErr error
		lastWasFetch   bool
		retries        int
	)
	for {
		if err := loopCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if lastFetchErr == nil && lastVersionErr == nil {
				return fmt.Errorf("servers did not report %s after %s", targetLabel, opts.Timeout)
			}
			if lastFetchErr != nil && lastVersionErr != nil {
				return fmt.Errorf(
					"servers did not report %s after %s: last version state: %v; last admin API error: %v",
					targetLabel,
					opts.Timeout,
					lastVersionErr,
					lastFetchErr,
				)
			}
			if lastFetchErr != nil {
				return fmt.Errorf("admin API unavailable for %s while waiting for %s: %w", opts.Timeout, targetLabel, lastFetchErr)
			}
			return fmt.Errorf("servers did not report %s after %s: %w", targetLabel, opts.Timeout, lastVersionErr)
		}

		attemptCtx, attemptCancel := context.WithTimeout(loopCtx, 10*time.Second)
		info, err := fetch(attemptCtx)
		attemptCancel()
		if err != nil {
			lastFetchErr = err
			lastWasFetch = true
		} else {
			err = ensureVersionReachedTarget(targetTime, targetLabel, expectedServers, info)
			if err == nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			}
			if errors.Is(err, errUncomparableServerVersion) {
				return err
			}
			lastVersionErr = err
			lastWasFetch = false
		}
		retries++
		if onRetry != nil && retries%5 == 0 {
			if lastWasFetch {
				onRetry(lastFetchErr)
			} else {
				onRetry(lastVersionErr)
			}
		}

		select {
		case <-loopCtx.Done():
			continue
		case <-time.After(opts.Tick):
		}
	}
}

func versionWaitProgressLogger(run *tasks.Run) func(error) {
	return func(err error) {
		run.LogInfo("still waiting for version convergence: %v", err)
	}
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
	expectedServers := 0
	if hasTargetReleaseTime {
		info, err := rc.admin.ServerInfo(ctx)
		if err != nil {
			return fmt.Errorf("systemctl upgrade preflight: server info: %w", err)
		}
		expectedServers = len(info.Servers)
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
	postRestartWait := clusterUpgradePostRestartWait.withDefaults(2 * time.Minute)
	postRestartDeadline := time.Now().Add(postRestartWait.Timeout)
	if err := waitClusterHealthy(ctx, rc.admin, postRestartWait); err != nil {
		return fmt.Errorf("post-upgrade restart health: %w", err)
	}
	if hasTargetReleaseTime {
		run.LogInfo("cluster healthy, waiting for all servers to report %s", p.Version)
		run.MutateState(func(s *tasks.OperationProgress) {
			s.Detail = "Waiting for version convergence"
		})
		remaining := time.Until(postRestartDeadline)
		if remaining < minimumVersionConvergenceBudget(postRestartWait.Timeout) {
			return fmt.Errorf(
				"post-upgrade version check: cluster health took %s of the %s post-restart budget, leaving no time for version convergence",
				postRestartWait.Timeout-remaining,
				postRestartWait.Timeout,
			)
		}
		if err := waitServerVersionsReached(
			ctx,
			rc.admin.ServerInfo,
			targetReleaseTime,
			p.Version,
			expectedServers,
			WaitOptions{Timeout: remaining, Tick: postRestartWait.Tick},
			versionWaitProgressLogger(run),
		); err != nil {
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

func minimumVersionConvergenceBudget(postRestartBudget time.Duration) time.Duration {
	const preferredMinimum = 5 * time.Second
	if reducedMinimum := postRestartBudget / 10; reducedMinimum < preferredMinimum {
		return reducedMinimum
	}
	return preferredMinimum
}

// ---- redeploy_software: Buckit-only. stop -> reinstall -> start per host ----

type redeployExecutor struct{ deps Deps }

func (e *redeployExecutor) Validate(req tasks.DispatchRequest) error {
	// Provision is single-host only. Operators can select hosts from
	// different pools in a bulk-selection UI, but each host needs its
	// own same-pool peer for the config + data-layout derivation —
	// mixing pools in one run risks pulling the wrong layout. Reject
	// at dispatch so the UI catches the mistake immediately rather
	// than failing mid-run on the second host.
	if len(req.TargetHostIDs) != 1 {
		return fmt.Errorf("redeploy_software: exactly one target host required (got %d)", len(req.TargetHostIDs))
	}
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
	// Redeploy always installs the cluster's currently-reported version
	// so the target ends up matching its peers. The user-supplied
	// params (if any) are ignored — the UI no longer surfaces a
	// version picker for this op.
	version, err := resolveRedeployVersion(rc.cluster.Version)
	if err != nil {
		return fmt.Errorf("redeploy_software: %w", err)
	}
	// Redeploy is host-scoped — the per-node and bulk catalogs both
	// pass run.Targets. Filter rc.nodes to just those targets so the
	// host-statuses panel doesn't show unselected peers as "Pending".
	hosts, err := targetHosts(tasks.DispatchRequest{TargetHostIDs: run.Targets}, rc.nodes)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return errors.New("redeploy_software: no target hosts")
	}
	start := time.Now()
	seedHostStatuses(run, hosts)
	run.LogInfo("provisioning %d host(s) at %s", len(hosts), version)

	for i, n := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		run.LogInfo("%s: starting provisioning", n.Hostname)
		pkgMgr, err := pkgManagerForHost(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: detect package manager: %v", n.Hostname, err)
			return fmt.Errorf("%s detect package manager: %w", n.Hostname, err)
		}
		// Reject hosts that already carry config / data the redeploy
		// would clobber. Runs before bootstrap so a dirty host fails
		// without any state change on the target.
		setHostState(run, i, n, tasks.HostRunning, "preflight: clean host")
		run.LogInfo("%s: preflight clean-host check", n.Hostname)
		if err := preflightHostIsClean(ctx, e.deps, rc, n); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: preflight: %v", n.Hostname, err)
			return fmt.Errorf("%s preflight: %w", n.Hostname, err)
		}
		// Bootstrap missing /etc/default/minio + /etc/minio/config.env
		// (+ certs) from a peer *before* we touch anything
		// irreversible. Failing here leaves the target untouched —
		// no download, no half-installed rpm, no stranded
		// /tmp/buckit.rpm — so the operator can fix the situation
		// and retry cleanly.
		run.LogInfo("%s: bootstrapping config from peer", n.Hostname)
		bsPaths, err := bootstrapBuckitConfigFromPeer(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: config bootstrap: %v", n.Hostname, err)
			return fmt.Errorf("%s config bootstrap: %w", n.Hostname, err)
		}
		// Ensure the service user/group exist on the target BEFORE
		// the package install. The buckit package's postinstall would
		// create `buckit:buckit`, but a migrated cluster may run as
		// `minio-user:minio-user` (wired via the migration drop-in we
		// just copied) — the package wouldn't create that, so
		// buckit.service would fail to start. Idempotent: if the
		// account is already present (existing buckit host) this is a
		// no-op.
		setHostState(run, i, n, tasks.HostRunning, "ensuring service user/group")
		user, group := resolveServiceIdentity(ctx, e.deps, rc, n)
		run.LogInfo("%s: ensuring service identity %s:%s", n.Hostname, user, group)
		if err := ensureBuckitUserAndGroup(ctx, e.deps, rc, n, user, group); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: ensure user/group: %v", n.Hostname, err)
			return fmt.Errorf("%s ensure user/group: %w", n.Hostname, err)
		}
		// Hand the secondary env file and certs dir over to the
		// service user/group now that the account exists. Without
		// this, buckit.service starts but the binary (running as
		// `buckit`) can't read /etc/minio/config.env — it's still
		// owned by root:root mode 600 from the bootstrap copy and
		// fails with "Unable to read the config environment file:
		// permission denied".
		if err := chownBootstrappedConfig(ctx, e.deps, rc, n, bsPaths, user, group); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: chown config: %v", n.Hostname, err)
			return fmt.Errorf("%s chown config: %w", n.Hostname, err)
		}
		// Create the MINIO_VOLUMES local paths (the per-drive
		// /buckit subdirs) under the service user/group. Preflight
		// already verified each path is absent or empty, so this is
		// strictly additive — no risk of clobbering existing data.
		setHostState(run, i, n, tasks.HostRunning, "preparing data dirs")
		dataPaths, err := dataPathsFromPeer(ctx, e.deps, rc, n)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: data dirs discovery: %v", n.Hostname, err)
			return fmt.Errorf("%s data dirs discovery: %w", n.Hostname, err)
		}
		// Verify each data path's parent is a real mount point before
		// mkdir runs. Without this, mkdir -p would silently create the
		// dir in the rootfs whenever the intended drive isn't mounted,
		// and buckit would happily write data to the system disk.
		if err := preflightDataMountsAttached(ctx, e.deps, rc, n, dataPaths); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: mount check: %v", n.Hostname, err)
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		run.LogInfo("%s: preparing %d data dir(s)", n.Hostname, len(dataPaths))
		if err := prepareBuckitDataDirs(ctx, e.deps, rc, n, dataPaths, user, group); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: prepare data dirs: %v", n.Hostname, err)
			return fmt.Errorf("%s prepare data dirs: %w", n.Hostname, err)
		}
		artifact, err := resolveArtifactForNode(ctx, e.deps, rc, n, version, "", pkgMgr.Kind())
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: version resolve: %v", n.Hostname, err)
			return fmt.Errorf("%s version resolve: %w", n.Hostname, err)
		}
		expectedSHA256, err := deploy.FetchChecksum(ctx, artifact)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: checksum resolve: %v", n.Hostname, err)
			return fmt.Errorf("%s checksum resolve: %w", n.Hostname, err)
		}
		// Download + verify + inspect the candidate package *before*
		// stopping the service: that way a downgrade rejection (or any
		// earlier failure) leaves the node serving traffic. The actual
		// stop → install → start only runs once we know the candidate
		// is acceptable.
		setHostState(run, i, n, tasks.HostRunning, "downloading")
		run.LogInfo("%s: downloading %s", n.Hostname, artifact.URL)
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.DownloadCommand(artifact.URL)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: download: %v", n.Hostname, err)
			return fmt.Errorf("%s download: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "verifying checksum")
		if _, err := runHostStep(ctx, e.deps, rc, n, pkgMgr.VerifyChecksumCommand(expectedSHA256)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: checksum: %v", n.Hostname, err)
			return fmt.Errorf("%s checksum: %w", n.Hostname, err)
		}
		packageAction, err := inspectInstallAction(ctx, e.deps, rc, n, pkgMgr)
		if err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: package inspect: %v", n.Hostname, err)
			return fmt.Errorf("%s package inspect: %w", n.Hostname, err)
		}
		if packageAction == deploy.InstallActionDowngrade {
			const msg = "redeploy_software: selected version is older than the version currently installed; downgrade is not supported"
			setHostState(run, i, n, tasks.HostFailed, msg)
			run.LogError("%s: %s", n.Hostname, msg)
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
			run.LogError("%s: stop: %v", n.Hostname, err)
			return fmt.Errorf("%s stop: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostRunning, actionLabel)
		run.LogInfo("%s: %s", n.Hostname, actionLabel)
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoBash(rc.sshCreds.User, actionCommand)); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: %s: %v", n.Hostname, packageAction, err)
			return fmt.Errorf("%s %s: %w", n.Hostname, packageAction, err)
		}
		setHostState(run, i, n, tasks.HostRunning, "start")
		run.LogInfo("%s: starting buckit.service", n.Hostname)
		if _, err := runHostStep(ctx, e.deps, rc, n, sudoSystemctl(rc.sshCreds.User, "start buckit.service")); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: start: %v", n.Hostname, err)
			return fmt.Errorf("%s start: %w", n.Hostname, err)
		}
		if err := waitHostHealthy(ctx, e.deps, rc, n, WaitOptions{Timeout: 60 * time.Second}); err != nil {
			setHostState(run, i, n, tasks.HostFailed, err.Error())
			run.LogError("%s: health: %v", n.Hostname, err)
			return fmt.Errorf("%s health: %w", n.Hostname, err)
		}
		setHostState(run, i, n, tasks.HostSucceeded, "provisioned")
		run.LogOK("%s: provisioned", n.Hostname)
	}
	duration := time.Since(start)
	run.LogOK("provisioning complete in %s", formatDuration(duration))
	run.MutateState(func(s *tasks.OperationProgress) {
		s.Detail = "Provisioning complete"
		s.Summary = []tasks.SummaryItem{
			{Label: "Hosts", Value: fmt.Sprintf("%d", len(hosts))},
			{Label: "Duration", Value: formatDuration(duration)},
		}
	})
	refreshClusterRow(ctx, e.deps, run.ClusterID, rc.admin)
	return nil
}

// bootstrapBuckitConfigFromPeer makes sure target has the runtime
// config files buckit.service reads. The primary env-file path is
// derived from systemd (`systemctl show buckit.service
// EnvironmentFile(s)`) so a custom unit that points at a non-default
// location is honoured. The secondary env file (MINIO_CONFIG_ENV_FILE)
// and the certs directory (from --certs-dir inside MINIO_OPTS) are
// parsed out of the primary env-file content on the source peer. If
// the target already has the primary env file it's a no-op. Otherwise
// the function picks a peer that has it, copies each artefact via
// base64-over-stdout, and writes it onto the target. Single-node
// clusters with no peer to copy from are surfaced as a clear error so
// the operator knows to use the new-cluster wizard for cold
// bootstraps.
// bootstrappedPaths records the files the bootstrap copied that need
// service-user ownership before buckit.service starts. Empty fields
// mean "peer didn't have one" — caller should treat as a no-op.
//
// Why only these two: systemd loads the primary env file and parses
// drop-ins as root, so those stay root-owned. The buckit binary
// itself (running as the service user) loads the secondary env file
// (MINIO_CONFIG_ENV_FILE) and reads from the certs dir — those need
// to be readable by the service user.
type bootstrappedPaths struct {
	Secondary string
	CertsDir  string
}

func bootstrapBuckitConfigFromPeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) (bootstrappedPaths, error) {
	// Derive env-file paths from systemd. Try target first (cheapest,
	// no extra ssh hop) and fall back to peers when target has no
	// buckit.service loaded yet — e.g. on a fresh node being added.
	envFiles, _ := probeBuckitEnvironmentFiles(ctx, deps, rc, target)
	if len(envFiles) == 0 {
		envFiles = probeEnvironmentFilesFromAnyPeer(ctx, deps, rc, target)
	}
	if len(envFiles) == 0 {
		return bootstrappedPaths{}, fmt.Errorf(
			"%s and its peers have no buckit.service unit loaded — for a fresh single-node cluster use the new-cluster wizard, not redeploy",
			target.Hostname,
		)
	}
	primary := envFiles[0]

	have, err := remoteFileExists(ctx, deps, rc, target, primary)
	if err != nil {
		return bootstrappedPaths{}, fmt.Errorf("probe %s on %s: %w", primary, target.Hostname, err)
	}
	if have {
		return bootstrappedPaths{}, nil
	}
	peer, ok := pickConfigSourcePeer(ctx, deps, rc, target, primary)
	if !ok {
		return bootstrappedPaths{}, fmt.Errorf(
			"%s is missing %s and no other node in this cluster has it — for a fresh single-node cluster use the new-cluster wizard, not redeploy",
			target.Hostname, primary,
		)
	}
	// Read the primary env file body on the peer once and derive
	// secondary + certs-dir from its content rather than hardcoding.
	body, err := readRemoteFileText(ctx, deps, rc, peer, primary)
	if err != nil {
		return bootstrappedPaths{}, fmt.Errorf("read %s from %s: %w", primary, peer.Hostname, err)
	}
	if err := copyRemoteFile(ctx, deps, rc, peer, target, primary, "600"); err != nil {
		return bootstrappedPaths{}, fmt.Errorf("copy %s from %s: %w", primary, peer.Hostname, err)
	}
	var copied bootstrappedPaths
	if secondary := extractEnvVar(body, "MINIO_CONFIG_ENV_FILE"); secondary != "" {
		if onPeer, _ := remoteFileExists(ctx, deps, rc, peer, secondary); onPeer {
			if err := copyRemoteFile(ctx, deps, rc, peer, target, secondary, "600"); err != nil {
				return bootstrappedPaths{}, fmt.Errorf("copy %s from %s: %w", secondary, peer.Hostname, err)
			}
			copied.Secondary = secondary
		}
	}
	if certsDir := extractCertsDirFromOpts(body); certsDir != "" {
		if onPeer, _ := remoteFileExists(ctx, deps, rc, peer, certsDir); onPeer {
			parent := pathDir(certsDir)
			entry := pathBase(certsDir)
			if err := copyRemoteTarball(ctx, deps, rc, peer, target, parent, entry); err != nil {
				return bootstrappedPaths{}, fmt.Errorf("copy %s from %s: %w", certsDir, peer.Hostname, err)
			}
			copied.CertsDir = certsDir
		}
	}
	// Persistent systemd drop-ins (e.g. /etc/systemd/system/buckit.service.d/*.conf)
	// belong to the operator, not the package — the package install in
	// the next step won't restore them. Copy each one individually so
	// the target gets the same unit-level overrides (Restart=, resource
	// limits, etc.) as the peer.
	dropIns, err := probeBuckitDropInPaths(ctx, deps, rc, peer)
	if err != nil {
		return bootstrappedPaths{}, fmt.Errorf("probe drop-ins on %s: %w", peer.Hostname, err)
	}
	for _, p := range dropIns {
		if err := copyRemoteFile(ctx, deps, rc, peer, target, p, "644"); err != nil {
			return bootstrappedPaths{}, fmt.Errorf("copy %s from %s: %w", p, peer.Hostname, err)
		}
	}
	return copied, nil
}

// chownBootstrappedConfig hands the secondary env file and certs dir
// over to the service user/group on the target. Mirrors deploy's
// `install -o user -g group -m 600` for the secondary and the
// recursive chown deploy does on /etc/minio/certs. Files that systemd
// itself reads (primary env file, drop-ins) intentionally stay
// root-owned. No-op when both paths are empty.
func chownBootstrappedConfig(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, paths bootstrappedPaths, user, group string) error {
	if paths.Secondary == "" && paths.CertsDir == "" {
		return nil
	}
	owner := shellQuote(user) + ":" + shellQuote(group)
	var script strings.Builder
	script.WriteString("set -e\n")
	if paths.Secondary != "" {
		fmt.Fprintf(&script, "chown %s %s\n", owner, shellQuote(paths.Secondary))
	}
	if paths.CertsDir != "" {
		fmt.Fprintf(&script, "chown -R %s %s\n", owner, shellQuote(paths.CertsDir))
	}
	if _, err := runHostStep(ctx, deps, rc, target, sudoBash(rc.sshCreds.User, script.String())); err != nil {
		return fmt.Errorf("%s chown config: %w", target.Hostname, err)
	}
	return nil
}

// probeBuckitUnitProps asks systemd on n for buckit.service's User,
// Group, and EnvironmentFile(s). Returns the parsed UnitProps. An
// empty result (all fields blank) means buckit.service is not loaded
// on n — systemctl show prints empty values and exits 0 for unknown
// units, which the caller interprets as "no unit here".
func probeBuckitUnitProps(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) (deploy.UnitProps, error) {
	cmd := sudoBash(rc.sshCreds.User, "systemctl show buckit.service -p User -p Group -p EnvironmentFiles -p EnvironmentFile")
	out, err := runHostStep(ctx, deps, rc, n, cmd)
	if err != nil {
		return deploy.UnitProps{}, err
	}
	return deploy.ParseUnitProps(out), nil
}

// probeBuckitEnvironmentFiles is a thin convenience wrapper for
// callers that only need the EnvironmentFile list.
func probeBuckitEnvironmentFiles(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) ([]string, error) {
	props, err := probeBuckitUnitProps(ctx, deps, rc, n)
	if err != nil {
		return nil, err
	}
	return props.EnvironmentFiles, nil
}

// probeUnitPropsFromAnyPeer queries peers' systemd for the
// buckit.service UnitProps. Selection mirrors pickConfigSourcePeer's
// ordering — same-pool online > same-pool any > cross-pool online >
// cross-pool any — so the unit-file User/Group/EnvironmentFile values
// we adopt come from a peer whose package + drop-ins are most likely
// to match the target's. Returns zero-value props when no peer has
// the unit loaded.
func probeUnitPropsFromAnyPeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) deploy.UnitProps {
	for _, sameScope := range []bool{true, false} {
		for _, onlineOnly := range []bool{true, false} {
			for _, n := range rc.nodes {
				if n.ID == target.ID {
					continue
				}
				if sameScope && n.Pool != target.Pool {
					continue
				}
				if !sameScope && n.Pool == target.Pool {
					continue
				}
				if onlineOnly && n.State != domain.NodeOnline {
					continue
				}
				if !onlineOnly && n.State == domain.NodeOnline {
					continue
				}
				if props, err := probeBuckitUnitProps(ctx, deps, rc, n); err == nil && (len(props.EnvironmentFiles) > 0 || props.User != "" || props.Group != "") {
					return props
				}
			}
		}
	}
	return deploy.UnitProps{}
}

// probeEnvironmentFilesFromAnyPeer is a back-compat wrapper for
// callers that only need the EnvironmentFile list.
func probeEnvironmentFilesFromAnyPeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) []string {
	return probeUnitPropsFromAnyPeer(ctx, deps, rc, target).EnvironmentFiles
}

// probeBuckitDropInPaths asks systemd on n for buckit.service's
// drop-in file paths. Filters to /etc/* — package-shipped paths under
// /usr/* are restored by the package install step, and runtime paths
// under /run/* are transient and shouldn't be persisted across hosts.
// Returns nil when the unit has no operator-added drop-ins.
func probeBuckitDropInPaths(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) ([]string, error) {
	cmd := sudoBash(rc.sshCreds.User, "systemctl show buckit.service -p DropInPaths")
	out, err := runHostStep(ctx, deps, rc, n, cmd)
	if err != nil {
		return nil, err
	}
	return parseDropInPaths(out), nil
}

// parseDropInPaths consumes `systemctl show -p DropInPaths` output and
// returns the persistent operator-owned drop-in files (those under
// /etc/). Exported separately from probeBuckitDropInPaths so the
// filter logic can be unit-tested without an ssh transport.
func parseDropInPaths(out string) []string {
	var paths []string
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "DropInPaths=") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "DropInPaths="))
		for _, p := range strings.Fields(v) {
			if !strings.HasPrefix(p, "/etc/") {
				continue
			}
			paths = append(paths, p)
		}
	}
	return paths
}

// readRemoteFileText reads a small text file via base64-over-stdout to
// avoid mangling and returns the decoded body as a string.
func readRemoteFileText(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, path string) (string, error) {
	cmd := sudoBash(rc.sshCreds.User, "base64 -w0 "+shellQuote(path))
	out, err := runHostStep(ctx, deps, rc, n, cmd)
	if err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("decode %s on %s: %w", path, n.Hostname, err)
	}
	return string(decoded), nil
}

// extractEnvVar returns the (last) assignment of `name=` in body,
// stripped of surrounding quotes. Tolerates an `export` prefix and
// `KEY=value` / `KEY="value"` / `KEY='value'` forms. Inline `#`
// comments after an unquoted value are stripped; quoted values keep
// any `#` they contain. Later assignments win (shell semantics).
func extractEnvVar(body, name string) string {
	var found string
	prefix := name + "="
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v := line[len(prefix):]
		if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
			quote := v[0]
			if end := strings.IndexByte(v[1:], quote); end >= 0 {
				v = v[1 : 1+end]
			} else {
				v = v[1:]
			}
		} else {
			if i := strings.IndexByte(v, '#'); i >= 0 {
				v = v[:i]
			}
			v = strings.TrimSpace(v)
		}
		found = v
	}
	return found
}

// extractCertsDirFromOpts pulls the --certs-dir argument out of
// MINIO_OPTS in body, if any. Returns "" when MINIO_OPTS is absent or
// doesn't carry the flag. Handles both `--certs-dir /path` and
// `--certs-dir=/path` spellings.
func extractCertsDirFromOpts(body string) string {
	opts := extractEnvVar(body, "MINIO_OPTS")
	if opts == "" {
		return ""
	}
	fields := strings.Fields(opts)
	for i, f := range fields {
		switch {
		case f == "--certs-dir" && i+1 < len(fields):
			return fields[i+1]
		case strings.HasPrefix(f, "--certs-dir="):
			return strings.TrimPrefix(f, "--certs-dir=")
		}
	}
	return ""
}

// pickConfigSourcePeer chooses a node to copy bootstrap config from.
// Selection priority (highest first):
//
//  1. same-pool, online, has marker
//  2. same-pool, any state, has marker
//  3. cross-pool, online, has marker
//  4. cross-pool, any state, has marker
//
// Pool affinity wins over the admin API's online/offline signal
// because per-host SSH overrides, unit-file drop-ins and mount layout
// are more likely to match within a pool than across pools — a
// cross-pool peer that happens to be online but uses different
// credentials would fail mid-bootstrap. Falling back to offline same-
// pool peers matters when the cluster admin state is stale or the
// only file-holders happen to be in a degraded state but are still
// SSH-reachable: we'd rather try stale config than refuse to redeploy.
func pickConfigSourcePeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, marker string) (domain.Node, bool) {
	return pickConfigSourcePeerWith(rc.nodes, target, func(n domain.Node) bool {
		ok, _ := remoteFileExists(ctx, deps, rc, n, marker)
		return ok
	})
}

// pickConfigSourcePeerWith is the dependency-injected core of
// pickConfigSourcePeer: takes the candidate node list and an
// existence-probe callback so unit tests can pin the priorities
// without an SSH transport. See pickConfigSourcePeer for the full
// priority order.
func pickConfigSourcePeerWith(nodes []domain.Node, target domain.Node, peerHasMarker func(domain.Node) bool) (domain.Node, bool) {
	if peer, ok := pickPeerInPoolScope(nodes, target, peerHasMarker, true); ok {
		return peer, true
	}
	return pickPeerInPoolScope(nodes, target, peerHasMarker, false)
}

// pickPeerInPoolScope does one pass over nodes restricted to either
// same-pool peers (samePool=true) or cross-pool peers (samePool=false).
// Within scope, online wins over the offline fallback.
func pickPeerInPoolScope(nodes []domain.Node, target domain.Node, peerHasMarker func(domain.Node) bool, samePool bool) (domain.Node, bool) {
	var fallback domain.Node
	var haveFallback bool
	for _, n := range nodes {
		if n.ID == target.ID {
			continue
		}
		if samePool && n.Pool != target.Pool {
			continue
		}
		if !samePool && n.Pool == target.Pool {
			continue
		}
		if !peerHasMarker(n) {
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

func pathBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
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

// resolveRedeployVersion picks the GitHub release tag whose name
// contains the cluster's currently-reported ServerInfo.Version. The
// catalog is iterated newest-first so the most recent matching tag
// wins. No normalization — the cluster version must appear verbatim
// inside the tag.
func resolveRedeployVersion(clusterVersion string) (string, error) {
	needle := strings.TrimSpace(clusterVersion)
	if needle == "" {
		return "", errors.New("cluster has not reported a version yet; refresh the cluster and retry")
	}
	for _, v := range deploy.SupportedVersions() {
		if strings.Contains(v.Tag, needle) {
			return v.Tag, nil
		}
	}
	return "", fmt.Errorf("no release in the catalog matches cluster version %q", clusterVersion)
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
	return hostHasSystemdUnit(ctx, deps, rc, n, "buckit.service")
}

// hostHasSystemdUnit probes whether a named systemd unit is loaded on a
// host. Uses `systemctl show -p LoadState` (which always exits 0) so we
// can distinguish "unit missing" from "ssh broken" — `systemctl cat`
// exits 1 in both cases and would muddle the two.
func hostHasSystemdUnit(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, unit string) (bool, error) {
	out, err := runHostStep(ctx, deps, rc, n, loadStateProbeCommand(unit))
	if err != nil {
		return false, err
	}
	return parseLoadStateLoaded(out), nil
}

// loadStateProbeCommand builds the shell snippet hostHasSystemdUnit runs
// on the target host. Extracted so tests can assert the exact wire bytes
// without standing up an SSH server.
func loadStateProbeCommand(unit string) string {
	return fmt.Sprintf(
		`state=$(systemctl show -p LoadState --value %s 2>/dev/null || true); printf "%%s" "$state"`,
		shellQuote(unit),
	)
}

// parseLoadStateLoaded interprets the output of loadStateProbeCommand.
// Anything other than the literal "loaded" (after trimming whitespace)
// is treated as the unit being unavailable for restart — that catches
// "not-found", "masked", "error", and the empty-string case where
// systemctl itself wasn't on the host.
func parseLoadStateLoaded(showOutput string) bool {
	return strings.TrimSpace(showOutput) == "loaded"
}

// unitMissingError formats the operator-facing error for a set of hosts
// where the cluster's service unit isn't installed. The recovery hint
// depends on the engine: Buckit clusters have first-class redeploy /
// admin-API upgrade paths; MinIO clusters don't get those, so the hint
// nudges the operator toward reinstall or re-import instead.
func unitMissingError(unit string, missing []string, engine domain.ClusterEngine) error {
	hint := `use "Upgrade cluster via Admin API" or "Redeploy software" first`
	if engine == domain.EngineMinio {
		hint = `the host is not running a managed minio.service — reinstall MinIO or import a different cluster`
	}
	return fmt.Errorf("%s is not installed on %s — %s",
		unit, strings.Join(missing, ", "), hint)
}

// preflightUnitPresent verifies that the cluster's service unit is loaded
// on every target host. Returns a clear, operator-facing error naming the
// missing hosts and pointing at the right recovery action. SSH/transport
// failures are surfaced as-is — we don't want to mask a connectivity
// problem as a precondition failure.
//
// Wire this at the start of any executor that drives `systemctl <verb>
// buckit.service` (or minio.service) so the operator sees the friendly
// message instead of a downstream "Unit not loaded" from the first
// restart attempt.
func preflightUnitPresent(ctx context.Context, deps Deps, rc *runCtx, hosts []domain.Node) error {
	if len(hosts) == 0 {
		return nil
	}
	unit := unitName(rc.cluster.Engine)
	var missing []string
	for _, n := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := hostHasSystemdUnit(ctx, deps, rc, n, unit)
		if err != nil {
			return fmt.Errorf("%s: %s probe failed: %w", n.Hostname, unit, err)
		}
		if !ok {
			missing = append(missing, n.Hostname)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return unitMissingError(unit, missing, rc.cluster.Engine)
}
