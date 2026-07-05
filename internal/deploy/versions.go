package deploy

import (
	"context"
	"encoding/json"
	"errors"
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
	if a := releaseArtifact(v, "rpm", arch); a != nil {
		return a.URL
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

// RPMArtifactForArch returns the resolved Artifact for the RPM URL of v
// at arch, including any sha256 sidecar URLs the catalog knows about.
// Kept as the RPM-specific entrypoint; DebArtifactForArch is the
// parallel for Debian.
func RPMArtifactForArch(v *domain.BuckitVersion, arch string) Artifact {
	if v == nil {
		return Artifact{}
	}
	normArch := normalizeReleaseArch(arch)
	if a := releaseArtifact(v, "rpm", arch); a != nil {
		shaURLs := uniqueStrings(append([]string{strings.TrimSpace(a.SHA256URL)}, DefaultSHA256URLs(a.URL)...))
		return Artifact{
			Kind:       "rpm",
			URL:        a.URL,
			SHA256URLs: shaURLs,
			SHA256:     strings.TrimSpace(a.SHA256),
		}
	}
	url := RPMURLForArch(v, arch)
	shaURLs := make([]string, 0, 3)
	switch normArch {
	case "amd64":
		if s := strings.TrimSpace(v.SHA256URLAmd64); s != "" {
			shaURLs = append(shaURLs, s)
		}
	case "arm64":
		if s := strings.TrimSpace(v.SHA256URLArm64); s != "" {
			shaURLs = append(shaURLs, s)
		}
	}
	if s := strings.TrimSpace(v.SHA256URL); s != "" {
		shaURLs = append(shaURLs, s)
	}
	shaURLs = append(shaURLs, DefaultSHA256URLs(url)...)
	shaURLs = uniqueStrings(shaURLs)
	sum := strings.TrimSpace(v.SHA256)
	switch normArch {
	case "amd64":
		if s := strings.TrimSpace(v.SHA256Amd64); s != "" {
			sum = s
		}
	case "arm64":
		if s := strings.TrimSpace(v.SHA256Arm64); s != "" {
			sum = s
		}
	}
	return Artifact{
		Kind:       "rpm",
		URL:        url,
		SHA256URLs: shaURLs,
		SHA256:     sum,
	}
}

// DebURLForArch returns the catalog's .deb URL for arch. The catalog
// today stores a single DebURL plus per-Artifacts entries that the
// release classifier fills in for amd64 / arm64. The arch-specific
// entry wins; the catalog-wide DebURL is the fallback.
func DebURLForArch(v *domain.BuckitVersion, arch string) string {
	if v == nil {
		return ""
	}
	if a := releaseArtifact(v, "deb", arch); a != nil {
		return a.URL
	}
	return v.DebURL
}

// DebArtifactForArch mirrors RPMArtifactForArch for .deb URLs. Sha256
// sidecar URLs follow the same DefaultSHA256URLs convention.
func DebArtifactForArch(v *domain.BuckitVersion, arch string) Artifact {
	if v == nil {
		return Artifact{}
	}
	if a := releaseArtifact(v, "deb", arch); a != nil {
		shaURLs := uniqueStrings(append([]string{strings.TrimSpace(a.SHA256URL)}, DefaultSHA256URLs(a.URL)...))
		return Artifact{
			Kind:       "deb",
			URL:        a.URL,
			SHA256URLs: shaURLs,
			SHA256:     strings.TrimSpace(a.SHA256),
		}
	}
	url := DebURLForArch(v, arch)
	return Artifact{
		Kind:       "deb",
		URL:        url,
		SHA256URLs: DefaultSHA256URLs(url),
	}
}

// ResolveRPMURL returns the Buckit RPM URL for the requested version and
// optional architecture. Empty arch falls back to the catalog's generic RPM
// URL, which preserves the current migration behavior.
func ResolveRPMURL(version, arch string) (string, error) {
	artifact, err := ResolveRPMArtifact(version, arch)
	if err != nil {
		return "", err
	}
	return artifact.URL, nil
}

func ResolveRPMArtifact(version, arch string) (Artifact, error) {
	v := VersionByTag(version)
	if v == nil {
		return Artifact{}, errors.New("unsupported version " + version)
	}
	artifact := RPMArtifactForArch(v, arch)
	if artifact.URL == "" {
		if normalizeReleaseArch(arch) == "" {
			return Artifact{}, errors.New("no rpm URL for " + version)
		}
		return Artifact{}, errors.New("no rpm URL for " + version + " on " + normalizeReleaseArch(arch))
	}
	return artifact, nil
}

// ResolveDebURL returns the Buckit DEB URL for the requested version
// and optional architecture.
func ResolveDebURL(version, arch string) (string, error) {
	artifact, err := ResolveDebArtifact(version, arch)
	if err != nil {
		return "", err
	}
	return artifact.URL, nil
}

func ResolveDebArtifact(version, arch string) (Artifact, error) {
	v := VersionByTag(version)
	if v == nil {
		return Artifact{}, errors.New("unsupported version " + version)
	}
	artifact := DebArtifactForArch(v, arch)
	if artifact.URL == "" {
		if normalizeReleaseArch(arch) == "" {
			return Artifact{}, errors.New("no deb URL for " + version)
		}
		return Artifact{}, errors.New("no deb URL for " + version + " on " + normalizeReleaseArch(arch))
	}
	return artifact, nil
}

// ResolveArtifact picks rpm or deb based on kind ("rpm" | "deb").
func ResolveArtifact(version, kind, arch string) (Artifact, error) {
	switch kind {
	case "rpm", "":
		return ResolveRPMArtifact(version, arch)
	case "deb":
		return ResolveDebArtifact(version, arch)
	default:
		return Artifact{}, errors.New("unsupported artifact kind " + kind)
	}
}

func ResolveBinaryURL(version, osName, arch string) (string, error) {
	artifact, err := ResolveBinaryArtifact(version, osName, arch)
	if err != nil {
		return "", err
	}
	return artifact.URL, nil
}

func BinaryURLForOSArch(v *domain.BuckitVersion, osName, arch string) string {
	artifact := BinaryArtifactForOSArch(v, osName, arch)
	return artifact.URL
}

func ResolveBinaryArtifact(version, osName, arch string) (Artifact, error) {
	v := VersionByTag(version)
	if v == nil {
		return Artifact{}, errors.New("unsupported version " + version)
	}
	artifact := BinaryArtifactForOSArch(v, osName, arch)
	if artifact.URL == "" {
		target := strings.TrimSpace(osName)
		if target == "" {
			target = "requested platform"
		}
		if normArch := normalizeReleaseArch(arch); normArch != "" {
			target += " " + normArch
		}
		return Artifact{}, errors.New("no binary URL for " + version + " on " + target)
	}
	return artifact, nil
}

func BinaryArtifactForOSArch(v *domain.BuckitVersion, osName, arch string) Artifact {
	if v == nil {
		return Artifact{}
	}
	normOS := strings.ToLower(strings.TrimSpace(osName))
	normArch := normalizeReleaseArch(arch)
	for i := range v.Artifacts {
		a := &v.Artifacts[i]
		if a.Kind != "binary" {
			continue
		}
		if normOS != "" && strings.ToLower(strings.TrimSpace(a.OS)) != normOS {
			continue
		}
		if normArch != "" && normalizeReleaseArch(a.Arch) != normArch {
			continue
		}
		shaURLs := uniqueStrings(append([]string{strings.TrimSpace(a.SHA256URL)}, DefaultSHA256URLs(a.URL)...))
		return Artifact{
			Kind:       "binary",
			URL:        a.URL,
			SHA256URLs: shaURLs,
			SHA256:     strings.TrimSpace(a.SHA256),
		}
	}
	return Artifact{}
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
	Digest             string `json:"digest"`
}

func releaseArtifact(v *domain.BuckitVersion, kind string, arch string) *domain.BuckitArtifact {
	if v == nil {
		return nil
	}
	normArch := normalizeReleaseArch(arch)
	for i := range v.Artifacts {
		a := &v.Artifacts[i]
		if a.Kind != kind {
			continue
		}
		if normArch == "" || normalizeReleaseArch(a.Arch) == normArch {
			return a
		}
	}
	if normArch == "" {
		for i := range v.Artifacts {
			a := &v.Artifacts[i]
			if a.Kind == kind {
				return a
			}
		}
	}
	return nil
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
			kind, osName, archName, ok := classifyReleaseAsset(a.Name)
			if !ok {
				continue
			}
			if isChecksumAssetName(a.Name) {
				continue
			}
			v.Artifacts = append(v.Artifacts, domain.BuckitArtifact{
				Kind:   kind,
				OS:     osName,
				Arch:   archName,
				URL:    a.BrowserDownloadURL,
				SHA256: parseGitHubDigest(a.Digest),
			})
		}
		for _, a := range r.Assets {
			if !isChecksumAssetName(a.Name) {
				continue
			}
			base := checksumTargetName(a.Name)
			for i := range v.Artifacts {
				if strings.EqualFold(pathBase(v.Artifacts[i].URL), base) {
					v.Artifacts[i].SHA256URL = a.BrowserDownloadURL
					break
				}
			}
		}
		for _, a := range v.Artifacts {
			switch a.Kind {
			case "rpm":
				switch normalizeReleaseArch(a.Arch) {
				case "amd64":
					v.RpmURLAmd64 = a.URL
					v.SHA256URLAmd64 = a.SHA256URL
					v.SHA256Amd64 = a.SHA256
					if v.RpmURL == "" {
						v.RpmURL = a.URL
					}
					if v.SHA256URL == "" {
						v.SHA256URL = a.SHA256URL
					}
					if v.SHA256 == "" {
						v.SHA256 = a.SHA256
					}
				case "arm64":
					v.RpmURLArm64 = a.URL
					v.SHA256URLArm64 = a.SHA256URL
					v.SHA256Arm64 = a.SHA256
					if v.RpmURL == "" {
						v.RpmURL = a.URL
					}
					if v.SHA256URL == "" {
						v.SHA256URL = a.SHA256URL
					}
					if v.SHA256 == "" {
						v.SHA256 = a.SHA256
					}
				}
			case "deb":
				if v.DebURL == "" {
					v.DebURL = a.URL
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func classifyReleaseAsset(name string) (kind string, osName string, arch string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case isChecksumAssetName(lower):
		target := checksumTargetName(lower)
		return classifyReleaseAsset(target)
	case strings.HasSuffix(lower, ".rpm"):
		return "rpm", "linux", inferAssetArch(lower), true
	case strings.HasSuffix(lower, ".deb"):
		return "deb", "linux", inferAssetArch(lower), true
	case strings.HasSuffix(lower, ".apk"):
		return "apk", "linux", inferAssetArch(lower), true
	case strings.Contains(lower, "linux"):
		return "binary", "linux", inferAssetArch(lower), true
	case strings.Contains(lower, "darwin"):
		return "binary", "darwin", inferAssetArch(lower), true
	case strings.Contains(lower, "windows"):
		return "binary", "windows", inferAssetArch(lower), true
	default:
		return "", "", "", false
	}
}

func inferAssetArch(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "aarch64"), strings.Contains(lower, "arm64"):
		return "arm64"
	case strings.Contains(lower, "x86_64"), strings.Contains(lower, "amd64"):
		return "amd64"
	default:
		return ""
	}
}

func isChecksumAssetName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(lower, ".sha256sum") || strings.HasSuffix(lower, ".rpm.sha256") || strings.HasSuffix(lower, ".sha256")
}

func checksumTargetName(name string) string {
	lower := strings.TrimSpace(name)
	switch {
	case strings.HasSuffix(strings.ToLower(lower), ".sha256sum"):
		return strings.TrimSuffix(lower, ".sha256sum")
	case strings.HasSuffix(strings.ToLower(lower), ".rpm.sha256"):
		return strings.TrimSuffix(lower, ".sha256")
	case strings.HasSuffix(strings.ToLower(lower), ".sha256"):
		return strings.TrimSuffix(lower, ".sha256")
	default:
		return lower
	}
}

func pathBase(rawURL string) string {
	parts := strings.Split(rawURL, "/")
	if len(parts) == 0 {
		return rawURL
	}
	return parts[len(parts)-1]
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func parseGitHubDigest(digest string) string {
	digest = strings.TrimSpace(strings.ToLower(digest))
	if !strings.HasPrefix(digest, "sha256:") {
		return ""
	}
	sum := strings.TrimPrefix(digest, "sha256:")
	if !isSHA256Hex(sum) {
		return ""
	}
	return sum
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
