package deploy

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestDeployedClusterIncludesVerifySummary(t *testing.T) {
	p := DeployParams{
		Name: "prod-east",
		Topology: domain.Topology{
			Parity:         4,
			SelectedMounts: []string{"/data/disk0", "/data/disk1"},
		},
		Hosts: []domain.HostRow{{Hostname: "n1"}, {Hostname: "n2"}},
	}
	got := deployedCluster("prod-east", p, VerifyResult{
		ConsoleURL:   "http://n1:9001",
		Version:      "RELEASE.2026-05-20T21-14-57Z",
		RawBytes:     400,
		UsedBytes:    200,
		UsableBytes:  300,
		NodesHealthy: 2,
		NodesTotal:   2,
		PoolsOnline:  1,
	})
	if got.HealthSummary == nil {
		t.Fatal("missing health summary")
	}
	if got.HealthSummary.Nodes.Online != 2 || got.HealthSummary.Nodes.Total != 2 {
		t.Fatalf("unexpected node summary: %+v", got.HealthSummary.Nodes)
	}
	if got.HealthSummary.Drives.Total != 4 || got.DriveCount != 4 {
		t.Fatalf("unexpected drive summary: drives=%+v count=%d", got.HealthSummary.Drives, got.DriveCount)
	}
	if got.UsedBytes != 200 || got.RawBytes != 400 || got.UsableBytes != 300 {
		t.Fatalf("unexpected bytes: %+v", got)
	}
	if got.ConsoleURL != "http://n1:9001" || got.Version == "" {
		t.Fatalf("unexpected cluster fields: %+v", got)
	}
}
