import { useEffect, useMemo } from "react";
import { DiscoveredDrive, NewClusterDraft, Topology as TopologyShape } from "../state";
import { intersectMounts } from "./driveIntersection";
import { topologyErrors } from "./topologyValidation";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
}

const GiB = 1024 ** 3;
const TiB = 1024 ** 4;

function humanSize(bytes: number): string {
  if (bytes >= TiB) return `${(bytes / TiB).toFixed(1)} TiB`;
  if (bytes >= GiB) return `${(bytes / GiB).toFixed(1)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(0)} MiB`;
  return `${(bytes / 1024).toFixed(0)} KiB`;
}

// Valid erasure set sizes — mirrors setSizes in buckit/cmd/endpoint-ellipses.go.
const VALID_SET_SIZES = [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];

// selectByPattern groups mounts by their directory prefix (stripping the
// trailing numeric segment), then picks the largest group. E.g. given
// ["/data/drive0", ..., "/data/drive15", "/var/lib/buckit-drives"], the
// prefix "/data/drive" has 16 entries and wins — "/var/lib/buckit-drives"
// is excluded. If all mounts share the same prefix (or there's no clear
// pattern), all are selected.
function selectByPattern(mounts: string[]): string[] {
  if (mounts.length <= 1) return [...mounts];
  const groups: Record<string, string[]> = {};
  for (const m of mounts) {
    // Strip trailing digits to get the prefix: /data/drive15 → /data/drive
    const prefix = m.replace(/\d+$/, "");
    (groups[prefix] ??= []).push(m);
  }
  // Pick the largest group.
  let best: string[] = [];
  for (const g of Object.values(groups)) {
    if (g.length > best.length) best = g;
  }
  // If the largest group is the same size as the full list, just return all.
  // Otherwise return only the pattern-matched group.
  return best.length > 0 ? best.sort() : [...mounts];
}

// computeSetSize picks the largest valid set size that evenly divides
// totalDrives, resulting in the fewest erasure sets. Mirrors
// commonSetDriveCount() in buckit/cmd/endpoint-ellipses.go.
function computeSetSize(totalDrives: number): number {
  if (totalDrives <= 0) return 16;
  if (totalDrives < VALID_SET_SIZES[0]) return totalDrives;
  let best = VALID_SET_SIZES[0];
  let bestSets = totalDrives;
  for (const s of VALID_SET_SIZES) {
    if (totalDrives % s === 0) {
      const sets = totalDrives / s;
      if (sets <= bestSets) {
        bestSets = sets;
        best = s;
      }
    }
  }
  return best;
}

// defaultParityBlocks mirrors DefaultParityBlocks() in
// buckit/internal/config/storageclass/storage-class.go.
function defaultParityBlocks(setDriveCount: number): number {
  if (setDriveCount <= 1) return 0;
  if (setDriveCount <= 3) return 1;
  if (setDriveCount <= 5) return 2;
  if (setDriveCount <= 7) return 3;
  return 4;
}

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
  const commonKey = common.join("|");
  const preferredMounts = useMemo(() => selectByPattern(common), [commonKey]);

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

  // Auto-select drives that share a common path pattern. If most drives
  // share a prefix like /data/drive, /data/disk, etc., select only those
  // and exclude outliers (e.g. /var/lib/buckit-drives). This mirrors what
  // Buckit expects: uniform mount paths across all hosts.
  useEffect(() => {
    const same =
      t.selectedMounts.length === preferredMounts.length &&
      t.selectedMounts.every((m, i) => m === preferredMounts[i]);
    if (!same) {
      update({
        topology: { ...t, selectedMounts: preferredMounts },
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [commonKey, preferredMounts]);

  // Recalculate setSize + parity whenever total drives changes (drive
  // selection toggled, host count changed, etc.). Uses the same defaults
  // Buckit would pick at startup.
  const drivesPerNode = t.selectedMounts.length;
  const totalDrives = validHosts.length * drivesPerNode;
  useEffect(() => {
    if (totalDrives <= 0) return;
    const setSize = computeSetSize(totalDrives);
    const parity = defaultParityBlocks(setSize);
    if (t.setSize !== setSize || t.parity !== parity) {
      update({ topology: { ...t, setSize, parity: parity as typeof t.parity } });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [totalDrives]);

  // Derived counts. Use a sample drive size from the first eligible
  // entry — real backend will validate uniformity in preflight.
  const sampleSize = sampleDriveSize(perHost);
  const rawBytes = totalDrives * sampleSize;
  const usableBytes = rawBytes - rawBytes * (t.parity / Math.max(t.setSize, 1));
  const errors = topologyErrors(draft);
  const hasUnselectedPreferredMount = preferredMounts.some(
    (mount) => !t.selectedMounts.includes(mount),
  );

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Topology</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Select drives on each node to use as storage drives. Adjust set size
          and parity if needed.
        </p>
      </header>

      {/* ── case banner ───────────────────────────────────────────── */}
      {caseKind === "A" && errors.length === 0 && (
        <div className="banner banner--info">
          <span>✓</span>
          <span>
            {drivesPerNode} drive{drivesPerNode === 1 ? "" : "s"} selected{" "}
            across {validHosts.length}{" "}
            host{validHosts.length === 1 ? "" : "s"}. Ready to deploy.
          </span>
        </div>
      )}
      {caseKind === "A" && errors.length > 0 && (
        <div className="banner banner--warning">
          <span>⚠</span>
          <span>
            {drivesPerNode} drive{drivesPerNode === 1 ? "" : "s"} selected{" "}
            across {validHosts.length}{" "}
            host{validHosts.length === 1 ? "" : "s"}. Fix the errors below to continue.
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
              onChange={(e) => {
                const newSetSize = parseInt(e.target.value, 10);
                update({
                  topology: {
                    ...t,
                    setSize: newSetSize,
                    parity: defaultParityBlocks(newSetSize) as typeof t.parity,
                  },
                });
              }}
            >
              {VALID_SET_SIZES.filter((s) => totalDrives > 0 && totalDrives % s === 0).map((n) => {
                const rec = computeSetSize(totalDrives);
                return <option key={n} value={n}>{n}{n === rec ? " (recommended)" : ""}</option>;
              })}
            </select>
            <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
              Drives grouped into one erasure unit. Must evenly divide total drives.
            </p>
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
              {Array.from({ length: Math.floor(t.setSize / 2) }, (_, i) => i + 1).map((n) => {
                const rec = defaultParityBlocks(t.setSize);
                return <option key={n} value={n}>{n}{n === rec ? " (recommended)" : ""}</option>;
              })}
            </select>
            <p className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: 4 }}>
              Drives reserved for redundancy per set. Higher = more fault tolerance, less usable space. Max: {Math.floor(t.setSize / 2)}.
            </p>
          </div>
        </div>

        {drivesPerNode > 0 && (
          <div className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            Total drives: <b>{totalDrives}</b> ·{" "}
            <b>{totalDrives / t.setSize || 0}</b> erasure set
            {totalDrives / t.setSize === 1 ? "" : "s"} · Usable capacity:{" "}
            <b>~{humanSize(usableBytes)}</b> of{" "}
            {humanSize(rawBytes)} raw
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
              EC:{t.parity} tolerates loss of up to <b>{t.parity} drives</b> per
              set of {t.setSize} without data loss. Each object is split into{" "}
              {t.setSize - t.parity} data shards + {t.parity} parity shards.
              Usable capacity ≈ {Math.round((1 - t.parity / t.setSize) * 100)}%
              of raw.
            </span>
          </div>
        )}

        <details className="subtle" style={{ fontSize: "var(--fs-xs)", marginTop: "var(--s-2)" }}>
          <summary style={{ cursor: "pointer" }}>How are set size and parity calculated?</summary>
          <div className="vstack" style={{ gap: "var(--s-2)", marginTop: "var(--s-2)", paddingLeft: "var(--s-3)" }}>
            <p>
              <b>Set size</b> is the number of drives in one erasure unit. Buckit
              supports set sizes from 2 to 16. The default is the largest value
              that evenly divides your total drive count, resulting in the fewest
              erasure sets.
            </p>
            <p>
              <b>Parity</b> is how many drives per set are reserved for redundancy.
              The default depends on set size:
            </p>
            <ul style={{ paddingLeft: "1.2em", margin: 0 }}>
              <li>Set size 2–3 → parity 1</li>
              <li>Set size 4–5 → parity 2</li>
              <li>Set size 6–7 → parity 3</li>
              <li>Set size 8–16 → parity 4</li>
            </ul>
            <p>
              Maximum allowed parity is half the set size (e.g. set size 16 → max
              parity 8). Higher parity means more fault tolerance but less usable
              capacity.
            </p>
            <p>
              <b>Your configuration:</b> {totalDrives} total drives ÷ set size{" "}
              {t.setSize} = {Math.floor(totalDrives / t.setSize)} erasure set
              {Math.floor(totalDrives / t.setSize) === 1 ? "" : "s"}. Each set
              stores {t.setSize - t.parity} data shards + {t.parity} parity
              shards per object.
            </p>
          </div>
        </details>
      </div>
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
              These mountpoints exist on every host and will be used as
              storage drives.
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
            {hasUnselectedPreferredMount && (
              <>
                <div className="banner banner--warning">
                  <span>⚠</span>
                  <span>
                    Deploying with {t.selectedMounts.length} of{" "}
                    {preferredMounts.length} drives with the{" "}
                    <span className="mono">
                      {mountPatternLabel(preferredMounts)}
                    </span>{" "}
                    pattern per host. Unused drives can't be added to this
                    pool later — you'd have to add a new pool with its own
                    erasure set.
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

function mountPatternLabel(mounts: string[]): string {
  if (mounts.length === 0) return "selected";
  const base = mounts[0].replace(/\d+$/, "");
  return base === mounts[0] ? mounts[0] : base;
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
