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
		Hosts:     []domain.HostRow{{ID: "h1", Hostname: "node1"}},
		Topology:  domain.Topology{SelectedMounts: []string{"/data/disk1"}},
		SSH:       domain.SshCreds{User: "ops"},
		Version:   "custom",
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

func TestDeployValidateTopology(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*DeployParams)
		wantErr string
	}{
		{
			name: "parity exceeds set maximum",
			mutate: func(p *DeployParams) {
				p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1"}}
				p.Topology = domain.Topology{SetSize: 8, Parity: 5, SelectedMounts: []string{"/data/d1", "/data/d2", "/data/d3", "/data/d4", "/data/d5", "/data/d6", "/data/d7", "/data/d8"}}
			},
			wantErr: "parity must be between 1 and 4",
		},
		{
			name: "set size is not a divisor",
			mutate: func(p *DeployParams) {
				p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1"}}
				p.Topology = domain.Topology{SetSize: 6, Parity: 2, SelectedMounts: []string{"/data/d1", "/data/d2", "/data/d3", "/data/d4", "/data/d5", "/data/d6", "/data/d7", "/data/d8"}}
			},
			wantErr: "set size 6 must be a supported divisor",
		},
		{
			name: "standalone cannot set parity",
			mutate: func(p *DeployParams) {
				p.Topology = domain.Topology{SetSize: 1, Parity: 1, SelectedMounts: []string{"/data/disk1"}}
			},
			wantErr: "parity requires at least two total drives",
		},
		{
			name: "custom valid set size and parity",
			mutate: func(p *DeployParams) {
				p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1"}, {ID: "h2", Hostname: "node2"}}
				p.Topology = domain.Topology{SetSize: 4, Parity: 2, SelectedMounts: []string{"/data/d1", "/data/d2", "/data/d3", "/data/d4"}}
			},
		},
		{
			name: "omitted parity uses the Buckit default",
			mutate: func(p *DeployParams) {
				p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1"}}
				p.Topology = domain.Topology{SetSize: 4, SelectedMounts: []string{"/data/d1", "/data/d2", "/data/d3", "/data/d4"}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)
			err := p.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() returned %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestFromDraftDefaultsOmittedParity(t *testing.T) {
	p := FromDraft(domain.NewClusterDraft{
		Hosts: []domain.HostRow{{ID: "h1", Hostname: "node1"}},
		Topology: domain.Topology{
			SetSize:        4,
			SelectedMounts: []string{"/data/d1", "/data/d2", "/data/d3", "/data/d4"},
		},
	})
	if p.Topology.Parity != 2 {
		t.Fatalf("FromDraft() parity = %d, want Buckit default 2", p.Topology.Parity)
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
