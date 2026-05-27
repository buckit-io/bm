// SSH credentials step for the migrate wizard. The cluster was already
// imported with S3 admin credentials; this step adds the SSH credentials
// bm needs to stop the running MinIO process, install Buckit, and start
// the new service on each host.
//
// Hosts come from the imported cluster — rendered read-only here so the
// operator sees which machines they're configuring SSH for.

import { useState } from "react";
import {
  HostRow,
  MigrationDraft,
  SshCreds,
  SshOverrides,
} from "../state";
import { SshOverrideFields } from "../../shared/SshOverrideFields";
import { newClusterDiscover } from "../../../../api/client";

const AUTH_OPTIONS: {
  value: SshCreds["authMethod"];
  label: string;
  hint: string;
}[] = [
  {
    value: "agent",
    label: "Use SSH agent",
    hint:
      "Run ssh-add to load your keys into ssh-agent; Buckit Manager will authenticate through the agent.",
  },
  {
    value: "key",
    label: "Use key file",
    hint:
      "Point Buckit Manager at a private key file on disk. Typically ~/.ssh/id_ed25519 or ~/.ssh/id_rsa.",
  },
  {
    value: "password",
    label: "Password",
    hint:
      "Authenticate with an SSH password. Most production servers disable this — use only for legacy hosts.",
  },
];

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

export function SshCredentials({ draft, update }: Props) {
  const [probing, setProbing] = useState(false);

  const setHostOverride = (id: string, override: SshOverrides | undefined) =>
    update({
      hosts: draft.hosts.map((h) =>
        h.id === id ? { ...h, sshOverride: override } : h,
      ),
    });

  const setHostPort = (id: string, port: number) =>
    update({
      hosts: draft.hosts.map((h) =>
        h.id === id ? { ...h, port, probe: "idle" } : h,
      ),
    });

  const probeAll = async () => {
    setProbing(true);
    update({
      hosts: draft.hosts.map((h) =>
        h.hostname.trim()
          ? ({ ...h, probe: "probing" } as HostRow)
          : ({ ...h, probe: "idle" } as HostRow),
      ),
    });
    const targets = draft.hosts.filter((h) => h.hostname.trim());
    if (targets.length === 0) {
      setProbing(false);
      return;
    }
    try {
      // Reuse the new-cluster discover endpoint — it's a generic SSH
      // probe (not bound to a committed cluster). The migrate wizard
      // doesn't need the full discovery payload here, only the per-host
      // reachability state.
      const resp = await newClusterDiscover({
        hosts: targets.map((h) => ({
          id: h.id,
          hostname: h.hostname,
          port: h.port || 22,
          probe: h.probe,
          sshOverride: h.sshOverride,
        })),
        ssh: draft.ssh,
      });
      const results = resp as unknown as Record<
        string,
        { state: string; error?: string }
      >;
      update({
        hosts: draft.hosts.map((h) => {
          if (!h.hostname.trim()) return { ...h, probe: "idle" } as HostRow;
          const r = results[h.id];
          if (!r) return { ...h, probe: "timeout" } as HostRow;
          if (r.state === "done") return { ...h, probe: "reachable" } as HostRow;
          const err = (r.error ?? "").toLowerCase();
          const authFailed =
            err.includes("auth") ||
            err.includes("permission") ||
            err.includes("publickey") ||
            err.includes("password");
          return {
            ...h,
            probe: authFailed ? "auth_failed" : "timeout",
          } as HostRow;
        }),
      });
    } catch {
      update({
        hosts: draft.hosts.map((h) =>
          h.hostname.trim()
            ? ({ ...h, probe: "timeout" } as HostRow)
            : ({ ...h, probe: "idle" } as HostRow),
        ),
      });
    } finally {
      setProbing(false);
    }
  };

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          SSH credentials
        </h2>
        <p
          className="muted"
          style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
        >
          Buckit Manager needs SSH access to each host to install
          Buckit, swap the systemd unit, and start the new service.
          Configure the default credentials below; override per-host if
          any node differs.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Default SSH credentials</h3>
        <div className="vstack" style={{ gap: "var(--s-2)" }}>
          {AUTH_OPTIONS.map((opt) => (
            <label
              key={opt.value}
              className="hstack"
              style={{ gap: "var(--s-2)", alignItems: "flex-start" }}
            >
              <input
                type="radio"
                checked={draft.ssh.authMethod === opt.value}
                onChange={() =>
                  update({ ssh: { ...draft.ssh, authMethod: opt.value } })
                }
                style={{ marginTop: 4 }}
              />
              <div className="vstack" style={{ gap: 2 }}>
                <span>{opt.label}</span>
                <span
                  className="subtle"
                  style={{ fontSize: "var(--fs-xs)" }}
                >
                  {opt.hint}
                </span>
              </div>
            </label>
          ))}
        </div>

        {draft.ssh.authMethod === "key" && (
          <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
            <div className="field" style={{ minWidth: 320, flex: 1 }}>
              <label className="field-label">Key file path</label>
              <input
                className="input"
                value={draft.ssh.keyPath ?? ""}
                onChange={(e) =>
                  update({ ssh: { ...draft.ssh, keyPath: e.target.value } })
                }
                placeholder="~/.ssh/id_ed25519 or ~/.ssh/id_rsa"
              />
            </div>
            <div className="field" style={{ minWidth: 200 }}>
              <label className="field-label">Passphrase (optional)</label>
              <input
                type="password"
                className="input"
                value={draft.ssh.keyPassphrase ?? ""}
                onChange={(e) =>
                  update({
                    ssh: { ...draft.ssh, keyPassphrase: e.target.value },
                  })
                }
              />
            </div>
          </div>
        )}

        <div className="vstack" style={{ gap: "var(--s-2)" }}>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "minmax(200px, 220px) max-content",
              gap: "var(--s-2) var(--s-4)",
              alignItems: "center",
            }}
          >
            <label className="field-label" htmlFor="migrate-ssh-user">
              SSH user
            </label>
            <div />
            <input
              id="migrate-ssh-user"
              className="input"
              value={draft.ssh.user}
              onChange={(e) =>
                update({ ssh: { ...draft.ssh, user: e.target.value } })
              }
              placeholder="buckit"
            />
            <label
              className="hstack"
              style={{
                gap: 6,
                opacity: draft.ssh.user.trim() === "root" ? 0.55 : 1,
              }}
              title={
                draft.ssh.user.trim() === "root"
                  ? "Not needed when connecting as root."
                  : undefined
              }
            >
              <input
                type="checkbox"
                checked={draft.ssh.user.trim() === "root" ? false : draft.ssh.sudo}
                disabled={draft.ssh.user.trim() === "root"}
                onChange={(e) =>
                  update({ ssh: { ...draft.ssh, sudo: e.target.checked } })
                }
              />
              Use sudo (passwordless)
            </label>
          </div>
          <span className="field-hint">
            Use <span className="mono">root</span>, or a non-root SSH user
            with passwordless <span className="mono">sudo</span> that can
            install packages, write <span className="mono">/etc</span>{" "}
            config, prepare storage directories, and manage the systemd
            service.
          </span>
        </div>

        {draft.ssh.authMethod === "password" && (
          <div className="field" style={{ width: 200 }}>
            <label className="field-label">SSH password</label>
            <input
              type="password"
              className="input"
              value={draft.ssh.password ?? ""}
              onChange={(e) =>
                update({ ssh: { ...draft.ssh, password: e.target.value } })
              }
            />
          </div>
        )}

        <div className="vstack" style={{ gap: 2 }}>
          <label className="hstack" style={{ gap: 8, alignItems: "center" }}>
            <input
              type="checkbox"
              checked={draft.persistSsh}
              onChange={(e) => update({ persistSsh: e.target.checked })}
            />
            <span>Save these credentials for future operations</span>
          </label>
          <span
            className="field-hint"
            style={{ paddingLeft: 24 }}
          >
            Stored encrypted in Buckit Manager's local database so
            post-migration ops (restart, upgrade, redeploy) don't have
            to ask again. Passwords are sealed with AES-GCM.
          </span>
        </div>
      </div>

      <div className="card card--table">
        <div
          className="hstack"
          style={{
            padding: "var(--s-3) var(--s-4)",
            borderBottom: "1px solid var(--c-border)",
            justifyContent: "space-between",
          }}
        >
          <h3 className="card-stat__title">Hosts ({draft.hosts.length})</h3>
          <button
            className="btn btn--sm"
            onClick={probeAll}
            disabled={probing}
          >
            {probing ? "Testing…" : "Test SSH connection"}
          </button>
        </div>
        <table className="table table--center">
          <thead>
            <tr>
              <th>Hostname / IP</th>
              <th style={{ width: 110 }}>SSH port</th>
              <th
                style={{ width: 170 }}
                title="When on, this host overrides the default SSH config."
              >
                Custom SSH credentials
              </th>
              <th style={{ width: 130 }}>Probe</th>
            </tr>
          </thead>
          <tbody>
            {[...draft.hosts]
              .sort((a, b) =>
                a.hostname.localeCompare(b.hostname, undefined, { numeric: true }),
              )
              .flatMap((h) => [
              <tr key={h.id}>
                <td className="mono">{h.hostname}</td>
                <td>
                  <input
                    className="input"
                    type="number"
                    min={1}
                    max={65535}
                    value={h.port}
                    onChange={(e) =>
                      setHostPort(h.id, parseInt(e.target.value, 10) || 22)
                    }
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
                  />
                </td>
                <td>{probePill(h.probe)}</td>
              </tr>,
              h.sshOverride && (
                <tr key={h.id + "-override"}>
                  <td colSpan={4} style={{ padding: 0 }}>
                    <SshOverrideFields
                      defaults={draft.ssh}
                      value={h.sshOverride}
                      onChange={(o) => setHostOverride(h.id, o)}
                    />
                  </td>
                </tr>
              ),
              ])}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function probePill(p: HostRow["probe"]) {
  const map: Record<
    HostRow["probe"],
    { text: string; cls: string; color?: string }
  > = {
    idle: { text: "—", cls: "subtle" },
    probing: { text: "Probing…", cls: "subtle" },
    reachable: { text: "✓ Reachable", cls: "", color: "var(--c-success)" },
    auth_failed: { text: "✗ Auth failed", cls: "", color: "var(--c-danger)" },
    timeout: { text: "✗ Timeout", cls: "", color: "var(--c-danger)" },
  };
  const v = map[p];
  return (
    <span className={v.cls} style={v.color ? { color: v.color } : undefined}>
      {v.text}
    </span>
  );
}
