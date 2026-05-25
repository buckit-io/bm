package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
)

// Per-request budget for the SSH dial + journalctl run. Slightly generous
// because journalctl with `--since "7 days ago"` on a chatty host can scan
// for a few seconds.
const nodeLogsTimeout = 20 * time.Second

// Maximum lines journalctl will emit per request. 10k is plenty for the
// largest preset (7d) while keeping the response well under a megabyte for
// typical log densities.
const nodeLogsHardLimit = 10000

// nodeLogsResponse is the shape rendered by the NodeLogs page. `lines` are
// the raw journalctl output in short-iso format; the UI does substring
// filtering and renders them verbatim.
type nodeLogsResponse struct {
	Host      string    `json:"host"`
	Unit      string    `json:"unit"`
	Since     string    `json:"since"`
	Limit     int       `json:"limit"`
	FetchedAt time.Time `json:"fetchedAt"`
	Lines     []string  `json:"lines"`
	Truncated bool      `json:"truncated"`
	ExitCode  int       `json:"exitCode"`
	// Stderr is surfaced for failure cases (auth denied to journal, unit
	// not found) so the UI can show a one-line hint without the operator
	// dropping to a shell. Empty on success.
	Stderr string `json:"stderr,omitempty"`
}

// rangeBudget maps the dropdown preset to the --since duration journalctl
// understands. Whitelisted to keep the value off the shell command line.
var rangeBudget = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  1 * time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// allowedUnits are the systemd units bm is willing to tail. Whitelisted so
// the operator can't pivot this endpoint into an arbitrary command runner.
// Stored as full unit names so they round-trip through journalctl, ps,
// systemctl, etc. without surprises.
var allowedUnits = map[string]struct{}{
	"buckit.service": {},
	"minio.service":  {},
}

func getNodeLogs(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Clusters == nil || opts.Nodes == nil || opts.SSHConfig == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "node logs require store + ssh wiring")
			return
		}
		q := r.URL.Query()

		// Validate query params first — these are operator-supplied, and
		// rejecting them before we touch state keeps the failure mode obvious.
		since := strings.TrimSpace(q.Get("since"))
		if since == "" {
			since = "1h"
		}
		budget, ok := rangeBudget[since]
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "since must be one of: 15m, 1h, 6h, 24h, 7d")
			return
		}

		unit := strings.TrimSpace(q.Get("unit"))
		if unit != "" {
			if _, ok := allowedUnits[unit]; !ok {
				writeError(w, http.StatusBadRequest, "bad_request", "unit must be one of: buckit.service, minio.service")
				return
			}
		}

		limit := 2000
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
				return
			}
			if n > nodeLogsHardLimit {
				n = nodeLogsHardLimit
			}
			limit = n
		}

		clusterID := chi.URLParam(r, "id")
		nodeID := chi.URLParam(r, "nodeId")

		cluster, err := opts.Clusters.Get(r.Context(), clusterID)
		if err != nil {
			if errors.Is(err, clusters.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "cluster not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		node, err := opts.Nodes.Get(r.Context(), clusterID, nodeID)
		if err != nil {
			if errors.Is(err, nodes.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "node not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}

		if unit == "" {
			unit = defaultUnitForCluster(cluster)
		}

		sshCfg, err := opts.SSHConfig.Get(r.Context(), clusterID)
		if err != nil {
			if errors.Is(err, sshconfig.ErrNotFound) {
				writeError(w, http.StatusFailedDependency, "ssh_not_configured", "SSH is not configured for this cluster")
				return
			}
			writeError(w, http.StatusInternalServerError, "ssh_config_failed", err.Error())
			return
		}

		if opts.SSHPool == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "ssh pool not configured")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), nodeLogsTimeout)
		defer cancel()

		override := overrideForNode(sshCfg, node.ID)
		creds := bmssh.Merge(sshCfg.SSH, override)
		if err := creds.Validate(); err != nil {
			writeError(w, http.StatusFailedDependency, "ssh_creds_invalid", err.Error())
			return
		}

		client, err := opts.SSHPool.Get(ctx, clusterID, hostRefFromNode(node), creds)
		if err != nil {
			writeError(w, http.StatusBadGateway, "ssh_dial_failed", err.Error())
			return
		}

		cmd := buildJournalctlCmd(unit, budget, limit, creds)
		res, err := bmssh.Run(ctx, client, cmd)
		if err != nil {
			writeError(w, http.StatusBadGateway, "journalctl_failed", err.Error())
			return
		}

		lines := splitNonEmptyLines(res.Stdout)
		resp := nodeLogsResponse{
			Host:      node.Hostname,
			Unit:      unit,
			Since:     since,
			Limit:     limit,
			FetchedAt: time.Now().UTC(),
			Lines:     lines,
			Truncated: len(lines) >= limit,
			ExitCode:  res.ExitCode,
		}
		if res.ExitCode != 0 {
			resp.Stderr = strings.TrimSpace(res.Stderr)
			if resp.Stderr == "" {
				resp.Stderr = "journalctl exited " + strconv.Itoa(res.ExitCode)
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// buildJournalctlCmd produces the exact remote command line. Inputs are all
// whitelisted by the handler — no operator-controlled string ever lands in
// the shell command unescaped. Sudo wrapping defers to deploy.SudoWrap so
// the rules match every other SSH-backed action (root → never sudo; non-root
// without creds.Sudo → never sudo; otherwise → `sudo -n bash -c ...`).
func buildJournalctlCmd(unit string, budget time.Duration, limit int, creds bmssh.Resolved) string {
	since := time.Now().Add(-budget).UTC().Format("2006-01-02 15:04:05")
	cmd := fmt.Sprintf(
		"journalctl -u %s --since %q --no-pager --output=short-iso --lines=%d",
		unit, since, limit,
	)
	return deploy.SudoWrap(creds, cmd)
}

func splitNonEmptyLines(s string) []string {
	if s == "" {
		return []string{}
	}
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func defaultUnitForCluster(c domain.Cluster) string {
	if c.Engine == domain.EngineMinio {
		return "minio.service"
	}
	return "buckit.service"
}

func overrideForNode(cfg domain.ClusterSshConfig, nodeID string) *domain.SshOverrides {
	if cfg.Overrides == nil {
		return nil
	}
	ov, ok := cfg.Overrides[nodeID]
	if !ok {
		return nil
	}
	return &ov
}

func hostRefFromNode(n domain.Node) domain.HostRef {
	port := n.SSHPort
	if port == 0 {
		port = 22
	}
	return domain.HostRef{ID: n.ID, Hostname: n.Hostname, Port: port}
}
