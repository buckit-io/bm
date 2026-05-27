package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/domain"
)

// Request is the body the import wizard POSTs to /api/v1/clusters/import/discover.
type Request struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Insecure bool   `json:"insecure,omitempty"`
}

// Discover walks the import flow against a real admin endpoint. Emits
// progress lines on out as it goes; final return is the candidate (ok path)
// or a typed domain.ImportError wrapped in a sentinel error.
//
// out is not closed by Discover — callers own its lifecycle so the HTTP
// handler can encode the final result frame after Discover returns.
func Discover(ctx context.Context, req Request, out chan<- domain.DiscoveryProgress) (*domain.ImportCandidate, error) {
	emit := func(level, format string, args ...any) {
		if out == nil {
			return
		}
		select {
		case out <- domain.DiscoveryProgress{TS: time.Now().UTC(), Level: level, Text: fmt.Sprintf(format, args...)}:
		case <-ctx.Done():
		}
	}

	if strings.TrimSpace(req.URL) == "" {
		emit("error", "URL is required")
		return nil, &ImportError{Inner: domain.ImportError{Kind: domain.ImportErrInvalidURL, Message: "URL is required."}}
	}
	parsedURL, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || parsedURL.Host == "" {
		emit("error", "Could not parse URL: %s", req.URL)
		return nil, &ImportError{Inner: domain.ImportError{Kind: domain.ImportErrInvalidURL, Message: "Could not parse URL."}}
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		emit("error", "Access key and secret key are required")
		return nil, &ImportError{Inner: domain.ImportError{Kind: domain.ImportErrAuth, Message: "Access key and secret key are required."}}
	}

	emit("info", "Connecting to %s", req.URL)
	emit("info", "Authenticating as %s", req.Username)

	client, err := admin.New(domain.AdminCreds{
		URL:       req.URL,
		AccessKey: req.Username,
		SecretKey: req.Password,
		Insecure:  req.Insecure,
	})
	if err != nil {
		emit("error", "%v", err)
		return nil, &ImportError{Inner: domain.ImportError{Kind: domain.ImportErrInvalidURL, Message: err.Error()}}
	}

	emit("info", "GET /minio/admin/v3/info")
	info, err := client.ServerInfo(ctx)
	if err != nil {
		kind := mapAdminError(err)
		emit("error", "%v", err)
		return nil, &ImportError{Inner: domain.ImportError{Kind: kind, Message: err.Error()}}
	}

	engine := ParseEngine(info.Version)
	emit("ok", "Server reports %s %s", engineLabel(engine), info.Version)

	nodes, totals := buildNodes(parsedURL.Host, info)
	emit("ok", "Found %d nodes across %d pool(s)", len(nodes), totals.poolCount)

	// Per-host detail lines so the import modal feels like a live discover.
	for _, n := range nodes {
		dataDrives := 0
		var driveSize int64
		for _, d := range n.Drives {
			if d.IsBoot {
				continue
			}
			dataDrives++
			if d.SizeBytes > driveSize {
				driveSize = d.SizeBytes
			}
		}
		emit("info", "Discovering %s", n.Hostname)
		emit("info", "  └─ %d data drives, %s each", dataDrives, prettyBytes(driveSize))
		if n.Kernel != "" || n.RAMBytes != nil {
			ramGB := int64(0)
			if n.RAMBytes != nil {
				ramGB = *n.RAMBytes / (1024 * 1024 * 1024)
			}
			emit("info", "  └─ Kernel %s, %d GB RAM", orDash(n.Kernel), ramGB)
		}
	}

	emit("info", "Computing topology")
	emit("ok", "Discovery complete")

	return &domain.ImportCandidate{
		SuggestedName: slugifyHost(parsedURL.Hostname()),
		URL:           req.URL,
		Username:      req.Username,
		Password:      req.Password,
		Engine:        engine,
		Version:       info.Version,
		Nodes:         nodes,
		PoolCount:     totals.poolCount,
		DriveCount:    totals.driveCount,
		RawBytes:      info.Raw,
		UsedBytes:     info.Used,
		UsableBytes:   info.Usable,
		Parity:        info.Parity,
		ConsoleURL:    info.ConsoleURL,
	}, nil
}

// ImportError lets callers do errors.As(err, &ie) to unwrap a typed import error.
type ImportError struct {
	Inner domain.ImportError
}

func (e *ImportError) Error() string {
	return fmt.Sprintf("import %s: %s", e.Inner.Kind, e.Inner.Message)
}

// mapAdminError narrows an admin.Error to the import-flow error kinds.
func mapAdminError(err error) domain.ImportErrorKind {
	var ae *admin.Error
	if errors.As(err, &ae) {
		switch ae.Kind {
		case admin.ErrUnreachable:
			return domain.ImportErrUnreachable
		case admin.ErrAuth:
			return domain.ImportErrAuth
		}
	}
	return domain.ImportErrUnreachable
}

type topology struct {
	poolCount  int
	driveCount int
}

func buildNodes(hostHint string, info *domain.ServerInfo) ([]domain.Node, topology) {
	clusterID := "__import-" + time.Now().UTC().Format("20060102150405")
	pools := map[int]struct{}{}
	driveCount := 0

	out := make([]domain.Node, 0, len(info.Servers))
	for i, s := range info.Servers {
		hostname := hostnameFromEndpoint(s.Endpoint)
		if hostname == "" {
			hostname = fmt.Sprintf("%s-%d", baseSlug(hostHint), i+1)
		}
		pools[s.PoolNumber] = struct{}{}
		drives := make([]domain.Drive, 0, len(s.Drives))
		for _, d := range s.Drives {
			drives = append(drives, domain.Drive{
				Mount:     d.Mount,
				Device:    d.Device,
				SizeBytes: d.SizeBytes,
				UsedBytes: d.UsedBytes,
				State:     d.State,
				IsBoot:    d.IsBoot,
			})
			if !d.IsBoot {
				driveCount++
			}
		}
		out = append(out, domain.Node{
			ID:        fmt.Sprintf("%s-n%d", clusterID, i+1),
			ClusterID: clusterID,
			Hostname:  hostname,
			SSHPort:   22,
			APIPort:   portFromEndpoint(s.Endpoint),
			State:     s.State,
			Version:   s.Version,
			UptimeSec: s.Uptime,
			OS:        s.OS,
			Arch:      s.Arch,
			Kernel:    s.Kernel,
			CPUModel:  s.CPUModel,
			CPUCores:  s.CPUCores,
			CPUThreads: s.CPUThreads,
			CPUMaxMHz: s.CPUMaxMHz,
			RAMBytes:  s.RAMBytes,
			NIC:       s.NIC,
			Drives:    drives,
			Pool:      s.PoolNumber,
			Pingable:  s.State == domain.NodeOnline,
			Sshable:   false, // SSH not yet configured at import time
		})
	}
	poolList := make([]int, 0, len(pools))
	for p := range pools {
		poolList = append(poolList, p)
	}
	sort.Ints(poolList)
	return out, topology{poolCount: len(pools), driveCount: driveCount}
}

func engineLabel(e domain.ClusterEngine) string {
	if e == domain.EngineMinio {
		return "MinIO"
	}
	return "Buckit"
}

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

// SlugifyHost is the exported helper API handlers use to derive cluster ids
// from operator-supplied names. Mirrors web/src/mock/api.ts:slugifyHost.
func SlugifyHost(host string) string { return slugifyHost(host) }

func slugifyHost(host string) string {
	s := strings.ToLower(host)
	var b strings.Builder
	prevDash := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "imported"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func baseSlug(host string) string {
	s := slugifyHost(host)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return s
}

func prettyBytes(n int64) string {
	if n <= 0 {
		return "—"
	}
	const (
		KiB = 1024
		MiB = KiB * 1024
		GiB = MiB * 1024
		TiB = GiB * 1024
	)
	switch {
	case n >= TiB:
		return fmt.Sprintf("%d TiB", n/TiB)
	case n >= GiB:
		return fmt.Sprintf("%d GiB", n/GiB)
	case n >= MiB:
		return fmt.Sprintf("%d MiB", n/MiB)
	default:
		return fmt.Sprintf("%d KiB", n/KiB)
	}
}

func orDash(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
