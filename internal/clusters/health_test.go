package clusters

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestSummarize(t *testing.T) {
	nodes := []domain.Node{
		{
			State: domain.NodeOnline,
			Drives: []domain.Drive{
				{IsBoot: true, State: domain.DriveReady},
				{State: domain.DriveReady},
				{State: domain.DriveHealing},
			},
		},
		{
			State: domain.NodeDegraded,
			Drives: []domain.Drive{
				{State: domain.DriveFailed},
			},
		},
		{
			State: domain.NodeOffline,
		},
	}
	s := Summarize(nodes)
	if s.Nodes.Online != 1 || s.Nodes.Degraded != 1 || s.Nodes.Offline != 1 || s.Nodes.Total != 3 {
		t.Fatalf("node rollup: %+v", s.Nodes)
	}
	if s.Drives.Ready != 1 || s.Drives.Healing != 1 || s.Drives.Failed != 1 || s.Drives.Total != 3 {
		t.Fatalf("drive rollup: %+v", s.Drives)
	}
}

func TestRollup(t *testing.T) {
	cases := []struct {
		name string
		c    domain.Cluster
		s    domain.HealthSummary
		want domain.HealthState
	}{
		{"empty", domain.Cluster{}, domain.HealthSummary{}, domain.HealthUnknown},
		{"healthy", domain.Cluster{Parity: 4}, domain.HealthSummary{Nodes: domain.NodeRollup{Online: 4, Total: 4}, Drives: domain.DriveRollup{Ready: 10, Total: 10}}, domain.HealthHealthy},
		{"degraded by offline", domain.Cluster{Parity: 4}, domain.HealthSummary{Nodes: domain.NodeRollup{Online: 3, Offline: 1, Total: 4}, Drives: domain.DriveRollup{Ready: 10, Total: 10}}, domain.HealthDegraded},
		{"degraded by healing", domain.Cluster{Parity: 4}, domain.HealthSummary{Nodes: domain.NodeRollup{Online: 4, Total: 4}, Drives: domain.DriveRollup{Ready: 9, Healing: 1, Total: 10}}, domain.HealthDegraded},
		{"critical offline > parity", domain.Cluster{Parity: 4}, domain.HealthSummary{Nodes: domain.NodeRollup{Online: 3, Offline: 5, Total: 8}, Drives: domain.DriveRollup{Total: 10}}, domain.HealthCritical},
		{"critical drives > parity", domain.Cluster{Parity: 4}, domain.HealthSummary{Nodes: domain.NodeRollup{Online: 4, Total: 4}, Drives: domain.DriveRollup{Failed: 5, Total: 10}}, domain.HealthCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rollup(tc.c, tc.s); got != tc.want {
				t.Fatalf("want %s, got %s", tc.want, got)
			}
		})
	}
}
