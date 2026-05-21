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
	"strings"
	"sync"
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

var (
	refreshTCPProbe = func(ctx context.Context, address string) bool {
		d := net.Dialer{Timeout: refreshDialTimeout}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	refreshHTTPProbe = func(ctx context.Context, client *http.Client, rawURL string) bool {
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
	}
)

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
				body, _ := json.Marshal(map[string]any{"ok": false, "error": ie.Inner})
				writeSSE(w, eventID, "result", string(body))
				flusher.Flush()
				return
			}
			body, _ := json.Marshal(map[string]any{
				"ok":    false,
				"error": domain.ImportError{Kind: domain.ImportErrUnreachable, Message: err.Error()},
			})
			writeSSE(w, eventID, "result", string(body))
			flusher.Flush()
			return
		}

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
			ConsoleURL:     req.Candidate.ConsoleURL,
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
		creds := domain.AdminCreds{
			URL:       req.Candidate.URL,
			AccessKey: req.Candidate.Username,
			SecretKey: req.Candidate.Password,
			Insecure:  req.Insecure,
		}
		if err := opts.ClusterAdmin.Put(r.Context(), clusterID, creds); err != nil {
			writeError(w, http.StatusInternalServerError, "put_admin_failed", err.Error())
			return
		}

		syncAlias(r.Context(), opts)

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
		// Re-fetch after refresh so we return the updated rows.
		updated, err := opts.Clusters.List(ctx)
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
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		refreshConcurrently(ctx, opts, []domain.Cluster{c}, true)
		updated, err := opts.Clusters.Get(ctx, id)
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
		markUnreachable(ctx, opts, c, err)
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
	}
	if info.ConsoleURL != "" {
		c.ConsoleURL = info.ConsoleURL
	} else if consoleURL := fallbackConsoleURL(creds.URL); consoleURL != "" {
		c.ConsoleURL = consoleURL
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
		startAsyncNodeProbeRefresh(opts, c.ID, nodeList, creds)
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
		n.State = s.State
		n.Version = s.Version
		n.UptimeSec = s.Uptime
		n.OS = s.OS
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

func startAsyncNodeProbeRefresh(opts Options, clusterID string, nodeList []domain.Node, creds domain.AdminCreds) {
	if opts.Nodes == nil || len(nodeList) == 0 {
		return
	}
	snapshot := append([]domain.Node(nil), nodeList...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshProbeTimeout)
		defer cancel()
		_ = refreshNodeConnectivity(ctx, opts, clusterID, snapshot, creds)
	}()
}

func refreshNodeConnectivity(ctx context.Context, opts Options, _ string, nodeList []domain.Node, creds domain.AdminCreds) error {
	httpClient := refreshProbeHTTPClient(creds)
	secure := refreshProbeSecure(creds.URL)

	sem := make(chan struct{}, refreshProbeConcurrency)
	var wg sync.WaitGroup
	for _, node := range nodeList {
		wg.Add(1)
		go func(node domain.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			next := node
			next.Pingable = probePing(ctx, node)
			next.APIAccessible = probeHTTP(ctx, httpClient, secure, node.Hostname, 9000, "/minio/health/live")
			next.ConsoleAccessible = probeHTTP(ctx, httpClient, secure, node.Hostname, 9001, "/")
			next.Sshable = refreshTCPProbe(ctx, net.JoinHostPort(node.Hostname, fmt.Sprintf("%d", sshPortOrDefault(node.SSHPort))))
			_ = opts.Nodes.Put(ctx, next)
		}(node)
	}
	wg.Wait()
	return ctx.Err()
}

func probePing(ctx context.Context, node domain.Node) bool {
	if refreshTCPProbe(ctx, net.JoinHostPort(node.Hostname, fmt.Sprintf("%d", sshPortOrDefault(node.SSHPort)))) {
		return true
	}
	return refreshTCPProbe(ctx, net.JoinHostPort(node.Hostname, "9000"))
}

func probeHTTP(ctx context.Context, client *http.Client, secure bool, hostname string, port int, path string) bool {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	rawURL := fmt.Sprintf("%s://%s:%d%s", scheme, hostname, port, path)
	return refreshHTTPProbe(ctx, client, rawURL)
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

func fallbackConsoleURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:9001", scheme, u.Hostname())
}

func sshPortOrDefault(port int) int {
	if port > 0 {
		return port
	}
	return 22
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
