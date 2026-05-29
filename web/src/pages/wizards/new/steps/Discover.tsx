import { useEffect, useState } from "react";
import { DiscoveryResult, NewClusterDraft } from "../state";
import { Pill } from "../../../../components/Pill";
import { newClusterDiscover } from "../../../../api/client";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

function disksSummary(r: DiscoveryResult): string {
  if (!r.drives) return "—";
  const data = r.drives.filter((d) => !d.isBoot && !d.isSystem);
  if (data.length === 0) return "no data drives";
  const mounted = data.filter((d) => d.mount).length;
  const unformatted = data.filter((d) => !d.fsType).length;
  const sample = data[0];
  const size = humanDriveSize(sample.sizeBytes);
  const parts = [`${mounted} mounted`];
  if (unformatted > 0) parts.push(`${unformatted} unformatted`);
  return `${parts.join(", ")} · ${size} each`;
}

function humanDriveSize(bytes: number): string {
  if (bytes >= 1024 ** 4) return `${(bytes / 1024 ** 4).toFixed(1)} TiB`;
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(0)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(0)} MiB`;
  return `${(bytes / 1024).toFixed(0)} KiB`;
}

function statusPill(s: DiscoveryResult["state"]) {
  switch (s) {
    case "done": return <Pill tone="success" icon="✓">Done</Pill>;
    case "running": return <Pill tone="info" icon="⟳">…</Pill>;
    case "failed": return <Pill tone="danger" icon="✗">Failed</Pill>;
    default: return <Pill tone="neutral">Pending</Pill>;
  }
}

function osSummary(r: DiscoveryResult) {
  return (
    <div className="vstack" style={{ gap: 2 }}>
      <span>{r.os ?? "—"}</span>
      {(r.kernel || r.arch) && (
        <span className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          {[r.kernel, r.arch].filter(Boolean).join(" · ")}
        </span>
      )}
    </div>
  );
}

export function Discover({ draft, update }: Props) {
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const initial: Record<string, DiscoveryResult> = {};
    validHosts.forEach((h) => (initial[h.id] = { state: "running" }));
    update({ discovery: initial });

    const ctrl = new AbortController();
    newClusterDiscover(
      {
        hosts: validHosts.map((h) => ({
          id: h.id,
          hostname: h.hostname,
          port: h.port || 22,
          probe: h.probe,
          sshOverride: h.sshOverride,
        })),
        ssh: draft.ssh,
      },
      ctrl.signal,
    )
      .then((resp) => {
        if (ctrl.signal.aborted) return;
        update({
          discovery: resp as unknown as Record<string, DiscoveryResult>,
        });
      })
      .catch((err) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        const message = err instanceof Error ? err.message : "Discovery failed.";
        setError(message);
        const fail: Record<string, DiscoveryResult> = {};
        validHosts.forEach((h) => (fail[h.id] = { state: "failed" }));
        update({ discovery: fail });
      });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const done = Object.values(draft.discovery).filter((r) => r.state === "done").length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Discovery</h2>
        <span data-testid="new-cluster-discovery-title" style={{ display: "none" }}>
          Discovery
        </span>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Probing each host over SSH for OS, kernel, architecture, CPU, RAM,
          and disks.
        </p>
        {error && (
          <p className="subtle" style={{ color: "var(--c-danger)", marginTop: 4 }}>
            {error}
          </p>
        )}
      </header>

      <div className="hstack">
        <span className="subtle">Progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 280 }}>
          <div
            className="progress__bar"
            style={{ width: `${(done / validHosts.length) * 100 || 0}%` }}
          />
        </div>
        <span className="subtle">
          <span data-testid="new-cluster-discovery-progress">
          {done} / {validHosts.length} complete
          </span>
        </span>
      </div>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>OS</th>
              <th>Cores</th>
              <th>RAM</th>
              <th>Disks</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {validHosts.map((h) => {
              const r = draft.discovery[h.id] ?? { state: "pending" as const };
              return (
                <tr key={h.id}>
                  <td>
                    <span className="mono">{h.hostname}</span>
                  </td>
                  <td>{osSummary(r)}</td>
                  <td>{r.cores ?? "—"}</td>
                  <td>{r.ramGiB ? `${r.ramGiB} GiB` : "—"}</td>
                  <td>{disksSummary(r)}</td>
                  <td>{statusPill(r.state)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
