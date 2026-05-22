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

const rpmDownloadPath = "/tmp/buckit.rpm"

type RPMArtifact struct {
	URL        string
	SHA256URLs []string
	SHA256     string
}

func CustomRPMArtifact(url string) RPMArtifact {
	u := strings.TrimSpace(url)
	return RPMArtifact{URL: u, SHA256URLs: DefaultSHA256URLs(u)}
}

func DefaultSHA256URLs(url string) []string {
	u := strings.TrimSpace(url)
	if u == "" {
		return nil
	}
	return []string{u + ".sha256sum", u + ".sha256"}
}

func FetchRPMChecksum(ctx context.Context, artifact RPMArtifact) (string, error) {
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

func DownloadRPMCommand(url string) string {
	return fmt.Sprintf("curl -fSL -o %s %s", rpmDownloadPath, ShellEscape(url))
}

func VerifyRPMChecksumCommand(expectedSHA256 string) string {
	line := fmt.Sprintf("%s  %s\\n", strings.TrimSpace(expectedSHA256), rpmDownloadPath)
	quotedLine := ShellEscape(line)
	return strings.Join([]string{
		"if command -v sha256sum >/dev/null 2>&1; then",
		"printf " + quotedLine + " | sha256sum -c -;",
		"elif command -v shasum >/dev/null 2>&1; then",
		"printf " + quotedLine + " | shasum -a 256 -c -;",
		"else",
		"echo 'sha256 tool not found' >&2;",
		"exit 1;",
		"fi",
	}, " ")
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
