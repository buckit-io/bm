// Package version exposes build-time metadata for the bm binary.
//
// Values are populated via -ldflags at release build time. The defaults
// below are used for `go build` and `go run` during local development.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version line.
func String() string {
	return "bm " + Version + " (commit " + Commit + ", built " + Date + ")"
}
