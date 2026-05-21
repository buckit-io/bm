// Package discovery handles the import flow — turning an operator-supplied URL
// + admin creds into a previewable ImportCandidate via the admin API.
//
// The engine parser is in its own file so future revisions (e.g. shifting the
// cutoff after a calendar slip) are a one-line constant change.
package discovery

import (
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

// engineCutoff separates MinIO-era RELEASE timestamps from Buckit-era ones.
// Any RELEASE.<ts> before this date is treated as MinIO; on/after is Buckit.
// Non-RELEASE formats (v1.0.0, plain version strings, empty) default to Buckit.
//
// Rationale:
//   - MinIO's last community-edition RELEASE was 2025-10-15T17-29-55Z.
//   - Buckit's first release shipped after 2026-05-01.
// Shift this constant when reality moves.
var engineCutoff = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

const releasePrefix = "RELEASE."

// releaseTimestampLayout matches the layout MinIO/Buckit servers report:
//   RELEASE.2025-10-15T17-29-55Z  (note dashes inside the time portion)
const releaseTimestampLayout = "2006-01-02T15-04-05Z"

// ParseEngine classifies a version string as MinIO or Buckit. See the package
// comment for the cutoff rule.
func ParseEngine(version string) domain.ClusterEngine {
	v := strings.TrimSpace(version)
	if !strings.HasPrefix(v, releasePrefix) {
		return domain.EngineBuckit
	}
	tsStr := strings.TrimPrefix(v, releasePrefix)
	ts, err := time.Parse(releaseTimestampLayout, tsStr)
	if err != nil {
		// Treat malformed RELEASE strings as Buckit — the version came from a
		// known-to-be-Buckit-or-MinIO admin endpoint already, and we'd rather
		// fail open than misclassify an upgraded server.
		return domain.EngineBuckit
	}
	if ts.Before(engineCutoff) {
		return domain.EngineMinio
	}
	return domain.EngineBuckit
}
