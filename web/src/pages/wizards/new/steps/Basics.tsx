import { NewClusterDraft } from "../state";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

export function Basics({ draft, update }: Props) {
  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 560 }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Basics</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Name your cluster and pick the Buckit version to deploy.
        </p>
      </header>

      <div className="field">
        <label className="field-label" htmlFor="name">Cluster name</label>
        <input
          id="name"
          className="input"
          value={draft.name}
          onChange={(e) => update({ name: e.target.value })}
          placeholder="prod-east"
        />
        <span className="field-hint">
          Lowercase letters, digits, dashes. Must be unique within this manager.
        </span>
      </div>

      <div className="field">
        <label className="field-label" htmlFor="desc">Description (optional)</label>
        <input
          id="desc"
          className="input"
          value={draft.description}
          onChange={(e) => update({ description: e.target.value })}
          placeholder="Customer-facing production"
        />
      </div>

      <div className="field">
        <span className="field-label">Intended use</span>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          {(["production", "staging", "dev"] as const).map((u) => (
            <label key={u} className="hstack" style={{ gap: 6 }}>
              <input
                type="radio"
                name="use"
                checked={draft.intendedUse === u}
                onChange={() => update({ intendedUse: u })}
              />
              {u[0].toUpperCase() + u.slice(1)}
            </label>
          ))}
        </div>
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
          <option value="v0.98.0">v0.98.0</option>
          <option value="custom">Custom URL…</option>
        </select>
      </div>
    </div>
  );
}
