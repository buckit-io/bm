import { useEffect, useRef } from "react";
import { MigrationDraft, MinioSnapshot } from "../state";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

const SNAPSHOT: MinioSnapshot = {
  buckets: 142,
  largestBucket: { name: "logs-archive", size: "412 TiB" },
  versioning: 38,
  lifecycle: 21,
  objectLock: 4,
  users: 17,
  groups: 3,
  customPolicies: 11,
  serviceAccounts: 42,
  policies: 57,
  notifications: 9,
  replicationTargets: 3,
  warnings: ["This cluster replicates to external targets. Buckit will preserve the configuration; the targets must remain reachable post-migration."],
};

export function Snapshot({ draft, update }: Props) {
  const fired = useRef(false);
  useEffect(() => {
    if (fired.current || draft.snapshot) return;
    fired.current = true;
    setTimeout(() => update({ snapshot: SNAPSHOT }), 700);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const s = draft.snapshot;
  if (!s)
    return (
      <p className="muted">Capturing snapshot of current MinIO state…</p>
    );

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Snapshot</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Captured via the MinIO admin API. Stored as a versioned artifact in
            the manager and used to verify post-cutover.
          </p>
        </div>
        <div className="hstack">
          <button className="btn btn--sm">Re-run snapshot</button>
          <button className="btn btn--sm">Download snapshot ⤓</button>
        </div>
      </header>

      <div className="cards-row">
        <div className="card card-stat">
          <div className="card-stat__title">Buckets</div>
          <div className="card-stat__value">{s.buckets}</div>
          <div className="card-stat__sub">Largest: {s.largestBucket.name} ({s.largestBucket.size})</div>
          <div className="card-stat__sub">{s.versioning} versioned · {s.lifecycle} with lifecycle · {s.objectLock} with object lock</div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">IAM</div>
          <div className="card-stat__sub"><b>Users</b> {s.users}</div>
          <div className="card-stat__sub"><b>Groups</b> {s.groups}</div>
          <div className="card-stat__sub"><b>Policies (custom)</b> {s.customPolicies}</div>
          <div className="card-stat__sub"><b>Service accounts</b> {s.serviceAccounts}</div>
        </div>
        <div className="card card-stat">
          <div className="card-stat__title">Bucket configs</div>
          <div className="card-stat__sub"><b>Bucket policies</b> {s.policies}</div>
          <div className="card-stat__sub"><b>Notification configs</b> {s.notifications}</div>
          <div className="card-stat__sub"><b>Lifecycle rules</b> {s.lifecycle}</div>
          <div className="card-stat__sub"><b>Replication targets</b> {s.replicationTargets}</div>
        </div>
      </div>

      {s.warnings.map((w, i) => (
        <div key={i} className="banner banner--warning">
          <span>⚠</span>
          <span>{w}</span>
        </div>
      ))}
    </div>
  );
}
