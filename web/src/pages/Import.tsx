import { FormEvent, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { commitImport } from "../api/client";
import { discoverClusterStream, SseError } from "../api/sse";
import type {
  DiscoveryProgress,
  ImportCandidate,
  ImportError,
} from "../api/types";
import { formatBytes } from "../lib/format";
import "./Import.css";

type Phase =
  | { kind: "idle" }
  | { kind: "discovering"; lines: DiscoveryProgress[] }
  | {
      kind: "ready";
      lines: DiscoveryProgress[];
      candidate: ImportCandidate;
      chosenName: string;
    }
  | { kind: "saving"; candidate: ImportCandidate; chosenName: string }
  | { kind: "error"; lines: DiscoveryProgress[]; error: ImportError };

export function Import() {
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [url, setUrl] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });

  const abortRef = useRef<AbortController | null>(null);

  // Cancel any in-flight discover stream when the page unmounts (e.g. the
  // operator hits Cancel and navigates back).
  useEffect(() => {
    return () => abortRef.current?.abort();
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setPhase({ kind: "discovering", lines: [] });
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      const result = await discoverClusterStream(
        { url, username, password, insecure: true },
        {
          signal: ctrl.signal,
          onProgress: (line) => {
            setPhase((p) =>
              p.kind === "discovering" ? { ...p, lines: [...p.lines, line] } : p,
            );
          },
        },
      );
      setPhase((p) => {
        const lines = p.kind === "discovering" ? p.lines : [];
        if (!result.ok) return { kind: "error", lines, error: result.error };
        return {
          kind: "ready",
          lines,
          candidate: result.candidate,
          chosenName: "",
        };
      });
    } catch (err) {
      // Aborted streams (operator navigated away) are silent — the page
      // is unmounting. Anything else surfaces as a generic error frame.
      if (err instanceof DOMException && err.name === "AbortError") return;
      const message =
        err instanceof SseError
          ? `${err.message}`
          : err instanceof Error
            ? err.message
            : "Discovery failed.";
      setPhase((p) => {
        const lines = p.kind === "discovering" ? p.lines : [];
        return {
          kind: "error",
          lines,
          error: { kind: "unreachable", message },
        };
      });
    }
  }

  async function onSave() {
    if (phase.kind !== "ready") return;
    const candidate = phase.candidate;
    const chosenName = phase.chosenName;
    setPhase({ kind: "saving", candidate, chosenName });
    try {
      const { clusterId } = await commitImport({ candidate, chosenName, insecure: true });
      qc.invalidateQueries({ queryKey: ["clusters"] });
      navigate(`/clusters/${clusterId}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Save failed.";
      setPhase({
        kind: "error",
        lines: [],
        error: { kind: "unreachable", message },
      });
    }
  }

  function onCloseModal() {
    setPhase({ kind: "idle" });
  }

  return (
    <div className="import">
      <header className="import__header">
        <h1>Import existing Buckit or MinIO cluster</h1>
        <p className="muted">
          Provide the cluster's S3 endpoint and root credentials. We'll
          discover every node, drive, and pool.
        </p>
      </header>

      <form className="card import__card" onSubmit={onSubmit}>
        <div className="field">
          <label className="field-label" htmlFor="url">
            Cluster URL
          </label>
          <input
            id="url"
            className="input"
            placeholder="https://node1.example.com:9000"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            autoComplete="off"
            autoFocus
            required
          />
          <p className="subtle import__hint">
            The S3 endpoint of any node in the cluster. Use the load
            balancer if you have one.
          </p>
        </div>

        <div className="field">
          <label className="field-label" htmlFor="username">
            Access key
          </label>
          <input
            id="username"
            className="input"
            placeholder="MINIO_ROOT_USER"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
          <p className="subtle import__hint">
            The root username the server was started with.
          </p>
        </div>

        <div className="field">
          <label className="field-label" htmlFor="password">
            Secret key
          </label>
          <input
            id="password"
            type="password"
            className="input"
            placeholder="MINIO_ROOT_PASSWORD"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
          <p className="subtle import__hint">
            The matching root password.
          </p>
        </div>

        <div className="hstack" style={{ justifyContent: "flex-end" }}>
          <button
            type="button"
            className="btn"
            onClick={() => navigate(-1)}
          >
            Cancel
          </button>
          <button type="submit" className="btn btn--primary">
            Add cluster
          </button>
        </div>
      </form>

      {phase.kind !== "idle" && (
        <ImportModal
          phase={phase}
          onChangeName={(name) =>
            setPhase((p) =>
              p.kind === "ready" ? { ...p, chosenName: name } : p,
            )
          }
          onSave={onSave}
          onClose={onCloseModal}
        />
      )}
    </div>
  );
}

interface ImportModalProps {
  phase: Exclude<Phase, { kind: "idle" }>;
  onChangeName: (name: string) => void;
  onSave: () => void;
  onClose: () => void;
}

function ImportModal({ phase, onChangeName, onSave, onClose }: ImportModalProps) {
  const logRef = useRef<HTMLDivElement>(null);
  const lines =
    phase.kind === "saving" ? [] : (phase as { lines?: DiscoveryProgress[] }).lines ?? [];

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [lines.length]);

  // Backdrop click closes only in terminal states (error/ready). During
  // active discovery or saving we don't let the operator dismiss by accident.
  const dismissable = phase.kind === "error";
  const onBackdrop = () => {
    if (dismissable) onClose();
  };

  return (
    <div className="modal-backdrop" onClick={onBackdrop}>
      <div
        className="card modal modal--lg import__modal"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="import__modal-header">
          <h2>
            {phase.kind === "discovering" && "Discovering cluster"}
            {phase.kind === "ready" && "Cluster discovered"}
            {phase.kind === "saving" && "Saving cluster"}
            {phase.kind === "error" && "Discovery failed"}
          </h2>
          {phase.kind === "discovering" && (
            <span className="subtle import__modal-sub">
              Contacting the admin API…
            </span>
          )}
        </header>

        {lines.length > 0 && (
          <div className="import__log" ref={logRef}>
            {lines.map((l, i) => (
              <div
                key={i}
                className={`import__log-line import__log-line--${l.level}`}
              >
                <span className="import__log-icon">
                  {l.level === "ok"
                    ? "✓"
                    : l.level === "error"
                      ? "✗"
                      : "●"}
                </span>
                <span>{l.text}</span>
              </div>
            ))}
          </div>
        )}

        {phase.kind === "ready" && (
          <ImportSummary
            candidate={phase.candidate}
            chosenName={phase.chosenName}
            onChangeName={onChangeName}
            onSave={onSave}
            onClose={onClose}
          />
        )}

        {phase.kind === "error" && (
          <div className="vstack" style={{ gap: "var(--s-3)" }}>
            <div className="import__error" role="alert">
              {phase.error.message}
            </div>
            <div className="hstack" style={{ justifyContent: "flex-end" }}>
              <button className="btn btn--primary" onClick={onClose}>
                Close
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

interface ImportSummaryProps {
  candidate: ImportCandidate;
  chosenName: string;
  onChangeName: (name: string) => void;
  onSave: () => void;
  onClose: () => void;
}

function ImportSummary({
  candidate,
  chosenName,
  onChangeName,
  onSave,
  onClose,
}: ImportSummaryProps) {
  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <div className="import__summary">
        <SummaryRow
          label="Engine"
          value={candidate.engine === "minio" ? "MinIO" : "Buckit"}
        />
        <SummaryRow label="Version" value={candidate.version} mono />
        <SummaryRow label="Nodes" value={`${candidate.nodes.length}`} />
        <SummaryRow label="Pools" value={`${candidate.poolCount}`} />
        <SummaryRow label="Drives" value={`${candidate.driveCount}`} />
        <SummaryRow label="Parity" value={`EC:${candidate.parity}`} />
        <SummaryRow label="Usable" value={formatBytes(candidate.usableBytes)} />
        <SummaryRow label="Raw" value={formatBytes(candidate.rawBytes)} />
      </div>

      <div className="field">
        <label className="field-label" htmlFor="cluster-name">
          Name this cluster
        </label>
        <input
          id="cluster-name"
          className="input"
          value={chosenName}
          onChange={(e) => onChangeName(e.target.value)}
          placeholder={candidate.suggestedName}
          autoFocus
          required
        />
        <p className="subtle import__hint">
          Used as the cluster's display label.
        </p>
      </div>

      <div className="hstack" style={{ justifyContent: "flex-end" }}>
        <button type="button" className="btn" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="btn btn--primary"
          onClick={onSave}
          disabled={!chosenName.trim()}
        >
          Save
        </button>
      </div>
    </div>
  );
}

function SummaryRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <>
      <div className="import__summary-label">{label}</div>
      <div className={"import__summary-value" + (mono ? " mono" : "")}>{value}</div>
    </>
  );
}
