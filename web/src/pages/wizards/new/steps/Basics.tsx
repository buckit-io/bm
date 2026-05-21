import { useEffect, useRef, useState } from "react";
import { listVersions, validateCustomArtifact } from "../../../../api/client";
import type { BuckitVersion } from "../../../../api/types";
import {
  CUSTOM_VERSION,
  CustomUrlCheck,
  NewClusterDraft,
} from "../state";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

const CHECK_DEBOUNCE_MS = 700;

export function Basics({ draft, update }: Props) {
  const isCustom = draft.version === CUSTOM_VERSION;
  const [showPass, setShowPass] = useState(false);
  const [versions, setVersions] = useState<BuckitVersion[]>([]);

  // Fetch the version catalog from the backend on mount.
  useEffect(() => {
    listVersions().then(setVersions).catch(() => {});
  }, []);

  // The draft starts with a placeholder version. Once the real catalog
  // arrives, replace that stale value if it doesn't exist in the fetched list.
  useEffect(() => {
    if (draft.version === CUSTOM_VERSION) return;
    if (versions.length === 0) return;
    if (versions.some((v) => v.tag === draft.version)) return;
    update({ version: versions[0].tag });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versions, draft.version]);

  const setPort = (key: "port" | "consolePort", v: string) => {
    const n = parseInt(v, 10);
    update({
      api: {
        ...draft.api,
        [key]: Number.isFinite(n) ? n : 0,
      },
    });
  };
  const passLen = draft.credentials.rootPassword.length;
  const passWeak = passLen > 0 && passLen < 8;
  const portsClash = draft.api.port === draft.api.consolePort;

  // Debounced validation: re-run whenever customUrl changes, but only
  // when the operator is on the Custom URL path. Stale promises are
  // ignored via a generation counter so a slow check followed by a
  // faster edit doesn't clobber the newer result.
  const genRef = useRef(0);
  useEffect(() => {
    if (!isCustom) return;
    const url = draft.customUrl.trim();
    if (!url) {
      update({ customUrlCheck: { state: "idle" } });
      return;
    }
    const gen = ++genRef.current;
    update({ customUrlCheck: { state: "checking" } });
    const handle = setTimeout(async () => {
      try {
        const result = await validateCustomArtifact(url);
        if (gen !== genRef.current) return;
        update({ customUrlCheck: result as CustomUrlCheck });
      } catch (err) {
        if (gen !== genRef.current) return;
        const message = err instanceof Error ? err.message : "Validation failed.";
        update({ customUrlCheck: { state: "error", message } });
      }
    }, CHECK_DEBOUNCE_MS);
    return () => clearTimeout(handle);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.customUrl, isCustom]);

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
        <label className="field-label" htmlFor="ver">Buckit version</label>
        <select
          id="ver"
          className="select"
          value={draft.version}
          onChange={(e) => update({ version: e.target.value })}
        >
          {versions.map((v) => (
            <option key={v.tag} value={v.tag}>
              {v.label}
            </option>
          ))}
          <option value={CUSTOM_VERSION}>Custom URL…</option>
        </select>
      </div>

      {isCustom && (
        <div className="field">
          <label className="field-label" htmlFor="custom-url">
            Custom artifact URL
          </label>
          <input
            id="custom-url"
            className="input"
            value={draft.customUrl}
            onChange={(e) => update({ customUrl: e.target.value })}
            placeholder="https://example.com/buckit-v1.0.1-rc1.rpm"
            autoFocus
          />
          <CheckStatus check={draft.customUrlCheck} />
        </div>
      )}

      {/* ── Root credentials ──────────────────────────────────────── */}
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <div className="field-label" style={{ fontSize: "var(--fs-md)" }}>
          Root credentials
        </div>
        <div className="field">
          <label className="field-label" htmlFor="root-user">Root user</label>
          <input
            id="root-user"
            className="input"
            value={draft.credentials.rootUser}
            onChange={(e) =>
              update({
                credentials: {
                  ...draft.credentials,
                  // Strip any disallowed keystrokes on input. Letters,
                  // digits, underscores, and dashes only. The operator
                  // sees only valid characters land in the field.
                  rootUser: e.target.value.replace(/[^a-zA-Z0-9_-]/g, ""),
                },
              })
            }
            placeholder="admin"
            autoComplete="off"
            inputMode="text"
          />
          <span className="field-hint">
            Letters, digits, <span className="mono">_</span> and{" "}
            <span className="mono">-</span> only. Min 3 characters.
          </span>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="root-pass">Root password</label>
          <div className="hstack" style={{ gap: "var(--s-2)" }}>
            <input
              id="root-pass"
              type={showPass ? "text" : "password"}
              className="input"
              value={draft.credentials.rootPassword}
              onChange={(e) =>
                update({
                  credentials: {
                    ...draft.credentials,
                    rootPassword: e.target.value,
                  },
                })
              }
              autoComplete="new-password"
              style={{ flex: 1 }}
            />
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => setShowPass((v) => !v)}
            >
              {showPass ? "Hide" : "Show"}
            </button>
          </div>
          <span
            className="field-hint"
            style={passWeak ? { color: "var(--c-danger)" } : undefined}
          >
            {passWeak
              ? `Min 8 characters (currently ${passLen}).`
              : "Min 8 characters. 16+ recommended."}
          </span>
        </div>
      </div>

      {/* ── Ports ─────────────────────────────────────────────────── */}
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <div className="field-label" style={{ fontSize: "var(--fs-md)" }}>
          Ports
        </div>
        <div className="hstack" style={{ gap: "var(--s-4)", flexWrap: "wrap" }}>
          <div className="field" style={{ minWidth: 140 }}>
            <label className="field-label" htmlFor="api-port">S3 API</label>
            <input
              id="api-port"
              className="input"
              type="number"
              min={1}
              max={65535}
              value={draft.api.port}
              onChange={(e) => setPort("port", e.target.value)}
            />
          </div>
          <div className="field" style={{ minWidth: 140 }}>
            <label className="field-label" htmlFor="console-port">Console</label>
            <input
              id="console-port"
              className="input"
              type="number"
              min={1}
              max={65535}
              value={draft.api.consolePort}
              onChange={(e) => setPort("consolePort", e.target.value)}
            />
          </div>
        </div>
        {portsClash && (
          <span className="field-hint" style={{ color: "var(--c-danger)" }}>
            S3 API and Console ports must differ.
          </span>
        )}
      </div>

      {/* ── Region + Server URL ───────────────────────────────────── */}
      <div className="field">
        <label className="field-label" htmlFor="region">Region</label>
        <input
          id="region"
          className="input"
          value={draft.region}
          onChange={(e) => update({ region: e.target.value })}
          placeholder="us-east-1"
        />
        <span className="field-hint">
          Returned in S3 headers. Some clients require a specific value.
        </span>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="server-url">Server URL</label>
        <input
          id="server-url"
          className="input"
          value={draft.serverUrl}
          onChange={(e) => update({ serverUrl: e.target.value })}
          placeholder={`http://<first host>:${draft.api.port}`}
        />
        <span className="field-hint">
          Used in pre-signed URLs and admin responses. Prefer the load
          balancer URL when one fronts the cluster — pre-signed links
          will survive node failures. Otherwise leave blank to derive
          from the first host at deploy time.
        </span>
      </div>
    </div>
  );
}

function CheckStatus({ check }: { check: CustomUrlCheck }) {
  if (check.state === "idle") {
    return (
      <span className="field-hint">
        Paste a direct URL to a <span className="mono">.rpm</span>,{" "}
        <span className="mono">.deb</span>, or raw binary. We'll verify it's
        reachable.
      </span>
    );
  }
  if (check.state === "checking") {
    return <span className="field-hint">Checking URL…</span>;
  }
  if (check.state === "valid") {
    return (
      <span className="field-hint" style={{ color: "var(--c-success)" }}>
        ✓ {check.message}
        {check.sizeBytes && ` · ${Math.round(check.sizeBytes / 1024 / 1024)} MB`}
      </span>
    );
  }
  if (check.state === "warn") {
    return (
      <span className="field-hint" style={{ color: "var(--c-warning)" }}>
        ⚠ {check.message} You can proceed at your own risk.
      </span>
    );
  }
  return (
    <span className="field-hint" style={{ color: "var(--c-danger)" }}>
      ✗ {check.message}
    </span>
  );
}
