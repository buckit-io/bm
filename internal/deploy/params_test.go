package deploy

import (
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

func TestDeployArtifactURLUsesArm64RPM(t *testing.T) {
	restore := restoreVersionsCache([]domain.BuckitVersion{{
		Tag:         "v1.0.0",
		Label:       "v1.0.0",
		RpmURL:      "https://example.com/buckit-amd64.rpm",
		RpmURLAmd64: "https://example.com/buckit-amd64.rpm",
		RpmURLArm64: "https://example.com/buckit-arm64.rpm",
	}})
	defer restore()

	p := DeployParams{
		Version: "v1.0.0",
		Hosts:   []domain.HostRow{{ID: "h1", Hostname: "node1"}},
		Discovery: map[string]domain.WizardDiscoveryResult{
			"h1": {Arch: "arm64"},
		},
	}

	got, err := p.ArtifactURL()
	if err != nil {
		t.Fatalf("ArtifactURL() error = %v", err)
	}
	if got != "https://example.com/buckit-arm64.rpm" {
		t.Fatalf("want arm64 rpm, got %q", got)
	}
}

func TestDeployArtifactURLUsesAmd64RPM(t *testing.T) {
	restore := restoreVersionsCache([]domain.BuckitVersion{{
		Tag:         "v1.0.0",
		Label:       "v1.0.0",
		RpmURL:      "https://example.com/buckit-amd64.rpm",
		RpmURLAmd64: "https://example.com/buckit-amd64.rpm",
		RpmURLArm64: "https://example.com/buckit-arm64.rpm",
	}})
	defer restore()

	p := DeployParams{
		Version: "v1.0.0",
		Hosts:   []domain.HostRow{{ID: "h1", Hostname: "node1"}},
		Discovery: map[string]domain.WizardDiscoveryResult{
			"h1": {Arch: "amd64"},
		},
	}

	got, err := p.ArtifactURL()
	if err != nil {
		t.Fatalf("ArtifactURL() error = %v", err)
	}
	if got != "https://example.com/buckit-amd64.rpm" {
		t.Fatalf("want amd64 rpm, got %q", got)
	}
}

func restoreVersionsCache(versions []domain.BuckitVersion) func() {
	cacheMu.Lock()
	oldAt := cachedAt
	oldResult := cachedResult
	cachedAt = time.Now()
	cachedResult = versions
	cacheMu.Unlock()
	return func() {
		cacheMu.Lock()
		cachedAt = oldAt
		cachedResult = oldResult
		cacheMu.Unlock()
	}
}
