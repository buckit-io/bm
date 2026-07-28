package main

import (
	"testing"

	"github.com/buckit-io/bm/internal/version"
)

func TestDefaultWebDistUsesEmbeddedAssetsForRelease(t *testing.T) {
	originalVersion := version.Version
	version.Version = "RELEASE.2026-07-28T18-00-00Z"
	t.Cleanup(func() { version.Version = originalVersion })

	if got := defaultWebDist(); got != "" {
		t.Fatalf("defaultWebDist() = %q, want embedded assets (empty disk override)", got)
	}
}
