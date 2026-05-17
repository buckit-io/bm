import { NewClusterDraft } from "../state";

interface Props { draft: NewClusterDraft; }

const UNIT = `[Unit]
Description=Buckit Object Storage
After=network-online.target
AssertFileIsExecutable=/usr/local/bin/buckit

[Service]
Type=notify
EnvironmentFile=-/etc/default/minio
ExecStart=/usr/local/bin/buckit server $MINIO_OPTS $MINIO_VOLUMES
User=buckit
Group=buckit
Restart=always

[Install]
WantedBy=multi-user.target`;

export function Review({ draft }: Props) {
  const nodes = draft.hosts.filter((h) => h.hostname.trim());
  const t = draft.topology;
  const drivesPerNode = t.selectedMounts.length;
  const totalDrives = nodes.length * drivesPerNode;
  // Estimate drive size from the first discovered eligible drive (real
  // backend uses preflight-validated uniformity).
  const sampleSize =
    Object.values(draft.discovery)
      .flatMap((r) => r.drives ?? [])
      .find((d) => !d.isBoot && d.sizeBytes > 0)?.sizeBytes ?? 0;
  const TiB = 1024 ** 4;
  const rawTiB = (totalDrives * sampleSize) / TiB;
  const usableTiB = rawTiB - rawTiB * (t.parity / Math.max(t.setSize, 1));
  const mountExpansion =
    drivesPerNode > 0
      ? `/data/disk{1...${drivesPerNode}}`
      : "/data/disk{1...N}";
  const hostname = nodes[0]?.hostname || "node1";
  const serverUrl = draft.serverUrl.trim() || `http://${hostname}:${draft.api.port}`;
  const envFile = `MINIO_ROOT_USER=${draft.credentials.rootUser}
MINIO_ROOT_PASSWORD=********    # the password you set in Basics
MINIO_VOLUMES="http://${hostname}{1...${nodes.length}}${mountExpansion}"
MINIO_OPTS="--address :${draft.api.port} --console-address :${draft.api.consolePort}"
MINIO_REGION=${draft.region}
MINIO_SERVER_URL=${serverUrl}`;

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Review</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Verify the plan before deploy.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Summary</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          <div><div className="field-label">Cluster</div><div>{draft.name} ({draft.version})</div></div>
          <div><div className="field-label">Nodes</div><div>{nodes.length}</div></div>
          <div><div className="field-label">Pools</div><div>1</div></div>
          <div><div className="field-label">Drives</div><div>{totalDrives}</div></div>
          <div><div className="field-label">Parity</div><div>EC:{t.parity}</div></div>
          <div><div className="field-label">Drives / node</div><div>{drivesPerNode || "—"}</div></div>
          <div><div className="field-label">Usable / Raw</div><div>~{Math.round(usableTiB)} TiB / {Math.round(rawTiB)} TiB</div></div>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Install method</h3>
        <p>
          <span className="mono">dnf install</span>{" "}
          <span className="subtle">(RHEL 9 detected on all nodes)</span>
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Systemd unit (provided by package)</h3>
        <pre className="codeblock">{UNIT}</pre>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Environment file (/etc/default/minio)</h3>
        <pre className="codeblock">{envFile}</pre>
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          ℹ The unit, binary, and buckit user come from the package. The manager writes only this env file. MinIO-compatible names are preserved so fresh and migrated nodes are byte-identical.
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">What will happen</h3>
        <ol style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li>Fetch <span className="mono">buckit-{draft.version}.rpm</span> from GitHub Release (one-time, cached).</li>
          <li>scp the rpm to each node.</li>
          <li><span className="mono">dnf install -y /tmp/buckit-{draft.version}.rpm</span></li>
          <li>Write <span className="mono">/etc/default/minio</span> with cluster values.</li>
          <li><span className="mono">systemctl daemon-reload && enable --now buckit</span></li>
          <li>Wait for cluster health-ready (timeout 5m).</li>
        </ol>
      </div>

    </div>
  );
}
