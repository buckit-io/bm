// Package operations implements the executor catalog the new-cluster wizard
// + per-cluster Actions menu dispatch against. Each operation lives in its
// own file; shared helpers live here.
package operations

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/discovery"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/tasks"
)

// Deps is the dependency pack each executor receives. Constructed once at
// startup; shared by every executor instance.
type Deps struct {
	Clusters     *clusters.Repo
	Nodes        *nodes.Repo
	ClusterAdmin *clusteradmin.Repo
	SSHConfig    *sshconfig.Repo
	AdminPool    *admin.Pool
	SSHPool      *bmssh.Pool
}

// runCtx is the resolved per-op state. load() builds it once per Execute call.
type runCtx struct {
	cluster      domain.Cluster
	nodes        []domain.Node
	adminCreds   domain.AdminCreds
	sshCreds     domain.SshCreds
	sshOverrides map[string]domain.SshOverrides
	admin        *admin.Client

	// pkgManagers caches the detected PackageManager per node ID for
	// the lifetime of this op. Re-probing is cheap (~50ms over SSH) but
	// skipping the second probe per host keeps multi-step ops tight.
	pkgManagers map[string]deploy.PackageManager
}

// pkgManagerForHost detects (or returns the cached) PackageManager for
// n. Memoized on rc.pkgManagers so a single op doesn't re-probe the
// same host across download / inspect / install steps.
func pkgManagerForHost(ctx context.Context, deps Deps, rc *runCtx, n domain.Node) (deploy.PackageManager, error) {
	if rc.pkgManagers == nil {
		rc.pkgManagers = make(map[string]deploy.PackageManager)
	}
	if mgr, ok := rc.pkgManagers[n.ID]; ok {
		return mgr, nil
	}
	mgr, err := deploy.DetectPackageManager(ctx, func(probeCtx context.Context, cmd string) (string, error) {
		return runHostStep(probeCtx, deps, rc, n, cmd)
	})
	if err != nil {
		return nil, err
	}
	rc.pkgManagers[n.ID] = mgr
	return mgr, nil
}

// load fetches the cluster row, nodes, and creds in one shot. Returns an
// error if any of them is missing — every executor depends on all three.
func load(ctx context.Context, deps Deps, clusterID string) (*runCtx, error) {
	if deps.Clusters == nil || deps.Nodes == nil || deps.ClusterAdmin == nil {
		return nil, errors.New("operations: repos not wired")
	}
	cluster, err := deps.Clusters.Get(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load cluster: %w", err)
	}
	nodeList, err := deps.Nodes.List(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load nodes: %w", err)
	}
	adminCreds, err := deps.ClusterAdmin.Get(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load admin creds: %w", err)
	}
	rc := &runCtx{cluster: cluster, nodes: nodeList, adminCreds: adminCreds}

	if deps.SSHConfig != nil {
		if sshCfg, err := deps.SSHConfig.Get(ctx, clusterID); err == nil {
			rc.sshCreds = sshCfg.SSH
			rc.sshOverrides = sshCfg.Overrides
		}
	}
	if deps.AdminPool != nil {
		rc.admin, err = deps.AdminPool.Get(clusterID, adminCreds)
		if err != nil {
			return nil, fmt.Errorf("admin client: %w", err)
		}
	} else {
		rc.admin, err = admin.New(adminCreds)
		if err != nil {
			return nil, fmt.Errorf("admin client: %w", err)
		}
	}
	return rc, nil
}

// unitName picks the systemd unit name for the cluster's engine. Buckit
// clusters use buckit.service; imported MinIO clusters (until migrated)
// still run the upstream minio.service.
func unitName(engine domain.ClusterEngine) string {
	if engine == domain.EngineMinio {
		return "minio.service"
	}
	return "buckit.service"
}

// validateEngineCompat rejects Buckit-only ops dispatched against MinIO
// clusters. The dispatch handler maps the returned error to 400.
func validateEngineCompat(c domain.Cluster, kind tasks.OpKind) error {
	if !buckitOnly[kind] {
		return nil
	}
	if c.Engine != domain.EngineBuckit {
		return fmt.Errorf("engine_mismatch: %s is Buckit-only; migrate the cluster first", kind)
	}
	return nil
}

// buckitOnly lists ops that don't apply to MinIO clusters. MinIO Community
// Edition is frozen at its last 2025 release; the upgrade path is migrate
// to Buckit, not bump MinIO.
var buckitOnly = map[tasks.OpKind]bool{
	tasks.OpClusterUpgradeBySystemctl:   true,
	tasks.OpClusterUpgradeByAdminUpdate: true,
	tasks.OpRotateRootCreds:             true,
	tasks.OpRedeploySoftware:            true,
}

// validateEngineAtDispatch loads the cluster row and runs the engine check
// before dispatch returns. Lets Buckit-only ops surface 400 immediately
// instead of accepting the dispatch and failing async.
func validateEngineAtDispatch(deps Deps, clusterID string, kind tasks.OpKind) error {
	if deps.Clusters == nil {
		return errors.New("operations: clusters repo not wired")
	}
	c, err := deps.Clusters.Get(context.Background(), clusterID)
	if err != nil {
		// Let the executor surface the "cluster not found" error via the
		// normal Execute path; don't reject dispatch on a transient lookup.
		return nil
	}
	return validateEngineCompat(c, kind)
}

// hostRef converts a Node into the lighter HostRef every ssh helper uses.
func hostRef(n domain.Node) domain.HostRef {
	port := n.SSHPort
	if port == 0 {
		port = 22
	}
	return domain.HostRef{ID: n.ID, Hostname: n.Hostname, Port: port}
}

// resolvedCreds merges cluster-default SSH creds with any per-host override.
func resolvedCreds(rc *runCtx, nodeID string) bmssh.Resolved {
	var ov *domain.SshOverrides
	if rc.sshOverrides != nil {
		if v, ok := rc.sshOverrides[nodeID]; ok {
			vv := v
			ov = &vv
		}
	}
	return bmssh.Merge(rc.sshCreds, ov)
}

// targetHosts filters nodes by req.TargetHostIDs. Empty → all nodes.
func targetHosts(req tasks.DispatchRequest, nodeList []domain.Node) ([]domain.Node, error) {
	if len(req.TargetHostIDs) == 0 {
		return nodeList, nil
	}
	byID := make(map[string]domain.Node, len(nodeList))
	for _, n := range nodeList {
		byID[n.ID] = n
	}
	out := make([]domain.Node, 0, len(req.TargetHostIDs))
	for _, id := range req.TargetHostIDs {
		n, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("operations: unknown target host id %q", id)
		}
		out = append(out, n)
	}
	return out, nil
}

// runHostStep wraps ssh.Run with the exit-code-is-failure semantic the
// orchestrated ops depend on. Returns the captured stdout for callers who
// want it; the error is non-nil on transport failure OR non-zero exit.
func runHostStep(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, cmd string) (string, error) {
	client, err := deps.SSHPool.Get(ctx, rc.cluster.ID, hostRef(n), resolvedCreds(rc, n.ID))
	if err != nil {
		return "", fmt.Errorf("ssh dial %s: %w", n.Hostname, err)
	}
	r, err := bmssh.Run(ctx, client, cmd)
	if err != nil {
		return r.Stdout, err
	}
	if r.ExitCode != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = "exit " + strconv.Itoa(r.ExitCode)
		}
		return r.Stdout, fmt.Errorf("%s: %s", n.Hostname, msg)
	}
	return r.Stdout, nil
}

// WaitOptions tunes the health-wait loop.
type WaitOptions struct {
	Timeout time.Duration
	Tick    time.Duration
}

func (o WaitOptions) withDefaults(defaultTimeout time.Duration) WaitOptions {
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	if o.Tick == 0 {
		o.Tick = 2 * time.Second
	}
	return o
}

// waitClusterHealthy polls ServerInfo until every host's state is "online"
// or the timeout fires. Used after restart/start ops to confirm the cluster
// is back in service before terminating the executor.
func waitClusterHealthy(ctx context.Context, ac *admin.Client, opts WaitOptions) error {
	opts = opts.withDefaults(90 * time.Second)
	loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	for {
		if err := loopCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("cluster not healthy after %s", opts.Timeout)
		}
		attemptCtx, attemptCancel := context.WithTimeout(loopCtx, 10*time.Second)
		info, err := ac.ServerInfo(attemptCtx)
		attemptCancel()
		if err == nil && allOnline(info) {
			return nil
		}
		select {
		case <-loopCtx.Done():
			continue
		case <-time.After(opts.Tick):
		}
	}
}

func allOnline(info *domain.ServerInfo) bool {
	if info == nil || len(info.Servers) == 0 {
		return false
	}
	for _, s := range info.Servers {
		if s.State != domain.NodeOnline {
			return false
		}
	}
	return true
}

// waitHostHealthy polls the local /minio/health/live endpoint via SSH from
// the host itself — the most reliable signal that the service is running on
// that node. Used between hosts in rolling_restart and after start_cluster.
//
// The probe honours the cluster's admin-URL scheme: https clusters use
// curl -k against https://127.0.0.1 (loopback is rarely a cert SAN), and
// http clusters use plain http. Mirror of deploy.Installer.waitHealthy.
//
// Each probe iteration runs under its own 10 s sub-context so a hung
// SSH session (host accepted the TCP/handshake but never returns from
// the command) cannot consume the whole budget — the outer deadline
// still terminates the loop on time.
func waitHostHealthy(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, opts WaitOptions) error {
	opts = opts.withDefaults(60 * time.Second)
	loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	probe := healthProbeCommand(rc.adminCreds)
	for {
		if err := loopCtx.Err(); err != nil {
			return fmt.Errorf("%s not healthy after %s", n.Hostname, opts.Timeout)
		}
		probeCtx, probeCancel := context.WithTimeout(loopCtx, 10*time.Second)
		client, err := deps.SSHPool.Get(probeCtx, rc.cluster.ID, hostRef(n), resolvedCreds(rc, n.ID))
		if err == nil {
			r, runErr := bmssh.Run(probeCtx, client, probe)
			if runErr == nil && r.ExitCode == 0 {
				probeCancel()
				return nil
			}
		}
		probeCancel()
		select {
		case <-loopCtx.Done():
			return fmt.Errorf("%s not healthy after %s", n.Hostname, opts.Timeout)
		case <-time.After(opts.Tick):
		}
	}
}

// healthProbeCommand builds the curl invocation used by waitHostHealthy. It
// matches the host's S3 API scheme to the admin-URL scheme so TLS clusters
// don't get probed over plain http and vice versa.
func healthProbeCommand(c domain.AdminCreds) string {
	curlOpts := "-fsS --max-time 5"
	scheme := "http"
	if isHTTPSURL(c.URL) {
		curlOpts += " -k"
		scheme = "https"
	}
	return fmt.Sprintf("curl %s %s://127.0.0.1:%d/minio/health/live", curlOpts, scheme, apiPortFromCreds(c))
}

// isHTTPSURL reports whether the admin URL uses https. An unparseable or
// empty URL is treated as https — the same conservative default the refresh
// probes in the API package use, so all the loopback checks stay aligned.
func isHTTPSURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return true
	}
	return strings.EqualFold(u.Scheme, "https")
}

// waitSSHReturn polls SSH dial until a handshake succeeds. Used by
// reboot_host to detect the host coming back after the reboot drops
// its TCP connections.
//
// Each iteration runs under its own 15 s sub-context (10 s dial + a
// small `true` round-trip). Without that bound, a half-up host whose
// sshd accepts the handshake but whose session.Run never returns
// (kernel hang, hung systemd, IPMI-style serial-console wedge) would
// pin the outer loop forever — the `if time.Now().After(deadline)`
// check below only runs between iterations, so a single never-returning
// Run skips it entirely.
func waitSSHReturn(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, opts WaitOptions) error {
	opts = opts.withDefaults(5 * time.Minute)
	loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	for {
		if err := loopCtx.Err(); err != nil {
			return fmt.Errorf("%s did not return SSH after %s", n.Hostname, opts.Timeout)
		}
		// Drop any cached client first — the one that was open before
		// the reboot is stale.
		deps.SSHPool.Drop(rc.cluster.ID, n.ID)
		probeCtx, probeCancel := context.WithTimeout(loopCtx, 15*time.Second)
		client, err := deps.SSHPool.Get(probeCtx, rc.cluster.ID, hostRef(n), resolvedCreds(rc, n.ID))
		if err == nil {
			// A successful handshake is the signal; also verify a
			// no-op runs end-to-end so we don't mistake a half-up
			// sshd for a healthy host.
			_, runErr := bmssh.Run(probeCtx, client, "true")
			if runErr == nil {
				probeCancel()
				return nil
			}
		}
		probeCancel()
		select {
		case <-loopCtx.Done():
			return fmt.Errorf("%s did not return SSH after %s", n.Hostname, opts.Timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

// apiPortFromCreds extracts the S3 API port from the cluster's admin URL.
// Defaults to 9000 when the URL doesn't carry an explicit port.
func apiPortFromCreds(c domain.AdminCreds) int {
	u := c.URL
	if u == "" {
		return 9000
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	if _, port, err := net.SplitHostPort(u); err == nil {
		if p, err := strconv.Atoi(port); err == nil {
			return p
		}
	}
	return 9000
}

// refreshClusterRow re-fetches ServerInfo and persists an updated cluster row.
// Called by every executor on success. Best-effort — a failed refresh doesn't
// roll back the op (which already succeeded).
func refreshClusterRow(ctx context.Context, deps Deps, clusterID string, ac *admin.Client) {
	if deps.Clusters == nil || deps.Nodes == nil {
		return
	}
	c, err := deps.Clusters.Get(ctx, clusterID)
	if err != nil {
		return
	}
	info, err := ac.ServerInfo(ctx)
	now := time.Now().UTC()
	if err != nil {
		c.LastFetchedAt = &now
		if c.UnreachableSince == nil {
			c.UnreachableSince = &now
		}
		_ = deps.Clusters.Put(ctx, c)
		return
	}
	c.LastFetchedAt = &now
	c.UnreachableSince = nil
	if info.Version != "" {
		c.Version = info.Version
		// Engine is derived from Version, so re-classify on every refresh.
		// This also self-heals rows imported before ParseEngine learned to
		// accept bare / RFC3339 timestamps.
		c.Engine = discovery.ParseEngine(info.Version)
	}
	c.RawBytes = info.Raw
	c.UsedBytes = info.Used
	c.UsableBytes = info.Usable
	c.PoolCount = info.Pools

	nodeList, _ := deps.Nodes.List(ctx, clusterID)
	summary := clusters.Summarize(nodeList)
	c.HealthSummary = &summary
	c.Health = clusters.Rollup(c, summary)
	_ = deps.Clusters.Put(ctx, c)
}

// setAPIFrozen writes the cluster's APIFrozen flag. Best-effort.
func setAPIFrozen(ctx context.Context, deps Deps, clusterID string, frozen bool) {
	if deps.Clusters == nil {
		return
	}
	c, err := deps.Clusters.Get(ctx, clusterID)
	if err != nil {
		return
	}
	c.APIFrozen = frozen
	now := time.Now().UTC()
	c.LastActivityAt = now
	_ = deps.Clusters.Put(ctx, c)
}

// markUnreachable marks a cluster as unreachable now. Used by stop_cluster
// where the operator explicitly took the cluster down.
func markUnreachable(ctx context.Context, deps Deps, clusterID string) {
	if deps.Clusters == nil {
		return
	}
	c, err := deps.Clusters.Get(ctx, clusterID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	c.UnreachableSince = &now
	c.LastFetchedAt = &now
	c.Health = domain.HealthUnknown
	_ = deps.Clusters.Put(ctx, c)
}

// formatDuration renders a duration as a compact "47s" / "4m12s" string for
// history summaries.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

// Unused import suppression: ssh import is referenced via bmssh.
var _ = ssh.ErrNoAuth
