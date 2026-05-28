package update

import (
	"context"
	"crypto"
	_ "crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/buckit-io/selfupdate"

	"github.com/buckit-io/bm/internal/version"
)

const (
	defaultPointerBaseURL  = "https://buckit-io.github.io/bm/manager/bm/release"
	defaultReleasesBaseURL = "https://github.com/buckit-io/bm/releases/download"
	envMinisignPubKey      = "BM_UPDATE_MINISIGN_PUBKEY"
	// Buckit minisign public key for release signature verification.
	// Key ID: 62EB8D2A3DCDDA31
	defaultMinisignPubkey = "RWQx2s09Ko3rYmr64TBI992Oppjy00FUMQAUM9Ezf9czdMFOhB7CmKKU"
)

var releaseTagRegex = regexp.MustCompile(`RELEASE\.\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z(?:\.[A-Za-z0-9._-]+)?`)

type Status struct {
	CurrentVersion   string `json:"currentVersion"`
	InstalledVersion string `json:"installedVersion"`
	LatestVersion    string `json:"latestVersion,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	DownloadURL      string `json:"downloadUrl,omitempty"`
	CanApply         bool   `json:"canApply"`
	Reason           string `json:"reason,omitempty"`
	RestartRequired  bool   `json:"restartRequired"`
}

type ApplyResult struct {
	Applied          bool   `json:"applied"`
	CurrentVersion   string `json:"currentVersion"`
	InstalledVersion string `json:"installedVersion"`
	LatestVersion    string `json:"latestVersion,omitempty"`
	RestartRequired  bool   `json:"restartRequired"`
	Message          string `json:"message"`
}

type ReleaseInfo struct {
	Version     string
	DownloadURL string
	SHA256      []byte
}

type Service struct {
	Client                       *http.Client
	PointerBaseURL               string
	ReleasesBaseURL              string
	CurrentVersion               string
	TargetPath                   string
	MinisignPublicKey            string
	DisableSignatureVerification bool
}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Check(ctx context.Context) (Status, error) {
	currentVersion := s.currentVersion()
	targetPath, err := s.targetPath()
	if err != nil {
		return Status{}, err
	}
	installedVersion := s.installedVersion(targetPath, currentVersion)

	release, err := s.fetchLatest(ctx)
	if err != nil {
		return Status{
			CurrentVersion:   currentVersion,
			InstalledVersion: installedVersion,
		}, err
	}

	status := Status{
		CurrentVersion:   currentVersion,
		InstalledVersion: installedVersion,
		LatestVersion:    release.Version,
		DownloadURL:      release.DownloadURL,
		RestartRequired:  installedVersion != "" && installedVersion != currentVersion,
	}

	status.UpdateAvailable = compareReleaseTags(release.Version, installedVersion) > 0
	if status.RestartRequired {
		status.Reason = fmt.Sprintf("Update already installed on disk. Restart bm web to use %s.", installedVersion)
	}
	if status.UpdateAvailable {
		canApply, reason := s.canApply(targetPath, currentVersion)
		status.CanApply = canApply
		if reason != "" {
			status.Reason = reason
		}
	}
	return status, nil
}

func (s *Service) Apply(ctx context.Context) (ApplyResult, error) {
	status, err := s.Check(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	if status.RestartRequired && !status.UpdateAvailable {
		return ApplyResult{
			Applied:          false,
			CurrentVersion:   status.CurrentVersion,
			InstalledVersion: status.InstalledVersion,
			LatestVersion:    status.LatestVersion,
			RestartRequired:  true,
			Message:          status.Reason,
		}, nil
	}
	if !status.UpdateAvailable {
		return ApplyResult{
			Applied:          false,
			CurrentVersion:   status.CurrentVersion,
			InstalledVersion: status.InstalledVersion,
			LatestVersion:    status.LatestVersion,
			RestartRequired:  status.RestartRequired,
			Message:          "Already up to date.",
		}, nil
	}
	if !status.CanApply {
		return ApplyResult{}, errors.New(status.Reason)
	}

	release, err := s.fetchLatest(ctx)
	if err != nil {
		return ApplyResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.DownloadURL, nil)
	if err != nil {
		return ApplyResult{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("download release binary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ApplyResult{}, fmt.Errorf("download release binary: unexpected status %s", resp.Status)
	}

	opts := selfupdate.Options{
		TargetPath: s.TargetPath,
		Hash:       crypto.SHA256,
		Checksum:   release.SHA256,
	}
	if opts.TargetPath == "" {
		opts.TargetPath, err = s.targetPath()
		if err != nil {
			return ApplyResult{}, err
		}
	}
	if err := opts.CheckPermissions(); err != nil {
		return ApplyResult{}, fmt.Errorf("update permissions: %w", err)
	}
	if pub := s.minisignPublicKey(); pub != "" {
		v := selfupdate.NewVerifier()
		if err := v.LoadFromURL(release.DownloadURL+".minisig", pub, s.client().Transport); err != nil {
			return ApplyResult{}, fmt.Errorf("load minisign signature: %w", err)
		}
		opts.Verifier = v
	}
	if err := selfupdate.Apply(resp.Body, opts); err != nil {
		return ApplyResult{}, fmt.Errorf("apply update: %w", err)
	}

	installedVersion := s.installedVersion(opts.TargetPath, status.CurrentVersion)
	return ApplyResult{
		Applied:          true,
		CurrentVersion:   status.CurrentVersion,
		InstalledVersion: installedVersion,
		LatestVersion:    release.Version,
		RestartRequired:  installedVersion != "" && installedVersion != status.CurrentVersion,
		Message:          fmt.Sprintf("Update installed on disk. Restart bm web to use version %s.", release.Version),
	}, nil
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *Service) currentVersion() string {
	if s.CurrentVersion != "" {
		return s.CurrentVersion
	}
	return version.Version
}

func (s *Service) targetPath() (string, error) {
	if s.TargetPath != "" {
		return s.TargetPath, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return path, nil
}

func (s *Service) installedVersion(targetPath, fallback string) string {
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return fallback
	}
	tag := releaseTagRegex.Find(data)
	if len(tag) == 0 {
		return fallback
	}
	return string(tag)
}

func (s *Service) canApply(targetPath, currentVersion string) (bool, string) {
	if !isReleaseTag(currentVersion) {
		return false, "Development build; download the new binary and restart bm web manually."
	}
	opts := selfupdate.Options{TargetPath: targetPath}
	if err := opts.CheckPermissions(); err != nil {
		return false, fmt.Sprintf("Cannot write the bm binary at %s: %v", targetPath, err)
	}
	return true, ""
}

func (s *Service) fetchLatest(ctx context.Context) (ReleaseInfo, error) {
	pointerURL := s.pointerURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pointerURL, nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("fetch update pointer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("fetch update pointer: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("read update pointer: %w", err)
	}
	sum, tag, err := parsePointer(string(body))
	if err != nil {
		return ReleaseInfo{}, err
	}
	return ReleaseInfo{
		Version:     tag,
		DownloadURL: s.downloadURL(tag),
		SHA256:      sum,
	}, nil
}

func (s *Service) pointerURL() string {
	base := strings.TrimRight(s.PointerBaseURL, "/")
	if base == "" {
		base = defaultPointerBaseURL
	}
	return base + "/" + platform() + "/bm.sha256sum"
}

func (s *Service) downloadURL(tag string) string {
	base := strings.TrimRight(s.ReleasesBaseURL, "/")
	if base == "" {
		base = defaultReleasesBaseURL
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return base + "/" + url.PathEscape(tag) + "/bm-" + platform() + "." + tag + ext
}

func (s *Service) minisignPublicKey() string {
	if s.DisableSignatureVerification {
		return ""
	}
	if s.MinisignPublicKey != "" {
		return s.MinisignPublicKey
	}
	return firstNonEmpty(os.Getenv(envMinisignPubKey), defaultMinisignPubkey)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func platform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func parsePointer(data string) ([]byte, string, error) {
	fields := strings.Fields(strings.TrimSpace(data))
	if len(fields) != 2 {
		return nil, "", fmt.Errorf("invalid release pointer")
	}
	sum, err := hex.DecodeString(fields[0])
	if err != nil {
		return nil, "", fmt.Errorf("invalid release checksum: %w", err)
	}
	tag := releaseTagRegex.FindString(fields[1])
	if tag == "" {
		return nil, "", fmt.Errorf("invalid release pointer payload")
	}
	return sum, tag, nil
}

func isReleaseTag(v string) bool {
	return releaseTagRegex.MatchString(v)
}

func compareReleaseTags(a, b string) int {
	pa, oka := parseReleaseTag(a)
	pb, okb := parseReleaseTag(b)
	switch {
	case !oka && !okb:
		return strings.Compare(a, b)
	case oka && !okb:
		return 1
	case !oka && okb:
		return -1
	}
	if pa.timestamp.Before(pb.timestamp) {
		return -1
	}
	if pa.timestamp.After(pb.timestamp) {
		return 1
	}
	switch {
	case pa.stable && !pb.stable:
		return 1
	case !pa.stable && pb.stable:
		return -1
	}
	return strings.Compare(pa.suffix, pb.suffix)
}

type parsedReleaseTag struct {
	timestamp time.Time
	suffix    string
	stable    bool
}

func parseReleaseTag(v string) (parsedReleaseTag, bool) {
	if !strings.HasPrefix(v, "RELEASE.") {
		return parsedReleaseTag{}, false
	}
	rest := strings.TrimPrefix(v, "RELEASE.")
	parts := strings.SplitN(rest, ".", 2)
	ts, err := time.Parse("2006-01-02T15-04-05Z", parts[0])
	if err != nil {
		return parsedReleaseTag{}, false
	}
	out := parsedReleaseTag{timestamp: ts, stable: len(parts) == 1}
	if len(parts) == 2 {
		out.suffix = parts[1]
	}
	return out, true
}
