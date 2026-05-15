import { MigrationDraft } from "../state";
import { formatDuration } from "../../../../mock/data";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

export function Plan({ draft, update }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const totalSec =
    draft.plan.concurrency === "sequential"
      ? hosts.length * 90
      : Math.ceil(hosts.length / 2) * 90;

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Migration plan</h2>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Install method</h3>
        <p>
          <span className="mono">dnf install</span>{" "}
          <span className="subtle">(RHEL 9 detected on all nodes; minio installed via rpm)</span>
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">For each node, in sequence</h3>
        <ol style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li>Wait for cluster quorum (other nodes must be healthy)</li>
          <li><span className="mono">systemctl stop minio</span></li>
          <li>scp <span className="mono">buckit-1.0.0.rpm</span> to <span className="mono">/tmp/</span></li>
          <li><span className="mono">dnf install -y /tmp/buckit-1.0.0.rpm</span> <span className="subtle">(minio package kept for rollback)</span></li>
          <li><span className="mono">systemctl disable minio</span></li>
          <li><span className="mono">systemctl enable --now buckit</span> — reads same <span className="mono">/etc/default/minio</span></li>
          <li>Wait for node-healthy probe (timeout 2m)</li>
          <li>Wait for cluster-healthy probe (timeout 5m) before next node</li>
        </ol>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Not touched on any node</h3>
        <ul style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li><span className="mono">/etc/default/minio</span> — env file kept as-is</li>
          <li><span className="mono">/etc/minio/</span> — TLS certs, KMS config kept as-is</li>
          <li><span className="mono">.minio.sys/</span> — on-disk cluster state kept as-is</li>
          <li><span className="mono">/data/disk*</span> — data drives untouched</li>
          <li>minio package — installed and disabled; removed only on Finalize</li>
        </ul>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Rolling order</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          {(["sequential", "two_at_a_time"] as const).map((c) => (
            <label key={c} className="hstack" style={{ gap: 6 }}>
              <input
                type="radio"
                checked={draft.plan.concurrency === c}
                onChange={() => update({ plan: { ...draft.plan, concurrency: c } })}
              />
              {c === "sequential" ? "Sequential (safest)" : "Two at a time (faster, requires EC parity ≥ 2)"}
            </label>
          ))}
        </div>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-6)" }}>
          <div>
            <div className="field-label">Estimated downtime per node</div>
            <div>~30–90 s</div>
          </div>
          <div>
            <div className="field-label">Estimated total migration time</div>
            <div>~{formatDuration(totalSec)}</div>
          </div>
        </div>
      </div>

      <div className="banner banner--info">
        <span>ℹ</span>
        <span>
          A <b>Rollback</b> action stays available until you click <b>Finalize</b>.
          Rollback per node is symmetric: stop buckit, disable buckit.service,
          re-enable minio.service. Env file, certs, and on-disk state are
          unchanged throughout.
        </span>
      </div>

      <label className="hstack" style={{ gap: 8 }}>
        <input
          type="checkbox"
          checked={draft.plan.ack}
          onChange={(e) => update({ plan: { ...draft.plan, ack: e.target.checked } })}
        />
        I understand each node will briefly stop serving writes during cutover.
      </label>
    </div>
  );
}
