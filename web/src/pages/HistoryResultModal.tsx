// Read-only modal that renders the persisted result of a past
// operation. Reached from the View button on a History row. Visual
// shape mirrors the terminal phase of OperationModal so the operator
// sees the same per-host list / summary table / failure note they saw
// when the op completed.

import { useEffect } from "react";
import { Pill, PillTone } from "../components/Pill";
import {
  HistoryEntry,
  HostOpState,
  HostOpStatus,
  OperationResult,
  formatRelative,
} from "../mock/data";

interface Props {
  entry: HistoryEntry;
  onClose: () => void;
}

export function HistoryResultModal({ entry, onClose }: Props) {
  // Read-only modal — ESC dismisses.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const scope =
    !entry.hostScope
      ? "cluster-wide"
      : entry.hostScope.count === 1
        ? entry.hostScope.hostnames[0]
        : `${entry.hostScope.count} hosts`;

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="card modal modal--lg"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <header style={{ paddingBottom: "var(--s-2)" }}>
          <h2 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
            {entry.opLabel}{" "}
            <span className="subtle" style={{ fontWeight: 400 }}>
              · {entry.clusterName} · {scope}
            </span>
          </h2>
          <p
            className="subtle"
            style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}
          >
            {formatRelative(entry.at)} · {new Date(entry.at).toLocaleString()}
          </p>
        </header>

        <ResultBody entry={entry} />

        <div
          className="hstack"
          style={{ justifyContent: "flex-end", marginTop: "var(--s-3)" }}
        >
          <button className="btn btn--primary" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

function ResultBody({ entry }: { entry: HistoryEntry }) {
  const r: OperationResult | undefined = entry.result;
  if (!r) {
    // Running or pre-result row. Just show the status pill.
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        <div>{statusBanner(entry.status, entry.failureNote)}</div>
      </div>
    );
  }
  const hasSummary = !!r.summary && r.summary.length > 0;
  const hasHosts = !!r.hostStatuses && r.hostStatuses.length > 0;

  // Success with a host list: the host list carries the story; no banner.
  if (hasHosts && r.state === "succeeded") {
    return (
      <div className="vstack" style={{ gap: "var(--s-3)" }}>
        {hasSummary && <SummaryTable rows={r.summary!} />}
        <HostStatusList statuses={r.hostStatuses!} />
      </div>
    );
  }

  return (
    <div className="vstack" style={{ gap: "var(--s-3)" }}>
      {statusBanner(r.state, r.failureNote ?? entry.failureNote, r.detail)}
      {hasSummary && <SummaryTable rows={r.summary!} />}
      {hasHosts && <HostStatusList statuses={r.hostStatuses!} />}
    </div>
  );
}

function statusBanner(
  state: HistoryEntry["status"],
  failureNote?: string,
  detail?: string,
) {
  if (state === "succeeded") {
    return (
      <div className="banner banner--success">
        <span>✓</span>
        <span>{detail ?? "Succeeded."}</span>
      </div>
    );
  }
  if (state === "failed") {
    return (
      <div className="banner banner--danger">
        <span>✗</span>
        <span>{failureNote ?? detail ?? "Failed."}</span>
      </div>
    );
  }
  if (state === "canceled") {
    return (
      <div className="banner banner--warning">
        <span>⊘</span>
        <span>{detail ?? "Canceled."}</span>
      </div>
    );
  }
  return (
    <div className="banner banner--info">
      <span>⟳</span>
      <span>{detail ?? "Running."}</span>
    </div>
  );
}

function SummaryTable({ rows }: { rows: { label: string; value: string }[] }) {
  return (
    <div
      className="card"
      style={{
        padding: "var(--s-3) var(--s-4)",
        background: "var(--c-surface-2)",
      }}
    >
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          rowGap: "var(--s-2)",
          columnGap: "var(--s-4)",
          fontSize: "var(--fs-sm)",
        }}
      >
        {rows.map((r) => (
          <div key={r.label} style={{ display: "contents" }}>
            <div className="subtle">{r.label}</div>
            <div className="mono">{r.value}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function HostStatusList({ statuses }: { statuses: HostOpStatus[] }) {
  return (
    <div
      className="card"
      style={{
        padding: 0,
        maxHeight: 320,
        overflowY: "auto",
      }}
    >
      {statuses.map((h) => (
        <div
          key={h.hostId}
          className="rolling-row"
          style={{ gridTemplateColumns: "1fr 130px 60px" }}
        >
          <div className="rolling-row__name">{h.hostname}</div>
          <div>{hostStatePill(h.state)}</div>
          <div
            className="subtle"
            style={{ fontSize: "var(--fs-xs)", textAlign: "right" }}
          >
            {h.durationSec ? `${h.durationSec}s` : ""}
          </div>
        </div>
      ))}
    </div>
  );
}

function hostStatePill(s: HostOpState) {
  const map: Record<HostOpState, { tone: PillTone; icon: string; label: string }> = {
    pending: { tone: "neutral", icon: "·", label: "Pending" },
    running: { tone: "info", icon: "⟳", label: "Running" },
    succeeded: { tone: "success", icon: "✓", label: "Succeeded" },
    failed: { tone: "danger", icon: "✗", label: "Failed" },
  };
  const m = map[s];
  return <Pill tone={m.tone} icon={m.icon}>{m.label}</Pill>;
}
