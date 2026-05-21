// Inline SSH override fields rendered below a host row when the
// operator turns off "Use default SSH credentials" for that host.
// Stateless / controlled: the parent owns `host.sshOverride` and passes
// down a setter. Cluster defaults are shown as placeholders so the
// operator sees what they're diverging from.

import { SshCreds, SshOverrides } from "../new/state";

interface Props {
  defaults: SshCreds;
  value: SshOverrides;
  onChange: (next: SshOverrides) => void;
}

type AuthMethod = SshCreds["authMethod"];

export function SshOverrideFields({ defaults, value, onChange }: Props) {
  const authMethod: AuthMethod = value.authMethod ?? defaults.authMethod;
  const patch = (p: Partial<SshOverrides>) => onChange({ ...value, ...p });

  return (
    <div
      className="vstack"
      style={{
        gap: "var(--s-3)",
        padding: "var(--s-3) var(--s-4)",
        background: "var(--c-surface-2)",
        borderTop: "1px solid var(--c-border)",
      }}
    >
      <div className="field">
        <span className="field-label">Authentication method</span>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          {(["agent", "key", "password"] as const).map((m) => (
            <label key={m} className="hstack" style={{ gap: 6 }}>
              <input
                type="radio"
                checked={authMethod === m}
                onChange={() => patch({ authMethod: m })}
              />
              {labelFor(m)}
            </label>
          ))}
        </div>
      </div>

      <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
        <div className="field" style={{ minWidth: 200 }}>
          <label className="field-label">SSH user</label>
          <input
            className="input"
            value={value.user ?? ""}
            onChange={(e) => patch({ user: e.target.value })}
            placeholder={defaults.user}
          />
        </div>
        {(() => {
          // Effective SSH user: override.user if set, else cluster default.
          const effectiveUser = (value.user ?? defaults.user).trim();
          const isRoot = effectiveUser === "root";
          return (
            <label
              className="hstack"
              style={{
                gap: 6,
                alignSelf: "flex-end",
                paddingBottom: 6,
                opacity: isRoot ? 0.55 : 1,
              }}
              title={isRoot ? "Not needed when connecting as root." : undefined}
            >
              <input
                type="checkbox"
                checked={isRoot ? false : (value.sudo ?? defaults.sudo)}
                disabled={isRoot}
                onChange={(e) =>
                  patch({
                    sudo:
                      e.target.checked === defaults.sudo
                        ? undefined
                        : e.target.checked,
                  })
                }
              />
              Use sudo (passwordless)
            </label>
          );
        })()}
      </div>

      {authMethod === "key" && (
        <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
          <div className="field" style={{ minWidth: 320, flex: 1 }}>
            <label className="field-label">Key file path</label>
            <input
              className="input"
              value={value.keyPath ?? ""}
              onChange={(e) => patch({ keyPath: e.target.value })}
              placeholder={
                defaults.keyPath || "~/.ssh/id_ed25519 or ~/.ssh/id_rsa"
              }
            />
          </div>
          <div className="field" style={{ minWidth: 200 }}>
            <label className="field-label">Passphrase (optional)</label>
            <input
              type="password"
              className="input"
              value={value.keyPassphrase ?? ""}
              onChange={(e) => patch({ keyPassphrase: e.target.value })}
            />
          </div>
        </div>
      )}

      {authMethod === "password" && (
        <div className="field" style={{ width: 200 }}>
          <label className="field-label">SSH password</label>
          <input
            type="password"
            className="input"
            value={value.password ?? ""}
            onChange={(e) => patch({ password: e.target.value })}
          />
        </div>
      )}
    </div>
  );
}

function labelFor(m: AuthMethod): string {
  if (m === "agent") return "SSH agent";
  if (m === "key") return "Key file";
  return "Password";
}
