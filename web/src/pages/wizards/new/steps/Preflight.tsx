import { useEffect, useState } from "react";
import {
  NewClusterDraft,
  PreflightHostStatus,
  PreflightResult,
} from "../state";
import { Pill } from "../../../../components/Pill";
import { detectHostnamePattern } from "./hostnamePattern";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

// Each preflight check is defined once. The check function runs the
// (mock) SSH probe and produces per-host outcomes. The runner attaches
// the static fields (id, label, severity).
interface CheckDef {
  id: string;
  label: string;
  severity: "blocking" | "advisory";
  // Returns either a host-scoped result (with per-host outcomes) or an
  // overall result (just status + optional detail).
  evaluate(
    draft: NewClusterDraft,
    hostList: { id: string; hostname: string }[],
  ): EvalOutcome;
}

type EvalOutcome =
  | { kind: "host"; hostStatuses: PreflightHostStatus[]; detail?: string }
  | { kind: "overall"; status: "pass" | "warn" | "fail" | "skipped"; detail?: string };

function aggregate(hostStatuses: PreflightHostStatus[]): PreflightResult["result"] {
  if (hostStatuses.length === 0) return "pass";
  if (hostStatuses.some((s) => s.status === "fail")) return "fail";
  if (hostStatuses.some((s) => s.status === "warn")) return "warn";
  return "pass";
}

// ── mock check evaluators ──────────────────────────────────────────
//
// Each evaluator returns plausible per-host outcomes. The mock seeds
// failures on specific node indices so the operator can see what the UX
// looks like. Real backend (M5) will run SSH probes per check.

const CHECKS: CheckDef[] = [
  {
    id: "ssh",
    label: "SSH reachability",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h, i) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: i === 7 ? "fail" : "pass",
        message: i === 7 ? "Connection refused on port 22." : undefined,
      })),
    }),
  },
  {
    id: "sudo",
    label: "Sudo (passwordless)",
    severity: "blocking",
    evaluate: (draft, hosts) => {
      if (draft.ssh.user === "root") {
        return {
          kind: "overall",
          status: "skipped",
          detail: "Connecting as root — sudo not required.",
        };
      }
      return {
        kind: "host",
        hostStatuses: hosts.map((h) => ({
          hostId: h.id,
          hostname: h.hostname,
          status: "pass",
        })),
      };
    },
  },
  {
    id: "pkg_mgr",
    label: "Package manager available",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
        message: "dnf detected",
      })),
    }),
  },
  {
    id: "rpm",
    label: "Package URL reachable",
    severity: "blocking",
    evaluate: (draft) => ({
      kind: "overall",
      status: "pass",
      detail: `buckit-${draft.version}.rpm — 87 MB, sha256 verified`,
    }),
  },
  {
    id: "free",
    label: "Free space on selected drives",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
      })),
    }),
  },
  {
    id: "stale_format",
    label: "No stale .minio.sys/format.json",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
      })),
    }),
  },
  {
    id: "dns",
    label: "DNS / hostname resolution",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h, i) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: i === 4 ? "fail" : "pass",
        message:
          i === 4
            ? `Cannot resolve ${hosts[hosts.length - 1]?.hostname ?? "peer"} from this host.`
            : undefined,
      })),
    }),
  },
  {
    id: "ports",
    label: "Inter-node port reachability (9000, 9001)",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
      })),
    }),
  },
  {
    id: "ports_conflict",
    label: "Conflicting listeners on 9000/9001",
    severity: "blocking",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
      })),
    }),
  },
  {
    id: "arch_uniform",
    label: "Architecture uniformity (amd64 / arm64)",
    severity: "blocking",
    evaluate: (draft) => {
      const archs = Object.values(draft.discovery)
        .map((r) => r.arch)
        .filter((a): a is string => !!a);
      if (archs.length === 0) {
        return {
          kind: "overall",
          status: "fail",
          detail: "No architecture reported yet — re-run discovery.",
        };
      }
      const unique = Array.from(new Set(archs));
      if (unique.length === 1) {
        return {
          kind: "overall",
          status: "pass",
          detail: `All hosts on ${unique[0]}.`,
        };
      }
      return {
        kind: "overall",
        status: "fail",
        detail: `Mixed architectures: ${unique.join(", ")}. MinIO binaries are per-arch; every host in a cluster must share one.`,
      };
    },
  },
  {
    id: "os_uniform",
    label: "OS uniformity (same distro family)",
    severity: "advisory",
    evaluate: (draft) => {
      const oses = Object.values(draft.discovery)
        .map((r) => r.os)
        .filter((s): s is string => !!s);
      const unique = Array.from(new Set(oses));
      if (unique.length <= 1) {
        return {
          kind: "overall",
          status: "pass",
          detail: unique[0] ? `All hosts on ${unique[0]}.` : undefined,
        };
      }
      return {
        kind: "overall",
        status: "warn",
        detail: `Mixed: ${unique.join(", ")}. Allowed, but operational tuning (sysctl, ulimit) may differ per host.`,
      };
    },
  },
  {
    id: "xfs_fs",
    label: "Data drives formatted XFS",
    severity: "advisory",
    evaluate: (draft, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => {
        const drives =
          draft.discovery[h.id]?.drives?.filter(
            (d) =>
              !d.isBoot && draft.topology.selectedMounts.includes(d.mount),
          ) ?? [];
        const nonXfs = drives.filter(
          (d) => d.fsType && d.fsType.toLowerCase() !== "xfs",
        );
        if (nonXfs.length === 0) {
          return { hostId: h.id, hostname: h.hostname, status: "pass" };
        }
        const sample = nonXfs[0];
        return {
          hostId: h.id,
          hostname: h.hostname,
          status: "warn",
          message: `${nonXfs.length} drive${nonXfs.length === 1 ? "" : "s"} on ${sample.fsType} (e.g. ${sample.mount}). XFS is recommended.`,
        };
      }),
    }),
  },
  {
    id: "hostname_pattern",
    label: "Hostnames fit one brace-expansion pattern",
    severity: "blocking",
    evaluate: (_, hosts) => {
      if (hosts.length < 2) {
        return { kind: "overall", status: "pass", detail: "Single-host deployment." };
      }
      const fit = detectHostnamePattern(hosts.map((h) => h.hostname));
      if (fit.fits) {
        return { kind: "overall", status: "pass", detail: `Pattern: ${fit.pattern}` };
      }
      return { kind: "overall", status: "fail", detail: fit.reason };
    },
  },
  {
    id: "drive_uniformity",
    label: "Drive paths uniform across hosts",
    severity: "blocking",
    evaluate: (draft) => {
      const mounts = draft.topology.selectedMounts;
      if (mounts.length === 0) {
        return {
          kind: "overall",
          status: "fail",
          detail: "No drives selected on the Topology step.",
        };
      }
      return {
        kind: "overall",
        status: "pass",
        detail: `${mounts.length} mountpoints selected; same paths on every host.`,
      };
    },
  },
  {
    id: "time",
    label: "Time sync (skew < 1s)",
    severity: "advisory",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h, i) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: i === 2 ? "warn" : "pass",
        message: i === 2 ? "Clock skew of 1.4s versus the manager." : undefined,
      })),
    }),
  },
  {
    id: "buckit_pkg",
    label: "Existing buckit package",
    severity: "advisory",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
        message: "Not installed.",
      })),
    }),
  },
  {
    id: "minio_svc",
    label: "Existing minio service",
    severity: "advisory",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h, i) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: i === 1 ? "warn" : "pass",
        message:
          i === 1
            ? "minio installed but not running. Will be left in place."
            : undefined,
      })),
    }),
  },
  {
    id: "ulimit",
    label: "Kernel ulimit (nofile ≥ 65536)",
    severity: "advisory",
    evaluate: (_, hosts) => ({
      kind: "host",
      hostStatuses: hosts.map((h) => ({
        hostId: h.id,
        hostname: h.hostname,
        status: "pass",
      })),
    }),
  },
];

function resultPill(r: PreflightResult["result"]) {
  switch (r) {
    case "pass":
      return <Pill tone="success" icon="✓">Pass</Pill>;
    case "warn":
      return <Pill tone="warning" icon="⚠">Warning</Pill>;
    case "fail":
      return <Pill tone="danger" icon="✗">Fail</Pill>;
    case "skipped":
      return <Pill tone="neutral">Skipped</Pill>;
  }
}

export function Preflight({ draft, update }: Props) {
  const [running, setRunning] = useState(false);

  const run = () => {
    const hosts = draft.hosts
      .filter((h) => h.hostname.trim())
      .map((h) => ({ id: h.id, hostname: h.hostname.trim() }));
    setRunning(true);
    update({ preflight: [] });
    let i = 0;
    const out: PreflightResult[] = [];
    const tick = () => {
      if (i >= CHECKS.length) {
        setRunning(false);
        return;
      }
      const def = CHECKS[i];
      const outcome = def.evaluate(draft, hosts);
      const row: PreflightResult = {
        id: def.id,
        label: def.label,
        severity: def.severity,
        result:
          outcome.kind === "overall"
            ? outcome.status
            : aggregate(outcome.hostStatuses),
        detail: outcome.kind === "overall" ? outcome.detail : undefined,
        hostStatuses:
          outcome.kind === "host" ? outcome.hostStatuses : undefined,
      };
      out.push(row);
      update({ preflight: [...out] });
      i++;
      setTimeout(tick, 120);
    };
    tick();
  };

  useEffect(() => {
    if (draft.preflight.length === 0) run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const blockingFails = draft.preflight.filter(
    (p) => p.severity === "blocking" && p.result === "fail",
  ).length;
  const advisoryFails = draft.preflight.filter(
    (p) => p.severity === "advisory" && (p.result === "fail" || p.result === "warn"),
  ).length;
  const blockingWarn = draft.preflight.filter(
    (p) => p.severity === "blocking" && p.result === "warn",
  ).length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Preflight</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Verifying every host is ready to receive Buckit. Blocking
            failures must be resolved before deploy.
          </p>
        </div>
        <div className="hstack">
          {blockingFails > 0 && (
            <Pill tone="danger" icon="✗">
              {blockingFails} blocking
            </Pill>
          )}
          {advisoryFails + blockingWarn > 0 && (
            <Pill tone="warning" icon="⚠">
              {advisoryFails + blockingWarn} warning
              {advisoryFails + blockingWarn > 1 ? "s" : ""}
            </Pill>
          )}
          <button className="btn btn--sm" onClick={run} disabled={running}>
            Re-run
          </button>
        </div>
      </header>

      <div className="card card--table">
        <table className="table table--center">
          <thead>
            <tr>
              <th>Check</th>
              <th style={{ width: 90 }}>Severity</th>
              <th style={{ width: 110 }}>Nodes</th>
              <th style={{ width: 120 }}>Result</th>
            </tr>
          </thead>
          <tbody>
            {draft.preflight.flatMap((p) => {
              const failingHosts =
                p.hostStatuses?.filter((s) => s.status !== "pass") ?? [];
              const total = p.hostStatuses?.length ?? 0;
              const passing = total - failingHosts.length;
              return [
                <tr key={p.id}>
                  <td>{p.label}</td>
                  <td>
                    <span
                      className="subtle"
                      style={{ fontSize: "var(--fs-xs)" }}
                    >
                      {p.severity === "blocking" ? "Blocking" : "Advisory"}
                    </span>
                  </td>
                  <td className="subtle">
                    {p.hostStatuses ? `${passing} / ${total}` : "—"}
                  </td>
                  <td>{resultPill(p.result)}</td>
                </tr>,
                (p.detail || failingHosts.length > 0) && (
                  <tr key={p.id + "-d"} className="discover__detail">
                    <td colSpan={4}>
                      <div
                        className="vstack subtle"
                        style={{
                          gap: 2,
                          padding: "var(--s-2) 0",
                          fontSize: "var(--fs-xs)",
                        }}
                      >
                        {p.detail && <span>↳ {p.detail}</span>}
                        {failingHosts.map((s) => (
                          <span key={s.hostId}>
                            ↳ <span className="mono">{s.hostname}</span>
                            {s.message ? ` — ${s.message}` : ""}
                          </span>
                        ))}
                      </div>
                    </td>
                  </tr>
                ),
              ];
            })}
          </tbody>
        </table>
      </div>

      {blockingFails > 0 && (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>
            {blockingFails} blocking failure
            {blockingFails > 1 ? "s" : ""} — fix before deploy. Re-run
            after correcting.
          </span>
        </div>
      )}
      {blockingFails === 0 && (advisoryFails + blockingWarn) > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            {advisoryFails + blockingWarn} warning
            {advisoryFails + blockingWarn > 1 ? "s" : ""} — review, then
            continue.
          </span>
        </div>
      )}
    </div>
  );
}
