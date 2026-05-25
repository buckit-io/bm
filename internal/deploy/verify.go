package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/domain"
)

type VerifyResult struct {
	ConsoleURL      string
	Version         string
	RawBytes        int64
	UsedBytes       int64
	UsableBytes     int64
	NodesHealthy    int
	NodesTotal      int
	PoolsOnline     int
	SmokeTestPassed bool
}

func verifyCluster(ctx context.Context, p DeployParams) (VerifyResult, error) {
	verifyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	creds := domain.AdminCreds{
		URL:       adminURLForHost(p, firstDeployHost(p)),
		AccessKey: p.Credentials.RootUser,
		SecretKey: p.Credentials.RootPassword,
		// BYO certs aren't in the operator's system trust store. Skip
		// verification rather than maintaining a separate cert pool — this
		// matches the personal-tool trust model (see CLAUDE.md).
		Insecure: p.TLS.Enabled(),
	}
	adminClient, err := admin.New(creds)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("build admin client: %w", err)
	}
	s3Client, err := admin.NewS3(creds)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("build s3 client: %w", err)
	}

	info, err := adminClient.ServerInfo(verifyCtx)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("server info: %w", err)
	}
	if info == nil || len(info.Servers) == 0 {
		return VerifyResult{}, fmt.Errorf("server info: no servers reported")
	}
	online := 0
	for _, server := range info.Servers {
		if server.State == domain.NodeOnline {
			online++
		}
	}
	if online != len(info.Servers) {
		return VerifyResult{}, fmt.Errorf("server info: %d/%d nodes online", online, len(info.Servers))
	}

	bucket := fmt.Sprintf("bm-smoke-%d", time.Now().UTC().UnixNano())
	object := "smoke.txt"
	payload := []byte("bm deploy smoke test\n")
	if err := s3Client.SmokeTest(verifyCtx, bucket, object, payload); err != nil {
		return VerifyResult{}, fmt.Errorf("read/write smoke test: %w", err)
	}

	return VerifyResult{
		ConsoleURL:      consoleURLForHost(p, firstDeployHost(p)),
		Version:         info.Version,
		RawBytes:        info.Raw,
		UsedBytes:       info.Used,
		UsableBytes:     info.Usable,
		NodesHealthy:    online,
		NodesTotal:      len(info.Servers),
		PoolsOnline:     maxInt(info.Pools, 1),
		SmokeTestPassed: true,
	}, nil
}

// adminURLForHost is the URL bm itself dials to reach the cluster's admin API.
// It always points at a real node (never p.ServerURL) because the operator's
// ServerURL may be a load balancer that isn't reachable yet or that bm
// shouldn't depend on for its own management calls.
func adminURLForHost(p DeployParams, host domain.HostRow) string {
	scheme := "http"
	if p.TLS.Enabled() {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host.Hostname, p.API.Port)
}

func consoleURLForHost(p DeployParams, host domain.HostRow) string {
	scheme := "http"
	if p.TLS.Enabled() {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host.Hostname, p.API.ConsolePort)
}

func firstDeployHost(p DeployParams) domain.HostRow {
	if len(p.Hosts) > 0 {
		return p.Hosts[0]
	}
	return domain.HostRow{Hostname: "node1"}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
