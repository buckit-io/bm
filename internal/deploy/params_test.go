package deploy

import (
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func validParams() DeployParams {
	return DeployParams{
		Name: "prod-east",
		Credentials: domain.Credentials{
			RootUser:     "admin",
			RootPassword: "supersecret",
		},
		Hosts:    []domain.HostRow{{ID: "h1", Hostname: "node1"}},
		Topology: domain.Topology{SelectedMounts: []string{"/data/disk1"}},
		SSH:      domain.SshCreds{User: "ops"},
		Version:  "custom",
		CustomURL: "https://example.com/buckit.rpm",
	}
}

func TestDeployValidateAcceptsHealthyCredentials(t *testing.T) {
	if err := validParams().Validate(); err != nil {
		t.Fatalf("Validate() returned %v, want nil", err)
	}
}

func TestDeployValidateRejectsShortPassword(t *testing.T) {
	p := validParams()
	p.Credentials.RootPassword = "short"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "8-40") {
		t.Fatalf("Validate() = %v, want length error", err)
	}
}

func TestDeployValidateRejectsBadUsername(t *testing.T) {
	p := validParams()
	p.Credentials.RootUser = "a b"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "letters, digits") {
		t.Fatalf("Validate() = %v, want charset error", err)
	}
}

func TestDeployValidateRejectsPasswordWithSpaces(t *testing.T) {
	p := validParams()
	p.Credentials.RootPassword = "with space here"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "printable ASCII") {
		t.Fatalf("Validate() = %v, want printable-ASCII error", err)
	}
}

func TestDeployArtifactURLUsesArm64RPM(t *testing.T) {
	restore := RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:         "v1.0.0",
		Label:       "v1.0.0",
		RpmURL:      "https://example.com/buckit-amd64.rpm",
		RpmURLAmd64: "https://example.com/buckit-amd64.rpm",
		RpmURLArm64: "https://example.com/buckit-arm64.rpm",
		SHA256URL:   "https://example.com/buckit.sha256",
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
	restore := RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:         "v1.0.0",
		Label:       "v1.0.0",
		RpmURL:      "https://example.com/buckit-amd64.rpm",
		RpmURLAmd64: "https://example.com/buckit-amd64.rpm",
		RpmURLArm64: "https://example.com/buckit-arm64.rpm",
		SHA256URL:   "https://example.com/buckit.sha256",
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
