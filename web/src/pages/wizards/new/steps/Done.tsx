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
  // Done renders the post-deploy verification captured during the Deploy
  // step. Only fill in a fallback console URL if the verify summary has not
  // populated one yet.
  useEffect(() => {
    if (!draft.done.consoleUrl) {
      const host =
        draft.hosts.find((h) => h.hostname.trim())?.hostname ||
        "node1.example.com";
      update({
        done: {
          ...draft.done,
          consoleUrl: `http://${host}:${draft.api.consolePort}`,
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
  const totalHosts = draft.hosts.filter((h) => h.hostname.trim()).length;
  const quickChecks = [
    {
      label: `${d.nodesHealthy} / ${totalHosts} nodes healthy`,
      healthy: totalHosts > 0 && d.nodesHealthy === totalHosts,
    },
    {
      label: `${d.poolsOnline} / 1 pool online`,
      healthy: d.poolsOnline > 0,
    },
    {
      label: `Read/write smoke test ${d.smokeTestPassed ? "passed" : "pending"}`,
      healthy: d.smokeTestPassed,
    },
  ];

  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 720 }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-2xl)", fontWeight: 600 }}>
          <span data-testid="new-cluster-done-title">✓ {draft.name || "cluster"} is up</span>
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
              data-testid="new-cluster-console-url"
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
          {quickChecks.map((check) => (
            <li
              key={check.label}
              className="hstack"
              style={{ gap: "var(--s-2)", alignItems: "center" }}
            >
              <span
                aria-hidden="true"
                style={{
                  color: check.healthy ? "var(--c-success)" : "var(--c-fg-muted)",
                  fontSize: "var(--fs-sm)",
                  lineHeight: 1,
                }}
              >
                ●
              </span>
              <span>{check.label}</span>
            </li>
          ))}
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
        <pre className="codeblock">{`bm admin info ${alias}
bm admin user add ${alias} alice`}</pre>
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
