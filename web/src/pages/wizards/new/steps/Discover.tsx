import { useEffect, useRef, useState } from "react";
import { DiscoveredDrive, DiscoveryResult, NewClusterDraft } from "../state";
import { Pill } from "../../../../components/Pill";

const TiB = 1024 ** 4;

// Build a plausible per-host drive list. Default: 1 boot + 12 data
// drives mounted at /data/disk{1..12}, all 16 TiB XFS. For demo, one
// data drive on the third host comes up as ext4 so the XFS advisory
// preflight check has something to flag.
function mockDrives(hostIndex: number): DiscoveredDrive[] {
  const drives: DiscoveredDrive[] = [
    {
      device: "/dev/nvme0n1",
      mount: "/",
      sizeBytes: 256 * 1024 ** 3,
      fsType: "ext4",
      isBoot: true,
    },
  ];
  for (let i = 1; i <= 12; i++) {
    const isExt4 = hostIndex === 2 && i === 5; // mixed-fs demo
    drives.push({
      device: `/dev/sd${String.fromCharCode(96 + i)}`,
      mount: `/data/disk${i}`,
      sizeBytes: 16 * TiB,
      fsType: isExt4 ? "ext4" : "xfs",
    });
  }
  return drives;
}

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

function disksSummary(r: DiscoveryResult): string {
  if (!r.drives) return "—";
  const data = r.drives.filter((d) => !d.isBoot);
  if (data.length === 0) return "no data drives";
  const mounted = data.filter((d) => d.mount).length;
  const unformatted = data.filter((d) => !d.fsType).length;
  const sample = data[0];
  const sizeTiB = Math.round(sample.sizeBytes / (1024 ** 4));
  const parts = [`${mounted} mounted`];
  if (unformatted > 0) parts.push(`${unformatted} unformatted`);
  return `${parts.join(", ")} · ${sizeTiB} TiB each`;
}

function statusPill(s: DiscoveryResult["state"]) {
  switch (s) {
    case "done": return <Pill tone="success" icon="✓">Done</Pill>;
    case "running": return <Pill tone="info" icon="⟳">…</Pill>;
    case "failed": return <Pill tone="danger" icon="✗">Timeout</Pill>;
    default: return <Pill tone="neutral">Pending</Pill>;
  }
}

export function Discover({ draft, update }: Props) {
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const [expanded, setExpanded] = useState<string | null>(null);
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    const initial: Record<string, DiscoveryResult> = {};
    validHosts.forEach((h) => (initial[h.id] = { state: "pending" }));
    update({ discovery: initial });

    let i = 0;
    const tick = () => {
      if (i >= validHosts.length) return;
      const h = validHosts[i];
      i++;
      update({
        discovery: {
          ...initial,
          [h.id]: { state: "running" },
        },
      });
      setTimeout(() => {
        // mutate the result inline; React state must be set from latest
        const idx = i - 1; // i is already advanced
        initial[h.id] =
          i === validHosts.length && validHosts.length >= 8
            ? { state: "failed" }
            : {
                state: "done",
                // Mostly Ubuntu, one Rocky 9 to demo the OS-uniformity
                // advisory.
                os: idx === 1 ? "Rocky Linux 9.4" : "Ubuntu 24.04",
                arch: "amd64",
                kernel:
                  idx === 1 ? "5.14.0-427.el9.x86_64" : "6.8.0-31-generic",
                cores: 16,
                ramGiB: 64,
                drives: mockDrives(idx),
                nic: "eno1 / 10 GbE",
                sudoOk: true,
              };
        update({ discovery: { ...initial } });
        tick();
      }, 600 + Math.random() * 600);
    };
    tick();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const done = Object.values(draft.discovery).filter((r) => r.state === "done").length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Discovery</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Probing each host over SSH for OS, disks, NIC and existing services.
        </p>
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
          {done} / {validHosts.length} complete
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
                <>
                  <tr
                    key={h.id}
                    onClick={() => setExpanded(expanded === h.id ? null : h.id)}
                    style={{ cursor: "pointer" }}
                  >
                    <td>
                      <span className="mono">{h.hostname}</span>
                      <span className="subtle" style={{ marginLeft: 6 }}>
                        {expanded === h.id ? "▾" : "▸"}
                      </span>
                    </td>
                    <td>{r.os ?? "—"}</td>
                    <td>{r.cores ?? "—"}</td>
                    <td>{r.ramGiB ? `${r.ramGiB} GiB` : "—"}</td>
                    <td>{disksSummary(r)}</td>
                    <td>{statusPill(r.state)}</td>
                  </tr>
                  {expanded === h.id && r.state === "done" && (
                    <tr key={h.id + "-detail"} className="discover__detail">
                      <td colSpan={6}>
                        <div className="vstack subtle" style={{ gap: 4, padding: "var(--s-2) 0" }}>
                          <span><b>Kernel</b> {r.kernel}</span>
                          <span><b>NIC</b> {r.nic}</span>
                          <span><b>Time skew vs manager</b> &lt; 1s</span>
                          <span><b>Existing services</b> none detected</span>
                        </div>
                      </td>
                    </tr>
                  )}
                </>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
