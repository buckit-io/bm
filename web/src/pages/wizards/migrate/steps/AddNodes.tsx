import { useState } from "react";
import { HostRow, MigrationDraft } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

let idCounter = 200;
function row(): HostRow {
  return { id: "m" + idCounter++, hostname: "", port: 22, label: "", probe: "idle" };
}

function probePill(p: HostRow["probe"]) {
  switch (p) {
    case "reachable": return <Pill tone="success" icon="✓">Reachable</Pill>;
    case "probing": return <Pill tone="info" icon="⟳">Probing…</Pill>;
    case "auth_failed": return <Pill tone="danger" icon="✗">Auth failed</Pill>;
    case "timeout": return <Pill tone="danger" icon="✗">Timeout</Pill>;
    default: return <Pill tone="neutral">—</Pill>;
  }
}

export function AddNodes({ draft, update }: Props) {
  const [alias, setAlias] = useState("prod");
  const [probing, setProbing] = useState(false);

  const importFromAlias = () => {
    setProbing(true);
    setTimeout(() => {
      const imported: HostRow[] = Array.from({ length: 8 }, (_, i) => ({
        id: "m" + idCounter++,
        hostname: `legacy-node${i + 1}.example.com`,
        port: 22,
        label: "",
        probe: "idle",
      }));
      update({ importAlias: alias, hosts: imported });
      setProbing(false);
    }, 900);
  };

  const setHost = (i: number, patch: Partial<HostRow>) =>
    update({
      hosts: draft.hosts.map((h, idx) => (idx === i ? { ...h, ...patch } : h)),
    });
  const addHost = () => update({ hosts: [...draft.hosts, row()] });
  const removeHost = (i: number) =>
    update({ hosts: draft.hosts.filter((_, idx) => idx !== i) });

  const probeAll = () => {
    update({ hosts: draft.hosts.map((h) => ({ ...h, probe: "probing" })) });
    setTimeout(() => {
      update({
        hosts: draft.hosts.map((h) => ({
          ...h,
          probe: h.hostname.trim() ? "reachable" : "timeout",
        })),
      });
    }, 800);
  };

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Add nodes</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Import from an existing MinIO alias, or enter the hosts directly.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Import from a running MinIO alias</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-2)" }}>
          <select
            className="select"
            value={alias}
            onChange={(e) => setAlias(e.target.value)}
            style={{ maxWidth: 180 }}
          >
            <option value="prod">prod</option>
            <option value="staging">staging</option>
          </select>
          <span className="subtle">or</span>
          <input className="input" placeholder="https://minio.example.com" style={{ maxWidth: 280 }} />
          <input className="input" placeholder="Access key" style={{ maxWidth: 180 }} />
          <input className="input" placeholder="Secret key" type="password" style={{ maxWidth: 180 }} />
          <button className="btn btn--primary" onClick={importFromAlias} disabled={probing}>
            {probing ? "Probing…" : "Probe & import"}
          </button>
        </div>
        {draft.importAlias && (
          <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
            Imported {draft.hosts.length} nodes from alias <span className="mono">{draft.importAlias}</span>.
          </p>
        )}
      </div>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Hostname / IP</th>
              <th style={{ width: 110 }}>SSH port</th>
              <th>Label</th>
              <th style={{ width: 130 }}>Probe</th>
              <th style={{ width: 40 }}></th>
            </tr>
          </thead>
          <tbody>
            {draft.hosts.map((h, i) => (
              <tr key={h.id}>
                <td>
                  <input
                    className="input"
                    value={h.hostname}
                    onChange={(e) => setHost(i, { hostname: e.target.value, probe: "idle" })}
                    placeholder="legacy-node1.example.com"
                  />
                </td>
                <td>
                  <input
                    className="input"
                    type="number"
                    value={h.port}
                    onChange={(e) => setHost(i, { port: parseInt(e.target.value, 10) || 22 })}
                  />
                </td>
                <td>
                  <input
                    className="input"
                    value={h.label}
                    onChange={(e) => setHost(i, { label: e.target.value })}
                  />
                </td>
                <td>{probePill(h.probe)}</td>
                <td>
                  <button className="btn btn--ghost btn--sm" onClick={() => removeHost(i)}>✕</button>
                </td>
              </tr>
            ))}
            <tr>
              <td colSpan={5}>
                <button className="btn btn--ghost btn--sm" onClick={addHost}>+ Add row</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">SSH credentials</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          {(["agent", "key", "password"] as const).map((m) => (
            <label key={m} className="hstack" style={{ gap: 6 }}>
              <input
                type="radio"
                checked={draft.ssh.authMethod === m}
                onChange={() => update({ ssh: { ...draft.ssh, authMethod: m } })}
              />
              {m === "agent" ? "Use SSH agent" : m === "key" ? "Upload key" : "Password"}
            </label>
          ))}
          <div className="field" style={{ minWidth: 160 }}>
            <label className="field-label">SSH user</label>
            <input
              className="input"
              value={draft.ssh.user}
              onChange={(e) => update({ ssh: { ...draft.ssh, user: e.target.value } })}
            />
          </div>
        </div>
      </div>

      <div>
        <button className="btn" onClick={probeAll}>Probe reachability</button>
      </div>
    </div>
  );
}
