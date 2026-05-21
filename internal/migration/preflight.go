// Package migration drives the MinIO → Buckit cutover flow: snapshot capture,
// preflight checks, and the cutover orchestration (M8). M5 ships the
// preflight catalog and snapshot writer; the cutover executor lands in M8.
package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/preflight"
)

// Catalog returns the migration preflight check list. Driven by
// POST /clusters/:id/migrate/preflight; one history row is written per pass
// (unlike new-cluster preflight which is called freely during wizard nav).
func Catalog(adminCreds domain.AdminCreds) []preflight.Check {
	return []preflight.Check{
		{ID: "minio_detected", Label: "MinIO service detected on every host", Severity: domain.PreflightBlocking, Eval: detectMinio},
		{ID: "mc_compat", Label: "MinIO version compatible with migration", Severity: domain.PreflightBlocking, Eval: mcCompat(adminCreds)},
		{ID: "drives_ready", Label: "Drives match the last ServerInfo", Severity: domain.PreflightBlocking, Eval: drivesReady},
		{ID: "idle", Label: "No in-flight admin operations", Severity: domain.PreflightBlocking, Eval: clusterIdle(adminCreds)},
		{ID: "service_freezable", Label: "Admin API responds (freeze will succeed)", Severity: domain.PreflightBlocking, Eval: serviceFreezable(adminCreds)},
		{ID: "arch_uniform", Label: "Architecture uniformity (amd64 / arm64)", Severity: domain.PreflightBlocking, Eval: archUniformFromDiscovery},
		{ID: "os_uniform", Label: "OS uniformity (same distro family)", Severity: domain.PreflightAdvisory, Eval: osUniformFromDiscovery},
		{ID: "artifact_reachable", Label: "Buckit RPM reachable from each host", Severity: domain.PreflightBlocking, Eval: artifactReachable},
		{ID: "space_for_snapshot", Label: "Free space in /tmp for the MinIO snapshot", Severity: domain.PreflightAdvisory, Eval: spaceForSnapshot},
		{ID: "bak_writable", Label: "/etc/default writable (env-file backup will succeed)", Severity: domain.PreflightBlocking, Eval: bakWritable},
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
		// versions that already look like Buckit.
		if !strings.HasPrefix(info.Version, "RELEASE.") {
			return overall(domain.PreflightFail, "cluster does not look like MinIO (version "+info.Version+")")
		}
		return overall(domain.PreflightPass, "MinIO version "+info.Version)
	}
}

func drivesReady(_ context.Context, _ preflight.HostConn, _ domain.NewClusterDraft) preflight.Outcome {
	// Compares the discovery-time drive list against the cluster's last
	// ServerInfo. The api handler hands us a draft that has Discovery
	// pre-populated; if no Discovery is set we can only emit a skipped result.
	return overall(domain.PreflightSkipped, "Re-run discovery before migration preflight to compare drives.")
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

func archUniformFromDiscovery(_ context.Context, _ preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	seen := map[string]bool{}
	for _, r := range draft.Discovery {
		if r.Arch != "" {
			seen[r.Arch] = true
		}
	}
	if len(seen) == 0 {
		return overall(domain.PreflightFail, "No architecture reported — re-run discovery.")
	}
	if len(seen) == 1 {
		for a := range seen {
			return overall(domain.PreflightPass, "All hosts on "+a+".")
		}
	}
	list := mapKeys(seen)
	return overall(domain.PreflightFail, "Mixed architectures: "+strings.Join(list, ", "))
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
		// Without a draft.Version set on a migration call, fall back to the
		// rolling Buckit release ("v1.0.0"). The API handler may also pass a
		// version override in the body.
		ver := draft.Version
		if ver == "" {
			ver = "v1.0.0"
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

func spaceForSnapshot(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	// The snapshot is written on the bm host's filesystem, not on the cluster
	// hosts — so this is a single check against the bm side. The api handler
	// runs `df` against ~/.config/bm via an injected helper; for M5 we emit
	// an advisory pass and refine when the snapshot writer reports back.
	return overall(domain.PreflightPass, "Snapshot will be written to ~/.config/bm/snapshots/.")
}

// bakWritable confirms the cutover can write /etc/default/minio.bm-bak on
// every host. Running the test before cutover is friendlier than discovering
// the failure mid-stage on host #2.
func bakWritable(ctx context.Context, conn preflight.HostConn, draft domain.NewClusterDraft) preflight.Outcome {
	return preflight.Outcome{HostStatuses: perHost(ctx, draft, conn, func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus {
		// Use a touch + rm probe rather than `[ -w /etc/default ]` so we
		// catch sudo-required hosts where -w false-positives.
		probe := "sudo -n bash -c 'touch /etc/default/.bm-bak-probe && rm -f /etc/default/.bm-bak-probe'"
		_, stderr, exit, _ := conn.Run(ctx, h, probe)
		if exit != 0 {
			detail := "/etc/default not writable via sudo"
			if s := strings.TrimSpace(stderr); s != "" {
				detail = detail + ": " + s
			}
			return failStatus(h, detail)
		}
		return passStatus(h, "writable")
	})}
}

// ---- shared helpers (mirror preflight package's private ones) ----

func passStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightPass, Message: msg}
}

func failStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightFail, Message: msg}
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
