package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"strings"
)

// Artifact describes a downloadable package (RPM or DEB) plus the
// sources bm can consult to verify it. The shape is package-manager
// neutral — RpmURL vs DebURL is selected at the BuckitVersion layer.
type Artifact struct {
	// Kind is "rpm" or "deb" once threaded from detection; empty on
	// pre-detection paths (CustomRPMArtifact / CustomDebArtifact set it
	// explicitly).
	Kind       string
	URL        string
	SHA256URLs []string
	SHA256     string
}

// CustomRPMArtifact builds an Artifact for a user-supplied .rpm URL.
// Kept as the RPM-specific constructor so the wizard / cutover paths
// that still hard-code RPM keep working unchanged.
func CustomRPMArtifact(url string) Artifact {
	u := strings.TrimSpace(url)
	return Artifact{Kind: "rpm", URL: u, SHA256URLs: DefaultSHA256URLs(u)}
}

// CustomDebArtifact mirrors CustomRPMArtifact for .deb URLs.
func CustomDebArtifact(url string) Artifact {
	u := strings.TrimSpace(url)
	return Artifact{Kind: "deb", URL: u, SHA256URLs: DefaultSHA256URLs(u)}
}

// CustomArtifactFromURL picks rpm vs deb by URL suffix. Anything else
// returns an error so the user gets a clear failure instead of a
// confusing mid-install one.
func CustomArtifactFromURL(url string) (Artifact, error) {
	u := strings.TrimSpace(url)
	lower := strings.ToLower(u)
	switch {
	case strings.HasSuffix(lower, ".rpm"):
		return CustomRPMArtifact(u), nil
	case strings.HasSuffix(lower, ".deb"):
		return CustomDebArtifact(u), nil
	default:
		return Artifact{}, fmt.Errorf("custom artifact URL must end in .rpm or .deb: %s", u)
	}
}

func DefaultSHA256URLs(url string) []string {
	u := strings.TrimSpace(url)
	if u == "" {
		return nil
	}
	return []string{u + ".sha256sum", u + ".sha256"}
}

// FetchChecksum resolves a sha256 for the artifact by trying its
// SHA256URLs in order and falling back to the literal SHA256 field.
func FetchChecksum(ctx context.Context, artifact Artifact) (string, error) {
	if strings.TrimSpace(artifact.URL) == "" {
		return "", errors.New("artifact URL required")
	}
	for _, shaURL := range artifact.SHA256URLs {
		shaURL = strings.TrimSpace(shaURL)
		if shaURL == "" {
			continue
		}
		sum, err := fetchRPMChecksumFromURL(ctx, artifact.URL, shaURL)
		if err == nil {
			return sum, nil
		}
	}
	if sum := strings.TrimSpace(artifact.SHA256); isSHA256Hex(strings.ToLower(sum)) {
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("no sha256 available for %s", artifact.URL)
}

func fetchRPMChecksumFromURL(ctx context.Context, rpmURL string, shaURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain, application/octet-stream")
	req.Header.Set("User-Agent", "bm/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sha256 download: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	sum, err := parseSHA256File(string(body), rpmURL)
	if err != nil {
		return "", fmt.Errorf("parse sha256: %w", err)
	}
	return sum, nil
}

// DownloadRPMCommand is preserved as a thin wrapper so existing callers
// (migration cutover, orchestrated ops still on the Phase 1 seam)
// compile unchanged. The bytes it produces are identical to
// rpmManager{}.DownloadCommand(url).
func DownloadRPMCommand(url string) string {
	return rpmManager{}.DownloadCommand(url)
}

// VerifyRPMChecksumCommand is the same thin-wrapper story as
// DownloadRPMCommand.
func VerifyRPMChecksumCommand(expectedSHA256 string) string {
	return rpmManager{}.VerifyChecksumCommand(expectedSHA256)
}

// FetchRPMChecksum is a back-compat shim. New code should call
// FetchChecksum.
func FetchRPMChecksum(ctx context.Context, artifact Artifact) (string, error) {
	return FetchChecksum(ctx, artifact)
}

func parseSHA256File(body string, rpmURL string) (string, error) {
	targetName := artifactBaseName(rpmURL)
	type candidate struct {
		sum  string
		name string
	}
	var candidates []candidate
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(fields[0]))
		if !isSHA256Hex(sum) {
			continue
		}
		name := ""
		if len(fields) >= 2 {
			name = path.Base(strings.TrimLeft(fields[1], "*"))
		}
		candidates = append(candidates, candidate{sum: sum, name: name})
	}
	if len(candidates) == 0 {
		return "", errors.New("no sha256 found")
	}
	if targetName != "" {
		for _, c := range candidates {
			if c.name == targetName {
				return c.sum, nil
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0].sum, nil
	}
	return "", fmt.Errorf("no checksum entry for %s", targetName)
}

func artifactBaseName(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return path.Base(rawURL)
	}
	return path.Base(u.Path)
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
