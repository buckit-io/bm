package ssh

import (
	"context"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/domain"
)

// ProbeResult is what Probe returns for a single host. Mirrors the per-host
// facts the UI's preflight + node-detail pages want.
type ProbeResult struct {
	HostID   string `json:"hostId,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	OK       bool   `json:"ok"`
	RttMs    int64  `json:"rttMs,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
	OS       string `json:"os,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Probe dials host with creds, runs a tiny fact-gathering command set, and
// returns the result. Never returns a Go error — failure is surfaced via the
// ProbeResult.Error field so callers (the /ssh/test endpoint, the ssh_probe
// executor) can aggregate per-host outcomes without unwrapping wrapped errors.
func Probe(ctx context.Context, host domain.HostRef, creds Resolved) ProbeResult {
	res := ProbeResult{HostID: host.ID, Hostname: host.Hostname}
	if err := creds.Validate(); err != nil {
		res.Error = err.Error()
		return res
	}
	start := time.Now()
	client, err := Dial(ctx, host, creds)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer client.Close()
	res.RttMs = time.Since(start).Milliseconds()

	if line, ok := runOne(ctx, client, "uname -a"); ok {
		res.Kernel = strings.TrimSpace(line)
	}
	if line, ok := runOne(ctx, client, "hostname"); ok {
		host := strings.TrimSpace(line)
		if host != "" {
			res.Hostname = host
		}
	}
	if line, ok := runOne(ctx, client, "cat /etc/os-release"); ok {
		res.OS = parseOSRelease(line)
	}
	res.OK = true
	return res
}

func runOne(ctx context.Context, c *ssh.Client, cmd string) (string, bool) {
	r, err := Run(ctx, c, cmd)
	if err != nil || r.ExitCode != 0 {
		return "", false
	}
	return r.Stdout, true
}

// parseOSRelease pulls a short human description out of /etc/os-release. It
// prefers PRETTY_NAME, falls back to NAME+VERSION, then to the first line.
func parseOSRelease(body string) string {
	fields := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if eq := strings.IndexByte(line, '='); eq > 0 {
			k := line[:eq]
			v := strings.TrimSpace(line[eq+1:])
			v = strings.Trim(v, `"`)
			fields[k] = v
		}
	}
	if v := fields["PRETTY_NAME"]; v != "" {
		return v
	}
	if name, ok := fields["NAME"]; ok {
		if ver := fields["VERSION"]; ver != "" {
			return name + " " + ver
		}
		return name
	}
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
