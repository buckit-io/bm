import { useState } from "react";
import { MigrationDraft } from "../state";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
  onFinish: () => void;
}

export function Finalize({ draft, update, onFinish }: Props) {
  const [modalOpen, setModalOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");

  const doFinalize = () => {
    update({ finalized: true });
    setModalOpen(false);
    setTimeout(onFinish, 400);
  };

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Finalize</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          You're about to finalize the migration of <b>{draft.name}</b>.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">After finalizing</h3>
        <ul style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li><span className="mono">dnf remove minio</span> on each node, cleanly removing the minio binary, service unit, and user/group.</li>
          <li>Buckit retains the minio .rpm/.deb in the manager DB for 30 days for emergency rollback (manual reinstall).</li>
          <li>The wizard's <b>Rollback</b> option is removed.</li>
          <li>Cluster status moves from <b>Migrating</b> to <b>Active</b>.</li>
        </ul>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Preserved (not modified by finalize)</h3>
        <ul style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li><span className="mono">/etc/default/minio</span> — buckit.service continues to read it</li>
          <li><span className="mono">/etc/minio/</span> — TLS certs, KMS config</li>
          <li><span className="mono">.minio.sys/</span>, <span className="mono">xl.meta</span> — on-disk cluster state and data</li>
          <li><span className="mono">MINIO_*</span> env var names — read directly by the buckit binary</li>
        </ul>
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          These paths and names are intentionally kept MinIO-compatible. Fresh
          Buckit deployments use the same layout, so post-migration nodes are
          byte-identical to a fresh install at every layer except the binary
          and the unit name.
        </p>
      </div>

      <div className="hstack" style={{ justifyContent: "flex-end" }}>
        <button className="btn btn--danger" onClick={() => setModalOpen(true)}>
          Finalize migration
        </button>
      </div>

      {modalOpen && (
        <div className="modal-backdrop" onClick={() => setModalOpen(false)}>
          <div className="card modal" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
              Finalize migration?
            </h3>
            <p style={{ fontSize: "var(--fs-sm)" }}>
              This is irreversible. The minio package and service will be removed
              from every node. The data, env file, and on-disk state will not be
              touched.
            </p>
            <div className="field">
              <label className="field-label">
                Type the cluster name to confirm: <span className="mono">{draft.name}</span>
              </label>
              <input
                className="input"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder={draft.name}
              />
            </div>
            <div className="hstack" style={{ justifyContent: "flex-end" }}>
              <button className="btn" onClick={() => setModalOpen(false)}>Cancel</button>
              <button
                className="btn btn--danger"
                disabled={confirmText !== draft.name}
                onClick={doFinalize}
              >
                Finalize
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
