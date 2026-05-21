package preflight

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/buckit-io/bm/internal/domain"
)

const (
	freeSpaceFailBytes = 25 << 20
	freeSpaceWarnBytes = 1 << 30
	hostRunAttempts    = 2
)

// NewClusterCatalog is the check list driven by POST /clusters/new/preflight.
// Order matches the UI rendering order in
// web/src/pages/wizards/new/steps/Preflight.tsx so the operator sees the
// failures in the same sequence they'd see in the mock.
func NewClusterCatalog() []Check {
	return []Check{
		{ID: "ssh", Label: "SSH reachability", Severity: domain.PreflightBlocking, Eval: checkSSH},
		{ID: "sudo", Label: "Sudo (passwordless)", Severity: domain.PreflightBlocking, Eval: checkSudo},
		{ID: "pkg_mgr", Label: "Package manager available", Severity: domain.PreflightBlocking, Eval: checkPkgMgr},
		{ID: "rpm", Label: "Package URL reachable from this host", Severity: domain.PreflightBlocking, Eval: checkArtifactBM},
		{ID: "artifact_reachable", Label: "Package URL reachable from each host", Severity: domain.PreflightBlocking, Eval: checkArtifactPerHost},
		{ID: "free", Label: "Free space on selected drives", Severity: domain.PreflightBlocking, Eval: checkFreeSpace},
		{ID: "stale_format", Label: "No stale .minio.sys/format.json", Severity: domain.PreflightBlocking, Eval: checkStaleFormat},
		{ID: "dns", Label: "DNS / hostname resolution", Severity: domain.PreflightBlocking, Eval: checkDNS},
		{ID: "ports", Label: "Inter-node port reachability (9000, 9001)", Severity: domain.PreflightBlocking, Eval: checkPortsReachable},
		{ID: "ports_conflict", Label: "Conflicting listeners on 9000/9001", Severity: domain.PreflightBlocking, Eval: checkPortsConflict},
		{ID: "arch_uniform", Label: "Architecture uniformity (amd64 / arm64)", Severity: domain.PreflightBlocking, Eval: checkArchUniform},
		{ID: "os_uniform", Label: "OS uniformity (same distro family)", Severity: domain.PreflightAdvisory, Eval: checkOSUniform},
		{ID: "xfs_fs", Label: "Data drives formatted XFS", Severity: domain.PreflightAdvisory, Eval: checkXFS},
		{ID: "hostname_pattern", Label: "Hostnames fit one brace-expansion pattern", Severity: domain.PreflightBlocking, Eval: checkHostnamePattern},
		{ID: "drive_uniformity", Label: "Drive paths uniform across hosts", Severity: domain.PreflightBlocking, Eval: checkDriveUniformity},
		{ID: "time", Label: "Time sync (skew < 1s)", Severity: domain.PreflightAdvisory, Eval: checkTimeSync},
		{ID: "existing_service", Label: "Existing buckit/minio service", Severity: domain.PreflightAdvisory, Eval: checkExistingService},
		{ID: "buckit_pkg", Label: "Existing buckit package", Severity: domain.PreflightAdvisory, Eval: checkExistingBuckit},
	}
}

// ---- SSH-flavoured per-host checks ----

func checkSSH(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		_, _, exit, err := runHostCommand(ctx, conn, h, "true")
		if err != nil {
			return failStatus(h, errSummary(err))
		}
		if exit != 0 {
			return failStatus(h, fmt.Sprintf("exit code %d", exit))
		}
		return passStatus(h, "")
	})}
}

func checkSudo(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	if draft.SSH.User == "root" {
		return overall(domain.PreflightSkipped, "Connecting as root — sudo not required.")
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		_, _, exit, err := runHostCommand(ctx, conn, h, "sudo -n true")
		if err != nil {
			return failStatus(h, errSummary(err))
		}
		if exit != 0 {
			return failStatus(h, "passwordless sudo not available")
		}
		return passStatus(h, "")
	})}
}

func checkPkgMgr(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		for _, mgr := range []string{"dnf", "yum", "apt-get"} {
			_, _, exit, err := runHostCommand(ctx, conn, h, "command -v "+mgr)
			if err == nil && exit == 0 {
				return passStatus(h, mgr+" detected")
			}
		}
		return failStatus(h, "none of dnf, yum, apt-get found in PATH")
	})}
}

func checkFreeSpace(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	mounts := draft.Topology.SelectedMounts
	if len(mounts) == 0 {
		return overall(domain.PreflightFail, "No drives selected on the Topology step.")
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		for _, m := range mounts {
			cmd := fmt.Sprintf("df -B1 --output=avail %q", m)
			stdout, _, exit, err := runHostCommand(ctx, conn, h, cmd)
			if err != nil || exit != 0 {
				return failStatus(h, fmt.Sprintf("%s: %s", m, errSummary(err)))
			}
			lines := strings.Fields(stdout)
			if len(lines) < 2 {
				return failStatus(h, fmt.Sprintf("%s: unexpected df output", m))
			}
			avail, err := strconv.ParseInt(strings.TrimSpace(lines[len(lines)-1]), 10, 64)
			if err != nil {
				return failStatus(h, fmt.Sprintf("%s: parse df: %v", m, err))
			}
			// This is a simple sanity check, not a MinIO requirement. Warn when
			// free space is low, but only hard-fail once it is nearly exhausted.
			if avail <= freeSpaceFailBytes {
				return failStatus(h, fmt.Sprintf("%s: %d bytes free (<= 25 MiB)", m, avail))
			}
			if avail < freeSpaceWarnBytes {
				return warnStatus(h, fmt.Sprintf("%s: %d bytes free (< 1 GiB)", m, avail))
			}
		}
		return passStatus(h, "")
	})}
}

func checkStaleFormat(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	mounts := draft.Topology.SelectedMounts
	if len(mounts) == 0 {
		return overall(domain.PreflightSkipped, "")
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		var stale []string
		for _, m := range mounts {
			cmd := fmt.Sprintf("test -f %q/.minio.sys/format.json", storagePathForMount(m))
			_, _, exit, err := runHostCommand(ctx, conn, h, cmd)
			if err == nil && exit == 0 {
				stale = append(stale, m)
			}
		}
		if len(stale) > 0 {
			return failStatus(h, "stale format on "+formatList(stale))
		}
		return passStatus(h, "")
	})}
}

func checkDNS(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	hosts := applicableHosts(draft)
	peerNames := make([]string, len(hosts))
	for i, h := range hosts {
		peerNames[i] = h.Hostname
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		var failed []string
		for _, peer := range peerNames {
			if peer == h.Hostname {
				continue
			}
			_, _, exit, err := runHostCommand(ctx, conn, h, "getent hosts "+peer)
			if err != nil || exit != 0 {
				failed = append(failed, peer)
			}
		}
		if len(failed) > 0 {
			return failStatus(h, "cannot resolve "+formatList(failed))
		}
		return passStatus(h, "")
	})}
}

func checkPortsReachable(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	hosts := applicableHosts(draft)
	if len(hosts) < 2 {
		return overall(domain.PreflightSkipped, "Single-host deployment.")
	}
	apiPort := draft.API.Port
	consolePort := draft.API.ConsolePort
	if apiPort == 0 {
		apiPort = 9000
	}
	if consolePort == 0 {
		consolePort = 9001
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		var blocked []string
		for _, peer := range hosts {
			if peer.Hostname == h.Hostname {
				continue
			}
			for _, port := range []int{apiPort, consolePort} {
				cmd := fmt.Sprintf("timeout 2 bash -c '</dev/tcp/%s/%d'", peer.Hostname, port)
				_, _, exit, err := runHostCommand(ctx, conn, h, cmd)
				if err != nil || exit != 0 {
					blocked = append(blocked, fmt.Sprintf("%s:%d", peer.Hostname, port))
				}
			}
		}
		if len(blocked) > 0 {
			return failStatus(h, "cannot reach "+formatList(blocked))
		}
		return passStatus(h, "")
	})}
}

func checkPortsConflict(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	apiPort := draft.API.Port
	consolePort := draft.API.ConsolePort
	if apiPort == 0 {
		apiPort = 9000
	}
	if consolePort == 0 {
		consolePort = 9001
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		conflicts, detail, err := detectListeningConflicts(ctx, conn, h, apiPort, consolePort)
		if err != nil {
			return failStatus(h, errSummary(err))
		}
		if conflicts > 0 {
			if detail != "" {
				return failStatus(h, fmt.Sprintf("%d listener(s) already on %d/%d (%s)", conflicts, apiPort, consolePort, detail))
			}
			return failStatus(h, fmt.Sprintf("%d listener(s) already on %d/%d", conflicts, apiPort, consolePort))
		}
		return passStatus(h, "")
	})}
}

func checkTimeSync(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		hostTime, err := readHostUnixTime(ctx, conn, h)
		if err != nil {
			return warnStatus(h, "could not read host time")
		}
		// nowFn is overridable in tests via injectClock.
		drift := hostTime - nowFn()
		if drift < 0 {
			drift = -drift
		}
		if drift > 1.0 {
			return warnStatus(h, fmt.Sprintf("clock skew %.2fs vs manager", drift))
		}
		return passStatus(h, "")
	})}
}

func readHostUnixTime(ctx context.Context, conn HostConn, h domain.HostRow) (float64, error) {
	commands := []string{
		"date +%s.%N",
		"python3 -c 'import time; print(time.time())'",
		"python -c 'import time; print(time.time())'",
	}
	for attempt := 0; attempt < 2; attempt++ {
		for _, cmd := range commands {
			stdout, _, exit, err := conn.Run(ctx, h, cmd)
			if err != nil || exit != 0 {
				continue
			}
			hostTime, err := strconv.ParseFloat(strings.TrimSpace(stdout), 64)
			if err == nil {
				return hostTime, nil
			}
		}
	}
	return 0, fmt.Errorf("could not read host time")
}

func runHostCommand(ctx context.Context, conn HostConn, h domain.HostRow, cmd string) (string, string, int, error) {
	var lastStdout, lastStderr string
	var lastExit int
	var lastErr error
	for attempt := 0; attempt < hostRunAttempts; attempt++ {
		lastStdout, lastStderr, lastExit, lastErr = conn.Run(ctx, h, cmd)
		if lastErr == nil {
			return lastStdout, lastStderr, lastExit, nil
		}
	}
	return lastStdout, lastStderr, lastExit, lastErr
}

func detectListeningConflicts(ctx context.Context, conn HostConn, h domain.HostRow, apiPort, consolePort int) (int, string, error) {
	checks := []struct {
		detail string
		cmd    string
		parse  func(string) (int, error)
	}{
		{
			detail: "ss",
			cmd:    fmt.Sprintf("ss -ltn 'sport = :%d or sport = :%d'", apiPort, consolePort),
			parse:  parseTableListenerCount,
		},
		{
			detail: "netstat",
			cmd:    fmt.Sprintf("netstat -ltn 2>/dev/null | awk 'NR > 2 && ($4 ~ /[:.]%d$/ || $4 ~ /[:.]%d$/) {c++} END {print c+0}'", apiPort, consolePort),
			parse:  parseCount,
		},
		{
			detail: "lsof",
			cmd:    fmt.Sprintf("lsof -nP -iTCP:%d -iTCP:%d -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 {c++} END {print c+0}'", apiPort, consolePort),
			parse:  parseCount,
		},
		{
			detail: "/proc/net",
			cmd:    fmt.Sprintf("for f in /proc/net/tcp /proc/net/tcp6; do [ -r \"$f\" ] || continue; awk 'NR > 1 { split($2,a,\":\"); if ($4 == \"0A\" && (toupper(a[2]) == \"%04X\" || toupper(a[2]) == \"%04X\")) c++ } END { print c+0 }' \"$f\"; done | awk '{s+=$1} END {print s+0}'", apiPort, consolePort),
			parse:  parseCount,
		},
	}
	var lastErr error
	for _, check := range checks {
		stdout, _, exit, err := runHostCommand(ctx, conn, h, check.cmd)
		if err != nil {
			lastErr = err
			continue
		}
		if exit != 0 {
			lastErr = fmt.Errorf("%s exit %d", check.detail, exit)
			continue
		}
		count, err := check.parse(stdout)
		if err != nil {
			lastErr = err
			continue
		}
		return count, check.detail, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no listener inspection tool available")
	}
	return 0, "", lastErr
}

func storagePathForMount(mount string) string {
	return path.Join(mount, "buckit")
}

func parseTableListenerCount(stdout string) (int, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	count := 0
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		count++
	}
	return count, nil
}

func parseCount(stdout string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(stdout))
}

// nowFn lets tests inject a deterministic clock. Production reads real wall time.
var nowFn = func() float64 {
	return float64(unixNanoFunc()) / 1e9
}

// unixNanoFunc is split out so the test file can override it.
var unixNanoFunc = unixNanoNow

func checkExistingBuckit(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		_, _, exit, err := conn.Run(ctx, h, "rpm -q buckit")
		if err == nil && exit == 0 {
			return warnStatus(h, "buckit already installed (will be replaced)")
		}
		_, _, exit, err = conn.Run(ctx, h, "dpkg -s buckit")
		if err == nil && exit == 0 {
			return warnStatus(h, "buckit already installed (will be replaced)")
		}
		return passStatus(h, "")
	})}
}

func checkExistingService(ctx context.Context, _ HostConn, draft domain.NewClusterDraft) Outcome {
	return Outcome{HostStatuses: perHost(ctx, draft, func(_ context.Context, h domain.HostRow) domain.PreflightHostStatus {
		discovery, ok := draft.Discovery[h.ID]
		if !ok || discovery.ExistingService == "" {
			return passStatus(h, "")
		}
		return warnStatus(h, discovery.ExistingService+" service already present")
	})}
}

func checkXFS(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	mounts := draft.Topology.SelectedMounts
	if len(mounts) == 0 {
		return overall(domain.PreflightSkipped, "")
	}
	mountSet := map[string]bool{}
	for _, m := range mounts {
		mountSet[m] = true
	}
	hosts := applicableHosts(draft)
	statuses := make([]domain.PreflightHostStatus, 0, len(hosts))
	for _, h := range hosts {
		discovery, ok := draft.Discovery[h.ID]
		if !ok || len(discovery.Drives) == 0 {
			statuses = append(statuses, passStatus(h, ""))
			continue
		}
		var nonXFS []string
		for _, d := range discovery.Drives {
			if d.IsBoot || !mountSet[d.Mount] {
				continue
			}
			if d.FsType != "" && strings.ToLower(d.FsType) != "xfs" {
				nonXFS = append(nonXFS, fmt.Sprintf("%s (%s)", d.Mount, d.FsType))
			}
		}
		if len(nonXFS) > 0 {
			statuses = append(statuses, warnStatus(h, "non-XFS: "+formatList(nonXFS)))
		} else {
			statuses = append(statuses, passStatus(h, ""))
		}
	}
	return Outcome{HostStatuses: statuses}
}

// ---- Overall (non-host-scoped) checks ----

func checkArchUniform(_ context.Context, _ HostConn, draft domain.NewClusterDraft) Outcome {
	seen := map[string]bool{}
	for _, r := range draft.Discovery {
		if r.Arch != "" {
			seen[r.Arch] = true
		}
	}
	if len(seen) == 0 {
		return overall(domain.PreflightFail, "No architecture reported yet — re-run discovery.")
	}
	if len(seen) == 1 {
		var arch string
		for k := range seen {
			arch = k
		}
		return overall(domain.PreflightPass, "All hosts on "+arch+".")
	}
	list := make([]string, 0, len(seen))
	for k := range seen {
		list = append(list, k)
	}
	return overall(domain.PreflightFail, "Mixed architectures: "+formatList(list)+". MinIO binaries are per-arch.")
}

func checkOSUniform(_ context.Context, _ HostConn, draft domain.NewClusterDraft) Outcome {
	seen := map[string]bool{}
	for _, r := range draft.Discovery {
		if r.OS != "" {
			seen[r.OS] = true
		}
	}
	if len(seen) <= 1 {
		var detail string
		for k := range seen {
			detail = "All hosts on " + k + "."
		}
		return overall(domain.PreflightPass, detail)
	}
	list := make([]string, 0, len(seen))
	for k := range seen {
		list = append(list, k)
	}
	return overall(domain.PreflightWarn, "Mixed: "+formatList(list)+". Operational tuning may differ.")
}

func checkHostnamePattern(_ context.Context, _ HostConn, draft domain.NewClusterDraft) Outcome {
	hosts := applicableHosts(draft)
	if len(hosts) < 2 {
		return overall(domain.PreflightPass, "Single-host deployment.")
	}
	names := make([]string, len(hosts))
	for i, h := range hosts {
		names[i] = h.Hostname
	}
	pattern, ok := detectBraceExpansion(names)
	if !ok {
		return overall(domain.PreflightFail, "Hostnames do not fit a single brace-expansion pattern.")
	}
	return overall(domain.PreflightPass, "Pattern: "+pattern)
}

func checkDriveUniformity(_ context.Context, _ HostConn, draft domain.NewClusterDraft) Outcome {
	mounts := draft.Topology.SelectedMounts
	if len(mounts) == 0 {
		return overall(domain.PreflightFail, "No drives selected on the Topology step.")
	}
	mountSet := map[string]bool{}
	for _, m := range mounts {
		mountSet[m] = true
	}
	hosts := applicableHosts(draft)
	for _, h := range hosts {
		discovery, ok := draft.Discovery[h.ID]
		if !ok {
			continue
		}
		hostMounts := map[string]bool{}
		for _, d := range discovery.Drives {
			if !d.IsBoot && d.Mount != "" {
				hostMounts[d.Mount] = true
			}
		}
		for _, m := range mounts {
			if !hostMounts[m] {
				return overall(domain.PreflightFail, fmt.Sprintf("%s missing on %s", m, h.Hostname))
			}
		}
	}
	return overall(domain.PreflightPass, fmt.Sprintf("%d mountpoint(s) uniform across hosts.", len(mounts)))
}

// ---- Artifact reachability ----

func checkArtifactBM(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	rpmURL := resolveArtifactURL(draft)
	if rpmURL == "" {
		return overall(domain.PreflightFail, "Could not resolve artifact URL. "+artifactURLContext(draft))
	}
	status, size, err := conn.HEAD(ctx, rpmURL)
	if err != nil {
		return overall(domain.PreflightFail, fmt.Sprintf("HEAD %s: %s", rpmURL, errSummary(err)))
	}
	if status >= 400 {
		return overall(domain.PreflightFail, fmt.Sprintf("HEAD %s: HTTP %d", rpmURL, status))
	}
	if size > 0 {
		return overall(domain.PreflightPass, fmt.Sprintf("%s — %d bytes", rpmURL, size))
	}
	return overall(domain.PreflightPass, rpmURL)
}

func checkArtifactPerHost(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome {
	rpmURL := resolveArtifactURL(draft)
	if rpmURL == "" {
		return overall(domain.PreflightFail, "Could not resolve artifact URL. "+artifactURLContext(draft))
	}
	return Outcome{HostStatuses: perHost(ctx, draft, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		cmd := fmt.Sprintf("curl -fI --max-time 10 %s", shellEscape(rpmURL))
		_, _, exit, err := conn.Run(ctx, h, cmd)
		if err != nil {
			return failStatus(h, fmt.Sprintf("%s (%s)", errSummary(err), rpmURL))
		}
		if exit != 0 {
			return failStatus(h, fmt.Sprintf("curl exit %d for %s (check egress / proxy)", exit, rpmURL))
		}
		return passStatus(h, "reachable: "+rpmURL)
	})}
}

// resolveArtifactURL looks up the draft's chosen version against the
// supported-versions catalog OR uses CustomURL when version == "custom".
// Returns "" when neither resolves.
func resolveArtifactURL(draft domain.NewClusterDraft) string {
	if draft.Version == "custom" {
		return strings.TrimSpace(draft.CustomURL)
	}
	// Avoid importing internal/deploy to dodge a circular dep; resolution is
	// supplied by the caller via the HostConn impl in production, but for
	// preflight we just need *some* URL string. Defer to a callback when set.
	if resolver != nil {
		return resolver(draft.Version)
	}
	return ""
}

// resolver is wired by `internal/api` (or tests) to break the package cycle
// between preflight and deploy. nil-safe.
var resolver func(tag string) string

func artifactURLContext(draft domain.NewClusterDraft) string {
	if draft.Version == "custom" {
		customURL := strings.TrimSpace(draft.CustomURL)
		if customURL == "" {
			return "Selected version: custom. Custom URL is empty."
		}
		return "Selected version: custom. Custom URL: " + customURL
	}
	return "Selected version: " + draft.Version + "."
}

// SetVersionResolver injects a tag->URL lookup. Production wires this to
// deploy.VersionByTag at startup; tests can substitute a deterministic map.
func SetVersionResolver(fn func(tag string) string) { resolver = fn }

// ResolveURL is the exported version-resolver lookup, used by other packages
// (migration preflight) that need to recover an artifact URL from a tag.
func ResolveURL(tag string) string {
	if resolver == nil {
		return ""
	}
	return resolver(tag)
}

func shellEscape(s string) string {
	// Conservative: single-quote and escape embedded single quotes.
	if !strings.ContainsAny(s, " \t\"'$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// detectBraceExpansion is a tiny check for whether hostnames fit one
// `prefix{N..M}suffix` pattern. Returns the pattern + true on success.
func detectBraceExpansion(names []string) (string, bool) {
	if len(names) < 2 {
		return names[0], true
	}
	// Try to find a common prefix and suffix, then ensure the middles are
	// consecutive integers.
	prefix := names[0]
	for _, n := range names[1:] {
		prefix = commonPrefix(prefix, n)
	}
	suffix := names[0]
	for _, n := range names[1:] {
		suffix = commonSuffix(suffix, n)
	}
	if len(prefix)+len(suffix) >= len(names[0]) {
		return "", false
	}
	nums := make([]int, len(names))
	for i, n := range names {
		mid := n[len(prefix) : len(n)-len(suffix)]
		v, err := strconv.Atoi(mid)
		if err != nil {
			return "", false
		}
		nums[i] = v
	}
	min, max := nums[0], nums[0]
	for _, v := range nums {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	// Allow gaps but ensure all integers in [min,max] are present.
	seen := map[int]bool{}
	for _, v := range nums {
		seen[v] = true
	}
	for i := min; i <= max; i++ {
		if !seen[i] {
			return "", false
		}
	}
	return fmt.Sprintf("%s{%d..%d}%s", prefix, min, max, suffix), true
}

func commonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[:i]
}

func commonSuffix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return a[len(a)-i:]
}

// Keep the URL package imported even if unused later — net/url adds robust
// parsing if we want to validate the artifact URL more strictly.
var _ = url.Parse
