package deploy

import (
	"context"
	"fmt"
	"strings"
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
		URL:       serverURLForHost(p, firstDeployHost(p)),
		AccessKey: p.Credentials.RootUser,
		SecretKey: p.Credentials.RootPassword,
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

func serverURLForHost(p DeployParams, host domain.HostRow) string {
	if url := strings.TrimSpace(p.ServerURL); url != "" {
		return url
	}
	return fmt.Sprintf("http://%s:%d", host.Hostname, p.API.Port)
}

func consoleURLForHost(p DeployParams, host domain.HostRow) string {
	return fmt.Sprintf("http://%s:%d", host.Hostname, p.API.ConsolePort)
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
