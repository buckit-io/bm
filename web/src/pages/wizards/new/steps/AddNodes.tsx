import { useState } from "react";
import { HostRow, NewClusterDraft, SshOverrides } from "../state";
import { Pill } from "../../../../components/Pill";
import { expandHostPattern } from "./hostExpansion";
import { detectHostnamePattern } from "./hostnamePattern";
import { SshOverrideFields } from "../../shared/SshOverrideFields";

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
  const setHostOverride = (id: string, override: SshOverrides | undefined) =>
    update({
      hosts: draft.hosts.map((h) =>
        h.id === id ? { ...h, sshOverride: override } : h,
      ),
    });
  const addHost = () => update({ hosts: [...draft.hosts, newRow()] });
  const removeHost = (i: number) =>
    update({ hosts: draft.hosts.filter((_, idx) => idx !== i) });

  // Expand row `i` if its hostname contains a MinIO-style `{N...M}`
  // pattern. The current row becomes the first expanded hostname; the
  // rest are inserted immediately after, inheriting port and label.
  const expandRow = (i: number) => {
    const row = draft.hosts[i];
    if (!row) return;
    const expanded = expandHostPattern(row.hostname);
    if (expanded.length <= 1) return;
    const rows: HostRow[] = expanded.map((hostname, k) => ({
      id: k === 0 ? row.id : "h" + idCounter++,
      hostname,
      port: row.port,
      probe: "idle",
    }));
    update({
      hosts: [
        ...draft.hosts.slice(0, i),
        ...rows,
        ...draft.hosts.slice(i + 1),
      ],
    });
  };

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
    const rows: HostRow[] = [];
    for (const raw of pasteValue.split(/\r?\n/)) {
      const line = raw.trim();
      if (!line) continue;
      // Pull off a trailing `:NNN` SSH port hint only if there's no
      // expansion brace in the line — `{1...4}` itself contains `:`-less
      // colons but the port-strip in expandHostPattern handles the
      // scheme/port stripping internally.
      const looksLikePattern = line.includes("{");
      let portHint = 22;
      let payload = line;
      if (!looksLikePattern) {
        const m = line.match(/^(.+?):(\d+)$/);
        if (m) {
          payload = m[1];
          portHint = parseInt(m[2], 10) || 22;
        }
      }
      for (const hostname of expandHostPattern(payload)) {
        rows.push({
          id: "h" + idCounter++,
          hostname,
          port: portHint,
          probe: "idle",
        });
      }
    }
    if (rows.length) update({ hosts: rows });
    setPasteOpen(false);
    setPasteValue("");
  };

  const hostnames = draft.hosts
    .map((h) => h.hostname.trim())
    .filter(Boolean);
  const patternFit =
    hostnames.length >= 2 ? detectHostnamePattern(hostnames) : null;

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

      {patternFit?.fits && (
        <div className="banner banner--info">
          <span>✓</span>
          <span>
            Hostnames fit one pool —{" "}
            <span className="mono">{patternFit.pattern}</span>.
          </span>
        </div>
      )}
      {patternFit && !patternFit.fits && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            Hostnames don't fit a single brace-expansion pattern (
            {patternFit.reason}). All hosts in a pool must share one
            pattern — consider DNS aliases (e.g.{" "}
            <span className="mono">node{"{1...N}"}.example.com</span>) or
            renaming hosts before deploy.
          </span>
        </div>
      )}

      <div className="card card--table">
        <table className="table table--center">
          <thead>
            <tr>
              <th>Hostname / IP</th>
              <th style={{ width: 110 }}>SSH port</th>
              <th
                style={{ width: 170 }}
                title="When on, this host overrides the cluster-wide SSH config."
              >
                Custom SSH credentials
              </th>
              <th style={{ width: 130 }}>Probe</th>
              <th style={{ width: 40 }}></th>
            </tr>
          </thead>
          <tbody>
            {draft.hosts.flatMap((h, i) => [
              <tr key={h.id}>
                <td>
                  <input
                    className="input"
                    value={h.hostname}
                    onChange={(e) => setHost(i, { hostname: e.target.value, probe: "idle" })}
                    onBlur={() => expandRow(i)}
                    placeholder="hostname1 or hostname{1...4}"
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
                    type="checkbox"
                    className="toggle"
                    checked={!!h.sshOverride}
                    onChange={(e) =>
                      setHostOverride(h.id, e.target.checked ? {} : undefined)
                    }
                    aria-label="Custom SSH credentials"
                    title={
                      h.sshOverride
                        ? "Custom credentials active — toggle off to use cluster defaults"
                        : "Toggle on to set custom SSH credentials for this host"
                    }
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
              </tr>,
              h.sshOverride && (
                <tr key={h.id + "-override"}>
                  <td colSpan={5} style={{ padding: 0 }}>
                    <SshOverrideFields
                      defaults={draft.ssh}
                      value={h.sshOverride}
                      onChange={(o) => setHostOverride(h.id, o)}
                    />
                  </td>
                </tr>
              ),
            ])}
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
        <h3 className="card-stat__title">Default SSH credentials</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          {(["agent", "key", "password"] as const).map((m) => (
            <label key={m} className="hstack" style={{ gap: 6 }}>
              <input
                type="radio"
                checked={draft.ssh.authMethod === m}
                onChange={() => update({ ssh: { ...draft.ssh, authMethod: m } })}
              />
              {m === "agent" ? "Use SSH agent" : m === "key" ? "Use key file" : "Password"}
            </label>
          ))}
        </div>

        {draft.ssh.authMethod === "key" && (
          <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
            <div className="field" style={{ minWidth: 320, flex: 1 }}>
              <label className="field-label" htmlFor="key-path">Key file path</label>
              <input
                id="key-path"
                className="input"
                value={draft.ssh.keyPath ?? ""}
                onChange={(e) =>
                  update({ ssh: { ...draft.ssh, keyPath: e.target.value } })
                }
                placeholder="~/.ssh/id_ed25519"
              />
              <span className="field-hint">
                Path the bm process can read. Same key you'd pass to{" "}
                <span className="mono">ssh -i</span>.
              </span>
            </div>
            <div className="field" style={{ minWidth: 200 }}>
              <label className="field-label" htmlFor="key-pass">
                Passphrase (optional)
              </label>
              <input
                id="key-pass"
                type="password"
                className="input"
                value={draft.ssh.keyPassphrase ?? ""}
                onChange={(e) =>
                  update({ ssh: { ...draft.ssh, keyPassphrase: e.target.value } })
                }
                placeholder="leave blank for unencrypted keys"
              />
            </div>
          </div>
        )}

        {draft.ssh.authMethod === "password" && (
          <div className="field" style={{ maxWidth: 320 }}>
            <label className="field-label" htmlFor="ssh-pw">SSH password</label>
            <input
              id="ssh-pw"
              type="password"
              className="input"
              value={draft.ssh.password ?? ""}
              onChange={(e) =>
                update({ ssh: { ...draft.ssh, password: e.target.value } })
              }
            />
          </div>
        )}

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
              One host per line. Optional SSH port after a colon (e.g.{" "}
              <span className="mono">node1.example.com:2222</span>).
              Expansion is supported —{" "}
              <span className="mono">node{"{1...4}"}.example.net</span> or{" "}
              <span className="mono">node{"{1..4}"}.example.net</span>{" "}
              expands to four hosts.
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
