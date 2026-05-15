import { useState } from "react";
import { HostRow, NewClusterDraft } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

let idCounter = 100;
function newRow(): HostRow {
  return {
    id: "h" + (idCounter++),
    hostname: "",
    port: 22,
    label: "",
    probe: "idle",
  };
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
  const [pasteOpen, setPasteOpen] = useState(false);
  const [pasteValue, setPasteValue] = useState("");

  const setHost = (i: number, patch: Partial<HostRow>) =>
    update({
      hosts: draft.hosts.map((h, idx) => (idx === i ? { ...h, ...patch } : h)),
    });
  const addHost = () => update({ hosts: [...draft.hosts, newRow()] });
  const removeHost = (i: number) =>
    update({ hosts: draft.hosts.filter((_, idx) => idx !== i) });

  const probeAll = () => {
    update({
      hosts: draft.hosts.map((h) => ({ ...h, probe: "probing" })),
    });
    setTimeout(() => {
      update({
        hosts: draft.hosts.map((h, i) => ({
          ...h,
          probe:
            !h.hostname.trim()
              ? "timeout"
              : i % 9 === 7
                ? "auth_failed"
                : "reachable",
        })),
      });
    }, 900);
  };

  const importPaste = () => {
    const rows = pasteValue
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter(Boolean)
      .map<HostRow>((line) => {
        const [hostname, portStr] = line.split(/[:\s]+/);
        return {
          id: "h" + (idCounter++),
          hostname,
          port: parseInt(portStr || "22", 10) || 22,
          label: "",
          probe: "idle",
        };
      });
    if (rows.length) update({ hosts: rows });
    setPasteOpen(false);
    setPasteValue("");
  };

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Add nodes</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Enter the hosts that will form this cluster.
          </p>
        </div>
        <button className="btn btn--sm" onClick={() => setPasteOpen(true)}>
          Paste list
        </button>
      </header>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr>
              <th>Hostname / IP</th>
              <th style={{ width: 110 }}>SSH port</th>
              <th>Label (optional)</th>
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
                    placeholder="node1.example.com"
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
                    placeholder="rack-a-1"
                  />
                </td>
                <td>{probePill(h.probe)}</td>
                <td>
                  <button
                    className="btn btn--ghost btn--sm"
                    onClick={() => removeHost(i)}
                    aria-label="Remove row"
                  >
                    ✕
                  </button>
                </td>
              </tr>
            ))}
            <tr>
              <td colSpan={5}>
                <button className="btn btn--ghost btn--sm" onClick={addHost}>
                  + Add row
                </button>
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
              {m === "agent" ? "Use SSH agent" : m === "key" ? "Upload private key" : "Password"}
            </label>
          ))}
        </div>

        <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
          <div className="field" style={{ minWidth: 200 }}>
            <label className="field-label" htmlFor="user">SSH user</label>
            <input
              id="user"
              className="input"
              value={draft.ssh.user}
              onChange={(e) => update({ ssh: { ...draft.ssh, user: e.target.value } })}
            />
          </div>
          <label className="hstack" style={{ gap: 6, alignSelf: "flex-end", paddingBottom: 6 }}>
            <input
              type="checkbox"
              checked={draft.ssh.sudo}
              onChange={(e) => update({ ssh: { ...draft.ssh, sudo: e.target.checked } })}
            />
            Use sudo (passwordless)
          </label>
        </div>
      </div>

      <div>
        <button className="btn" onClick={probeAll}>
          Probe reachability
        </button>
      </div>

      {pasteOpen && (
        <div className="modal-backdrop" onClick={() => setPasteOpen(false)}>
          <div className="card modal" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>Paste host list</h3>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              One host per line. Optional port after a colon (e.g. <span className="mono">node1.example.com:2222</span>).
            </p>
            <textarea
              className="input"
              rows={8}
              value={pasteValue}
              onChange={(e) => setPasteValue(e.target.value)}
              style={{ height: "auto", padding: "var(--s-3)", fontFamily: "var(--font-mono)" }}
              placeholder="node1.example.com&#10;node2.example.com&#10;node3.example.com"
            />
            <div className="hstack" style={{ justifyContent: "flex-end" }}>
              <button className="btn" onClick={() => setPasteOpen(false)}>Cancel</button>
              <button className="btn btn--primary" onClick={importPaste}>Import</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
