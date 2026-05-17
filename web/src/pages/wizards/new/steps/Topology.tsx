import { useEffect, useMemo } from "react";
import { DiscoveredDrive, NewClusterDraft, Topology as TopologyShape } from "../state";
import { intersectMounts } from "./driveIntersection";
import { topologyErrors } from "./topologyValidation";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

const TiB = 1024 ** 4;

export function Topology({ draft, update }: Props) {
  const validHosts = draft.hosts.filter((h) => h.hostname.trim());
  const t = draft.topology;

  // Per-host drive lists from discovery.
  const perHost: Record<string, DiscoveredDrive[]> = useMemo(() => {
    const out: Record<string, DiscoveredDrive[]> = {};
    for (const h of validHosts) {
      const r = draft.discovery[h.id];
      out[h.id] = r?.drives ?? [];
    }
    return out;
  }, [validHosts, draft.discovery]);

  const intersection = useMemo(() => intersectMounts(perHost), [perHost]);
  const { common, extrasByHost, eligibleByHost } = intersection;

  // Case detection.
  const someoneHasNoEligible = validHosts.some(
    (h) => (eligibleByHost[h.id] ?? 0) === 0,
  );
  const caseKind: "A" | "B" | "C" =
    common.length === 0
      ? "C"
      : someoneHasNoEligible || Object.keys(extrasByHost).length > 0
        ? "B"
        : "A";

  // Auto-select the intersection on first arrival or whenever it
  // changes. Operator can opt to skip / configure in case C but cases
  // A/B always pre-select.
  const commonKey = common.join("|");
  useEffect(() => {
    const same =
      t.selectedMounts.length === common.length &&
      t.selectedMounts.every((m, i) => m === common[i]);
    if (!same) {
      update({ topology: { ...t, selectedMounts: [...common] } });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [commonKey]);

  // Derived counts. Use a sample drive size from the first eligible
  // entry — real backend will validate uniformity in preflight.
  const sampleSize = sampleDriveSize(perHost);
  const drivesPerNode = t.selectedMounts.length;
  const totalDrives = validHosts.length * drivesPerNode;
  const rawBytes = totalDrives * sampleSize;
  const usableBytes = rawBytes - rawBytes * (t.parity / Math.max(t.setSize, 1));
  const errors = topologyErrors(draft);

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Topology</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Drive selection is computed from discovery. Adjust parity and set
          size if needed.
        </p>
      </header>

      {/* ── case banner ───────────────────────────────────────────── */}
      {caseKind === "A" && (
        <div className="banner banner--info">
          <span>✓</span>
          <span>
            {common.length} common mountpoint{common.length === 1 ? "" : "s"}{" "}
            found across {validHosts.length}{" "}
            host{validHosts.length === 1 ? "" : "s"}. Ready to deploy.
          </span>
        </div>
      )}
      {caseKind === "B" && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            Using {common.length} drive
            {common.length === 1 ? "" : "s"} per host. Some hosts have
            eligible drives that aren't present on every other host — they
            will be unused.
          </span>
        </div>
      )}
      {caseKind === "C" && (
        <div className="banner banner--danger">
          <span>✗</span>
          <span>
            No common drive mountpoints found across all hosts. Mount
            drives at a consistent path (typically{" "}
            <span className="mono">/data/disk{"{1...N}"}</span>) on every
            host and re-run discovery to continue.
          </span>
        </div>
      )}

      {/* ── pool / parity controls ────────────────────────────────── */}
      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Pool 1</h3>
        <div className="topology__grid">
          <div>
            <div className="field-label">Nodes</div>
            <div
              className="card-stat__value"
              style={{ fontSize: "var(--fs-lg)" }}
            >
              {validHosts.length}
            </div>
          </div>
          <div>
            <div className="field-label">Drives / node</div>
            <div
              className="card-stat__value"
              style={{ fontSize: "var(--fs-lg)" }}
            >
              {drivesPerNode || "—"}
            </div>
          </div>
          <div>
            <div className="field-label">Set size</div>
            <select
              className="select"
              value={t.setSize}
              onChange={(e) =>
                update({
                  topology: { ...t, setSize: parseInt(e.target.value, 10) },
                })
              }
            >
              {[8, 12, 16, 24].map((n) => (
                <option key={n}>{n}</option>
              ))}
            </select>
          </div>
          <div>
            <div className="field-label">Parity</div>
            <select
              className="select"
              value={t.parity}
              onChange={(e) =>
                update({
                  topology: {
                    ...t,
                    parity: parseInt(e.target.value, 10) as TopologyShape["parity"],
                  },
                })
              }
            >
              {[2, 3, 4, 6, 8].map((n) => (
                <option key={n}>{n}</option>
              ))}
            </select>
          </div>
        </div>

        {drivesPerNode > 0 && (
          <div className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            Total drives: <b>{totalDrives}</b> ·{" "}
            <b>{totalDrives / t.setSize || 0}</b> erasure set
            {totalDrives / t.setSize === 1 ? "" : "s"} · Usable capacity:{" "}
            <b>~{Math.round(usableBytes / TiB)} TiB</b> of{" "}
            {Math.round(rawBytes / TiB)} TiB raw
          </div>
        )}

        {errors.length > 0 ? (
          <div className="banner banner--danger">
            <span>✗</span>
            <span>
              {errors.map((e, i) => (
                <div key={i}>{e}</div>
              ))}
            </span>
          </div>
        ) : (
          <div className="banner banner--info">
            <span>ℹ</span>
            <span>
              EC:{t.parity} tolerates loss of up to {t.parity} drives per set
              of {t.setSize}.
            </span>
          </div>
        )}
      </div>

      {/* ── selected mountpoints ──────────────────────────────────── */}
      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Selected drives</h3>
        {caseKind === "C" ? (
          <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            No mountpoints to select yet. Once drives are mounted
            consistently on every host (for example via{" "}
            <span className="mono">/etc/fstab</span>), return to the
            Discovery step and re-run to refresh.
          </p>
        ) : (
          <>
            <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
              These mountpoints exist on every host and will be included
              in <span className="mono">MINIO_VOLUMES</span>.
            </p>
            <div
              className="hstack"
              style={{ flexWrap: "wrap", gap: "var(--s-3) var(--s-3)" }}
            >
              {common.map((m) => {
                const on = t.selectedMounts.includes(m);
                return (
                  <button
                    key={m}
                    type="button"
                    onClick={() =>
                      update({
                        topology: {
                          ...t,
                          selectedMounts: on
                            ? t.selectedMounts.filter((x) => x !== m)
                            : [...t.selectedMounts, m].sort(byMount),
                        },
                      })
                    }
                    className={"chip-mount " + (on ? "is-on" : "is-off")}
                    title={
                      on ? "Remove from selection" : "Add back to selection"
                    }
                  >
                    <span className="mono">{m}</span>
                    <span className="chip-mount__icon" aria-hidden>
                      {on ? "✕" : "+"}
                    </span>
                  </button>
                );
              })}
            </div>
            {t.selectedMounts.length !== common.length && (
              <>
                <div className="banner banner--warning">
                  <span>⚠</span>
                  <span>
                    Deploying with {t.selectedMounts.length} of{" "}
                    {common.length} discoverable drives per host. Unused
                    drives can't be added to this pool later — you'd have
                    to add a new pool with its own erasure set.
                  </span>
                </div>
                <div>
                  <button
                    className="btn btn--ghost btn--sm"
                    onClick={() =>
                      update({
                        topology: { ...t, selectedMounts: [...common] },
                      })
                    }
                  >
                    Reset to all {common.length} mountpoints
                  </button>
                </div>
              </>
            )}
          </>
        )}

        {caseKind === "B" && (
          <div
            className="vstack"
            style={{
              gap: "var(--s-2)",
              marginTop: "var(--s-2)",
              paddingTop: "var(--s-3)",
              borderTop: "1px solid var(--c-border)",
            }}
          >
            <div className="field-label">Unused drives</div>
            {validHosts.map((h) => {
              const extras = extrasByHost[h.id];
              if (!extras || extras.length === 0) return null;
              return (
                <div
                  key={h.id}
                  className="subtle"
                  style={{ fontSize: "var(--fs-sm)" }}
                >
                  <span className="mono">{h.hostname}</span> —{" "}
                  {extras.map((m, i) => (
                    <span key={m} className="mono">
                      {m}
                      {i < extras.length - 1 ? ", " : ""}
                    </span>
                  ))}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

// Natural sort so /data/disk2 < /data/disk10 (matches the helper in
// driveIntersection so re-inclusion lands in stable position).
function byMount(a: string, b: string): number {
  const ax = a.split(/(\d+)/).map((x) => (/^\d+$/.test(x) ? parseInt(x, 10) : x));
  const bx = b.split(/(\d+)/).map((x) => (/^\d+$/.test(x) ? parseInt(x, 10) : x));
  for (let i = 0; i < Math.min(ax.length, bx.length); i++) {
    if (ax[i] === bx[i]) continue;
    if (typeof ax[i] === "number" && typeof bx[i] === "number") {
      return (ax[i] as number) - (bx[i] as number);
    }
    return String(ax[i]) < String(bx[i]) ? -1 : 1;
  }
  return ax.length - bx.length;
}

function sampleDriveSize(
  perHost: Record<string, DiscoveredDrive[]>,
): number {
  for (const drives of Object.values(perHost)) {
    for (const d of drives) {
      if (!d.isBoot && d.sizeBytes > 0) return d.sizeBytes;
    }
  }
  return 0;
}
