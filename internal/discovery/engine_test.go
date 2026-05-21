package discovery

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestParseEngine(t *testing.T) {
	cases := []struct {
		in   string
		want domain.ClusterEngine
	}{
		{"RELEASE.2025-10-15T17-29-55Z", domain.EngineMinio},
		{"RELEASE.2024-01-01T00-00-00Z", domain.EngineMinio},
		{"RELEASE.2026-05-01T00-00-00Z", domain.EngineBuckit}, // exactly at cutoff = buckit
		{"RELEASE.2026-05-15T12-34-56Z", domain.EngineBuckit},
		{"RELEASE.2027-12-31T23-59-59Z", domain.EngineBuckit},
		{"v1.0.0", domain.EngineBuckit},
		{"v2.3.4-rc1", domain.EngineBuckit},
		{"", domain.EngineBuckit},
		{"  RELEASE.2025-01-01T00-00-00Z  ", domain.EngineMinio}, // trimmed
		{"RELEASE.garbage", domain.EngineBuckit},                 // malformed -> buckit
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := ParseEngine(tc.in); got != tc.want {
				t.Fatalf("ParseEngine(%q): want %s, got %s", tc.in, tc.want, got)
			}
		})
	}
}
