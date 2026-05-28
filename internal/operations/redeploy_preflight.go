package operations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/buckit-io/pkg/v3/ellipses"

	"github.com/buckit-io/bm/internal/domain"
)

// buckitDropInDir is the canonical operator-owned drop-in directory
// for buckit.service. Package installs don't create or touch this
// path; anything inside it was put there by the operator (or a
// previous bm run that left state behind).
const buckitDropInDir = "/etc/systemd/system/buckit.service.d"

// preflightHostIsClean rejects a redeploy target that already carries
// state the redeploy would clobber. Two semantics:
//
//   - "must be absent": files the bootstrap will write byte-for-byte
//     onto the target — the env files. Any existing content is a
//     conflict.
//   - "must be absent or empty": directories the bootstrap will create
//     or populate without first wiping — the certs dir, the systemd
//     drop-in dir, and every local data path parsed from the peer's
//     MINIO_VOLUMES. An empty (or missing) directory is fine; existing
//     files inside it suggest leftover state from a previous deploy.
//
// All path probes for a target are batched into one ssh exec to keep
// the preflight cheap.
func preflightHostIsClean(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) error {
	envFiles, _ := probeBuckitEnvironmentFiles(ctx, deps, rc, target)
	if len(envFiles) == 0 {
		envFiles = probeEnvironmentFilesFromAnyPeer(ctx, deps, rc, target)
	}
	if len(envFiles) == 0 {
		return fmt.Errorf(
			"%s and its peers have no buckit.service unit loaded — for a fresh single-node cluster use the new-cluster wizard, not redeploy",
			target.Hostname,
		)
	}
	primary := envFiles[0]

	// Pull the peer's env-file body to learn the secondary, certs dir,
	// and MINIO_VOLUMES. The check has to know what the bootstrap is
	// about to copy.
	peer, ok := pickConfigSourcePeer(ctx, deps, rc, target, primary)
	if !ok {
		// No peer has the primary file. Bootstrap would also fail later
		// — surface the clean-host check now with the same hint.
		return fmt.Errorf(
			"%s: no peer in this cluster has %s; cannot verify clean state — for a fresh single-node cluster use the new-cluster wizard, not redeploy",
			target.Hostname, primary,
		)
	}
	body, err := readRemoteFileText(ctx, deps, rc, peer, primary)
	if err != nil {
		return fmt.Errorf("%s preflight: read %s from %s: %w", target.Hostname, primary, peer.Hostname, err)
	}

	// must-be-absent: env files
	strictPaths := []string{primary}
	if secondary := extractEnvVar(body, "MINIO_CONFIG_ENV_FILE"); secondary != "" {
		strictPaths = append(strictPaths, secondary)
	}
	// must-be-absent-or-empty: certs dir, systemd drop-in dir, data drives
	certsPath := extractCertsDirFromOpts(body)
	volumePaths := localVolumePathsFromEnv(body)

	var allPaths []string
	allPaths = append(allPaths, strictPaths...)
	if certsPath != "" {
		allPaths = append(allPaths, certsPath)
	}
	allPaths = append(allPaths, buckitDropInDir)
	allPaths = append(allPaths, volumePaths...)

	statuses, err := probePathsState(ctx, deps, rc, target, allPaths)
	if err != nil {
		return fmt.Errorf("%s preflight: %w", target.Hostname, err)
	}

	var problems []string
	for _, p := range strictPaths {
		if st, ok := statuses[p]; ok && st.kind != pathAbsent {
			problems = append(problems, fmt.Sprintf("config %s: %s", p, st.detail))
		}
	}
	if certsPath != "" {
		if st, ok := statuses[certsPath]; ok && st.kind == pathNonEmpty {
			problems = append(problems, fmt.Sprintf("certs %s: %s", certsPath, st.detail))
		}
	}
	if st, ok := statuses[buckitDropInDir]; ok && st.kind == pathNonEmpty {
		problems = append(problems, fmt.Sprintf("drop-in dir %s: %s", buckitDropInDir, st.detail))
	}
	for _, p := range volumePaths {
		if st, ok := statuses[p]; ok && st.kind == pathNonEmpty {
			problems = append(problems, fmt.Sprintf("data path %s: %s", p, st.detail))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf(
		"%s is not a clean host:\n  - %s\nremove or back up these paths before retrying redeploy",
		target.Hostname, strings.Join(problems, "\n  - "),
	)
}

// localVolumePathsFromEnv returns the set of LOCAL filesystem paths the
// cluster's MINIO_VOLUMES expects on each node. Handles three shapes:
//
//   - bare paths separated by spaces:        "/mnt/data1 /mnt/data2"
//   - http(s) URLs with ellipses-expansion:  "https://h{1...4}:9000/mnt/data{1...4}/buckit"
//   - mixed combinations of the above
//
// URL host:port is dropped (we only care what's on the local disk).
// Ellipses expansion (including hex and zero-padded ranges) is
// delegated to github.com/buckit-io/pkg/v3/ellipses, the same library
// the Buckit server itself uses to parse MINIO_VOLUMES — so the edge
// cases this preflight covers stay in lockstep with the runtime.
// Returns the deduped path list sorted for stable output.
func localVolumePathsFromEnv(body string) []string {
	raw := extractEnvVar(body, "MINIO_VOLUMES")
	if raw == "" {
		return nil
	}
	seen := map[string]struct{}{}
	for _, tok := range strings.Fields(raw) {
		path := tok
		if i := strings.Index(path, "://"); i >= 0 {
			rest := path[i+3:]
			if slash := strings.IndexByte(rest, '/'); slash >= 0 {
				path = rest[slash:]
			} else {
				continue
			}
		}
		for _, expanded := range expandVolumeArg(path) {
			expanded = strings.TrimSpace(expanded)
			if expanded == "" {
				continue
			}
			seen[expanded] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// expandVolumeArg expands ellipses syntax in a single MINIO_VOLUMES
// argument (already stripped of any URL scheme + host). The Buckit
// server uses ellipses.FindEllipsesPatterns for the same input, so
// deferring to it keeps the preflight aligned with what the runtime
// actually accepts. A literal arg (no ellipses) round-trips through as
// a one-element slice; a malformed pattern is treated as literal too —
// the preflight should still probe the path verbatim rather than
// silently drop it.
func expandVolumeArg(arg string) []string {
	if !ellipses.HasEllipses(arg) {
		return []string{arg}
	}
	patterns, err := ellipses.FindEllipsesPatterns(arg)
	if err != nil {
		return []string{arg}
	}
	combinations := patterns.Expand()
	out := make([]string, 0, len(combinations))
	for _, lbls := range combinations {
		out = append(out, strings.Join(lbls, ""))
	}
	return out
}

// pathStateKind classifies what probePathsState found at a remote path.
type pathStateKind int

const (
	pathAbsent pathStateKind = iota
	pathEmpty
	pathNonEmpty
)

type pathState struct {
	kind   pathStateKind
	detail string // first entry name when nonempty; "absent" / "empty" otherwise
}

// probePathsState runs a single bash exec on n that reports each
// requested path's state. One ssh round-trip per target host
// regardless of how many paths the caller wants checked.
func probePathsState(ctx context.Context, deps Deps, rc *runCtx, n domain.Node, paths []string) (map[string]pathState, error) {
	if len(paths) == 0 {
		return map[string]pathState{}, nil
	}
	var script strings.Builder
	script.WriteString("set -u\n")
	for _, p := range paths {
		fmt.Fprintf(&script, `printf '%%s\t' %s; `, shellQuote(p))
		fmt.Fprintf(&script,
			`if [ ! -e %s ]; then printf 'absent\n'; `+
				`elif [ -z "$(ls -A %s 2>/dev/null | head -n 1)" ]; then printf 'empty\n'; `+
				`else printf 'nonempty:%%s\n' "$(ls -A %s 2>/dev/null | head -n 1)"; fi`+"\n",
			shellQuote(p), shellQuote(p), shellQuote(p))
	}
	out, err := runHostStep(ctx, deps, rc, n, sudoBash(rc.sshCreds.User, script.String()))
	if err != nil {
		return nil, err
	}
	return parsePathStateOutput(out), nil
}

// parsePathStateOutput consumes the per-line `<path>\t<status>` output
// emitted by the script in probePathsState.
func parsePathStateOutput(out string) map[string]pathState {
	res := map[string]pathState{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		path := line[:tab]
		status := line[tab+1:]
		switch {
		case status == "absent":
			res[path] = pathState{kind: pathAbsent, detail: "absent"}
		case status == "empty":
			res[path] = pathState{kind: pathEmpty, detail: "empty"}
		case strings.HasPrefix(status, "nonempty:"):
			res[path] = pathState{kind: pathNonEmpty, detail: "contains " + strings.TrimPrefix(status, "nonempty:")}
		}
	}
	return res
}
