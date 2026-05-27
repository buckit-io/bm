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

// engineCutoff separates MinIO-era timestamps from Buckit-era ones. Any
// parseable timestamp before this date is treated as MinIO; on/after is Buckit.
// Strings that don't look like a timestamp at all (v1.0.0, empty, junk)
// default to Buckit.
//
// Rationale:
//   - MinIO's last community-edition RELEASE was 2025-10-15T17-29-55Z.
//   - Buckit's first release shipped after 2026-05-01.
// Shift this constant when reality moves.
var engineCutoff = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

const releasePrefix = "RELEASE."

// Layouts the MinIO/Buckit ecosystem stamps into ServerProperties.Version:
//   - releaseTimestampLayout: official MinIO releases (dashes in the time)
//   - rfc3339Layout: source builds whose Version global falls back to the
//     build time formatted with time.RFC3339 (colons in the time)
// Both appear in the wild — see comment on ParseEngine.
const (
	releaseTimestampLayout = "2006-01-02T15-04-05Z"
	rfc3339Layout          = "2006-01-02T15:04:05Z"
)

// ParseEngine classifies a version string as MinIO or Buckit. See the package
// comment for the cutoff rule.
//
// Accepted forms (all canonicalised to a single timestamp before cutoff check):
//   - RELEASE.2025-10-15T17-29-55Z  — official release ldflag form
//   - 2025-10-15T17-29-55Z          — bare dashes (Buckit build / stripped tag)
//   - 2025-10-15T17:29:55Z          — RFC3339 (source build fallback)
// Anything else (v1.0.0, empty, junk) is treated as Buckit — conservative
// default that hides the migrate button rather than offering it on a server
// we can't positively identify as legacy MinIO.
func ParseEngine(version string) domain.ClusterEngine {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, releasePrefix)
	ts, err := time.Parse(releaseTimestampLayout, v)
	if err != nil {
		ts, err = time.Parse(rfc3339Layout, v)
	}
	if err != nil {
		return domain.EngineBuckit
	}
	if ts.Before(engineCutoff) {
		return domain.EngineMinio
	}
	return domain.EngineBuckit
}
