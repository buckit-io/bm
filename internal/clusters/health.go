package clusters

import "github.com/buckit-io/bm/internal/domain"

// Summarize folds nodes into a HealthSummary. Boot drives are excluded from
// the drive counters. Mirrors web/src/mock/data.ts:computeHealthSummary.
func Summarize(nodes []domain.Node) domain.HealthSummary {
	var nOnline, nDegraded, nOffline int
	var dReady, dHealing, dFailed int
	for _, n := range nodes {
		switch n.State {
		case domain.NodeOnline:
			nOnline++
		case domain.NodeDegraded:
			nDegraded++
		default:
			// offline + unknown both count as not-serving
			nOffline++
		}
		for _, d := range n.Drives {
			if d.IsBoot {
				continue
			}
			switch d.State {
			case domain.DriveReady:
				dReady++
			case domain.DriveHealing:
				dHealing++
			default:
				dFailed++
			}
		}
	}
	return domain.HealthSummary{
		Nodes: domain.NodeRollup{
			Online:   nOnline,
			Degraded: nDegraded,
			Offline:  nOffline,
			Total:    len(nodes),
		},
		Drives: domain.DriveRollup{
			Ready:   dReady,
			Healing: dHealing,
			Failed:  dFailed,
			Total:   dReady + dHealing + dFailed,
		},
	}
}

// Rollup folds a HealthSummary into the cluster's headline status using the
// parity for the critical thresholds. Mirrors
// web/src/mock/data.ts:computeHealth.
func Rollup(c domain.Cluster, s domain.HealthSummary) domain.HealthState {
	if s.Nodes.Total == 0 {
		return domain.HealthUnknown
	}
	if s.Drives.Failed > c.Parity {
		return domain.HealthCritical
	}
	if s.Nodes.Offline > c.Parity {
		return domain.HealthCritical
	}
	if s.Nodes.Offline > 0 || s.Nodes.Degraded > 0 {
		return domain.HealthDegraded
	}
	if s.Drives.Healing > 0 || s.Drives.Failed > 0 {
		return domain.HealthDegraded
	}
	return domain.HealthHealthy
}
