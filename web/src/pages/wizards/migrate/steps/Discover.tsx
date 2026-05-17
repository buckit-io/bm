import { useEffect, useRef, useState } from "react";
import { DiscoveredDrive, DiscoveryResult, MigrationDraft, MinioNodeInfo } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

const TiB = 1024 ** 4;

function mockMinioDrives(): DiscoveredDrive[] {
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
    drives.push({
      device: `/dev/sd${String.fromCharCode(96 + i)}`,
      mount: `/data/disk${i}`,
      sizeBytes: 16 * TiB,
      fsType: "xfs",
    });
  }
  return drives;
}

function disksSummary(r: DiscoveryResult): string {
  if (!r.drives) return "—";
  const data = r.drives.filter((d) => !d.isBoot);
  if (data.length === 0) return "no data drives";
  const sample = data[0];
  const sizeTiB = Math.round(sample.sizeBytes / TiB);
  return `${data.length} × ${sizeTiB} TiB`;
}

function statePill(s: DiscoveryResult["state"]) {
  switch (s) {
    case "done": return <Pill tone="success" icon="✓">Done</Pill>;
    case "running": return <Pill tone="info" icon="⟳">…</Pill>;
    case "failed": return <Pill tone="danger" icon="✗">Timeout</Pill>;
    default: return <Pill tone="neutral">Pending</Pill>;
  }
}

export function Discover({ draft, update }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const [expanded, setExpanded] = useState<string | null>(hosts[0]?.id ?? null);
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    const d: Record<string, DiscoveryResult> = {};
    const m: Record<string, MinioNodeInfo> = {};
    hosts.forEach((h) => (d[h.id] = { state: "pending" }));
    update({ discovery: d, minioDetection: m });

    let i = 0;
    const tick = () => {
      if (i >= hosts.length) return;
      const h = hosts[i];
      i++;
      d[h.id] = { state: "running" };
      update({ discovery: { ...d } });
      setTimeout(() => {
        d[h.id] = {
          state: "done",
          os: "Ubuntu 24.04",
          cores: 16,
          ramGiB: 64,
          drives: mockMinioDrives(),
          existingService: "minio",
        };
        m[h.id] = {
          binaryPath: "/usr/local/bin/minio",
          binaryVersion: "v2024-12-01",
          serviceUnit: "minio.service",
          serviceActive: true,
          envFile: "/etc/default/minio",
          minioVolumes: `https://legacy-node{1...${hosts.length}}:9000/data/disk{1...12}`,
          minioOpts: "--console-address :9001",
          detectedPools: `1 pool, ${hosts.length}×12 drives, EC:4`,
        };
        update({ discovery: { ...d }, minioDetection: { ...m } });
        tick();
      }, 600);
    };
    tick();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Discovery</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Probing each host and capturing the existing MinIO configuration.
        </p>
      </header>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>OS</th>
              <th>Disks</th>
              <th>MinIO</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {hosts.map((h) => {
              const r = draft.discovery[h.id] ?? { state: "pending" as const };
              const m = draft.minioDetection[h.id];
              return (
                <>
                  <tr
                    key={h.id}
                    onClick={() => setExpanded(expanded === h.id ? null : h.id)}
                    style={{ cursor: "pointer" }}
                  >
                    <td>
                      <span className="mono">{h.hostname}</span>{" "}
                      <span className="subtle">{expanded === h.id ? "▾" : "▸"}</span>
                    </td>
                    <td>{r.os ?? "—"}</td>
                    <td>{disksSummary(r)}</td>
                    <td>
                      {m
                        ? <Pill tone="info">{m.binaryVersion}</Pill>
                        : <span className="subtle">—</span>}
                    </td>
                    <td>{statePill(r.state)}</td>
                  </tr>
                  {expanded === h.id && m && (
                    <tr key={h.id + "-d"} className="discover__detail">
                      <td colSpan={5}>
                        <div className="vstack subtle" style={{ gap: 4, padding: "var(--s-2) 0", fontSize: "var(--fs-xs)" }}>
                          <span><b>MinIO binary</b> <span className="mono">{m.binaryPath}</span> · {m.binaryVersion}</span>
                          <span><b>MinIO service</b> {m.serviceUnit} ({m.serviceActive ? "active, enabled" : "inactive"})</span>
                          <span><b>MinIO env</b> <span className="mono">{m.envFile}</span></span>
                          <span><b>MINIO_VOLUMES</b> <span className="mono">{m.minioVolumes}</span></span>
                          <span><b>MINIO_OPTS</b> <span className="mono">{m.minioOpts}</span></span>
                          <span><b>Detected pools</b> {m.detectedPools}</span>
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
