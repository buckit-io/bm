package operations

import (
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
)

func TestResolveRedeployVersion(t *testing.T) {
	// Pin the catalog: newer entry first (matches GitHub release ordering).
	restore := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{
		{Tag: "RELEASE.2026-08-01T10-00-00Z"},
		{Tag: "RELEASE.2025-09-07T16-13-09Z"},
		{Tag: "v1.0.0"},
	})
	defer restore()

	cases := []struct {
		name           string
		clusterVersion string
		want           string
		wantErrSubstr  string
	}{
		{
			name:           "bare dashes contained in tag",
			clusterVersion: "2025-09-07T16-13-09Z",
			want:           "RELEASE.2025-09-07T16-13-09Z",
		},
		{
			name:           "exact RELEASE. tag",
			clusterVersion: "RELEASE.2025-09-07T16-13-09Z",
			want:           "RELEASE.2025-09-07T16-13-09Z",
		},
		{
			name:           "semantic version exact",
			clusterVersion: "v1.0.0",
			want:           "v1.0.0",
		},
		{
			name:           "newest match wins when multiple contain needle",
			clusterVersion: "2026",
			want:           "RELEASE.2026-08-01T10-00-00Z",
		},
		{
			name:           "empty",
			clusterVersion: "",
			wantErrSubstr:  "not reported a version",
		},
		{
			name:           "no catalog match",
			clusterVersion: "2099-01-01T00-00-00Z",
			wantErrSubstr:  "no release in the catalog",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRedeployVersion(tc.clusterVersion)
			if tc.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("want err containing %q, got %v", tc.wantErrSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
