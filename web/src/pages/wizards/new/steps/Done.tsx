import { useEffect } from "react";
import { NewClusterDraft } from "../state";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
  onFinish: () => void;
}

function gen(n: number) {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  let s = "";
  for (let i = 0; i < n; i++) s += chars[Math.floor(Math.random() * chars.length)];
  return s;
}

export function Done({ draft, update, onFinish }: Props) {
  useEffect(() => {
    if (!draft.done.rootPass) {
      const host = draft.hosts.find((h) => h.hostname.trim())?.hostname || "node1.example.com";
      update({
        done: {
          ...draft.done,
          consoleUrl: `https://${host}:9000`,
          rootPass: gen(20).match(/.{1,4}/g)!.join("-"),
          nodesHealthy: draft.hosts.filter((h) => h.hostname.trim()).length,
          poolsOnline: 1,
          smokeTestPassed: true,
        },
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const d = draft.done;
  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 720 }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-2xl)", fontWeight: 600 }}>
          ✓ {draft.name || "cluster"} is up
        </h2>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Connection</h3>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Console URL</div>
            <a href={d.consoleUrl} target="_blank" rel="noreferrer" className="mono">
              {d.consoleUrl}
            </a>
          </div>
          <button className="btn btn--sm">Copy</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Root username</div>
            <div className="mono">{d.rootUser}</div>
          </div>
          <button className="btn btn--sm">Copy</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Root password</div>
            <div className="mono">
              {d.credsRevealed ? d.rootPass : "●●●●●●●●●●●●●●●●●●●●"}
            </div>
          </div>
          <div className="hstack">
            <button className="btn btn--sm" onClick={() => update({ done: { ...d, credsRevealed: !d.credsRevealed } })}>
              {d.credsRevealed ? "Hide" : "Reveal"}
            </button>
            <button className="btn btn--sm">Copy</button>
          </div>
        </div>
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          The root password is shown exactly once. Store it now in your secret manager.
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Quick checks</h3>
        <ul style={{ listStyle: "none", padding: 0, fontSize: "var(--fs-sm)" }}>
          <li>● {d.nodesHealthy} / {d.nodesHealthy} nodes healthy</li>
          <li>● {d.poolsOnline} / {d.poolsOnline} pool online</li>
          <li>● Read/write smoke test {d.smokeTestPassed ? "passed" : "pending"}</li>
        </ul>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Recommended next step</h3>
        <pre className="codeblock">
{`bm alias set ${draft.name || "prod"} ${d.consoleUrl} ${d.rootUser} <password>`}
        </pre>
      </div>

      <div className="hstack" style={{ justifyContent: "flex-end" }}>
        <button className="btn btn--primary" onClick={onFinish}>
          Go to cluster overview
        </button>
      </div>
    </div>
  );
}
