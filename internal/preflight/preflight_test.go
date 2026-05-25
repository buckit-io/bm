package preflight

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

// fakeConn is a HostConn that returns canned outputs per command match.
type fakeConn struct {
	// commands maps an exact command -> (stdout, stderr, exit, err).
	commands map[string]cannedResp
	// fallback runs when no exact match.
	fallback func(cmd string) cannedResp
	// HEAD returns 200/100 by default.
	headStatus int
	headSize   int64
	headErr    error
}

type cannedResp struct {
	stdout string
	stderr string
	exit   int
	err    error
}

func (f *fakeConn) Run(_ context.Context, _ domain.HostRow, cmd string) (string, string, int, error) {
	if r, ok := f.commands[cmd]; ok {
		return r.stdout, r.stderr, r.exit, r.err
	}
	if f.fallback != nil {
		r := f.fallback(cmd)
		return r.stdout, r.stderr, r.exit, r.err
	}
	return "", "", 0, nil
}

func (f *fakeConn) HEAD(_ context.Context, _ string) (int, int64, error) {
	return f.headStatus, f.headSize, f.headErr
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		commands:   map[string]cannedResp{},
		headStatus: 200,
		headSize:   1024,
	}
}

func basicDraft() domain.NewClusterDraft {
	a := 8
	r := 16
	mountDrives := []domain.DiscoveredDrive{
		{Mount: "/data/disk1", FsType: "xfs", SizeBytes: 16 << 40},
		{Mount: "/data/disk2", FsType: "xfs", SizeBytes: 16 << 40},
		{Mount: "/", IsBoot: true},
	}
	return domain.NewClusterDraft{
		Name:    "test",
		Version: "v1.0.0",
		API:     domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:  "us-east-1",
		Hosts: []domain.HostRow{
			{ID: "h1", Hostname: "node1", Port: 22, Probe: domain.HostProbeReachable},
			{ID: "h2", Hostname: "node2", Port: 22, Probe: domain.HostProbeReachable},
			{ID: "h3", Hostname: "node3", Port: 22, Probe: domain.HostProbeReachable},
		},
		SSH:      domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ops"},
		Topology: domain.Topology{SetSize: 4, Parity: 2, SelectedMounts: []string{"/data/disk1", "/data/disk2"}},
		Discovery: map[string]domain.WizardDiscoveryResult{
			"h1": {State: domain.WizardDiscoveryDone, Arch: "amd64", OS: "fake 1", Cores: &a, RamGiB: &r, Drives: mountDrives},
			"h2": {State: domain.WizardDiscoveryDone, Arch: "amd64", OS: "fake 1", Cores: &a, RamGiB: &r, Drives: mountDrives},
			"h3": {State: domain.WizardDiscoveryDone, Arch: "amd64", OS: "fake 1", Cores: &a, RamGiB: &r, Drives: mountDrives},
		},
	}
}

func happyFallback() func(cmd string) cannedResp {
	return func(cmd string) cannedResp {
		switch {
		case strings.HasPrefix(cmd, "command -v "):
			// dnf detected for all hosts.
			if strings.Contains(cmd, "dnf") {
				return cannedResp{stdout: "/usr/bin/dnf\n", exit: 0}
			}
			return cannedResp{exit: 1}
		case strings.HasPrefix(cmd, "df -B1 --output=avail"):
			return cannedResp{stdout: "Avail\n5000000000\n", exit: 0}
		case strings.HasPrefix(cmd, "test -f"):
			return cannedResp{exit: 1} // no stale format
		case strings.HasPrefix(cmd, "getent hosts"):
			return cannedResp{exit: 0}
		case strings.HasPrefix(cmd, "timeout 2 bash -c '</dev/tcp/"):
			return cannedResp{exit: 0}
		case strings.HasPrefix(cmd, "ss -ltn"):
			return cannedResp{stdout: "State   Recv-Q Send-Q   Local Address:Port\n"}
		case strings.HasPrefix(cmd, "rpm -q") || strings.HasPrefix(cmd, "dpkg -s"):
			return cannedResp{exit: 1} // no existing buckit
		case strings.HasPrefix(cmd, "curl -fI"):
			return cannedResp{exit: 0}
		case cmd == "sudo -n true" || cmd == "true":
			return cannedResp{exit: 0}
		case strings.HasPrefix(cmd, "date +%s.%N"):
			return cannedResp{stdout: fmt.Sprintf("%.3f\n", nowFn()), exit: 0}
		}
		return cannedResp{exit: 0}
	}
}

func TestRunAllHappyPath(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)

	conn := newFakeConn()
	conn.fallback = happyFallback()

	results := RunAll(context.Background(), conn, basicDraft())
	if len(results) == 0 {
		t.Fatal("no results")
	}
	for _, r := range results {
		if r.Severity == domain.PreflightBlocking && r.Result == domain.PreflightFail {
			t.Errorf("blocking check %s failed: %+v", r.ID, r)
		}
	}
}

func TestRunAllArchMismatch(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = happyFallback()

	d := basicDraft()
	d.Discovery["h2"] = domain.WizardDiscoveryResult{State: domain.WizardDiscoveryDone, Arch: "arm64"}

	got := findResult(RunAll(context.Background(), conn, d), "arch_uniform")
	if got.Result != domain.PreflightFail {
		t.Fatalf("want arch_uniform fail, got %s (%s)", got.Result, got.Detail)
	}
}

func TestRunAllArtifactUnreachable(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = func(cmd string) cannedResp {
		if strings.HasPrefix(cmd, "curl -fI") {
			return cannedResp{exit: 22, err: nil} // simulate corp-proxy block
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "artifact_reachable")
	if got.Result != domain.PreflightFail {
		t.Fatalf("want artifact_reachable fail, got %s", got.Result)
	}
}

func TestFreeSpaceWarnBelowOneGiB(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = func(cmd string) cannedResp {
		if strings.HasPrefix(cmd, "df -B1 --output=avail") {
			return cannedResp{stdout: "Avail\n50000000\n", exit: 0}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "free")
	if got.Result != domain.PreflightWarn {
		t.Fatalf("want free warn, got %s (%s)", got.Result, got.Detail)
	}
	if len(got.HostStatuses) == 0 || got.HostStatuses[0].Status != domain.PreflightWarn {
		t.Fatalf("want host warning, got %+v", got.HostStatuses)
	}
}

func TestFreeSpaceFailAtOrBelowTwentyFiveMiB(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = func(cmd string) cannedResp {
		if strings.HasPrefix(cmd, "df -B1 --output=avail") {
			return cannedResp{stdout: "Avail\n20000000\n", exit: 0}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "free")
	if got.Result != domain.PreflightFail {
		t.Fatalf("want free fail, got %s (%s)", got.Result, got.Detail)
	}
	if len(got.HostStatuses) == 0 || got.HostStatuses[0].Status != domain.PreflightFail {
		t.Fatalf("want host failure, got %+v", got.HostStatuses)
	}
}

func TestSudoSkippedAsRoot(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = happyFallback()

	d := basicDraft()
	d.SSH.User = "root"
	got := findResult(RunAll(context.Background(), conn, d), "sudo")
	if got.Result != domain.PreflightSkipped {
		t.Fatalf("want skipped, got %s", got.Result)
	}
}

func TestTimeSyncRetriesTransientReadFailure(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	// Per-host parallelism in preflight means this closure runs on multiple
	// goroutines — increment via atomic so the race detector stays happy.
	var dateCalls atomic.Int64
	conn.fallback = func(cmd string) cannedResp {
		if cmd == "date +%s.%N" {
			if dateCalls.Add(1) == 1 {
				return cannedResp{exit: 1}
			}
			return cannedResp{stdout: fmt.Sprintf("%.3f\n", nowFn()), exit: 0}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "time")
	if got.Result != domain.PreflightPass {
		t.Fatalf("want time pass after retry, got %s (%s)", got.Result, got.Detail)
	}
}

func TestPortsConflictFallsBackToProcNet(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = func(cmd string) cannedResp {
		switch {
		case strings.HasPrefix(cmd, "ss -ltn"):
			return cannedResp{stderr: "ss: command not found", exit: 127}
		case strings.HasPrefix(cmd, "netstat -ltn"):
			return cannedResp{stderr: "netstat: command not found", exit: 127}
		case strings.HasPrefix(cmd, "lsof -nP"):
			return cannedResp{stderr: "lsof: command not found", exit: 127}
		case strings.HasPrefix(cmd, "for f in /proc/net/tcp /proc/net/tcp6;"):
			return cannedResp{stdout: "1\n", exit: 0}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "ports_conflict")
	if got.Result != domain.PreflightFail {
		t.Fatalf("want ports_conflict fail, got %s (%s)", got.Result, got.Detail)
	}
	if len(got.HostStatuses) == 0 || !strings.Contains(got.HostStatuses[0].Message, "/proc/net") {
		t.Fatalf("want /proc/net detail, got %+v", got.HostStatuses)
	}
}

func TestPortsConflictRetriesTransientSessionFailure(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	// preflight fans out per host in parallel; the closure below runs on
	// many goroutines and shares `ssCalls`. Use atomic to avoid the race
	// detector tripping on the increment/read sequence.
	var ssCalls atomic.Int64
	conn.fallback = func(cmd string) cannedResp {
		if strings.HasPrefix(cmd, "ss -ltn") {
			if ssCalls.Add(1) == 1 {
				return cannedResp{err: fmt.Errorf("new session: ssh: rejected: connect failed (open failed)")}
			}
			return cannedResp{stdout: "State Recv-Q Send-Q Local Address:Port Peer Address:Port\n", exit: 0}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "ports_conflict")
	if got.Result != domain.PreflightPass {
		t.Fatalf("want ports_conflict pass after retry, got %s (%s)", got.Result, got.Detail)
	}
}

func TestStaleFormatChecksManagedBuckitSubdir(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = func(cmd string) cannedResp {
		if cmd == "test -f \"/data/disk1/buckit\"/.minio.sys/format.json" {
			return cannedResp{exit: 0}
		}
		if cmd == "test -f \"/data/disk2/buckit\"/.minio.sys/format.json" {
			return cannedResp{exit: 1}
		}
		return happyFallback()(cmd)
	}

	got := findResult(RunAll(context.Background(), conn, basicDraft()), "stale_format")
	if got.Result != domain.PreflightFail {
		t.Fatalf("want stale_format fail, got %s (%s)", got.Result, got.Detail)
	}
	if len(got.HostStatuses) == 0 || !strings.Contains(got.HostStatuses[0].Message, "/data/disk1") {
		t.Fatalf("want stale mount detail, got %+v", got.HostStatuses)
	}
}

func TestExistingServiceWarnsFromDiscovery(t *testing.T) {
	SetVersionResolver(func(_ string) string { return "https://example.com/buckit.rpm" })
	defer SetVersionResolver(nil)
	conn := newFakeConn()
	conn.fallback = happyFallback()

	d := basicDraft()
	r := d.Discovery["h2"]
	r.ExistingService = "minio"
	d.Discovery["h2"] = r

	got := findResult(RunAll(context.Background(), conn, d), "existing_service")
	if got.Result != domain.PreflightWarn {
		t.Fatalf("want existing_service warn, got %s (%s)", got.Result, got.Detail)
	}
	if len(got.HostStatuses) == 0 {
		t.Fatalf("want host statuses, got none")
	}
	found := false
	for _, hs := range got.HostStatuses {
		if hs.HostID == "h2" {
			found = true
			if hs.Status != domain.PreflightWarn || !strings.Contains(hs.Message, "minio service already present") {
				t.Fatalf("want minio warning for h2, got %+v", hs)
			}
		}
	}
	if !found {
		t.Fatalf("missing h2 status: %+v", got.HostStatuses)
	}
}

func TestHostnamePattern(t *testing.T) {
	cases := []struct {
		name    string
		hosts   []string
		wantOK  bool
		pattern string
	}{
		{"sequential", []string{"node1", "node2", "node3"}, true, "node{1..3}"},
		{"with prefix and suffix", []string{"node1.lan", "node2.lan", "node3.lan"}, true, "node{1..3}.lan"},
		{"non-sequential gap", []string{"node1", "node3", "node5"}, false, ""},
		{"no shared prefix", []string{"alpha", "beta"}, false, ""},
		{"single", []string{"only-host"}, true, "only-host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pat, ok := detectBraceExpansion(tc.hosts)
			if ok != tc.wantOK {
				t.Fatalf("want %v, got %v (pat=%q)", tc.wantOK, ok, pat)
			}
			if tc.wantOK && tc.pattern != "" && pat != tc.pattern {
				t.Fatalf("want pattern %q, got %q", tc.pattern, pat)
			}
		})
	}
}

func TestCheckPkgMgrDetectsKindAndRejectsMixed(t *testing.T) {
	t.Run("rpm-only cluster passes with kind in detail", func(t *testing.T) {
		conn := newFakeConn()
		conn.fallback = func(cmd string) cannedResp {
			if cmd == "command -v dnf" {
				return cannedResp{stdout: "/usr/bin/dnf\n", exit: 0}
			}
			return cannedResp{exit: 1}
		}
		out := checkPkgMgr(context.Background(), conn, basicDraft())
		if len(out.HostStatuses) == 0 {
			t.Fatal("expected host statuses")
		}
		for _, s := range out.HostStatuses {
			if s.Status != domain.PreflightPass {
				t.Errorf("%s: want pass, got %s (%s)", s.Hostname, s.Status, s.Message)
			}
			if !strings.Contains(s.Message, "rpm") {
				t.Errorf("%s: detail should mention rpm kind, got %q", s.Hostname, s.Message)
			}
		}
	})

	t.Run("apt-only cluster passes with deb in detail", func(t *testing.T) {
		conn := newFakeConn()
		conn.fallback = func(cmd string) cannedResp {
			if cmd == "command -v apt-get" {
				return cannedResp{stdout: "/usr/bin/apt-get\n", exit: 0}
			}
			return cannedResp{exit: 1}
		}
		out := checkPkgMgr(context.Background(), conn, basicDraft())
		for _, s := range out.HostStatuses {
			if s.Status != domain.PreflightPass {
				t.Errorf("%s: want pass, got %s (%s)", s.Hostname, s.Status, s.Message)
			}
			if !strings.Contains(s.Message, "deb") {
				t.Errorf("%s: detail should mention deb kind, got %q", s.Hostname, s.Message)
			}
		}
	})

	t.Run("none found fails", func(t *testing.T) {
		conn := newFakeConn()
		conn.fallback = func(cmd string) cannedResp {
			return cannedResp{exit: 1}
		}
		out := checkPkgMgr(context.Background(), conn, basicDraft())
		for _, s := range out.HostStatuses {
			if s.Status != domain.PreflightFail {
				t.Errorf("%s: want fail, got %s", s.Hostname, s.Status)
			}
		}
	})

	t.Run("mixed managers fails every host with one consistent message", func(t *testing.T) {
		conn := newFakeConn()
		// node1 returns dnf, node3 returns apt-get only.
		conn.commands = map[string]cannedResp{}
		conn.fallback = func(cmd string) cannedResp {
			return cannedResp{exit: 1}
		}
		// Use commands map to dispatch per-host doesn't work with fakeConn
		// (it ignores HostRow). Instead, inject host-keyed Run wrapper.
		hostConn := &hostKeyedConn{base: conn, per: map[string]map[string]cannedResp{
			"node1": {"command -v dnf": cannedResp{stdout: "/usr/bin/dnf\n"}},
			"node2": {"command -v dnf": cannedResp{stdout: "/usr/bin/dnf\n"}},
			"node3": {"command -v apt-get": cannedResp{stdout: "/usr/bin/apt-get\n"}},
		}}
		out := checkPkgMgr(context.Background(), hostConn, basicDraft())
		failures := 0
		var msg string
		for _, s := range out.HostStatuses {
			if s.Status == domain.PreflightFail {
				failures++
				if msg == "" {
					msg = s.Message
				} else if s.Message != msg {
					t.Errorf("inconsistent mixed-cluster messages: %q vs %q", msg, s.Message)
				}
			}
		}
		if failures != len(out.HostStatuses) {
			t.Errorf("expected all hosts to fail, got %d/%d", failures, len(out.HostStatuses))
		}
		if !strings.Contains(msg, "mixed package managers") {
			t.Errorf("expected mixed-cluster message, got %q", msg)
		}
	})
}

// hostKeyedConn is a HostConn that dispatches Run by hostname so a
// single test can simulate two different hosts reporting different
// command -v results.
type hostKeyedConn struct {
	base *fakeConn
	per  map[string]map[string]cannedResp
}

func (c *hostKeyedConn) Run(ctx context.Context, h domain.HostRow, cmd string) (string, string, int, error) {
	if hm, ok := c.per[h.Hostname]; ok {
		if r, ok := hm[cmd]; ok {
			return r.stdout, r.stderr, r.exit, r.err
		}
		return "", "", 1, nil
	}
	return c.base.Run(ctx, h, cmd)
}

func (c *hostKeyedConn) HEAD(ctx context.Context, url string) (int, int64, error) {
	return c.base.HEAD(ctx, url)
}

// silence unused import in test helpers when fmt is no longer used.
var _ = fmt.Sprintf

func findResult(rs []domain.PreflightResult, id string) domain.PreflightResult {
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	return domain.PreflightResult{}
}
