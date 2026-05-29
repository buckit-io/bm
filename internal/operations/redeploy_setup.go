package operations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/buckit-io/bm/internal/domain"
)

// Defaults for the buckit service identity. Matches the buckit package
// postinstall script (`../buckit/packaging/scripts/postinstall.sh`):
// `groupadd --system buckit && useradd --system --gid buckit ... buckit`.
// Used as a fallback when the peer's unit declares neither User= nor
// Group= — systemd's documented default would be root, but in practice
// every Buckit cluster runs as a dedicated system user, so falling back
// to `buckit` (rather than root) preserves least-privilege.
const (
	defaultBuckitUser  = "buckit"
	defaultBuckitGroup = "buckit"
)

// resolveServiceIdentity returns the (user, group) the cluster's
// buckit.service runs as, discovered from a peer's systemd. Falls back
// to defaults when the peer hasn't declared either (raw systemd default
// is root, but we choose `buckit` to match the package postinstall).
func resolveServiceIdentity(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) (user, group string) {
	props := probeUnitPropsFromAnyPeer(ctx, deps, rc, target)
	user = strings.TrimSpace(props.User)
	group = strings.TrimSpace(props.Group)
	if user == "" {
		user = defaultBuckitUser
	}
	if group == "" {
		group = defaultBuckitGroup
	}
	return user, group
}

// ensureBuckitUserAndGroup creates the service user/group on the
// target if they're missing. Idempotent: re-running on a host that
// already has them is a no-op. Mirrors the buckit package's
// postinstall script — same flags, same comment — so that running
// this step BEFORE the package install gives the install nothing to
// do, and the package's own postinstall stays a no-op too.
//
// We do this explicitly (rather than relying on the package install)
// because peers that were migrated from MinIO use a non-default user
// (typically `minio-user`) wired in via the migration drop-in. The
// package install only knows how to create `buckit`; without this
// step, buckit.service would start as a user that doesn't exist on a
// fresh target.
func ensureBuckitUserAndGroup(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, user, group string) error {
	script := fmt.Sprintf(`set -e
if ! getent group %s >/dev/null 2>&1; then
    groupadd --system %s
fi
if ! getent passwd %s >/dev/null 2>&1; then
    useradd --system \
        --gid %s \
        --no-create-home \
        --home-dir /var/lib/%s \
        --shell /sbin/nologin \
        --comment "Buckit Object Storage" \
        %s
fi
`,
		shellQuote(group),
		shellQuote(group),
		shellQuote(user),
		shellQuote(group),
		shellQuote(user),
		shellQuote(user),
	)
	if _, err := runHostStep(ctx, deps, rc, target, sudoBash(rc.sshCreds.User, script)); err != nil {
		return fmt.Errorf("%s ensure %s:%s: %w", target.Hostname, user, group, err)
	}
	return nil
}

// preflightDataMountsAttached verifies that each data path lands on a
// real mount point on n. Catches the "intended drive not mounted"
// case where mkdir -p would otherwise create the data dir in the
// rootfs and buckit would silently write data to the system disk
// until it fills the root partition.
//
// "Lands on a real mount point" means EITHER the data path itself is
// a mount point (MINIO_VOLUMES="/mnt/data/drive0" with the drive
// mounted directly at /mnt/data/drive0) OR its parent is a mount
// point (MINIO_VOLUMES="/mnt/data1/buckit" with the drive mounted at
// /mnt/data1, and buckit lives in a subdir). Both layouts are common.
//
// Single-drive nodes that intentionally run on the root filesystem
// (e.g. MINIO_VOLUMES="/buckit") still pass because the parent is "/",
// which IS a mount point.
//
// One ssh round-trip per target, batched across all paths.
func preflightDataMountsAttached(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var script strings.Builder
	script.WriteString("set -u\n")
	for _, p := range paths {
		parent := pathDir(p)
		fmt.Fprintf(&script,
			`printf '%%s\t' %s; if mountpoint -q %s 2>/dev/null || mountpoint -q %s 2>/dev/null; then printf 'mounted\n'; else printf 'not-mounted:%%s\n' %s; fi`+"\n",
			shellQuote(p), shellQuote(p), shellQuote(parent), shellQuote(parent),
		)
	}
	out, err := runHostStep(ctx, deps, rc, target, sudoBash(rc.sshCreds.User, script.String()))
	if err != nil {
		return fmt.Errorf("%s mount check: %w", target.Hostname, err)
	}
	problems := parseMountCheckOutput(out)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s mount check failed:\n  - %s\nbring up the listed filesystems before retrying redeploy",
		target.Hostname,
		joinSorted(problems, "\n  - "),
	)
}

// parseMountCheckOutput consumes the `<path>\t<mounted|not-mounted:<parent>>`
// lines emitted by preflightDataMountsAttached's script and returns
// one human-readable problem string per non-mounted path. Exported
// separately for unit testing.
func parseMountCheckOutput(out string) []string {
	var problems []string
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
		if strings.HasPrefix(status, "not-mounted:") {
			parent := strings.TrimPrefix(status, "not-mounted:")
			problems = append(problems, fmt.Sprintf("%s: parent %s is not a mount point", path, parent))
		}
	}
	return problems
}

func joinSorted(items []string, sep string) string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	return strings.Join(cp, sep)
}

// prepareBuckitDataDirs creates the local MINIO_VOLUMES paths on the
// target with the right owner + mode. Mirrors deploy.prepareStorageCmd
// (mkdir -p + chown + chmod 755). The volume paths come from the
// peer's env file via localVolumePathsFromEnv, so they cover the same
// ellipses syntax MINIO_VOLUMES itself accepts (including the
// single-node bare-path form).
//
// Each command is independent; we run them under `set -e` so the
// first failure stops the rest, mirroring deploy's own ordering.
func prepareBuckitDataDirs(ctx context.Context, deps Deps, rc *runCtx, target domain.Node, paths []string, user, group string) error {
	if len(paths) == 0 {
		return nil
	}
	owner := shellQuote(user) + ":" + shellQuote(group)
	var script strings.Builder
	script.WriteString("set -e\n")
	for _, p := range paths {
		q := shellQuote(p)
		fmt.Fprintf(&script, "mkdir -p %s\nchown %s %s\nchmod 755 %s\n", q, owner, q, q)
	}
	if _, err := runHostStep(ctx, deps, rc, target, sudoBash(rc.sshCreds.User, script.String())); err != nil {
		return fmt.Errorf("%s prepare data dirs: %w", target.Hostname, err)
	}
	return nil
}

// dataPathsFromPeer returns the local MINIO_VOLUMES paths the target
// is expected to own. The full MINIO_VOLUMES string lists every pool;
// we filter it down to the paths the chosen peer ACTUALLY has on
// disk, so a pool-2 target doesn't end up with pool-1's mount paths
// created in its rootfs. pickConfigSourcePeer already prefers
// same-pool peers, so in the common case "paths that exist on peer"
// is exactly "paths that belong to target's pool".
//
// Returns an empty slice when no peer has the primary env file — the
// caller should treat that as "nothing to prepare" rather than an
// error (the bootstrap step will already have surfaced a clearer
// failure).
func dataPathsFromPeer(ctx context.Context, deps Deps, rc *runCtx, target domain.Node) ([]string, error) {
	envFiles, _ := probeBuckitEnvironmentFiles(ctx, deps, rc, target)
	if len(envFiles) == 0 {
		envFiles = probeEnvironmentFilesFromAnyPeer(ctx, deps, rc, target)
	}
	if len(envFiles) == 0 {
		return nil, nil
	}
	primary := envFiles[0]
	peer, ok := pickConfigSourcePeer(ctx, deps, rc, target, primary)
	if !ok {
		return nil, nil
	}
	if peer.Pool != target.Pool {
		// No same-pool peer has the env file — we can't tell which
		// MINIO_VOLUMES paths the target's pool actually uses, and
		// guessing from a cross-pool peer would create the wrong
		// mount paths in the target's rootfs. Surface this as an
		// error so the operator knows to use the (not-yet-shipped)
		// pool-add wizard rather than redeploy for this case.
		return nil, fmt.Errorf(
			"%s is in pool %d but no peer in that pool has %s — redeploy can't infer this pool's data layout from another pool",
			target.Hostname, target.Pool, primary,
		)
	}
	body, err := readRemoteFileText(ctx, deps, rc, peer, primary)
	if err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", primary, peer.Hostname, err)
	}
	candidates := localVolumePathsFromEnv(body)
	if len(candidates) == 0 {
		return nil, nil
	}
	statuses, err := probePathsState(ctx, deps, rc, peer, candidates)
	if err != nil {
		return nil, fmt.Errorf("probe data paths on %s: %w", peer.Hostname, err)
	}
	existing := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if st, ok := statuses[p]; ok && st.kind != pathAbsent {
			existing = append(existing, p)
		}
	}
	return existing, nil
}
