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
	TLS         domain.TLSConfig                        `json:"tls,omitempty"`
	Discovery   map[string]domain.WizardDiscoveryResult `json:"discovery,omitempty"`
	Topology    domain.Topology                         `json:"topology"`
	// PersistSsh asks the deploy executor's commit step to save the SSH
	// config to the cluster_ssh bucket so post-deploy ops can reuse it.
	// Defaults to true (preserves the pre-flag behavior — every deploy
	// before this flag landed always saved).
	PersistSsh bool `json:"persistSsh"`
}

var supportedErasureSetSizes = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

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
	// Normalize line endings in pasted PEM bodies — Windows-edited / Notepad
	// paste produces CRLF, which makes bash heredoc terminator matching fail
	// (`BMPEM\r` never matches `BMPEM`) and hangs the install step.
	tls := d.TLS
	tls.CertPEM = normalizeLineEndings(tls.CertPEM)
	tls.KeyPEM = normalizeLineEndings(tls.KeyPEM)
	tls.CABundlePEM = normalizeLineEndings(tls.CABundlePEM)
	params := DeployParams{
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
		TLS:         tls,
		Discovery:   d.Discovery,
		Topology:    d.Topology,
		PersistSsh:  d.PersistSsh,
	}
	// Older/direct API clients may omit parity. Match Buckit's default rather
	// than rejecting an otherwise valid erasure deployment.
	if params.totalDriveCount() > 1 && params.Topology.Parity == 0 {
		params.Topology.Parity = params.effectiveParity()
	}
	return params
}

func normalizeLineEndings(s string) string {
	if s == "" {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
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
	if err := p.validateTopology(); err != nil {
		return err
	}
	if p.SSH.User == "" {
		return errors.New("deploy: ssh user required")
	}
	if _, err := p.ArtifactURL(); err != nil {
		return err
	}
	if err := p.validateTLS(); err != nil {
		return err
	}
	return nil
}

// totalDriveCount is the number of endpoints in the first (and only) pool
// created by the remote deployment wizard: every selected mount on every host.
func (p DeployParams) totalDriveCount() int {
	return len(p.Hosts) * len(p.Topology.SelectedMounts)
}

// defaultSetSize mirrors Buckit's automatic largest-divisor choice.
func (p DeployParams) defaultSetSize() int {
	total := p.totalDriveCount()
	best := 0
	for _, size := range supportedErasureSetSizes {
		if total%size == 0 && size > best {
			best = size
		}
	}
	return best
}

// resolvedSetSize returns the requested set size, or Buckit's automatic
// largest-divisor choice for legacy callers that omit topology.setSize.
func (p DeployParams) resolvedSetSize() int {
	if p.Topology.SetSize > 0 {
		return p.Topology.SetSize
	}
	return p.defaultSetSize()
}

// defaultParityBlocks mirrors Buckit's DefaultParityBlocks() in
// internal/config/storageclass/storage-class.go.
func defaultParityBlocks(setSize int) int {
	switch {
	case setSize <= 1:
		return 0
	case setSize <= 3:
		return 1
	case setSize <= 5:
		return 2
	case setSize <= 7:
		return 3
	default:
		return 4
	}
}

func (p DeployParams) effectiveParity() int {
	if p.Topology.Parity != 0 || p.totalDriveCount() <= 1 {
		return p.Topology.Parity
	}
	return defaultParityBlocks(p.resolvedSetSize())
}

func (p DeployParams) validateTopology() error {
	total := p.totalDriveCount()
	if total <= 1 {
		if p.Topology.Parity != 0 {
			return errors.New("deploy: parity requires at least two total drives")
		}
		return nil
	}

	setSize := p.resolvedSetSize()
	if setSize < 2 || setSize > 16 || total%setSize != 0 {
		return fmt.Errorf("deploy: set size %d must be a supported divisor of %d total drives", setSize, total)
	}
	maxParity := setSize / 2
	parity := p.effectiveParity()
	if parity < 1 || parity > maxParity {
		return fmt.Errorf("deploy: parity must be between 1 and %d for erasure set size %d", maxParity, setSize)
	}
	return nil
}

// ArtifactURL resolves the deploy's artifact URL from version/customUrl.
// Kept for Validate() which checks only that a URL can be produced;
// runtime callers use ArtifactForKind to honour the host's detected
// package format.
func (p DeployParams) ArtifactURL() (string, error) {
	if p.Version == "custom" {
		u := strings.TrimSpace(p.CustomURL)
		if u == "" {
			return "", errors.New("deploy: customUrl required when version=custom")
		}
		return u, nil
	}
	// Validate-time fallback: prefer rpm if available, else deb.
	a, err := p.ArtifactForKind("rpm")
	if err == nil {
		return a.URL, nil
	}
	a, err = p.ArtifactForKind("deb")
	if err != nil {
		return "", err
	}
	return a.URL, nil
}

// ArtifactForKind resolves the Artifact for the host's detected package
// format. kind is "rpm" or "deb". Custom URLs auto-detect kind from
// suffix and are rejected if the suffix doesn't match the host.
func (p DeployParams) ArtifactForKind(kind string) (Artifact, error) {
	if p.Version == "custom" {
		u := strings.TrimSpace(p.CustomURL)
		if u == "" {
			return Artifact{}, errors.New("deploy: customUrl required when version=custom")
		}
		art, err := CustomArtifactFromURL(u)
		if err != nil {
			return Artifact{}, errors.New("deploy: " + err.Error())
		}
		if kind != "" && art.Kind != kind {
			return Artifact{}, fmt.Errorf("deploy: custom URL is a %s artifact but host uses %s", art.Kind, kind)
		}
		return art, nil
	}
	arch, err := p.clusterArch()
	if err != nil {
		return Artifact{}, err
	}
	art, err := ResolveArtifact(p.Version, kind, arch)
	if err != nil {
		return Artifact{}, errors.New("deploy: " + err.Error())
	}
	return art, nil
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
