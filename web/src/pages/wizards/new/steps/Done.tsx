import { useEffect } from "react";
import { NewClusterDraft } from "../state";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
  onFinish: () => void;
}

// Cluster names can be anything ("My Production East!"), but the alias
// has to be CLI-friendly: lowercase letters, digits, dashes only. bm
// derives the alias from the cluster name at save time.
function toAliasName(name: string): string {
  const slug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32);
  return slug || "cluster";
}

export function Done({ draft, update, onFinish }: Props) {
  // Done is now a verification surface — credentials and topology are
  // already what the operator set in Basics. We just populate the
  // post-deploy facts (console URL, health counts) the first time the
  // step renders.
  useEffect(() => {
    if (!draft.done.consoleUrl) {
      const host =
        draft.hosts.find((h) => h.hostname.trim())?.hostname ||
        "node1.example.com";
      update({
        done: {
          ...draft.done,
          consoleUrl: `http://${host}:${draft.api.consolePort}`,
          nodesHealthy: draft.hosts.filter((h) => h.hostname.trim()).length,
          poolsOnline: 1,
          smokeTestPassed: true,
        },
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const d = draft.done;
  const rootUser = draft.credentials.rootUser || "admin";
  const rootPass = draft.credentials.rootPassword;
  const alias = toAliasName(draft.name);
  const aliasDiffers = alias !== draft.name.trim();

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
            <a
              href={d.consoleUrl}
              target="_blank"
              rel="noreferrer"
              className="mono"
            >
              {d.consoleUrl}
            </a>
          </div>
          <button className="btn btn--sm">Copy</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Root username</div>
            <div className="mono">{rootUser}</div>
          </div>
          <button className="btn btn--sm">Copy</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Root password</div>
            <div className="mono">
              {d.credsRevealed ? rootPass : "●".repeat(Math.max(rootPass.length, 8))}
            </div>
          </div>
          <div className="hstack">
            <button
              className="btn btn--sm"
              onClick={() =>
                update({ done: { ...d, credsRevealed: !d.credsRevealed } })
              }
            >
              {d.credsRevealed ? "Hide" : "Reveal"}
            </button>
            <button className="btn btn--sm">Copy</button>
          </div>
        </div>
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          The password you set in Basics. Confirm it's recorded in your
          secret manager — it won't be shown again from the manager.
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Quick checks</h3>
        <ul style={{ listStyle: "none", padding: 0, fontSize: "var(--fs-sm)" }}>
          <li>
            ● {d.nodesHealthy} / {d.nodesHealthy} nodes healthy
          </li>
          <li>
            ● {d.poolsOnline} / {d.poolsOnline} pool online
          </li>
          <li>
            ● Read/write smoke test{" "}
            {d.smokeTestPassed ? "passed" : "pending"}
          </li>
        </ul>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">bm alias</h3>
        <p style={{ fontSize: "var(--fs-sm)" }}>
          ✓ Alias <span className="mono">{alias}</span> saved to{" "}
          <span className="mono">~/.bm/config.json</span>
          {aliasDiffers && (
            <>
              {" "}
              <span className="subtle">
                (derived from cluster name{" "}
                <span className="mono">{draft.name}</span>)
              </span>
            </>
          )}
          .
        </p>
        <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          With the alias saved, the <span className="mono">bm</span> CLI
          can target this cluster by name.
        </p>
        <div
          className="vstack"
          style={{ gap: "var(--s-1)", fontSize: "var(--fs-sm)" }}
        >
          <span className="mono">bm admin info {alias}</span>
          <span className="mono">bm admin user add {alias} alice</span>
        </div>
        <details>
          <summary
            className="subtle"
            style={{ fontSize: "var(--fs-xs)", cursor: "pointer" }}
          >
            If you need to run the bm CLI on another machine
          </summary>
          <pre className="codeblock" style={{ marginTop: "var(--s-2)" }}>
{`bm alias set ${alias} ${d.consoleUrl} ${rootUser} <password>`}
          </pre>
        </details>
      </div>

      <div className="hstack" style={{ justifyContent: "flex-end" }}>
        <button className="btn btn--primary" onClick={onFinish}>
          Go to cluster overview
        </button>
      </div>
    </div>
  );
}
