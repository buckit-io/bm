package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/preflight"
	bmssh "github.com/buckit-io/bm/internal/ssh"
)

const (
	newClusterProbeConcurrency = 8
	artifactValidateTimeout    = 10 * time.Second
)

// ---- versions endpoint ----

func listVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		versions := deploy.SupportedVersions()
		if versions == nil {
			logAction(r, "versions.fetch", "result", "failed", "reason", "github_unreachable")
			writeError(w, http.StatusBadGateway, "github_unreachable", "could not fetch releases from GitHub")
			return
		}
		logAction(r, "versions.fetch", "result", "ok", "count", len(versions))
		writeJSON(w, http.StatusOK, versions)
	}
}

// ---- artifact validate ----

type artifactValidateRequest struct {
	URL string `json:"url"`
}

func validateArtifact() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req artifactValidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		u := strings.TrimSpace(req.URL)
		if u == "" {
			logAction(r, "artifact.validate", "result", "failed", "reason", "empty_url")
			writeJSON(w, http.StatusOK, domain.CustomUrlCheck{State: domain.CustomUrlError, Message: "URL is required."})
			return
		}
		logAction(r, "artifact.validate", "url", u)
		ctx, cancel := context.WithTimeout(r.Context(), artifactValidateTimeout)
		defer cancel()

		client := &http.Client{Timeout: artifactValidateTimeout}
		req2, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			logAction(r, "artifact.validate", "result", "failed", "url", u, "reason", err.Error())
			writeJSON(w, http.StatusOK, domain.CustomUrlCheck{State: domain.CustomUrlError, Message: err.Error()})
			return
		}
		resp, err := client.Do(req2)
		if err != nil {
			logAction(r, "artifact.validate", "result", "failed", "url", u, "reason", err.Error())
			writeJSON(w, http.StatusOK, domain.CustomUrlCheck{State: domain.CustomUrlError, Message: err.Error()})
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			logAction(r, "artifact.validate", "result", "failed", "url", u, "status", resp.StatusCode)
			writeJSON(w, http.StatusOK, domain.CustomUrlCheck{State: domain.CustomUrlError, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
			return
		}

		check := domain.CustomUrlCheck{
			State:     domain.CustomUrlValid,
			SizeBytes: resp.ContentLength,
		}
		// Try a sibling .sha256.
		if sha, ok := fetchSidecar(ctx, client, u+".sha256"); ok {
			check.SHA256 = sha
			logAction(r, "artifact.validate", "result", "valid", "url", u, "size_bytes", resp.ContentLength, "sha256", sha)
		} else {
			check.State = domain.CustomUrlWarn
			check.Message = "Reachable, but no sha256 sidecar found at " + u + ".sha256"
			logAction(r, "artifact.validate", "result", "warn", "url", u, "size_bytes", resp.ContentLength, "reason", check.Message)
		}
		writeJSON(w, http.StatusOK, check)
	}
}

func fetchSidecar(ctx context.Context, c *http.Client, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", false
	}
	// sha256sum format: "<hex>  filename"; we just want the first field.
	parts := strings.Fields(strings.TrimSpace(string(body)))
	if len(parts) == 0 {
		return "", false
	}
	hex := parts[0]
	if len(hex) != 64 {
		return "", false
	}
	return hex, true
}

// ---- new-cluster discover (rich probe) ----

type newDiscoverRequest struct {
	Hosts []domain.HostRow `json:"hosts"`
	SSH   domain.SshCreds  `json:"ssh"`
}

func newClusterDiscover(pool *bmssh.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req newDiscoverRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		logAction(r, "new_cluster.discover", "hosts", len(req.Hosts), "ssh_user", req.SSH.User, "auth", req.SSH.AuthMethod)

		out := make(map[string]domain.WizardDiscoveryResult, len(req.Hosts))
		var mu sync.Mutex
		sem := make(chan struct{}, newClusterProbeConcurrency)
		var wg sync.WaitGroup

		for _, h := range req.Hosts {
			if h.Hostname == "" {
				continue
			}
			wg.Add(1)
			go func(h domain.HostRow) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				creds := bmssh.Merge(req.SSH, h.SSHOverride)
				ref := domain.HostRef{ID: h.ID, Hostname: h.Hostname, Port: h.Port}
				var result domain.WizardDiscoveryResult
				if pool == nil {
					result = domain.WizardDiscoveryResult{State: domain.WizardDiscoveryFailed, Error: "ssh pool not configured"}
				} else {
					client, err := pool.Get(ctx, "draft-"+h.ID, ref, creds)
					if err != nil {
						result = domain.WizardDiscoveryResult{State: domain.WizardDiscoveryFailed, Error: err.Error()}
					} else {
						result = deploy.RichProbe(ctx, client)
					}
				}
				mu.Lock()
				out[h.ID] = result
				mu.Unlock()
			}(h)
		}
		wg.Wait()
		okCount := 0
		failCount := 0
		for hostID, result := range out {
			if result.State == domain.WizardDiscoveryDone {
				okCount++
				continue
			}
			failCount++
			logAction(r, "new_cluster.discover.host", "host_id", hostID, "state", result.State, "error", result.Error)
		}
		logAction(r, "new_cluster.discover", "result", "complete", "ok_hosts", okCount, "failed_hosts", failCount)
		writeJSON(w, http.StatusOK, out)
	}
}

// ---- new-cluster preflight ----

func newClusterPreflight(pool *bmssh.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var draft domain.NewClusterDraft
		if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		conn := &poolHostConn{pool: pool, ssh: draft.SSH, httpClient: &http.Client{Timeout: 10 * time.Second}}
		results := preflight.RunAll(ctx, conn, draft)
		logAction(r, "new_cluster.preflight", "cluster", draft.Name, "hosts", len(draft.Hosts), "mounts", len(draft.Topology.SelectedMounts))
		logPreflightResults(r, "new_cluster.preflight", results)
		writeJSON(w, http.StatusOK, results)
	}
}

// poolHostConn implements preflight.HostConn against the SSH pool. The
// per-host SSH client is cached under a synthetic clusterId ("draft-<hostId>")
// so the wizard's discover + preflight + retries reuse the same connection.
type poolHostConn struct {
	pool       *bmssh.Pool
	ssh        domain.SshCreds
	httpClient *http.Client
}

func (p *poolHostConn) Run(ctx context.Context, h domain.HostRow, cmd string) (string, string, int, error) {
	if p.pool == nil {
		return "", "", 0, fmt.Errorf("ssh pool not configured")
	}
	creds := bmssh.Merge(p.ssh, h.SSHOverride)
	ref := domain.HostRef{ID: h.ID, Hostname: h.Hostname, Port: h.Port}
	client, err := p.pool.Get(ctx, "draft-"+h.ID, ref, creds)
	if err != nil {
		return "", "", 0, err
	}
	r, err := bmssh.Run(ctx, client, cmd)
	if err != nil {
		return r.Stdout, r.Stderr, -1, err
	}
	return r.Stdout, r.Stderr, r.ExitCode, nil
}

func (p *poolHostConn) HEAD(ctx context.Context, url string) (int, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.ContentLength, nil
}
