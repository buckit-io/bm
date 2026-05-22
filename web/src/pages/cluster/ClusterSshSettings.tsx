// Per-cluster SSH credentials configuration page. Routed at
// /clusters/:clusterId/ssh and reached from the "Configure SSH
// credentials" item in the Cluster Actions menu.
//
// The form mirrors the migrate wizard's SSH step so the operator sees
// the same controls regardless of where they configure SSH from. Hosts
// come from the cluster's node list and are rendered read-only; the
// operator configures default credentials and may override per host.

import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useCluster, useNodes } from "../../api/hooks";
import { Node } from "../../api/types";
import {
  getClusterSshConfig,
  newClusterDiscover,
  saveClusterSshConfig,
} from "../../api/client";
import { SshOverrideFields } from "../wizards/shared/SshOverrideFields";
import {
  HostRow,
  SshCreds,
  SshOverrides,
} from "../wizards/new/state";

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

function hostsFromNodes(
  nodes: Node[],
  overrides: Record<string, SshOverrides> = {},
): HostRow[] {
  return nodes.map((n) => ({
    id: n.id,
    hostname: n.hostname,
    port: 22,
    probe: "idle",
    sshOverride: overrides[n.id],
  }));
}

const DEFAULT_SSH: SshCreds = {
  authMethod: "agent",
  user: "",
  sudo: true,
};

export function ClusterSshSettings() {
  const { clusterId = "" } = useParams();
  const navigate = useNavigate();
  const { data: cluster, isLoading: loadingCluster } = useCluster(clusterId);
  const { data: nodes, isLoading: loadingNodes } = useNodes(clusterId);

  const [ssh, setSsh] = useState<SshCreds>(DEFAULT_SSH);
  const [hosts, setHosts] = useState<HostRow[]>([]);
  const [probing, setProbing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [configLoaded, setConfigLoaded] = useState(false);
  // True once we've fetched config AND nodes have arrived. Used to
  // gate the form so we don't briefly render with hardcoded defaults
  // before the existing config seeds in.
  const ready = configLoaded && !!nodes;

  // Fetch existing config + seed hosts from the cluster's nodes.
  // We fetch the config eagerly; the result is paired with nodes as
  // soon as both are available.
  useEffect(() => {
    if (!clusterId) return;
    let cancelled = false;
    getClusterSshConfig(clusterId).then((cfg) => {
      if (cancelled) return;
      if (cfg) setSsh(cfg.ssh);
      // Stash overrides on a ref-like state until nodes arrive.
      setPendingOverrides(cfg?.overrides ?? {});
      setConfigLoaded(true);
    });
    return () => {
      cancelled = true;
    };
  }, [clusterId]);

  const [pendingOverrides, setPendingOverrides] = useState<
    Record<string, SshOverrides>
  >({});

  // Apply nodes + overrides into the hosts state once both are ready.
  useEffect(() => {
    if (!nodes) return;
    setHosts(hostsFromNodes(nodes, pendingOverrides));
  }, [nodes, pendingOverrides]);

  const setHostOverride = (id: string, override: SshOverrides | undefined) =>
    setHosts((prev) =>
      prev.map((h) => (h.id === id ? { ...h, sshOverride: override } : h)),
    );

  const probeAll = async () => {
    setProbing(true);
    setHosts((prev) => prev.map((h) => ({ ...h, probe: "probing" })));
    const targets = hosts.filter((h) => h.hostname.trim());
    if (targets.length === 0) {
      setProbing(false);
      return;
    }
    try {
      const resp = await newClusterDiscover({
        hosts: targets.map((h) => ({
          id: h.id,
          hostname: h.hostname,
          port: h.port || 22,
          probe: h.probe,
          sshOverride: h.sshOverride,
        })),
        ssh,
      });
      const results = resp as unknown as Record<
        string,
        { state: string; error?: string }
      >;
      setHosts((prev) =>
        prev.map((h) => {
          if (!h.hostname.trim()) return { ...h, probe: "idle" } as HostRow;
          const r = results[h.id];
          if (!r) return { ...h, probe: "timeout" } as HostRow;
          if (r.state === "done") return { ...h, probe: "reachable" } as HostRow;
          const err = (r.error ?? "").toLowerCase();
          const authFailed =
            err.includes("auth") || err.includes("permission") ||
            err.includes("publickey") || err.includes("password");
          return { ...h, probe: authFailed ? "auth_failed" : "timeout" } as HostRow;
        }),
      );
    } catch {
      setHosts((prev) =>
        prev.map((h) =>
          h.hostname.trim()
            ? ({ ...h, probe: "timeout" } as HostRow)
            : ({ ...h, probe: "idle" } as HostRow),
        ),
      );
    } finally {
      setProbing(false);
    }
  };

  const canSave = useMemo(() => {
    if (!ssh.user.trim()) return false;
    if (ssh.authMethod === "key" && !ssh.keyPath?.trim()) return false;
    if (ssh.authMethod === "password" && !ssh.password) return false;
    return true;
  }, [ssh]);

  const onSave = async () => {
    if (!clusterId) return;
    setSaving(true);
    const overrides: Record<string, SshOverrides> = {};
    for (const h of hosts) {
      if (h.sshOverride) overrides[h.id] = h.sshOverride;
    }
    try {
      await saveClusterSshConfig(clusterId, { ssh, overrides });
      navigate(`/clusters/${clusterId}`);
    } finally {
      setSaving(false);
    }
  };

  if (loadingCluster || loadingNodes || !ready) {
    return <p className="muted">Loading…</p>;
  }
  if (!cluster) {
    return <p className="muted">Cluster not found.</p>;
  }

  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 920 }}>
      <header className="vstack" style={{ gap: 4 }}>
        <Link
          to={`/clusters/${cluster.id}`}
          className="subtle"
          style={{ fontSize: "var(--fs-sm)" }}
        >
          ← {cluster.name}
        </Link>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          SSH credentials
        </h2>
        <p
          className="muted"
          style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
        >
          Buckit Manager uses SSH to install upgrades, swap systemd
          units, and run rolling restarts on this cluster. Configure
          the default credentials below; override per-host if any node
          differs.
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
                checked={ssh.authMethod === opt.value}
                onChange={() => setSsh({ ...ssh, authMethod: opt.value })}
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

        {ssh.authMethod === "key" && (
          <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
            <div className="field" style={{ minWidth: 320, flex: 1 }}>
              <label className="field-label">Key file path</label>
              <input
                className="input"
                value={ssh.keyPath ?? ""}
                onChange={(e) => setSsh({ ...ssh, keyPath: e.target.value })}
                placeholder="~/.ssh/id_ed25519 or ~/.ssh/id_rsa"
              />
            </div>
            <div className="field" style={{ minWidth: 200 }}>
              <label className="field-label">Passphrase (optional)</label>
              <input
                type="password"
                className="input"
                value={ssh.keyPassphrase ?? ""}
                onChange={(e) =>
                  setSsh({ ...ssh, keyPassphrase: e.target.value })
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
            <label className="field-label" htmlFor="cluster-ssh-user">
              SSH user
            </label>
            <div />
            <input
              id="cluster-ssh-user"
              className="input"
              value={ssh.user}
              onChange={(e) => setSsh({ ...ssh, user: e.target.value })}
              placeholder="buckit"
            />
            <label
              className="hstack"
              style={{
                gap: 6,
                opacity: ssh.user.trim() === "root" ? 0.55 : 1,
              }}
              title={
                ssh.user.trim() === "root"
                  ? "Not needed when connecting as root."
                  : undefined
              }
            >
              <input
                type="checkbox"
                checked={ssh.user.trim() === "root" ? false : ssh.sudo}
                disabled={ssh.user.trim() === "root"}
                onChange={(e) => setSsh({ ...ssh, sudo: e.target.checked })}
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

        {ssh.authMethod === "password" && (
          <div className="field" style={{ width: 200 }}>
            <label className="field-label">SSH password</label>
            <input
              type="password"
              className="input"
              value={ssh.password ?? ""}
              onChange={(e) => setSsh({ ...ssh, password: e.target.value })}
            />
          </div>
        )}
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
          <h3 className="card-stat__title">Hosts ({hosts.length})</h3>
          <button
            className="btn btn--sm"
            onClick={probeAll}
            disabled={probing || hosts.length === 0}
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
            {hosts.flatMap((h) => [
              <tr key={h.id}>
                <td className="mono">{h.hostname}</td>
                <td className="mono subtle">{h.port}</td>
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
                      defaults={ssh}
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

      <div className="hstack" style={{ justifyContent: "flex-end", gap: "var(--s-2)" }}>
        <button
          className="btn"
          onClick={() => navigate(`/clusters/${cluster.id}`)}
        >
          Cancel
        </button>
        <button
          className="btn btn--primary"
          onClick={onSave}
          disabled={!canSave || saving}
        >
          {saving ? "Saving…" : "Save"}
        </button>
      </div>
    </div>
  );
}

function probePill(p: HostRow["probe"]) {
  const map: Record<HostRow["probe"], { text: string; cls: string }> = {
    idle: { text: "—", cls: "subtle" },
    probing: { text: "Probing…", cls: "subtle" },
    reachable: { text: "✓ Reachable", cls: "" },
    auth_failed: { text: "✗ Auth failed", cls: "" },
    timeout: { text: "✗ Timeout", cls: "" },
  };
  const v = map[p];
  return <span className={v.cls}>{v.text}</span>;
}
