package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/discovery"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
)

const refreshConcurrency = 8
const refreshProbeConcurrency = 8
const refreshProbeTimeout = 15 * time.Second
const refreshDialTimeout = 2 * time.Second

// tcpProbeFn / httpProbeFn are aliased so atomic.Pointer can hold them.
// Stored as pointers in atomic boxes so test injection is race-safe even
// when a previous test's async probe goroutine is still running while the
// next test reassigns the implementation. (refreshNodeConnectivity is fired
// fire-and-forget via startAsyncNodeProbeRefresh — its goroutine can outlive
// the test that triggered it.)
type tcpProbeFn func(ctx context.Context, address string) bool
type httpProbeFn func(ctx context.Context, client *http.Client, rawURL string) bool
type icmpProbeFn func(ctx context.Context, hostname string) bool

// consoleProbeFn discovers a cluster's console listen port by asking its S3
// endpoint where it redirects browsers. Boxed in an atomic.Pointer so tests
// can swap in a deterministic implementation without real network I/O.
type consoleProbeFn func(ctx context.Context, creds domain.AdminCreds) (port int, ok bool)

var (
	refreshTCPProbe     atomic.Pointer[tcpProbeFn]
	refreshHTTPProbe    atomic.Pointer[httpProbeFn]
	refreshICMPProbe    atomic.Pointer[icmpProbeFn]
	refreshConsoleProbe atomic.Pointer[consoleProbeFn]
)

const icmpProbeTimeout = 1 * time.Second

func init() {
	tcp := tcpProbeFn(func(ctx context.Context, address string) bool {
		d := net.Dialer{Timeout: refreshDialTimeout}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	refreshTCPProbe.Store(&tcp)

	httpProbe := httpProbeFn(func(ctx context.Context, client *http.Client, rawURL string) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 400
	})
	refreshHTTPProbe.Store(&httpProbe)

	icmp := icmpProbeFn(shellPing)
	refreshICMPProbe.Store(&icmp)

	console := consoleProbeFn(probeConsolePortViaRedirect)
	refreshConsoleProbe.Store(&console)
}

// shellPing invokes the OS `ping` binary with a single packet and a 1s
// timeout. Avoids raw-socket privileges by delegating to the OS, which has
// already worked out the unprivileged-ICMP story per platform (setuid on
// Linux, native on macOS, IcmpSendEcho2 on Windows). Returns true iff ping
// exits 0.
func shellPing(ctx context.Context, hostname string) bool {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, icmpProbeTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(pingCtx, "ping", "-n", "1", "-w", "1000", hostname)
	case "linux":
		// iputils-ping: -W is seconds.
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-W", "1", hostname)
	default:
		// macOS / BSD: -W is milliseconds; use -t for whole-run timeout.
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-t", "1", hostname)
	}
	return cmd.Run() == nil
}

// ---- read paths ----

func listClusters(repo *clusters.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeJSON(w, http.StatusOK, []domain.Cluster{})
			return
		}
		got, err := repo.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

func getCluster(repo *clusters.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
		got, err := repo.Get(r.Context(), chi.URLParam(r, "id"))
		if errors.Is(err, clusters.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// ---- discover (SSE) ----

func postDiscover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req discovery.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		logAction(r, "import.discover", "url", req.URL, "username", req.Username, "insecure", req.Insecure)

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "no_streaming", "server does not support streaming")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		progressCh := make(chan domain.DiscoveryProgress, 32)
		done := make(chan struct{})
		eventID := 0

		// Pump progress lines into SSE frames concurrently with Discover.
		go func() {
			for line := range progressCh {
				eventID++
				body, _ := json.Marshal(line)
				writeSSE(w, eventID, "log", string(body))
				flusher.Flush()
			}
			close(done)
		}()

		candidate, err := discovery.Discover(r.Context(), req, progressCh)
		close(progressCh)
		<-done
		eventID++

		if err != nil {
			var ie *discovery.ImportError
			if errors.As(err, &ie) {
				logAction(r, "import.discover", "result", "failed", "kind", ie.Inner.Kind, "message", ie.Inner.Message)
				body, _ := json.Marshal(map[string]any{"ok": false, "error": ie.Inner})
				writeSSE(w, eventID, "result", string(body))
				flusher.Flush()
				return
			}
			logAction(r, "import.discover", "result", "failed", "message", err.Error())
			body, _ := json.Marshal(map[string]any{
				"ok":    false,
				"error": domain.ImportError{Kind: domain.ImportErrUnreachable, Message: err.Error()},
			})
			writeSSE(w, eventID, "result", string(body))
			flusher.Flush()
			return
		}

		logAction(r, "import.discover", "result", "ok", "engine", candidate.Engine, "nodes", len(candidate.Nodes), "pools", candidate.PoolCount)
		body, _ := json.Marshal(map[string]any{"ok": true, "candidate": candidate})
		writeSSE(w, eventID, "result", string(body))
		flusher.Flush()
	}
}

// ---- commit ----

type commitRequest struct {
	Candidate   domain.ImportCandidate `json:"candidate"`
	ChosenName  string                 `json:"chosenName"`
	Insecure    bool                   `json:"insecure,omitempty"`
	Description string                 `json:"description,omitempty"`
}

func postCommit(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil || opts.ClusterAdmin == nil || opts.Nodes == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repos not configured")
			return
		}
		var req commitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		chosen := strings.TrimSpace(req.ChosenName)
		if chosen == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "chosenName required")
			return
		}
		clusterID, err := allocateClusterID(r.Context(), opts.Clusters, chosen)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "alloc_failed", err.Error())
			return
		}
		now := time.Now().UTC()

		creds := domain.AdminCreds{
			URL:       req.Candidate.URL,
			AccessKey: req.Candidate.Username,
			SecretKey: req.Candidate.Password,
			Insecure:  req.Insecure,
		}
		// Resolve the console wiring once, here at import time, and persist it
		// so later refreshes never re-probe. The probe port (per-node
		// reachability) and the deep-link URL are deliberately kept separate:
		// a custom MINIO_BROWSER_REDIRECT_URL may front the console on a
		// different port (e.g. 443 behind a load balancer), which must not be
		// used as the node console probe target.
		consolePort := resolveConsolePort(r.Context(), req.Candidate.ConsoleAddress, creds)
		consoleURL := consoleDeepLink(req.Candidate.BrowserRedirectURL, creds, consolePort)

		c := domain.Cluster{
			ID:             clusterID,
			Name:           chosen,
			Description:    req.Description,
			Engine:         req.Candidate.Engine,
			Version:        req.Candidate.Version,
			Health:         domain.HealthUnknown,
			HealthSummary:  nil,
			NodeCount:      len(req.Candidate.Nodes),
			PoolCount:      req.Candidate.PoolCount,
			DriveCount:     req.Candidate.DriveCount,
			Parity:         req.Candidate.Parity,
			UsableBytes:    req.Candidate.UsableBytes,
			RawBytes:       req.Candidate.RawBytes,
			UsedBytes:      req.Candidate.UsedBytes,
			LastFetchedAt:  &now,
			SSHConfigured:  false,
			LastActivityAt: now,
			CreatedAt:      now,
			ConsoleURL:     consoleURL,
			ConsolePort:    consolePort,
		}
		if req.Description == "" {
			c.Description = fmt.Sprintf("Imported from %s", req.Candidate.URL)
		}

		// Re-key node IDs from the temporary import-id to the final cluster id.
		nodeList := make([]domain.Node, 0, len(req.Candidate.Nodes))
		for i, n := range req.Candidate.Nodes {
			nodeID := fmt.Sprintf("%s-n%d", clusterID, i+1)
			nn := n
			nn.ID = nodeID
			nn.ClusterID = clusterID
			nodeList = append(nodeList, nn)
		}
		summary := clusters.Summarize(nodeList)
		c.HealthSummary = &summary
		c.Health = clusters.Rollup(c, summary)

		if err := opts.Clusters.Put(r.Context(), c); err != nil {
			writeError(w, http.StatusInternalServerError, "put_cluster_failed", err.Error())
			return
		}
		for _, n := range nodeList {
			if err := opts.Nodes.Put(r.Context(), n); err != nil {
				writeError(w, http.StatusInternalServerError, "put_node_failed", err.Error())
				return
			}
		}
		if err := opts.ClusterAdmin.Put(r.Context(), clusterID, creds); err != nil {
			writeError(w, http.StatusInternalServerError, "put_admin_failed", err.Error())
			return
		}

		syncAlias(r.Context(), opts)
		logAction(r, "import.commit", "result", "ok", "cluster_id", clusterID, "name", chosen, "engine", req.Candidate.Engine, "nodes", len(nodeList))

		writeJSON(w, http.StatusOK, map[string]string{"clusterId": clusterID})
	}
}

// allocateClusterID slugifies name and adds a numeric suffix on collision.
// Matches the mock's makeImportedId behaviour.
func allocateClusterID(ctx context.Context, repo *clusters.Repo, name string) (string, error) {
	slug := discovery.SlugifyHost(name)
	exists, err := repo.Exists(ctx, slug)
	if err != nil {
		return "", err
	}
	if !exists {
		return slug, nil
	}
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", slug, i)
		exists, err := repo.Exists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", errors.New("commit: too many slug collisions")
}

// ---- delete (cascade) ----

func deleteCluster(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repo not configured")
			return
		}
		id := chi.URLParam(r, "id")
		if err := opts.Clusters.Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		if opts.AdminPool != nil {
			opts.AdminPool.Drop(id)
		}
		syncAlias(r.Context(), opts)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- refresh ----

func refreshAllClusters(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil {
			writeJSON(w, http.StatusOK, []domain.Cluster{})
			return
		}
		all, err := opts.Clusters.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		refreshConcurrently(ctx, opts, all, false)
		// Re-fetch off r.Context(), not the refresh ctx. When every cluster is
		// unreachable the admin calls hold ctx open until its 30s deadline, so
		// ctx is drained by the time refreshConcurrently returns. The bbolt read
		// here is cheap and local — it must not inherit the exhausted ctx.
		updated, err := opts.Clusters.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func refreshOneCluster(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "cluster repo not configured")
			return
		}
		id := chi.URLParam(r, "id")
		c, err := opts.Clusters.Get(r.Context(), id)
		if errors.Is(err, clusters.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		refreshCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		refreshConcurrently(refreshCtx, opts, []domain.Cluster{c}, true)
		// Read the post-refresh row off the original request context, not
		// refreshCtx. When the cluster's admin API hangs until refreshCtx's
		// deadline (TCP retransmit on a dead host), the cheap local bbolt
		// read below would otherwise inherit a drained context and return
		// "context deadline exceeded" even though the row is right there.
		updated, err := opts.Clusters.Get(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func refreshConcurrently(ctx context.Context, opts Options, list []domain.Cluster, includeProbes bool) {
	sem := make(chan struct{}, refreshConcurrency)
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(c domain.Cluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			refreshOne(ctx, opts, c, includeProbes)
		}(list[i])
	}
	wg.Wait()
}

func refreshOne(ctx context.Context, opts Options, c domain.Cluster, includeProbes bool) {
	if opts.ClusterAdmin == nil || opts.Clusters == nil {
		return
	}
	creds, err := opts.ClusterAdmin.Get(ctx, c.ID)
	if err != nil {
		markUnreachable(ctx, opts, c, fmt.Errorf("load creds: %w", err))
		return
	}
	var client *admin.Client
	if opts.AdminPool != nil {
		client, err = opts.AdminPool.Get(c.ID, creds)
	} else {
		client, err = admin.New(creds)
	}
	if err != nil {
		markUnreachable(ctx, opts, c, err)
		return
	}
	info, err := client.ServerInfo(ctx)
	if err != nil {
		// The admin call typically returns only after the parent
		// ctx's deadline expires (TCP retransmit on a dead host), so
		// ctx is effectively drained at this point. The local bbolt
		// writes below would silently no-op against the drained ctx
		// — and worse, the cluster row would never get its
		// UnreachableSince stamp. Detach onto a fresh short-timeout
		// context for the recovery writes.
		recCtx, recCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer recCancel()
		markUnreachable(recCtx, opts, c, err)
		// Admin API is down → the cluster can no longer
		// authoritatively report whether a server is online, so the
		// per-node State pill would otherwise stay "Online" forever.
		// Flip each row to "unreachable" and kick off per-node
		// probes — the probes independently tell the operator which
		// host stopped responding (Ping/SSH/S3 API/Console dots),
		// even though we can't query the admin API for the
		// cluster-level state.
		if opts.Nodes != nil {
			nodeList, listErr := opts.Nodes.List(recCtx, c.ID)
			if listErr == nil {
				for i := range nodeList {
					changed := false
					if nodeList[i].State != domain.NodeUnreachable {
						nodeList[i].State = domain.NodeUnreachable
						changed = true
					}
					// Admin API is down, so the last-reported drive
					// state is no longer trustworthy. Flip data drives
					// to Unknown so the node detail page stops claiming
					// they're Ready.
					for di := range nodeList[i].Drives {
						if nodeList[i].Drives[di].IsBoot {
							continue
						}
						if nodeList[i].Drives[di].State != domain.DriveUnknown {
							nodeList[i].Drives[di].State = domain.DriveUnknown
							changed = true
						}
					}
					if changed {
						_ = opts.Nodes.Put(recCtx, nodeList[i])
					}
				}
				if includeProbes && len(nodeList) > 0 {
					startAsyncNodeProbeRefresh(opts, c.ID, nodeList, creds, c.ConsolePort)
				}
			}
		}
		return
	}

	// Refresh node rows from ServerInfo synchronously so the detail page's
	// state/version/drive columns track the same fetch as the cluster row.
	var nodeList []domain.Node
	if opts.Nodes != nil {
		nodeList, _ = opts.Nodes.List(ctx, c.ID)
		nodeList = mergeAdminNodeFacts(nodeList, info)
		for _, n := range nodeList {
			_ = opts.Nodes.Put(ctx, n)
		}
	}
	now := time.Now().UTC()
	c.LastFetchedAt = &now
	c.UnreachableSince = nil
	if info.Version != "" {
		c.Version = info.Version
		// Engine is derived from Version, so re-classify on every refresh.
		// This also self-heals rows imported before ParseEngine learned to
		// accept bare / RFC3339 timestamps.
		c.Engine = discovery.ParseEngine(info.Version)
	}
	if info.Parity > 0 {
		c.Parity = info.Parity
	}
	c.RawBytes = info.Raw
	c.UsedBytes = info.Used
	c.UsableBytes = info.Usable
	c.PoolCount = info.Pools
	summary := clusters.Summarize(nodeList)
	c.HealthSummary = &summary
	c.Health = clusters.Rollup(c, summary)
	_ = opts.Clusters.Put(ctx, c)
	if includeProbes {
		startAsyncNodeProbeRefresh(opts, c.ID, nodeList, creds, c.ConsolePort)
	}
}

func markUnreachable(ctx context.Context, opts Options, c domain.Cluster, _ error) {
	now := time.Now().UTC()
	if c.UnreachableSince == nil {
		c.UnreachableSince = &now
	}
	c.Health = domain.HealthUnknown
	c.LastFetchedAt = &now
	_ = opts.Clusters.Put(ctx, c)
}

func mergeAdminNodeFacts(existing []domain.Node, info *domain.ServerInfo) []domain.Node {
	if len(existing) == 0 || info == nil || len(info.Servers) == 0 {
		return existing
	}
	byHost := make(map[string]int, len(existing))
	for i, n := range existing {
		key := normalizeHostname(n.Hostname)
		if key != "" {
			byHost[key] = i
		}
	}
	used := make(map[int]struct{}, len(existing))
	out := make([]domain.Node, 0, len(existing))
	for _, s := range info.Servers {
		idx, ok := byHost[normalizeHostname(hostnameFromEndpoint(s.Endpoint))]
		if !ok {
			continue
		}
		used[idx] = struct{}{}
		n := existing[idx]
		if p := portFromEndpoint(s.Endpoint); p > 0 {
			n.APIPort = p
		}
		n.State = s.State
		n.Version = s.Version
		n.UptimeSec = s.Uptime
		n.OS = s.OS
		if s.Arch != "" {
			n.Arch = s.Arch
		}
		n.Kernel = s.Kernel
		n.CPUModel = s.CPUModel
		n.CPUCores = s.CPUCores
		n.CPUThreads = s.CPUThreads
		n.CPUMaxMHz = s.CPUMaxMHz
		n.RAMBytes = s.RAMBytes
		n.NIC = s.NIC
		n.Pool = s.PoolNumber
		n.Drives = make([]domain.Drive, 0, len(s.Drives))
		for _, d := range s.Drives {
			n.Drives = append(n.Drives, domain.Drive{
				Mount:      d.Mount,
				Device:     d.Device,
				SizeBytes:  d.SizeBytes,
				UsedBytes:  d.UsedBytes,
				State:      d.State,
				HealingPct: d.HealingPct,
				IsBoot:     d.IsBoot,
			})
		}
		out = append(out, n)
	}
	for i, n := range existing {
		if _, ok := used[i]; !ok {
			out = append(out, n)
		}
	}
	return out
}

func startAsyncNodeProbeRefresh(opts Options, clusterID string, nodeList []domain.Node, creds domain.AdminCreds, consolePort int) {
	if opts.Nodes == nil || len(nodeList) == 0 {
		return
	}
	snapshot := append([]domain.Node(nil), nodeList...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshProbeTimeout)
		defer cancel()
		_ = refreshNodeConnectivity(ctx, opts, clusterID, snapshot, creds, consolePort)
	}()
}

func refreshNodeConnectivity(ctx context.Context, opts Options, _ string, nodeList []domain.Node, creds domain.AdminCreds, consolePort int) error {
	httpClient := refreshProbeHTTPClient(creds)
	secure := refreshProbeSecure(creds.URL)
	cPort := consolePortOrDefault(consolePort)

	sem := make(chan struct{}, refreshProbeConcurrency)
	var wg sync.WaitGroup
	for _, node := range nodeList {
		wg.Add(1)
		go func(node domain.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			tcp := *refreshTCPProbe.Load()
			apiPort := apiPortOrDefault(node.APIPort)
			next := node
			next.Pingable = probePing(ctx, node)
			next.APIAccessible = probeHTTP(ctx, httpClient, secure, node.Hostname, apiPort, "/minio/health/live")
			next.ConsoleAccessible = probeHTTP(ctx, httpClient, secure, node.Hostname, cPort, "/")
			next.Sshable = tcp(ctx, net.JoinHostPort(node.Hostname, fmt.Sprintf("%d", sshPortOrDefault(node.SSHPort))))
			_ = opts.Nodes.Put(ctx, next)
		}(node)
	}
	wg.Wait()
	return ctx.Err()
}

func probePing(ctx context.Context, node domain.Node) bool {
	icmp := *refreshICMPProbe.Load()
	if icmp(ctx, node.Hostname) {
		return true
	}
	// ICMP may be blocked by firewall, unsupported by the runtime user, or
	// the `ping` binary may be missing. Fall back to TCP probes on the
	// ports we already know about — a service that accepts TCP is
	// reachable for any practical operator purpose.
	tcp := *refreshTCPProbe.Load()
	if tcp(ctx, net.JoinHostPort(node.Hostname, fmt.Sprintf("%d", sshPortOrDefault(node.SSHPort)))) {
		return true
	}
	return tcp(ctx, net.JoinHostPort(node.Hostname, fmt.Sprintf("%d", apiPortOrDefault(node.APIPort))))
}

func probeHTTP(ctx context.Context, client *http.Client, secure bool, hostname string, port int, path string) bool {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	rawURL := fmt.Sprintf("%s://%s:%d%s", scheme, hostname, port, path)
	httpProbe := *refreshHTTPProbe.Load()
	return httpProbe(ctx, client, rawURL)
}

func refreshProbeHTTPClient(creds domain.AdminCreds) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if refreshProbeSecure(creds.URL) && creds.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // operator opt-in
	}
	return &http.Client{
		Timeout:   refreshDialTimeout,
		Transport: transport,
	}
}

func refreshProbeSecure(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return true
	}
	return strings.EqualFold(u.Scheme, "https")
}

// resolveConsolePort determines the TCP port the web console listens on, for
// per-node reachability probes. The console listen address is not a
// first-class field in the admin info API (see internal/admin/mapping.go), so
// we resolve it through progressively less certain sources:
//
//  1. MINIO_CONSOLE_ADDRESS env var — explicit, free (no network).
//  2. A live S3 browser-redirect probe — the server reports its own console
//     port, correct even behind a load balancer.
//  3. Port 9001 — last resort. An unconfigured console binds a *random* port,
//     so there is no reliable default; 9001 is what real deployments most
//     often configure (bm's own deploys included).
//
// MINIO_BROWSER_REDIRECT_URL is intentionally NOT consulted here: it may front
// the console on an unrelated port (e.g. 443 via a load balancer), which would
// be wrong to probe per-node. It only feeds the deep-link in consoleDeepLink.
//
// Resolution is run once at import time and the result is persisted, so this
// never runs on the refresh hot path.
func resolveConsolePort(ctx context.Context, consoleAddress string, creds domain.AdminCreds) int {
	if port := portFromEndpoint(consoleAddress); port > 0 {
		return port
	}
	if probe := refreshConsoleProbe.Load(); probe != nil {
		if port, ok := (*probe)(ctx, creds); ok && port > 0 {
			return port
		}
	}
	return 9001
}

// consoleDeepLink builds the "Open console" link. An operator-configured
// MINIO_BROWSER_REDIRECT_URL wins verbatim (it's the canonical browser-facing
// URL, load balancer and all); otherwise we point at the admin endpoint's host
// on the resolved console port.
func consoleDeepLink(browserRedirectURL string, creds domain.AdminCreds, consolePort int) string {
	if u := strings.TrimSpace(browserRedirectURL); u != "" {
		return u
	}
	host := hostnameFromEndpoint(creds.URL)
	if host == "" {
		return ""
	}
	scheme := "http"
	if refreshProbeSecure(creds.URL) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, consolePort)
}

// probeConsolePortViaRedirect issues an unauthenticated, browser-like GET to
// the S3 endpoint root. With MINIO_BROWSER enabled and no redirect-URL
// override set, the server answers 307 with a Location pointing at its own
// console listener — we extract that port. Returns ok=false when the endpoint
// doesn't redirect (browser disabled, override set, or not a buckit/minio S3
// endpoint).
func probeConsolePortViaRedirect(ctx context.Context, creds domain.AdminCreds) (int, bool) {
	raw := strings.TrimSpace(creds.URL)
	if raw == "" {
		return 0, false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" {
		return 0, false
	}
	base.Path = "/"
	base.RawQuery = ""

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(base.Scheme, "https") && creds.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // operator opt-in
	}
	client := &http.Client{
		Timeout:   refreshDialTimeout,
		Transport: transport,
		// Read the Location ourselves rather than following the redirect.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	pctx, cancel := context.WithTimeout(ctx, refreshDialTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return 0, false
	}
	// guessIsBrowserReq keys off a Mozilla UA plus anonymous auth.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bm console probe)")
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
		http.StatusFound, http.StatusMovedPermanently, http.StatusSeeOther:
	default:
		return 0, false
	}
	port := portFromEndpoint(resp.Header.Get("Location"))
	if port <= 0 {
		return 0, false
	}
	return port, true
}

func sshPortOrDefault(port int) int {
	if port > 0 {
		return port
	}
	return 22
}

func apiPortOrDefault(port int) int {
	if port > 0 {
		return port
	}
	return 9000
}

func consolePortOrDefault(port int) int {
	if port > 0 {
		return port
	}
	return 9001
}

// portFromEndpoint extracts the TCP port from a MinIO server endpoint
// (e.g. "http://minio1:9000" or "minio1:9000"). Returns 0 if absent or
// unparseable, in which case callers fall back to apiPortOrDefault.
func portFromEndpoint(ep string) int {
	raw := strings.TrimSpace(ep)
	if raw == "" {
		return 0
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	p := u.Port()
	if p == "" {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func hostnameFromEndpoint(ep string) string {
	if ep == "" {
		return ""
	}
	host := ep
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
}

func normalizeHostname(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

// ---- helpers ----

func syncAlias(ctx context.Context, opts Options) {
	if opts.AliasPath == "" || opts.Store == nil {
		return
	}
	sync := resolveAliasSync(opts)
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = sync(syncCtx, opts.Store, opts.AliasPath)
}

// Suppress unused-import warnings for the repos that some handlers reference
// only via the Options struct.
var (
	_ = clusteradmin.New
	_ = nodes.New
)
