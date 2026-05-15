import { useEffect, useRef } from "react";
import { MigrationDraft, VerifyResult } from "../state";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

function makeVerify(d: MigrationDraft): VerifyResult {
  const s = d.snapshot!;
  const n = d.hosts.filter((h) => h.hostname.trim()).length;
  return {
    clusterHealthy: true,
    nodesReporting: { ok: n, total: n },
    bucketsOk: { ok: s.buckets, total: s.buckets },
    objectsSampled: { ok: 1000, total: 1000 },
    users: { ok: s.users, total: s.users },
    groups: { ok: s.groups, total: s.groups },
    policies: { ok: s.customPolicies, total: s.customPolicies },
    serviceAccounts: { ok: s.serviceAccounts, total: s.serviceAccounts },
    bucketPolicies: { ok: s.policies, total: s.policies },
    lifecycle: { ok: s.lifecycle, total: s.lifecycle },
    notifications: { ok: s.notifications, total: s.notifications },
    smokeOk: true,
  };
}

function row(label: string, sub: { ok: number; total: number }) {
  const ok = sub.ok === sub.total;
  return (
    <tr key={label}>
      <td>{label}</td>
      <td className="num">{sub.ok} / {sub.total}</td>
      <td>{ok ? <Pill tone="success" icon="✓">Match</Pill> : <Pill tone="danger" icon="✗">Mismatch</Pill>}</td>
    </tr>
  );
}

export function Verify({ draft, update }: Props) {
  const fired = useRef(false);
  useEffect(() => {
    if (fired.current || draft.verify) return;
    fired.current = true;
    setTimeout(() => update({ verify: makeVerify(draft) }), 700);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const v = draft.verify;
  if (!v) return <p className="muted">Running post-migration verification…</p>;

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Verify</h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            Comparing post-migration state against the pre-migration snapshot.
          </p>
        </div>
        <div className="hstack">
          <button className="btn btn--sm">Re-run verification</button>
          <button className="btn btn--sm">Download report ⤓</button>
        </div>
      </header>

      <div className="card card--table">
        <table className="table">
          <thead>
            <tr><th>Check</th><th className="num">Result</th><th style={{ width: 110 }}>Status</th></tr>
          </thead>
          <tbody>
            <tr><td>Cluster health</td><td className="num">—</td><td>{v.clusterHealthy ? <Pill tone="success" icon="●">Healthy</Pill> : <Pill tone="warning">Degraded</Pill>}</td></tr>
            {row("Nodes reporting", v.nodesReporting)}
            {row("Buckets", v.bucketsOk)}
            {row("Objects (sampled, 1000)", v.objectsSampled)}
            {row("IAM users", v.users)}
            {row("IAM groups", v.groups)}
            {row("Custom policies", v.policies)}
            {row("Service accounts", v.serviceAccounts)}
            {row("Bucket policies", v.bucketPolicies)}
            {row("Lifecycle rules", v.lifecycle)}
            {row("Notification configs", v.notifications)}
            <tr>
              <td>Smoke test (PUT/GET/DELETE 1 KiB)</td>
              <td className="num">—</td>
              <td>{v.smokeOk ? <Pill tone="success" icon="✓">Passed</Pill> : <Pill tone="danger">Failed</Pill>}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div className="banner banner--success">
        <span>✓</span>
        <span>All checks passed. The rollback option remains available until you finalize.</span>
      </div>
    </div>
  );
}
