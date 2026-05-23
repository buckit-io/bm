package deploy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/buckit-io/bm/internal/credentials"
	"github.com/buckit-io/bm/internal/domain"
)

// DeployParams is the executor-friendly subset of NewClusterDraft. The
// cluster_deploy dispatch path receives a full NewClusterDraft from the
// wizard; this struct picks out the fields the install pipeline actually
// reads, leaving the wizard's UI-only fields (probe state, customUrlCheck) out.
type DeployParams struct {
	Name        string                                  `json:"name"`
	Description string                                  `json:"description,omitempty"`
	Version     string                                  `json:"version"`
	CustomURL   string                                  `json:"customUrl,omitempty"`
	Credentials domain.Credentials                      `json:"credentials"`
	API         domain.APIPorts                         `json:"api"`
	Region      string                                  `json:"region"`
	ServerURL   string                                  `json:"serverUrl,omitempty"`
	Hosts       []domain.HostRow                        `json:"hosts"`
	SSH         domain.SshCreds                         `json:"ssh"`
	Discovery   map[string]domain.WizardDiscoveryResult `json:"discovery,omitempty"`
	Topology    domain.Topology                         `json:"topology"`
}

// FromDraft turns a NewClusterDraft into the executor's DeployParams. Drops
// the UI-only fields and validates basic invariants the wizard normally
// guards client-side.
func FromDraft(d domain.NewClusterDraft) DeployParams {
	hosts := make([]domain.HostRow, 0, len(d.Hosts))
	for _, h := range d.Hosts {
		if strings.TrimSpace(h.Hostname) == "" {
			continue
		}
		if h.Port == 0 {
			h.Port = 22
		}
		hosts = append(hosts, h)
	}
	if d.API.Port == 0 {
		d.API.Port = 9000
	}
	if d.API.ConsolePort == 0 {
		d.API.ConsolePort = 9001
	}
	if d.Region == "" {
		d.Region = "us-east-1"
	}
	return DeployParams{
		Name:        strings.TrimSpace(d.Name),
		Description: d.Description,
		Version:     d.Version,
		CustomURL:   d.CustomURL,
		Credentials: d.Credentials,
		API:         d.API,
		Region:      d.Region,
		ServerURL:   d.ServerURL,
		Hosts:       hosts,
		SSH:         d.SSH,
		Discovery:   d.Discovery,
		Topology:    d.Topology,
	}
}

// Validate enforces the invariants the install pipeline depends on.
// Returns a descriptive error so the dispatch path can reject with 400.
func (p DeployParams) Validate() error {
	if p.Name == "" {
		return errors.New("deploy: cluster name required")
	}
	if p.Credentials.RootUser == "" || p.Credentials.RootPassword == "" {
		return errors.New("deploy: root user + password required")
	}
	if err := credentials.ValidateRootUser(p.Credentials.RootUser); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	if err := credentials.ValidateRootPassword(p.Credentials.RootPassword); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	if len(p.Hosts) == 0 {
		return errors.New("deploy: at least one host required")
	}
	if len(p.Topology.SelectedMounts) == 0 {
		return errors.New("deploy: at least one drive mount required (topology.selectedMounts)")
	}
	if p.SSH.User == "" {
		return errors.New("deploy: ssh user required")
	}
	if _, err := p.ArtifactURL(); err != nil {
		return err
	}
	return nil
}

// ArtifactURL resolves the deploy's artifact URL from version/customUrl.
func (p DeployParams) ArtifactURL() (string, error) {
	if p.Version == "custom" {
		u := strings.TrimSpace(p.CustomURL)
		if u == "" {
			return "", errors.New("deploy: customUrl required when version=custom")
		}
		return u, nil
	}
	arch, err := p.clusterArch()
	if err != nil {
		return "", err
	}
	url, err := ResolveRPMURL(p.Version, arch)
	if err != nil {
		return "", errors.New("deploy: " + err.Error())
	}
	return url, nil
}

func (p DeployParams) clusterArch() (string, error) {
	seen := map[string]bool{}
	for _, h := range p.Hosts {
		r, ok := p.Discovery[h.ID]
		if !ok || r.Arch == "" {
			continue
		}
		seen[normalizeReleaseArch(r.Arch)] = true
	}
	if len(seen) == 0 {
		return "", nil
	}
	if len(seen) > 1 {
		return "", errors.New("deploy: mixed architectures in discovery")
	}
	for arch := range seen {
		return arch, nil
	}
	return "", nil
}
