package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

const (
	githubReleasesURL = "https://api.github.com/repos/buckit-io/buckit/releases"
	cacheTTL          = 5 * time.Minute
	fetchTimeout      = 10 * time.Second
)

var (
	cacheMu      sync.Mutex
	cachedAt     time.Time
	cachedResult []domain.BuckitVersion
)

// SupportedVersions returns the version catalog fetched from the GitHub
// releases API with a 5-minute cache. The first call blocks on the fetch;
// subsequent calls within cacheTTL return the cached result. On GitHub
// failure, returns nil (the handler surfaces a 502).
func SupportedVersions() []domain.BuckitVersion {
	cacheMu.Lock()
	if len(cachedResult) > 0 && time.Since(cachedAt) < cacheTTL {
		out := make([]domain.BuckitVersion, len(cachedResult))
		copy(out, cachedResult)
		cacheMu.Unlock()
		return out
	}
	cacheMu.Unlock()

	versions, _ := fetchGitHubReleases()
	if len(versions) == 0 {
		return nil
	}

	cacheMu.Lock()
	cachedResult = versions
	cachedAt = time.Now()
	cacheMu.Unlock()

	out := make([]domain.BuckitVersion, len(versions))
	copy(out, versions)
	return out
}

// VersionByTag returns the catalog entry for tag, or nil when the tag is
// not in the list.
func VersionByTag(tag string) *domain.BuckitVersion {
	for _, v := range SupportedVersions() {
		if v.Tag == tag {
			return &v
		}
	}
	return nil
}

func RPMURLForArch(v *domain.BuckitVersion, arch string) string {
	if v == nil {
		return ""
	}
	switch normalizeReleaseArch(arch) {
	case "arm64":
		if v.RpmURLArm64 != "" {
			return v.RpmURLArm64
		}
	case "amd64":
		if v.RpmURLAmd64 != "" {
			return v.RpmURLAmd64
		}
	}
	return v.RpmURL
}

// githubRelease is the subset of the GitHub releases API response we parse.
type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Name       string        `json:"name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchGitHubReleases() ([]domain.BuckitVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", githubReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bm/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github releases: %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	var out []domain.BuckitVersion
	for i, r := range releases {
		if r.Draft {
			continue
		}
		tag := r.TagName
		label := tag
		if i == 0 {
			label = tag + " (latest)"
		}
		if r.Prerelease {
			label = tag + " (pre-release)"
		}

		v := domain.BuckitVersion{Tag: tag, Label: label}
		for _, a := range r.Assets {
			name := strings.ToLower(a.Name)
			switch {
			case strings.HasSuffix(name, ".x86_64.rpm") || strings.HasSuffix(name, ".amd64.rpm"):
				v.RpmURLAmd64 = a.BrowserDownloadURL
				v.RpmURL = a.BrowserDownloadURL
			case strings.HasSuffix(name, ".aarch64.rpm") || strings.HasSuffix(name, ".arm64.rpm"):
				v.RpmURLArm64 = a.BrowserDownloadURL
				if v.RpmURL == "" {
					v.RpmURL = a.BrowserDownloadURL
				}
			case strings.HasSuffix(name, ".deb"):
				v.DebURL = a.BrowserDownloadURL
			case strings.HasSuffix(name, ".rpm.sha256") || strings.HasSuffix(name, ".sha256"):
				v.SHA256URL = a.BrowserDownloadURL
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func normalizeReleaseArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}
