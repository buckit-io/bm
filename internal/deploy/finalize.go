package deploy

import (
	"time"

	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
)

func deployedCluster(clusterID string, p DeployParams, verify VerifyResult) domain.Cluster {
	now := time.Now().UTC()
	nodeCount := len(p.Hosts)
	driveCount := nodeCount * len(p.Topology.SelectedMounts)
	summary := domain.HealthSummary{
		Nodes: domain.NodeRollup{
			Online:   verify.NodesHealthy,
			Total:    verify.NodesTotal,
			Offline:  maxInt(0, verify.NodesTotal-verify.NodesHealthy),
			Degraded: 0,
		},
		Drives: domain.DriveRollup{
			Ready:   driveCount,
			Total:   driveCount,
			Healing: 0,
			Failed:  0,
		},
	}
	return domain.Cluster{
		ID:             clusterID,
		Name:           p.Name,
		Description:    p.Description,
		Engine:         domain.EngineBuckit,
		Version:        verify.Version,
		Health:         clusters.Rollup(domain.Cluster{Parity: p.Topology.Parity}, summary),
		HealthSummary:  &summary,
		NodeCount:      nodeCount,
		PoolCount:      verify.PoolsOnline,
		DriveCount:     driveCount,
		Parity:         p.Topology.Parity,
		UsableBytes:    verify.UsableBytes,
		RawBytes:       verify.RawBytes,
		UsedBytes:      verify.UsedBytes,
		LastFetchedAt:  &now,
		SSHConfigured:  true,
		LastActivityAt: now,
		CreatedAt:      now,
		ConsoleURL:     verify.ConsoleURL,
		// bm chose the console port at deploy time — persist it directly so
		// node probes don't have to rediscover it.
		ConsolePort: p.API.ConsolePort,
	}
}
