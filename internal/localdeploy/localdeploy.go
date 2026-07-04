package localdeploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/credentials"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
)

const deploymentName = "local"
const existingBinaryWarningPrefix = "Buckit binary already exists at "

var pathPatternRE = regexp.MustCompile(`^(.*)\{(\d+)(?:\.\.|\.\.\.)(\d+)\}(.*)$`)

type Request struct {
	Version      string           `json:"version"`
	CustomURL    string           `json:"customUrl,omitempty"`
	CustomSHA256 string           `json:"customSha256,omitempty"`
	RootUser     string           `json:"rootUser"`
	RootPassword string           `json:"rootPassword"`
	APIPort      int              `json:"apiPort"`
	ConsolePort  int              `json:"consolePort"`
	DataPaths    []string         `json:"dataPaths"`
	Parity       int              `json:"parity,omitempty"`
	TLS          domain.TLSConfig `json:"tls,omitempty"`
}

type Response struct {
	ScriptPath string   `json:"scriptPath"`
	BinaryPath string   `json:"binaryPath"`
	DataPaths  []string `json:"dataPaths"`
	APIURL     string   `json:"apiUrl"`
	ConsoleURL string   `json:"consoleUrl"`
	Command    string   `json:"command"`
	SetSize    int      `json:"setSize,omitempty"`
	Parity     int      `json:"parity,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type Options struct {
	HomeDir string
	GOOS    string
	GOARCH  string
	Client  *http.Client
}

func Prepare(ctx context.Context, req Request, opts Options) (Response, error) {
	plan, err := preview(req, opts)
	if err != nil {
		return Response{}, err
	}
	home, err := homeDir(opts.HomeDir)
	if err != nil {
		return Response{}, err
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	paths := defaultPaths(home, goos)
	artifact, err := resolveBinaryArtifact(req, goos, goarch)
	if err != nil {
		return Response{}, err
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return Response{}, fmt.Errorf("create local deployment dir: %w", err)
	}
	for _, p := range plan.DataPaths {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return Response{}, fmt.Errorf("create data path %s: %w", p, err)
		}
	}
	plan.Warnings = warnRootDriveDataPaths(goos, plan.DataPaths, plan.Warnings)
	if err := os.MkdirAll(filepath.Dir(paths.Binary), 0o700); err != nil {
		return Response{}, fmt.Errorf("create binary dir: %w", err)
	}
	expectedSHA256, err := resolveChecksum(ctx, artifact)
	if err != nil {
		return Response{}, err
	}
	if err := downloadFile(ctx, opts.Client, artifact.URL, paths.Binary, executableMode(goos)); err != nil {
		return Response{}, err
	}
	if err := verifyFileSHA256(paths.Binary, expectedSHA256); err != nil {
		return Response{}, err
	}

	certsDir := ""
	if req.TLS.Enabled() {
		certsDir = filepath.Join(paths.Root, "certs")
		if err := writeTLSFiles(certsDir, req.TLS); err != nil {
			return Response{}, err
		}
	}

	script, err := renderScript(goos, scriptParams{
		BinaryPath:   paths.Binary,
		DataPaths:    plan.DataPaths,
		RootUser:     req.RootUser,
		RootPassword: req.RootPassword,
		APIPort:      req.APIPort,
		ConsolePort:  req.ConsolePort,
		CertsDir:     certsDir,
		SetSize:      plan.SetSize,
		Parity:       plan.Parity,
	})
	if err != nil {
		return Response{}, err
	}
	if err := os.WriteFile(paths.Script, []byte(script), executableMode(goos)); err != nil {
		return Response{}, fmt.Errorf("write start script: %w", err)
	}
	plan.Warnings = postPrepareWarnings(plan.Warnings)
	return plan, nil
}

func Preview(req Request, opts Options) (Response, error) {
	return preview(req, opts)
}

func preview(req Request, opts Options) (Response, error) {
	if err := validateBasic(req); err != nil {
		return Response{}, err
	}
	home, err := homeDir(opts.HomeDir)
	if err != nil {
		return Response{}, err
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos != "darwin" && goos != "windows" {
		return Response{}, fmt.Errorf("local deployment preparation is supported on macOS and Windows, not %s", goos)
	}
	if _, err := resolveBinaryArtifact(req, goos, goarch); err != nil {
		return Response{}, err
	}

	dataPaths, warnings, err := normalizeDataPaths(home, req.DataPaths)
	if err != nil {
		return Response{}, err
	}
	if req.TLS.Enabled() {
		tls := normalizeTLS(req.TLS)
		if err := deploy.ValidateTLSConfig(tls, nil); err != nil {
			return Response{}, err
		}
		if err := deploy.ValidateTLSConfig(tls, []string{"localhost", "127.0.0.1"}); err != nil {
			warnings = append(warnings, "TLS certificate should include localhost and 127.0.0.1 in Subject Alternative Names.")
		}
		req.TLS = tls
	}

	paths := defaultPaths(home, goos)
	dataPaths, warnings, err = validateAgainstDeploymentRoot(paths.Root, dataPaths, warnings)
	if err != nil {
		return Response{}, err
	}
	setSize, err := computeSetSize(len(dataPaths))
	if err != nil {
		return Response{}, err
	}
	parity, err := normalizeParity(req.Parity, len(dataPaths), setSize)
	if err != nil {
		return Response{}, err
	}
	warnings = warnExistingBinary(paths.Binary, warnings)

	scheme := "http"
	if req.TLS.Enabled() {
		scheme = "https"
	}
	return Response{
		ScriptPath: paths.Script,
		BinaryPath: paths.Binary,
		DataPaths:  dataPaths,
		APIURL:     fmt.Sprintf("%s://127.0.0.1:%d", scheme, req.APIPort),
		ConsoleURL: fmt.Sprintf("%s://127.0.0.1:%d", scheme, req.ConsolePort),
		Command:    commandFor(goos, paths.Script),
		SetSize:    setSize,
		Parity:     parity,
		Warnings:   warnings,
	}, nil
}

func validateBasic(req Request) error {
	if strings.TrimSpace(req.Version) == "" {
		return errors.New("version required")
	}
	if err := credentials.ValidateRootUser(req.RootUser); err != nil {
		return err
	}
	if err := credentials.ValidateRootPassword(req.RootPassword); err != nil {
		return err
	}
	if req.APIPort < 1 || req.APIPort > 65535 {
		return errors.New("apiPort must be between 1 and 65535")
	}
	if req.ConsolePort < 1 || req.ConsolePort > 65535 {
		return errors.New("consolePort must be between 1 and 65535")
	}
	if req.APIPort == req.ConsolePort {
		return errors.New("apiPort and consolePort must be different")
	}
	if req.TLS.Mode != "" && req.TLS.Mode != domain.TLSOff && req.TLS.Mode != domain.TLSBYO {
		return errors.New("tls.mode must be off or byo")
	}
	if req.TLS.Enabled() {
		if strings.TrimSpace(req.TLS.CertPEM) == "" {
			return errors.New("tls certPem required")
		}
		if strings.TrimSpace(req.TLS.KeyPEM) == "" {
			return errors.New("tls keyPem required")
		}
	}
	return nil
}

func validateAgainstDeploymentRoot(root string, dataPaths []string, warnings []string) ([]string, []string, error) {
	root = filepath.Clean(root)
	certs := filepath.Join(root, "certs")
	for _, p := range dataPaths {
		p = filepath.Clean(p)
		if samePath(p, root) || isParentPath(p, root) {
			return nil, nil, fmt.Errorf("data path must not overlap the local deployment directory %s: %s", root, p)
		}
		if samePath(p, certs) || isParentPath(p, certs) || isParentPath(certs, p) {
			return nil, nil, fmt.Errorf("data path must not overlap the local TLS directory %s: %s", certs, p)
		}
	}
	return dataPaths, warnings, nil
}

var validSetSizes = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

func computeSetSize(dataPathCount int) (int, error) {
	if dataPathCount <= 1 {
		return dataPathCount, nil
	}
	best := 0
	bestSets := dataPathCount
	for _, size := range validSetSizes {
		if dataPathCount%size != 0 {
			continue
		}
		sets := dataPathCount / size
		if best == 0 || sets <= bestSets {
			best = size
			bestSets = sets
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("data path count %d is not divisible by any supported erasure set size (2-16)", dataPathCount)
	}
	return best, nil
}

func normalizeParity(requested int, dataPathCount int, setSize int) (int, error) {
	if dataPathCount <= 1 {
		if requested > 0 {
			return 0, errors.New("parity requires at least two data paths")
		}
		return 0, nil
	}
	if requested == 0 {
		return defaultParityBlocks(setSize), nil
	}
	maxParity := setSize / 2
	if requested < 1 || requested > maxParity {
		return 0, fmt.Errorf("parity must be between 1 and %d for erasure set size %d", maxParity, setSize)
	}
	return requested, nil
}

func defaultParityBlocks(driveCount int) int {
	switch driveCount {
	case 1:
		return 0
	case 2, 3:
		return 1
	case 4, 5:
		return 2
	case 6, 7:
		return 3
	default:
		return 4
	}
}

func warnRootDriveDataPaths(goos string, dataPaths []string, warnings []string) []string {
	if len(dataPaths) < 2 {
		return warnings
	}
	rootPaths := dataPathsOnRootDrive(goos, dataPaths)
	if len(rootPaths) == 0 {
		return warnings
	}
	label := "Data paths"
	verb := "are"
	drive := "drives"
	if len(rootPaths) == 1 {
		label = "Data path"
		verb = "is"
		drive = "drive"
	}
	return append(warnings, fmt.Sprintf("%s %s %s on the root/OS %s. It's recommended using root drives only for dev or test deployment.", label, strings.Join(rootPaths, ", "), verb, drive))
}

func warnExistingBinary(binaryPath string, warnings []string) []string {
	info, err := os.Stat(binaryPath)
	if err != nil || info.IsDir() {
		return warnings
	}
	return append(warnings, fmt.Sprintf("%s%s and will be overwritten.", existingBinaryWarningPrefix, binaryPath))
}

func postPrepareWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return warnings
	}
	out := warnings[:0]
	for _, warning := range warnings {
		if strings.HasPrefix(warning, existingBinaryWarningPrefix) {
			continue
		}
		out = append(out, warning)
	}
	return out
}

func normalizeDataPaths(home string, raw []string) ([]string, []string, error) {
	if len(raw) == 0 {
		return nil, nil, errors.New("at least one data path required")
	}
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	var warnings []string
	for _, p := range raw {
		expanded, err := expandPathPattern(p)
		if err != nil {
			return nil, nil, err
		}
		for _, path := range expanded {
			n, err := normalizePath(home, path)
			if err != nil {
				return nil, nil, err
			}
			key := pathKey(n)
			if _, ok := seen[key]; ok {
				return nil, nil, fmt.Errorf("duplicate data path %s", n)
			}
			seen[key] = struct{}{}
			if existing, err := os.ReadDir(n); err == nil && len(existing) > 0 {
				return nil, nil, fmt.Errorf("data path already exists and is not empty: %s", n)
			}
			if isLikelySystemPath(n) {
				warnings = append(warnings, fmt.Sprintf("%s appears to be a system path", n))
			}
			out = append(out, n)
		}
	}
	sorted := append([]string(nil), out...)
	sort.Strings(sorted)
	for i, a := range sorted {
		for _, b := range sorted[i+1:] {
			if isParentPath(a, b) {
				return nil, nil, fmt.Errorf("data paths must not overlap: %s contains %s", a, b)
			}
		}
	}
	return out, warnings, nil
}

func expandPathPattern(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	match := pathPatternRE.FindStringSubmatch(path)
	if match == nil {
		if strings.Contains(path, "{") || strings.Contains(path, "}") {
			return nil, fmt.Errorf("unsupported data path pattern: %s", path)
		}
		return []string{path}, nil
	}
	start, err := strconv.Atoi(match[2])
	if err != nil {
		return nil, fmt.Errorf("invalid data path pattern: %s", path)
	}
	end, err := strconv.Atoi(match[3])
	if err != nil {
		return nil, fmt.Errorf("invalid data path pattern: %s", path)
	}
	if end < start {
		return nil, fmt.Errorf("data path range must ascend: %s", path)
	}
	if end-start > 255 {
		return nil, fmt.Errorf("data path range is too large: %s", path)
	}
	width := 0
	if strings.HasPrefix(match[2], "0") || strings.HasPrefix(match[3], "0") {
		width = max(len(match[2]), len(match[3]))
	}
	out := make([]string, 0, end-start+1)
	for n := start; n <= end; n++ {
		value := strconv.Itoa(n)
		if width > 0 {
			value = fmt.Sprintf("%0*d", width, n)
		}
		out = append(out, match[1]+value+match[4])
	}
	return out, nil
}

func normalizePath(home, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("data path cannot be blank")
	}
	if strings.Contains(p, "://") {
		return "", fmt.Errorf("data path must be a local filesystem path: %s", p)
	}
	if p == "~" {
		p = home
	} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("data path must be absolute: %s", p)
	}
	return filepath.Clean(p), nil
}

func isParentPath(parent, child string) bool {
	parent = pathKey(parent)
	child = pathKey(child)
	if parent == child {
		return false
	}
	if parent == string(filepath.Separator) {
		return strings.HasPrefix(child, parent)
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

func isLikelySystemPath(p string) bool {
	clean := filepath.Clean(p)
	switch clean {
	case "/", "/System", "/Library", "/Applications", "/bin", "/sbin", "/usr", "/var", "/opt", "C:\\", `C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`:
		return true
	}
	return false
}

func samePath(a, b string) bool {
	return pathKey(a) == pathKey(b)
}

func pathKey(p string) string {
	return strings.ToLower(filepath.Clean(p))
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

func resolveBinaryArtifact(req Request, goos, goarch string) (deploy.Artifact, error) {
	if req.Version == "custom" {
		u := strings.TrimSpace(req.CustomURL)
		if u == "" {
			return deploy.Artifact{}, errors.New("customUrl required when version=custom")
		}
		parsed, err := neturl.Parse(u)
		if err != nil {
			return deploy.Artifact{}, fmt.Errorf("customUrl: %w", err)
		}
		if parsed.Scheme != "https" {
			return deploy.Artifact{}, errors.New("customUrl must use https")
		}
		sha := strings.ToLower(strings.TrimSpace(req.CustomSHA256))
		if !isSHA256Hex(sha) {
			return deploy.Artifact{}, errors.New("customSha256 required when version=custom")
		}
		return deploy.Artifact{Kind: "binary", URL: u, SHA256: sha}, nil
	}
	artifact, err := deploy.ResolveBinaryArtifact(req.Version, goos, goarch)
	if err != nil {
		return deploy.Artifact{}, err
	}
	return artifact, nil
}

func resolveChecksum(ctx context.Context, artifact deploy.Artifact) (string, error) {
	sha, err := deploy.FetchChecksum(ctx, artifact)
	if err != nil {
		return "", fmt.Errorf("resolve buckit binary checksum: %w", err)
	}
	return sha, nil
}

func verifyFileSHA256(path string, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open buckit binary for checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash buckit binary: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(expected)) {
		return fmt.Errorf("buckit binary checksum mismatch: got %s, want %s", got, expected)
	}
	return nil
}

func normalizeTLS(t domain.TLSConfig) domain.TLSConfig {
	t.CertPEM = normalizeLineEndings(t.CertPEM)
	t.KeyPEM = normalizeLineEndings(t.KeyPEM)
	t.CABundlePEM = normalizeLineEndings(t.CABundlePEM)
	return t
}

func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

type paths struct {
	Root   string
	Script string
	Binary string
}

func defaultPaths(home, goos string) paths {
	root := filepath.Join(home, "buckit", deploymentName)
	if goos == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return paths{
			Root:   root,
			Script: filepath.Join(root, "Start-Buckit.ps1"),
			Binary: filepath.Join(localAppData, "Programs", "Buckit", "buckit.exe"),
		}
	}
	return paths{
		Root:   root,
		Script: filepath.Join(root, "start-buckit.sh"),
		Binary: filepath.Join(home, ".local", "bin", "buckit"),
	}
}

func writeTLSFiles(certsDir string, tls domain.TLSConfig) error {
	if err := os.MkdirAll(filepath.Join(certsDir, "CAs"), 0o700); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "public.crt"), []byte(tls.CertPEM), 0o600); err != nil {
		return fmt.Errorf("write public.crt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "private.key"), []byte(tls.KeyPEM), 0o600); err != nil {
		return fmt.Errorf("write private.key: %w", err)
	}
	if strings.TrimSpace(tls.CABundlePEM) != "" {
		if err := os.WriteFile(filepath.Join(certsDir, "CAs", "ca.crt"), []byte(tls.CABundlePEM), 0o600); err != nil {
			return fmt.Errorf("write ca.crt: %w", err)
		}
	}
	return nil
}

func downloadFile(ctx context.Context, client *http.Client, url, target string, mode os.FileMode) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download buckit binary: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download buckit binary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download buckit binary: HTTP %d", resp.StatusCode)
	}
	tmp := target + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create buckit binary: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write buckit binary: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close buckit binary: %w", err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod buckit binary: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install buckit binary: %w", err)
	}
	return nil
}

func executableMode(goos string) os.FileMode {
	if goos == "windows" {
		return 0o600
	}
	return 0o700
}

type scriptParams struct {
	BinaryPath   string
	DataPaths    []string
	RootUser     string
	RootPassword string
	APIPort      int
	ConsolePort  int
	CertsDir     string
	SetSize      int
	Parity       int
}

func renderScript(goos string, p scriptParams) (string, error) {
	if goos == "windows" {
		var b strings.Builder
		b.WriteString("$ErrorActionPreference = 'Stop'\n")
		b.WriteString("$env:MINIO_ROOT_USER = ")
		b.WriteString(psQuote(p.RootUser))
		b.WriteString("\n$env:MINIO_ROOT_PASSWORD = ")
		b.WriteString(psQuote(p.RootPassword))
		if len(p.DataPaths) > 1 {
			b.WriteString("\n$env:MINIO_ROOTDRIVE_THRESHOLD_SIZE = '1B'")
			b.WriteString("\n$env:MINIO_ERASURE_SET_DRIVE_COUNT = ")
			b.WriteString(psQuote(strconv.Itoa(p.SetSize)))
			b.WriteString("\n$env:MINIO_STORAGE_CLASS_STANDARD = ")
			b.WriteString(psQuote(fmt.Sprintf("EC:%d", p.Parity)))
		}
		b.WriteString("\n\n$Volumes = @(\n")
		for _, v := range p.DataPaths {
			b.WriteString("  ")
			b.WriteString(psQuote(v))
			b.WriteString("\n")
		}
		b.WriteString(")\n\n")
		b.WriteString("Write-Host \"Storage class: $(if ($env:MINIO_STORAGE_CLASS_STANDARD) { $env:MINIO_STORAGE_CLASS_STANDARD } else { 'default' })\"\n")
		b.WriteString("Write-Host 'Data paths:'\n")
		b.WriteString("foreach ($Volume in $Volumes) { Write-Host \"  $Volume\" }\n\n& ")
		b.WriteString(psQuote(p.BinaryPath))
		b.WriteString(" server @Volumes --address ")
		b.WriteString(psQuote(":" + strconv.Itoa(p.APIPort)))
		b.WriteString(" --console-address ")
		b.WriteString(psQuote(":" + strconv.Itoa(p.ConsolePort)))
		if p.CertsDir != "" {
			b.WriteString(" --certs-dir ")
			b.WriteString(psQuote(p.CertsDir))
		}
		b.WriteString("\n")
		return b.String(), nil
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n\n")
	b.WriteString("export MINIO_ROOT_USER=")
	b.WriteString(shQuote(p.RootUser))
	b.WriteString("\nexport MINIO_ROOT_PASSWORD=")
	b.WriteString(shQuote(p.RootPassword))
	if len(p.DataPaths) > 1 {
		b.WriteString("\nexport MINIO_ROOTDRIVE_THRESHOLD_SIZE='1B'")
		b.WriteString("\nexport MINIO_ERASURE_SET_DRIVE_COUNT=")
		b.WriteString(shQuote(strconv.Itoa(p.SetSize)))
		b.WriteString("\nexport MINIO_STORAGE_CLASS_STANDARD=")
		b.WriteString(shQuote(fmt.Sprintf("EC:%d", p.Parity)))
	}
	b.WriteString("\n\n")
	b.WriteString("echo \"Storage class: ${MINIO_STORAGE_CLASS_STANDARD:-default}\"\n")
	b.WriteString("echo 'Data paths:'\n")
	for _, v := range p.DataPaths {
		b.WriteString("echo '  ")
		b.WriteString(strings.ReplaceAll(v, "'", "'\"'\"'"))
		b.WriteString("'\n")
	}
	b.WriteString("\nexec ")
	b.WriteString(shQuote(p.BinaryPath))
	b.WriteString(" server")
	for _, v := range p.DataPaths {
		b.WriteString(" \\\n  ")
		b.WriteString(shQuote(v))
	}
	b.WriteString(" \\\n  --address ")
	b.WriteString(shQuote(":" + strconv.Itoa(p.APIPort)))
	b.WriteString(" \\\n  --console-address ")
	b.WriteString(shQuote(":" + strconv.Itoa(p.ConsolePort)))
	if p.CertsDir != "" {
		b.WriteString(" \\\n  --certs-dir ")
		b.WriteString(shQuote(p.CertsDir))
	}
	b.WriteString("\n")
	return b.String(), nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func commandFor(goos, script string) string {
	if goos == "windows" {
		return `powershell -ExecutionPolicy Bypass -File ` + psQuote(script)
	}
	return shQuote(script)
}

func homeDir(override string) (string, error) {
	return homeDirValue(override)
}

func homeDirValue(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Clean(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Clean(home), nil
}
