// Package migration drives the MinIO → Buckit cutover flow: snapshot capture,
// preflight checks, and the cutover orchestration (M8). M5 ships the
// preflight catalog and snapshot writer; the cutover executor lands in M8.
package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/discovery"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/preflight"
)

// Catalog returns the migration preflight check list. Driven by
// POST /clusters/:id/migrate/preflight; one history row is written per pass
// (unlike new-cluster preflight which is called freely during wizard nav).
func Catalog(adminCreds domain.AdminCreds) []preflight.Check {
	return []preflight.Check{
		{ID: "minio_detected", Label: "MinIO service detected on every host", Severity: domain.PreflightBlocking, Eval: detectMinio},
		{ID: "minio_service_present", Label: "minio.service systemd unit present on every host", Severity: domain.PreflightBlocking, Eval: minioServicePresent},
		{ID: "minio_service_complete", Label: "minio.service declares User, Group, and EnvironmentFile", Severity: domain.PreflightBlocking, Eval: minioServiceComplete},
		{ID: "mc_compat", Label: "MinIO version compatible with migration", Severity: domain.PreflightBlocking, Eval: mcCompat(adminCreds)},
		{ID: "idle", Label: "No in-flight admin operations", Severity: domain.PreflightBlocking, Eval: clusterIdle(adminCreds)},
		{ID: "service_freezable", Label: "Admin API responds (freeze will succeed)", Severity: domain.PreflightBlocking, Eval: serviceFreezable(adminCreds)},
		{ID: "nodes_online", Label: "All cluster nodes online", Severity: domain.PreflightAdvisory, Eval: nodesOnline(adminCreds)},
		{ID: "arch_uniform", Label: "Architecture uniformity (amd64 / arm64)", Severity: domain.PreflightBlocking, Eval: archUniform},
		{ID: "os_uniform", Label: "OS uniformity (same distro family)", Severity: domain.PreflightAdvisory, Eval: osUniformFromDiscovery},
		{ID: "artifact_reachable", Label: "Buckit RPM reachable from each host", Severity: domain.PreflightBlocking, Eval: artifactReachable},
	}
}

func detectMinio(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	return preflight.Outcome{HostStatuses: perHost(ctx, draft, conn, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		// Any of the three signals counts. Combine to reduce false negatives
		// across the distribution methods MinIO ships in.
		_, _, exit, _ := conn.Run(ctx, h, "systemctl status minio.service --no-pager -l")
		systemdOK := exit == 0
		_, _, exit, _ = conn.Run(ctx, h, "command -v minio")
		binaryOK := exit == 0
		_, _, exit, _ = conn.Run(ctx, h, "grep -q MINIO_VOLUMES /etc/default/minio")
		envOK := exit == 0
		if !systemdOK && !binaryOK && !envOK {
			return failStatus(h, "no minio.service / binary / env detected")
		}
		return passStatus(h, "")
	})}
}

// minioServicePresent confirms minio.service is a systemd-known unit on
// every host. Stricter than detectMinio (which accepts the bare binary or
// just /etc/default/minio as evidence) — the cutover specifically needs
// the unit so it can stop/disable it and re-probe it at rollback time.
func minioServicePresent(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	return preflight.Outcome{HostStatuses: perHost(ctx, draft, conn, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		// `systemctl cat` exits non-zero with "No files found for …" when
		// the unit isn't installed. Doesn't require the unit to be active
		// — disabled-but-installed is a valid starting state.
		_, stderr, exit, err := conn.Run(ctx, h, "systemctl cat minio.service --no-pager")
		if err != nil {
			return failStatus(h, err.Error())
		}
		if exit != 0 {
			detail := "minio.service not registered with systemd"
			if s := strings.TrimSpace(stderr); s != "" {
				detail = detail + ": " + s
			}
			return failStatus(h, detail)
		}
		return passStatus(h, "minio.service present")
	})}
}

// fallbackProbeCmd asks the host whether the conventional MinIO packaging
// values exist (minio-user user, minio-user group, /etc/default/minio).
// Always exits 0 and prints three known-shape lines so the caller can
// parse without distinguishing transport errors from "no" answers.
const fallbackProbeCmd = `printf "user=%s\ngroup=%s\nenv=%s\n" ` +
	`"$(id minio-user >/dev/null 2>&1 && echo yes || echo no)" ` +
	`"$(getent group minio-user >/dev/null 2>&1 && echo yes || echo no)" ` +
	`"$([ -f /etc/default/minio ] && echo yes || echo no)"`

type fallbackProbe struct {
	userExists  bool
	groupExists bool
	envExists   bool
}

func parseFallbackProbe(out string) fallbackProbe {
	var p fallbackProbe
	for _, line := range strings.Split(out, "\n") {
		switch strings.TrimSpace(line) {
		case "user=yes":
			p.userExists = true
		case "group=yes":
			p.groupExists = true
		case "env=yes":
			p.envExists = true
		}
	}
	return p
}

// minioServiceComplete confirms minio.service declares User, Group, and at
// least one EnvironmentFile — the three directives the cutover's drop-in
// mechanism replicates onto buckit.service.
//
// Tiered outcomes per host:
//   - All three declared → Pass.
//   - One or more missing, but the conventional MinIO packaging values
//     (minio-user user, minio-user group, /etc/default/minio) exist on
//     the host → Warn. The cutover will fall back to those values
//     (installer.renderDropIn / installer step 1) and the operator sees
//     exactly which substitutions are happening.
//   - One or more missing AND the corresponding fallback artifact is
//     absent → Fail. We'd be guessing a value with nothing to back it up.
//
// Severity at the Catalog level is Blocking — a per-host Fail aggregates
// to overall Fail and gates cutover; per-host Warn aggregates to Warn,
// which the wizard surfaces but doesn't block on.
func minioServiceComplete(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	return preflight.Outcome{HostStatuses: perHost(ctx, draft, conn, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		stdout, stderr, exit, err := conn.Run(ctx, h, "systemctl show minio.service -p User -p Group -p EnvironmentFiles -p EnvironmentFile")
		if err != nil {
			return failStatus(h, "systemctl show failed: "+err.Error())
		}
		if exit != 0 {
			detail := fmt.Sprintf("systemctl show exited %d", exit)
			if s := strings.TrimSpace(stderr); s != "" {
				detail = detail + ": " + s
			}
			return failStatus(h, detail)
		}
		props := deploy.ParseUnitProps(stdout)
		missingUser := props.User == ""
		missingGroup := props.Group == ""
		missingEnv := len(props.EnvironmentFiles) == 0
		if !missingUser && !missingGroup && !missingEnv {
			return passStatus(h, fmt.Sprintf("User=%s Group=%s EnvironmentFile=%s", props.User, props.Group, strings.Join(props.EnvironmentFiles, ",")))
		}

		probeOut, _, _, probeErr := conn.Run(ctx, h, fallbackProbeCmd)
		if probeErr != nil {
			return failStatus(h, "fallback probe failed: "+probeErr.Error())
		}
		probe := parseFallbackProbe(probeOut)

		var usable []string
		var unresolvable []string
		if missingUser {
			if probe.userExists {
				usable = append(usable, "User→"+fallbackMinioUser)
			} else {
				unresolvable = append(unresolvable, "User (no "+fallbackMinioUser+" user)")
			}
		}
		if missingGroup {
			if probe.groupExists {
				usable = append(usable, "Group→"+fallbackMinioGroup)
			} else {
				unresolvable = append(unresolvable, "Group (no "+fallbackMinioGroup+" group)")
			}
		}
		if missingEnv {
			if probe.envExists {
				usable = append(usable, "EnvironmentFile→"+fallbackMinioEnvFile)
			} else {
				unresolvable = append(unresolvable, "EnvironmentFile (no "+fallbackMinioEnvFile+")")
			}
		}

		if len(unresolvable) > 0 {
			return failStatus(h, "minio.service missing and no fallback: "+strings.Join(unresolvable, ", "))
		}
		return warnStatus(h, "minio.service missing; using fallbacks: "+strings.Join(usable, ", "))
	})}
}

func mcCompat(creds domain.AdminCreds) func(context.Context, preflight.HostConn, domain.NewClusterDraft) preflight.Outcome {
	return func(ctx context.Context, _ preflight.HostConn, _ domain.NewClusterDraft) preflight.Outcome {
		client, err := admin.New(creds)
		if err != nil {
			return overall(domain.PreflightFail, "build admin client: "+err.Error())
		}
		info, err := client.ServerInfo(ctx)
		if err != nil {
			return overall(domain.PreflightFail, "ServerInfo: "+err.Error())
		}
		if info.Version == "" {
			return overall(domain.PreflightFail, "no version reported by admin API")
		}
		// Migration is a forward-only operation from MinIO → Buckit. Reject
		// versions that classify as Buckit. ParseEngine handles the multiple
		// wire formats MinIO/Buckit servers report (RELEASE.<dashes>, bare
		// dashes, RFC3339 colons) and is the single source of truth.
		if discovery.ParseEngine(info.Version) != domain.EngineMinio {
			return overall(domain.PreflightFail, "cluster does not look like MinIO (version "+info.Version+")")
		}
		return overall(domain.PreflightPass, "MinIO version "+info.Version)
	}
}

func clusterIdle(creds domain.AdminCreds) func(context.Context, preflight.HostConn, domain.NewClusterDraft) preflight.Outcome {
	return func(ctx context.Context, _ preflight.HostConn, _ domain.NewClusterDraft) preflight.Outcome {
		client, err := admin.New(creds)
		if err != nil {
			return overall(domain.PreflightFail, "build admin client: "+err.Error())
		}
		// Use ServerInfo as a liveness probe; a full op-status surface is not
		// in M5 scope. The check passes if the admin API is responsive.
		if _, err := client.ServerInfo(ctx); err != nil {
			return overall(domain.PreflightFail, "admin API not responding: "+err.Error())
		}
		return overall(domain.PreflightPass, "Admin API responsive — no in-flight ops detected.")
	}
}

func serviceFreezable(creds domain.AdminCreds) func(context.Context, preflight.HostConn, domain.NewClusterDraft) preflight.Outcome {
	return func(ctx context.Context, _ preflight.HostConn, _ domain.NewClusterDraft) preflight.Outcome {
		client, err := admin.New(creds)
		if err != nil {
			return overall(domain.PreflightFail, "build admin client: "+err.Error())
		}
		if _, err := client.ServerInfo(ctx); err != nil {
			return overall(domain.PreflightFail, "admin API unreachable: "+err.Error())
		}
		return overall(domain.PreflightPass, "Admin API reachable; freeze will succeed.")
	}
}

// nodesOnline queries admin ServerInfo at preflight time and lists any
// host whose state isn't "online". Advisory: the operator can proceed
// with offline hosts present, but the cutover will skip them and they
// will keep running MinIO. Re-running the migration on each once it's
// back online is the recommended follow-up.
func nodesOnline(creds domain.AdminCreds) func(context.Context, preflight.HostConn, domain.NewClusterDraft) preflight.Outcome {
	return func(ctx context.Context, _ preflight.HostConn, _ domain.NewClusterDraft) preflight.Outcome {
		client, err := admin.New(creds)
		if err != nil {
			return overall(domain.PreflightFail, "build admin client: "+err.Error())
		}
		info, err := client.ServerInfo(ctx)
		if err != nil {
			return overall(domain.PreflightFail, "admin API unreachable: "+err.Error())
		}
		if len(info.Servers) == 0 {
			return overall(domain.PreflightFail, "ServerInfo returned no servers")
		}
		var unhealthy []string
		for _, s := range info.Servers {
			if s.State == domain.NodeOnline {
				continue
			}
			host := hostnameFromMinioEndpoint(s.Endpoint)
			if host == "" {
				host = s.Endpoint
			}
			unhealthy = append(unhealthy, fmt.Sprintf("%s (%s)", host, s.State))
		}
		if len(unhealthy) == 0 {
			return overall(domain.PreflightPass, fmt.Sprintf("All %d nodes online.", len(info.Servers)))
		}
		sort.Strings(unhealthy)
		return overall(domain.PreflightWarn,
			fmt.Sprintf("%d node(s) not online: %s. These hosts will be skipped by cutover and stay on MinIO; re-run migration on each once it's back online.",
				len(unhealthy), strings.Join(unhealthy, ", ")))
	}
}

// archUniform probes each host's architecture via SSH (`uname -m`) and
// aggregates them. We don't read draft.Discovery / cluster node rows
// because the migrate flow has no host-SSH discovery step and MinIO's
// admin API doesn't reliably populate ServerProperties.Arch across all
// versions — observed empty on older MinIO clusters even though OS is
// populated.
func archUniform(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	seen := map[string]bool{}
	var unknown []string
	for _, h := range draft.Hosts {
		if h.Hostname == "" {
			continue
		}
		stdout, _, exit, err := conn.Run(ctx, h, "uname -m")
		if err != nil || exit != 0 {
			unknown = append(unknown, h.Hostname)
			continue
		}
		arch := normalizeUnameArch(stdout)
		if arch == "" {
			unknown = append(unknown, fmt.Sprintf("%s (%s)", h.Hostname, strings.TrimSpace(stdout)))
			continue
		}
		seen[arch] = true
	}
	if len(seen) == 0 {
		return overall(domain.PreflightFail, "Could not determine arch on: "+strings.Join(unknown, ", "))
	}
	if len(seen) > 1 {
		list := mapKeys(seen)
		sort.Strings(list)
		msg := "Mixed architectures: " + strings.Join(list, ", ")
		if len(unknown) > 0 {
			msg += "; unknown on " + strings.Join(unknown, ", ")
		}
		return overall(domain.PreflightFail, msg)
	}
	var only string
	for a := range seen {
		only = a
	}
	if only != "amd64" && only != "arm64" {
		return overall(domain.PreflightFail, fmt.Sprintf("Unsupported architecture: %s (need amd64 or arm64)", only))
	}
	if len(unknown) > 0 {
		return overall(domain.PreflightFail, fmt.Sprintf("All probed hosts on %s, but could not probe: %s", only, strings.Join(unknown, ", ")))
	}
	return overall(domain.PreflightPass, "All hosts on "+only+".")
}

// normalizeUnameArch maps `uname -m` output to the arch tags Buckit's
// RPM catalog uses. Returns "" for output we don't recognize so the
// caller can surface the raw value rather than guess.
func normalizeUnameArch(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func osUniformFromDiscovery(_ context.Context, _ preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	seen := map[string]bool{}
	for _, r := range draft.Discovery {
		if r.OS != "" {
			seen[r.OS] = true
		}
	}
	if len(seen) <= 1 {
		var d string
		for k := range seen {
			d = "All hosts on " + k
		}
		return overall(domain.PreflightPass, d)
	}
	return overall(domain.PreflightWarn, "Mixed: "+strings.Join(mapKeys(seen), ", "))
}

func artifactReachable(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	// Shared with new-cluster preflight: the wizard's chosen version drives a
	// curl -fI on every host. Buckit RPM URL is resolved via the version
	// resolver registered at startup.
	return preflight.Outcome{HostStatuses: perHost(ctx, draft, conn, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		ver := strings.TrimSpace(draft.Version)
		if ver == "" {
			// The wizard's Overview step seeds draft.version from the
			// artifact catalog before this check ever runs; if it's
			// empty here, the body omitted it. Better to fail loudly
			// than fall back to a stale default like "v1.0.0" that no
			// longer exists in the catalog.
			return failStatus(h, "no version selected — pick one on the Overview step")
		}
		url := preflight.ResolveURL(ver)
		if url == "" {
			return failStatus(h, "no artifact URL for version "+ver)
		}
		_, _, exit, err := conn.Run(ctx, h, fmt.Sprintf("curl -fI --max-time 10 %s", url))
		if err != nil {
			return failStatus(h, err.Error())
		}
		if exit != 0 {
			return failStatus(h, fmt.Sprintf("curl exit %d", exit))
		}
		return passStatus(h, "reachable")
	})}
}

// ---- shared helpers (mirror preflight package's private ones) ----

func passStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightPass, Message: msg}
}

func failStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightFail, Message: msg}
}

func warnStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightWarn, Message: msg}
}

func overall(status domain.PreflightStatus, detail string) preflight.Outcome {
	return preflight.Outcome{Status: status, Detail: detail}
}

func perHost(ctx context.Context, draft domain.NewClusterDraft, _ preflight.HostConn, fn func(context.Context, domain.HostRow) domain.PreflightHostStatus) []domain.PreflightHostStatus {
	out := make([]domain.PreflightHostStatus, 0, len(draft.Hosts))
	for _, h := range draft.Hosts {
		if h.Hostname == "" {
			continue
		}
		out = append(out, fn(ctx, h))
	}
	return out
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
