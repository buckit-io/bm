import { useEffect, useState } from "react";
import { NewClusterDraft, PreflightResult } from "../state";
import { Pill } from "../../../../components/Pill";
import { newClusterPreflight } from "../../../../api/client";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

// uniqueMessage returns the single host-status message when every host
// reports the same one (and there's at least one), otherwise "". Used
// to summarise checks like pkg_mgr where the operator only cares about
// the answer, not the per-host repetition.
function uniqueMessage(
  hostStatuses: PreflightResult["hostStatuses"] | undefined,
): string {
  if (!hostStatuses || hostStatuses.length === 0) return "";
  const messages = new Set(
    hostStatuses.map((s) => s.message ?? "").filter((m) => m !== ""),
  );
  if (messages.size !== 1) return "";
  return [...messages][0];
}

// severityLabel returns the effective severity for the row. A check's
// declared severity only matters when it hard-fails — a `warn` result
// never blocks deploy (the gate counts blocking *fails* only), so a
// warning on a blocking check is surfaced as "Advisory" to avoid the
// contradictory "Blocking" badge next to a Warning pill.
function severityLabel(p: PreflightResult): string {
  if (p.result === "warn") return "Advisory";
  return p.severity === "blocking" ? "Blocking" : "Advisory";
}

function resultPill(r: PreflightResult["result"]) {
  switch (r) {
    case "pass":
      return <Pill tone="success" icon="✓">Pass</Pill>;
    case "warn":
      return <Pill tone="warning" icon="⚠">Warning</Pill>;
    case "fail":
      return <Pill tone="danger" icon="✗">Fail</Pill>;
    case "skipped":
      return <Pill tone="neutral">Skipped</Pill>;
  }
}

export function Preflight({ draft, update }: Props) {
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // run() POSTs the draft to /api/v1/clusters/new/preflight and stores
  // the result list on the draft. The backend runs the full check
  // catalog server-side (preflight.RunAll) and returns one
  // PreflightResult per check — JSON shape matches the wizard's
  // PreflightResult interface field-for-field.
  const run = () => {
    setRunning(true);
    setError(null);
    update({ preflight: [] });
    newClusterPreflight(draft)
      .then((rows) => {
        update({ preflight: rows as unknown as PreflightResult[] });
      })
      .catch((err) => {
        const message = err instanceof Error ? err.message : "Preflight failed.";
        setError(message);
      })
      .finally(() => setRunning(false));
  };

  useEffect(() => {
    if (draft.preflight.length === 0) run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const blockingFails = draft.preflight.filter(
    (p) => p.severity === "blocking" && p.result === "fail",
  ).length;
  const advisoryFails = draft.preflight.filter(
    (p) => p.severity === "advisory" && (p.result === "fail" || p.result === "warn"),
  ).length;
  const blockingWarn = draft.preflight.filter(
    (p) => p.severity === "blocking" && p.result === "warn",
  ).length;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Preflight</h2>
          <span data-testid="new-cluster-preflight-title" style={{ display: "none" }}>
            Preflight
          </span>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Verifying every host is ready to receive Buckit. Blocking
            failures must be resolved before deploy.
          </p>
          {error && (
            <p
              className="subtle"
              style={{ color: "var(--c-danger)", marginTop: 4 }}
            >
              {error}
            </p>
          )}
        </div>
        <div className="hstack">
          {blockingFails > 0 && (
            <Pill tone="danger" icon="✗">
              {blockingFails} blocking
            </Pill>
          )}
          {advisoryFails + blockingWarn > 0 && (
            <Pill tone="warning" icon="⚠">
              {advisoryFails + blockingWarn} warning
              {advisoryFails + blockingWarn > 1 ? "s" : ""}
            </Pill>
          )}
          <button className="btn btn--sm" onClick={run} disabled={running}>
            {running ? "Running…" : "Re-run"}
          </button>
        </div>
      </header>

      <div className="card card--table">
        <div data-testid="new-cluster-preflight-table">
        <table className="table table--center">
          <thead>
            <tr>
              <th>Check</th>
              <th style={{ width: 90 }}>Severity</th>
              <th style={{ width: 110 }}>Nodes</th>
              <th style={{ width: 120 }}>Result</th>
            </tr>
          </thead>
          <tbody>
            {draft.preflight.flatMap((p) => {
              const failingHosts =
                p.hostStatuses?.filter((s) => s.status !== "pass") ?? [];
              const total = p.hostStatuses?.length ?? 0;
              const passing = total - failingHosts.length;
              // For pkg_mgr, surface the detected manager (e.g. "Detected: dnf (rpm)")
              // when every host passes — gives the operator a clear signal about
              // which install path bm will use without making them read per-host detail.
              const pkgMgrDetected =
                p.id === "pkg_mgr" && failingHosts.length === 0
                  ? uniqueMessage(p.hostStatuses)
                  : "";
              return [
                <tr key={p.id}>
                  <td>{p.label}</td>
                  <td>
                    <span
                      className="subtle"
                      style={{ fontSize: "var(--fs-xs)" }}
                    >
                      {severityLabel(p)}
                    </span>
                  </td>
                  <td className="subtle">
                    {p.hostStatuses ? `${passing} / ${total}` : "—"}
                  </td>
                  <td>{resultPill(p.result)}</td>
                </tr>,
                (p.detail || failingHosts.length > 0 || pkgMgrDetected) && (
                  <tr key={p.id + "-d"} className="discover__detail">
                    <td colSpan={4}>
                      <div
                        className="vstack subtle"
                        style={{
                          gap: 2,
                          padding: "var(--s-2) 0",
                          fontSize: "var(--fs-xs)",
                        }}
                      >
                        {p.detail && <span>↳ {p.detail}</span>}
                        {pkgMgrDetected && <span>↳ Detected: {pkgMgrDetected}</span>}
                        {failingHosts.map((s) => (
                          <span key={s.hostId}>
                            ↳ <span className="mono">{s.hostname}</span>
                            {s.message ? ` — ${s.message}` : ""}
                          </span>
                        ))}
                      </div>
                    </td>
                  </tr>
                ),
              ];
            })}
          </tbody>
        </table>
        </div>
      </div>

      {blockingFails > 0 && (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>
            {blockingFails} blocking failure
            {blockingFails > 1 ? "s" : ""} — fix before deploy. Re-run
            after correcting.
          </span>
        </div>
      )}
      {blockingFails === 0 && advisoryFails + blockingWarn > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            {advisoryFails + blockingWarn} warning
            {advisoryFails + blockingWarn > 1 ? "s" : ""} — review, then
            continue.
          </span>
        </div>
      )}
    </div>
  );
}
