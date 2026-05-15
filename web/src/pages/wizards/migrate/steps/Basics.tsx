import { MigrationDraft } from "../state";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

export function Basics({ draft, update }: Props) {
  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 680 }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Basics</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Migrate an existing MinIO cluster to Buckit on the same hosts and disks.
        </p>
      </header>

      <div className="banner banner--info">
        <span>ℹ</span>
        <div>
          <div className="banner__title">In-place migration</div>
          <p style={{ marginTop: 4 }}>
            Buckit will stop <span className="mono">minio.service</span> on each
            node, install the <span className="mono">buckit</span> binary alongside
            the existing <span className="mono">minio</span> binary, and start a
            new <span className="mono">buckit.service</span> that reads the same
            <span className="mono"> /etc/default/minio</span> env file and the same
            data drives. The on-disk format (<span className="mono">xl.meta</span>,
            <span className="mono"> .minio.sys/</span>) is unchanged and no data is
            copied. Expect a brief write-unavailable window during cutover on each
            node. A <b>Rollback</b> option remains available until you click
            <b> Finalize</b> at the end of the wizard.
          </p>
        </div>
      </div>

      <div className="field">
        <label className="field-label" htmlFor="name">Cluster name (post-migration)</label>
        <input
          id="name"
          className="input"
          value={draft.name}
          onChange={(e) => update({ name: e.target.value })}
        />
      </div>

      <div className="field">
        <label className="field-label" htmlFor="ver">Buckit version</label>
        <select
          id="ver"
          className="select"
          value={draft.version}
          onChange={(e) => update({ version: e.target.value })}
        >
          <option value="v1.0.0">v1.0.0 (latest stable)</option>
          <option value="v0.99.0">v0.99.0</option>
        </select>
      </div>
    </div>
  );
}
