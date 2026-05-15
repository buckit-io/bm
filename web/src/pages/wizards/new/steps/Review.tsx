import { useState } from "react";
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
  const totalDrives = nodes.length * t.drivesPerNode;
  const rawTiB = totalDrives * t.driveSizeTiB;
  const usableTiB = rawTiB - rawTiB * (t.parity / t.setSize);
  const envFile = `MINIO_ROOT_USER=…    # auto-generated, shown post-deploy
MINIO_ROOT_PASSWORD=…
MINIO_VOLUMES="https://${nodes[0]?.hostname || "node1"}{1...${nodes.length}}/data/disk{1...${t.drivesPerNode}}"
MINIO_OPTS="--console-address :9001"`;
  const [ack, setAck] = useState(false);

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Review</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Verify the plan before deploy.
          </p>
        </div>
        <button className="btn btn--sm">Download plan ⤓</button>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Summary</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          <div><div className="field-label">Cluster</div><div>{draft.name} ({draft.version})</div></div>
          <div><div className="field-label">Nodes</div><div>{nodes.length}</div></div>
          <div><div className="field-label">Pools</div><div>1</div></div>
          <div><div className="field-label">Drives</div><div>{totalDrives}</div></div>
          <div><div className="field-label">Parity</div><div>EC:{t.parity}</div></div>
          <div><div className="field-label">Usable</div><div>~{Math.round(usableTiB)} TiB</div></div>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Install method</h3>
        <p>
          <span className="mono">dnf install</span>{" "}
          <span className="subtle">(RHEL 9 detected on all nodes)</span>
        </p>
        <button className="btn btn--sm" style={{ alignSelf: "flex-start" }}>
          Override for individual nodes…
        </button>
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

      <label className="hstack" style={{ gap: 8 }}>
        <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
        I have backed up any existing data on selected drives.
      </label>
      {!ack && (
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          Check the box above to enable Deploy.
        </p>
      )}
    </div>
  );
}
