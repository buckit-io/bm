import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMe } from "../../../api/hooks";
import { prepareLocalDeployment, previewLocalDeployment, listVersions } from "../../../api/client";
import type {
  BuckitVersion,
  LocalPrepareRequest,
  LocalPrepareResponse,
  LocalTLSConfig,
} from "../../../api/types";
import { StepperStep } from "../../../components/Stepper";
import { WizardShell } from "../../../layouts/WizardShell";
import {
  ROOT_PASSWORD_HINT,
  ROOT_USER_HINT,
  validateRootPassword,
  validateRootUser,
} from "../../../lib/credentials";
import { looksLikePemCert, looksLikePemKey } from "../../../lib/tls";
import { localRootOSDrivePaths, parseLocalDataPaths } from "./localDataPaths";
import type { ParsedLocalDataPaths } from "./localDataPaths";
import "./Wizard.css";

const FALLBACK_VERSIONS: BuckitVersion[] = [
  { tag: "v1.0.0", label: "v1.0.0 (latest stable)" },
];

const STEPS: StepperStep[] = [
  { id: "settings", label: "Settings" },
  { id: "storage", label: "Storage" },
  { id: "review", label: "Review" },
  { id: "ready", label: "Ready" },
];

const VALID_SET_SIZES = [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];

interface LocalDraft {
  version: string;
  credentials: { rootUser: string; rootPassword: string };
  api: { port: number; consolePort: number };
  tls: Required<LocalTLSConfig>;
  dataPaths: string[];
  parity: number;
  preview?: LocalPrepareResponse;
  previewError?: string;
  previewing: boolean;
  prepared?: LocalPrepareResponse;
  prepareError?: string;
  preparing: boolean;
}

function emptyDraft(): LocalDraft {
  return {
    version: "v1.0.0",
    credentials: { rootUser: "", rootPassword: "" },
    api: { port: 9000, consolePort: 9001 },
    tls: { mode: "off", certPem: "", keyPem: "", caBundlePem: "" },
    dataPaths: [""],
    parity: 0,
    previewing: false,
    preparing: false,
  };
}

export function LocalSingleNodeWizard() {
  const navigate = useNavigate();
  const { data: session } = useMe();
  const [draft, setDraft] = useState<LocalDraft>(() => emptyDraft());
  const [index, setIndex] = useState(0);
  const previewSeq = useRef(0);
  const update = (patch: Partial<LocalDraft>) =>
    setDraft((d) => ({
      ...d,
      ...patch,
      preview: patch.preview ?? d.preview,
      prepared: patch.prepared ?? d.prepared,
    }));

  const settingsInvalid = useMemo(() => {
    if (!validateRootUser(draft.credentials.rootUser).ok) return true;
    if (!validateRootPassword(draft.credentials.rootPassword).ok) return true;
    const p = draft.api.port;
    const c = draft.api.consolePort;
    if (!Number.isInteger(p) || p < 1 || p > 65535) return true;
    if (!Number.isInteger(c) || c < 1 || c > 65535) return true;
    if (p === c) return true;
    if (draft.tls.mode === "byo") {
      if (!looksLikePemCert(draft.tls.certPem)) return true;
      if (!looksLikePemKey(draft.tls.keyPem)) return true;
    }
    return false;
  }, [draft]);

  const parsedDataPaths = useMemo(() => parseLocalDataPaths(draft.dataPaths), [draft.dataPaths]);
  const pathCount = parsedDataPaths.error ? 0 : parsedDataPaths.paths.length;
  const setSize = pathCount > 1 ? computeSetSize(pathCount) : pathCount;
  const maxParity = Math.floor(setSize / 2);
  const storageInvalid =
    !!parsedDataPaths.error ||
    !!parsedDataPaths.incomplete ||
    (pathCount > 1 && setSize === 0) ||
    (pathCount > 1 && (draft.parity < 1 || draft.parity > maxParity)) ||
    (pathCount <= 1 && draft.parity !== 0);

  useEffect(() => {
    if (parsedDataPaths.error) return;
    if (parsedDataPaths.incomplete) return;
    if (pathCount > 1 && setSize === 0) return;
    const nextParity = pathCount > 1 ? defaultParityBlocks(setSize) : 0;
    if (draft.parity !== nextParity) {
      update({ parity: nextParity });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathCount, parsedDataPaths.error, parsedDataPaths.incomplete, setSize]);

  const localRequest = useMemo<LocalPrepareRequest | null>(() => {
    if (settingsInvalid || storageInvalid) return null;
    return {
      version: draft.version,
      rootUser: draft.credentials.rootUser,
      rootPassword: draft.credentials.rootPassword,
      apiPort: draft.api.port,
      consolePort: draft.api.consolePort,
      dataPaths: parsedDataPaths.paths,
      parity: pathCount > 1 ? draft.parity : undefined,
      tls: draft.tls,
    };
  }, [draft.version, draft.credentials, draft.api, draft.parity, draft.tls, parsedDataPaths.paths, pathCount, settingsInvalid, storageInvalid]);

  useEffect(() => {
    if (index !== 2 || !localRequest) return;
    const seq = ++previewSeq.current;
    setDraft((d) => ({ ...d, preview: undefined, previewing: true, previewError: undefined }));
    previewLocalDeployment(localRequest)
      .then((preview) => {
        if (seq !== previewSeq.current) return;
        setDraft((d) => ({ ...d, preview, previewing: false, previewError: undefined }));
      })
      .catch((err) => {
        if (seq !== previewSeq.current) return;
        const message = err instanceof Error ? err.message : "Failed to preview local deployment.";
        setDraft((d) => ({ ...d, previewing: false, previewError: message }));
      });
  }, [index, localRequest]);

  const nextDisabled =
    (index === 0 && settingsInvalid) ||
    (index === 1 && storageInvalid) ||
    (index === 2 && (draft.preparing || draft.previewing || !!draft.previewError));

  const prepare = async () => {
    if (!localRequest) return;
    update({ preparing: true, prepareError: undefined });
    try {
      const prepared = await prepareLocalDeployment(localRequest);
      setDraft((d) => ({ ...d, prepared, preparing: false, prepareError: undefined }));
      setIndex(3);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to prepare local deployment.";
      setDraft((d) => ({ ...d, preparing: false, prepareError: message }));
    }
  };

  const next = () => {
    if (index === 2) {
      void prepare();
      return;
    }
    setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  };
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const stepBody = (() => {
    switch (index) {
      case 0:
        return <SettingsStep draft={draft} update={update} />;
      case 1:
        return <StorageStep draft={draft} update={update} parsedDataPaths={parsedDataPaths} goos={session?.goos} />;
      case 2:
        return <ReviewStep draft={draft} parsedDataPaths={parsedDataPaths} />;
      case 3:
        return <ReadyStep draft={draft} />;
      default:
        return null;
    }
  })();

  return (
    <WizardShell
      title="Configure local Buckit"
      steps={STEPS}
      currentIndex={index}
      onJump={(i) => i <= index && setIndex(i)}
      onBack={index > 0 && index < STEPS.length - 1 ? back : undefined}
      onNext={index < STEPS.length - 1 ? next : undefined}
      nextDisabled={nextDisabled}
      nextLabel={index === 2 ? (draft.preparing ? "Preparing…" : "Prepare deploy") : "Next →"}
    >
      <div className="wizard__inner local-wizard">
        {stepBody}
        {index === 3 && (
          <div className="local-wizard__footer-actions">
            <button className="btn" onClick={() => navigate("/clusters")}>Close</button>
          </div>
        )}
      </div>
    </WizardShell>
  );
}

interface StepProps {
  draft: LocalDraft;
  update: (patch: Partial<LocalDraft>) => void;
}

function SettingsStep({ draft, update }: StepProps) {
  const [versions, setVersions] = useState<BuckitVersion[]>(FALLBACK_VERSIONS);
  const [showPass, setShowPass] = useState(false);

  useEffect(() => {
    listVersions().then(setVersions).catch(() => {});
  }, []);

  useEffect(() => {
    if (versions.length === 0) return;
    if (versions.some((v) => v.tag === draft.version)) return;
    update({ version: versions[0].tag });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versions, draft.version]);

  const userValidation = draft.credentials.rootUser.length > 0
    ? validateRootUser(draft.credentials.rootUser)
    : { ok: true as const };
  const passValidation = draft.credentials.rootPassword.length > 0
    ? validateRootPassword(draft.credentials.rootPassword)
    : { ok: true as const };
  const portsClash = draft.api.port === draft.api.consolePort;

  const setPort = (key: "port" | "consolePort", v: string) => {
    const n = parseInt(v, 10);
    update({
      api: { ...draft.api, [key]: Number.isFinite(n) ? n : 0 },
    });
  };

  return (
    <div className="vstack local-wizard__narrow">
      <header>
        <h2>Settings</h2>
        <p className="muted">Choose the Buckit binary, root credentials, ports, and TLS mode.</p>
      </header>

      <div className="field">
        <label className="field-label" htmlFor="local-version">Buckit version</label>
        <select
          id="local-version"
          className="select"
          value={draft.version}
          onChange={(e) => update({ version: e.target.value })}
        >
          {versions.map((v) => (
            <option key={v.tag} value={v.tag}>{v.label}</option>
          ))}
        </select>
      </div>

      <div className="local-wizard__grid">
        <div className="field">
          <label className="field-label" htmlFor="local-root-user">Root user</label>
          <input
            id="local-root-user"
            className="input"
            value={draft.credentials.rootUser}
            onChange={(e) =>
              update({ credentials: { ...draft.credentials, rootUser: e.target.value } })
            }
            autoComplete="off"
          />
          <span className="field-hint" style={!userValidation.ok ? { color: "var(--c-danger)" } : undefined}>
            {userValidation.ok ? ROOT_USER_HINT : userValidation.error}
          </span>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="local-root-password">Root password</label>
          <div className="hstack" style={{ gap: "var(--s-2)" }}>
            <input
              id="local-root-password"
              className="input"
              type={showPass ? "text" : "password"}
              value={draft.credentials.rootPassword}
              onChange={(e) =>
                update({ credentials: { ...draft.credentials, rootPassword: e.target.value } })
              }
              autoComplete="new-password"
            />
            <button className="btn btn--sm" onClick={() => setShowPass((s) => !s)}>
              {showPass ? "Hide" : "Show"}
            </button>
          </div>
          <span className="field-hint" style={!passValidation.ok ? { color: "var(--c-danger)" } : undefined}>
            {passValidation.ok ? ROOT_PASSWORD_HINT : passValidation.error}
          </span>
        </div>
      </div>

      <div className="local-wizard__grid">
        <div className="field">
          <label className="field-label" htmlFor="local-api-port">API port</label>
          <input
            id="local-api-port"
            className="input"
            type="number"
            min={1}
            max={65535}
            value={draft.api.port || ""}
            onChange={(e) => setPort("port", e.target.value)}
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="local-console-port">Console port</label>
          <input
            id="local-console-port"
            className="input"
            type="number"
            min={1}
            max={65535}
            value={draft.api.consolePort || ""}
            onChange={(e) => setPort("consolePort", e.target.value)}
          />
          {portsClash && <span className="field-hint" style={{ color: "var(--c-danger)" }}>Ports must be different.</span>}
        </div>
      </div>

      <div className="field">
        <label className="field-label" htmlFor="local-tls-mode">TLS</label>
        <select
          id="local-tls-mode"
          className="select"
          value={draft.tls.mode}
          onChange={(e) =>
            update({ tls: { ...draft.tls, mode: e.target.value as "off" | "byo" } })
          }
        >
          <option value="off">No TLS</option>
          <option value="byo">Bring your own certificate</option>
        </select>
      </div>

      {draft.tls.mode === "byo" && (
        <div className="vstack">
          <div className="field">
            <label className="field-label" htmlFor="local-cert">Certificate PEM</label>
            <textarea
              id="local-cert"
              className="input"
              rows={7}
              value={draft.tls.certPem}
              onChange={(e) => update({ tls: { ...draft.tls, certPem: e.target.value } })}
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="local-key">Private key PEM</label>
            <textarea
              id="local-key"
              className="input"
              rows={7}
              value={draft.tls.keyPem}
              onChange={(e) => update({ tls: { ...draft.tls, keyPem: e.target.value } })}
            />
            <span className="field-hint">
              Certificate SANs should include every hostname or IP address you will use to connect, such as localhost, 127.0.0.1, or this computer's DNS name.
            </span>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="local-ca">CA bundle PEM (optional)</label>
            <textarea
              id="local-ca"
              className="input"
              rows={5}
              value={draft.tls.caBundlePem}
              onChange={(e) => update({ tls: { ...draft.tls, caBundlePem: e.target.value } })}
            />
          </div>
        </div>
      )}
    </div>
  );
}

function StorageStep({
  draft,
  update,
  parsedDataPaths,
  goos,
}: StepProps & { parsedDataPaths: ParsedLocalDataPaths; goos?: string }) {
  const pathCount = parsedDataPaths.error ? 0 : parsedDataPaths.paths.length;
  const isErasure = pathCount > 1;
  const setSize = isErasure ? computeSetSize(pathCount) : pathCount;
  const maxParity = Math.floor(setSize / 2);
  const recommendedParity = isErasure && setSize > 0 ? defaultParityBlocks(setSize) : 0;
  const usablePct = isErasure && setSize > 0 ? Math.round((1 - draft.parity / setSize) * 100) : 100;
  const rootOSDrivePaths = parsedDataPaths.error ? [] : localRootOSDrivePaths(goos, parsedDataPaths.paths);
  const pathExample = localDataPathExample(goos);
  const setPath = (idx: number, value: string) => {
    update({ dataPaths: draft.dataPaths.map((p, i) => (i === idx ? value : p)) });
  };
  const addPath = () => {
    update({ dataPaths: [...draft.dataPaths, ""] });
  };
  const removePath = (idx: number) => {
    update({ dataPaths: draft.dataPaths.filter((_, i) => i !== idx) });
  };

  return (
    <div className="vstack local-wizard__narrow">
      <header>
        <h2>Storage</h2>
        <p className="muted">Input one or more local filesystem paths for storing object data.</p>
        <p className="muted">
          Learn about{" "}
          <a
            href="https://buckit.sh/docs/operations/concepts/erasure-coding"
            target="_blank"
            rel="noreferrer"
          >
            erasure coding ↗
          </a>{" "}
          or use the{" "}
          <a
            href="https://buckit.sh/docs/_static/ec-calculator"
            target="_blank"
            rel="noreferrer"
          >
            Erasure Code Calculator ↗
          </a>
          .
        </p>
      </header>

      {rootOSDrivePaths.length > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>{rootOSDriveWarning(rootOSDrivePaths)}</span>
        </div>
      )}

      <div className="vstack">
        {draft.dataPaths.map((path, idx) => (
          <div className="local-wizard__path-row" key={idx}>
            <div className="field" style={{ flex: 1 }}>
              <label className="field-label" htmlFor={`local-data-path-${idx}`}>
                Data path {idx + 1}
              </label>
              <input
                id={`local-data-path-${idx}`}
                className="input mono"
                value={path}
                onChange={(e) => setPath(idx, e.target.value)}
                placeholder={`e.g. ${pathExample}`}
              />
            </div>
            <button
              className="btn btn--sm"
              onClick={() => removePath(idx)}
              disabled={draft.dataPaths.length === 1}
            >
              Remove
            </button>
          </div>
        ))}
      </div>

      <button className="btn" onClick={addPath}>+ Add one more row</button>
      {parsedDataPaths.error ? (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>{parsedDataPaths.error}</span>
        </div>
      ) : isErasure && setSize === 0 ? (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>
            {pathCount} data paths cannot be divided into a supported erasure set size. Add or remove paths so the count is divisible by a value from 2 to 16.
          </span>
        </div>
      ) : parsedDataPaths.paths.length > 1 ? (
        <div className="banner banner--info">
          <span>ℹ</span>
          <span>
            Expanded to {parsedDataPaths.paths.length} data paths. Buckit will use erasure coding.
          </span>
        </div>
      ) : null}
      <p className="field-hint">
        Use one path per row or a numeric range, e.g. <span className="mono">{pathExample}</span>. Missing directories will be created. Existing directories must be empty and writable by the current user.
      </p>

      {!parsedDataPaths.error && !parsedDataPaths.incomplete && !(isErasure && setSize === 0) && (
        <div className="local-wizard__topology">
          <div className="local-wizard__grid">
            <div>
              <div className="field-label">Mode</div>
              <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>
                {isErasure ? "Erasure coded" : "Standalone"}
              </div>
              <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                {isErasure
                  ? "Multiple data paths are grouped into one local erasure set."
                  : "One data path with no erasure-coded redundancy."}
              </p>
            </div>
            <div>
              <div className="field-label">Data paths</div>
              <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>
                {pathCount || "—"}
              </div>
              <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                {isErasure ? "Path count is the local erasure set size." : "Usable capacity is the path capacity."}
              </p>
            </div>
            {isErasure && setSize > 0 ? (
              <>
                <div>
                  <div className="field-label">Set size</div>
                  <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>
                    {setSize}
                  </div>
                  <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                    {pathCount / setSize} erasure set{pathCount / setSize === 1 ? "" : "s"}.
                  </p>
                </div>
                <div>
                  <div className="field-label">Parity</div>
                  <select
                    className="select"
                    value={draft.parity}
                    onChange={(e) => update({ parity: parseInt(e.target.value, 10) })}
                  >
                    {Array.from({ length: maxParity }, (_, i) => i + 1).map((n) => (
                      <option key={n} value={n}>
                        EC:{n}{n === recommendedParity ? " (recommended)" : ""}
                      </option>
                    ))}
                  </select>
                  <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                    Max: EC:{maxParity}. Higher parity improves tolerance and reduces usable capacity.
                  </p>
                </div>
                <div>
                  <div className="field-label">Usable estimate</div>
                  <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>
                    ~{usablePct}%
                  </div>
                  <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                    {setSize - draft.parity} data shards + {draft.parity} parity shards per set.
                  </p>
                </div>
              </>
            ) : (
              <>
                <div>
                  <div className="field-label">Parity</div>
                  <div className="card-stat__value" style={{ fontSize: "var(--fs-lg)" }}>
                    None
                  </div>
                  <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
                    Add another data path to use erasure coding.
                  </p>
                </div>
              </>
            )}
          </div>
          <div className="banner banner--info">
            <span>ℹ</span>
            <span>
              {isErasure
                ? `EC:${draft.parity} tolerates loss of up to ${draft.parity} data path${draft.parity === 1 ? "" : "s"} per set.`
                : "Standalone mode does not provide erasure-coded redundancy."}
            </span>
          </div>
        </div>
      )}
    </div>
  );
}

function ReviewStep({
  draft,
  parsedDataPaths,
}: {
  draft: LocalDraft;
  parsedDataPaths: ParsedLocalDataPaths;
}) {
  const scheme = draft.tls.mode === "byo" ? "https" : "http";
  return (
    <div className="vstack local-wizard__narrow">
      <header>
        <h2>Review</h2>
        <p className="muted">Buckit will be downloaded, a start script will be created, and local directories will be prepared.</p>
      </header>

      {draft.previewing && (
        <div className="banner banner--info">
          <span>ℹ</span>
          <span>Checking local file targets…</span>
        </div>
      )}

      {draft.preview?.warnings && draft.preview.warnings.length > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            {draft.preview.warnings.map((w) => (
              <span className="local-wizard__warning-line" key={w}>{w}</span>
            ))}
          </span>
        </div>
      )}

      {draft.previewError && (
        <div className="card local-wizard__error">
          {draft.previewError}
        </div>
      )}

      <div className="card local-wizard__summary">
        <Summary label="Version" value={draft.version} />
        <Summary label="API" value={`${scheme}://127.0.0.1:${draft.api.port}`} />
        <Summary label="Console" value={`${scheme}://127.0.0.1:${draft.api.consolePort}`} />
        <Summary label="TLS" value={draft.tls.mode === "byo" ? "Enabled" : "Off"} />
        {draft.preview?.binaryPath && <Summary label="Buckit binary" value={draft.preview.binaryPath} />}
        <Summary label="Storage mode" value={parsedDataPaths.paths.length > 1 ? `Erasure coded, set size ${computeSetSize(parsedDataPaths.paths.length)}, EC:${draft.parity}` : "Standalone"} />
        <Summary label="Data paths" value={parsedDataPaths.paths.join("\n")} />
      </div>

      {draft.prepareError && (
        <div className="card local-wizard__error">
          {draft.prepareError}
        </div>
      )}
    </div>
  );
}

function ReadyStep({ draft }: { draft: LocalDraft }) {
  const prepared = draft.prepared;
  const [copyState, setCopyState] = useState<string>("");
  if (!prepared) return null;
  const readyWarnings = (prepared.warnings ?? []).filter((warning) => !isRootOSDriveWarning(warning));
  const commandOptions = prepared.windowsCmdCommand && prepared.windowsPowerShellCommand
    ? [
        { id: "cmd", label: "Command Prompt", command: prepared.windowsCmdCommand },
        { id: "powershell", label: "PowerShell", command: prepared.windowsPowerShellCommand },
      ]
    : [{ id: "terminal", label: "", command: prepared.command }];
  const importParams = new URLSearchParams({
    url: prepared.apiUrl,
    username: draft.credentials.rootUser,
  });
  const importURL = `/clusters/import?${importParams.toString()}`;
  const copyCommand = async (id: string, command: string) => {
    try {
      await navigator.clipboard.writeText(command);
      setCopyState(`${id}:copied`);
      window.setTimeout(() => setCopyState(""), 1800);
    } catch {
      setCopyState(`${id}:error`);
    }
  };
  return (
    <div className="vstack local-wizard__narrow">
      <header>
        <h2>Ready</h2>
        <p className="muted">Buckit files and configuration are setup. Complete following steps to start local Buckit server.</p>
      </header>

      <div className="local-wizard__actions">
        <section className="local-wizard__action-step">
          <span className="local-wizard__action-number">1</span>
          <div className="local-wizard__action-body">
            <h3>Start the Buckit server</h3>
            <p className="muted">{commandOptions.length > 1 ? "Choose the terminal you use." : "Run this command in a terminal."}</p>
            {commandOptions.map((option) => {
              const copied = copyState === `${option.id}:copied`;
              const copyFailed = copyState === `${option.id}:error`;
              return (
                <div key={option.id}>
                  {option.label && <p className="muted"><strong>{option.label}</strong></p>}
                  <div className="local-wizard__command-box">
                    <pre className="local-wizard__command"><span className="local-wizard__prompt">&gt;</span> {option.command}</pre>
                    <button
                      className={"local-wizard__copy-button" + (copied ? " is-copied" : "")}
                      type="button"
                      onClick={() => void copyCommand(option.id, option.command)}
                      aria-label={copied ? `Copied ${option.label || "command"}` : `Copy ${option.label || "command"}`}
                      title={copied ? "Copied" : copyFailed ? "Copy failed" : "Copy command"}
                    >
                      {copied ? <CheckIcon /> : <CopyIcon />}
                    </button>
                    {copyFailed && <span className="local-wizard__copy-error">Copy failed</span>}
                  </div>
                </div>
              );
            })}
          </div>
        </section>

        <section className="local-wizard__action-step">
          <span className="local-wizard__action-number">2</span>
          <div className="local-wizard__action-body">
            <h3>Import into Buckit Manager <span className="local-wizard__optional">(optional)</span></h3>
            <p className="muted">
              After Buckit server starts successfully, click <Link to={importURL}>here</Link> to import into Buckit Manager for easy monitoring and management.
            </p>
          </div>
        </section>
      </div>

      <div className="local-wizard__details">
        <h3>Local Buckit setup</h3>
        <div className="card local-wizard__summary">
          <Summary label="Start script" value={prepared.scriptPath} />
          <Summary label="Buckit binary" value={prepared.binaryPath} />
          <Summary label="API" value={prepared.apiUrl} />
          <Summary label="Console" value={prepared.consoleUrl} />
          <Summary label="Storage mode" value={prepared.dataPaths.length > 1 ? `Erasure coded, set size ${prepared.setSize ?? 0}, EC:${prepared.parity ?? 0}` : "Standalone"} />
          <Summary label="Data paths" value={prepared.dataPaths.join("\n")} />
        </div>
      </div>

      {readyWarnings.length > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            {readyWarnings.map((w) => (
              <span className="local-wizard__warning-line" key={w}>{w}</span>
            ))}
          </span>
        </div>
      )}

    </div>
  );
}

function Summary({ label, value }: { label: string; value: string }) {
  return (
    <div className="local-wizard__summary-row">
      <span className="field-label">{label}</span>
      <span className="mono">{value || "—"}</span>
    </div>
  );
}

function rootOSDriveWarning(paths: string[]): string {
  const label = paths.length === 1 ? "Data path" : "Data paths";
  const verb = paths.length === 1 ? "is" : "are";
  const drive = paths.length === 1 ? "drive" : "drives";
  return `${label} ${paths.join(", ")} ${verb} on the root/OS ${drive}. Using root drives is acceptable for development or testing, but it is not recommended for production deployments.`;
}

function isRootOSDriveWarning(warning: string): boolean {
  return warning.includes("root/OS drive") || warning.includes("system/root drive");
}

function localDataPathExample(goos: string | undefined): string {
  if (goos === "windows") return String.raw`D:\buckit\data{1...4}`;
  return "/Volumes/data/buckit/data{1...4}";
}

function CopyIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" width="16" height="16">
      <path d="M8 8h10v12H8z" fill="none" stroke="currentColor" strokeWidth="2" />
      <path d="M6 16H4V4h10v2" fill="none" stroke="currentColor" strokeWidth="2" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" width="16" height="16">
      <path d="m5 12 5 5L20 7" fill="none" stroke="currentColor" strokeWidth="2" />
    </svg>
  );
}

function computeSetSize(totalDrives: number): number {
  if (totalDrives <= 0) return 0;
  if (totalDrives < VALID_SET_SIZES[0]) return totalDrives;
  let best = 0;
  let bestSets = totalDrives;
  for (const size of VALID_SET_SIZES) {
    if (totalDrives % size !== 0) continue;
    const sets = totalDrives / size;
    if (best === 0 || sets <= bestSets) {
      best = size;
      bestSets = sets;
    }
  }
  return best;
}

function defaultParityBlocks(driveCount: number): number {
  if (driveCount <= 1) return 0;
  if (driveCount <= 3) return 1;
  if (driveCount <= 5) return 2;
  if (driveCount <= 7) return 3;
  return 4;
}
