import { useEffect, useRef } from "react";
import { NewClusterDraft, DeployNodeState } from "../state";
import { TaskLogStream } from "../../../../components/TaskLogStream";

interface Props {
  draft: NewClusterDraft;
  update: (patch: Partial<NewClusterDraft>) => void;
  onDone: () => void;
}

const ORDER: DeployNodeState["state"][] = [
  "pending",
  "downloading",
  "installing",
  "writing_config",
  "starting",
  "healthy",
];

const STATE_LABELS: Record<DeployNodeState["state"], string> = {
  pending: "Pending",
  downloading: "Downloading",
  installing: "Installing binary",
  writing_config: "Writing config",
  starting: "Starting service",
  healthy: "Service healthy",
  failed: "Failed",
};

const STATE_GLYPH: Record<DeployNodeState["state"], string> = {
  pending: "·",
  downloading: "⟳",
  installing: "⟳",
  writing_config: "⟳",
  starting: "⟳",
  healthy: "✓",
  failed: "✗",
};

export function Deploy({ draft, update, onDone }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const startedAt = new Date().toISOString();
    const init: Record<string, DeployNodeState> = {};
    hosts.forEach((h) => {
      init[h.id] = { state: "pending", elapsedSec: 0, lastEvent: "Queued" };
    });
    update({
      deploy: { startedAt, perNode: init, overallPct: 0, canceled: false },
    });

    let stage = 0;
    const advance = () => {
      stage++;
      if (stage >= ORDER.length) {
        // All nodes healthy
        update({
          deploy: {
            startedAt,
            perNode: Object.fromEntries(
              hosts.map((h, i) => [
                h.id,
                {
                  state: "healthy",
                  elapsedSec: 60 + i * 3,
                  lastEvent: "Started buckit.service",
                } as DeployNodeState,
              ]),
            ),
            overallPct: 100,
            canceled: false,
          },
        });
        setTimeout(onDone, 600);
        return;
      }

      const per: Record<string, DeployNodeState> = {};
      hosts.forEach((h, i) => {
        // staggered: each subsequent node lags one stage behind
        const idx = Math.max(0, stage - i);
        const s = ORDER[Math.min(idx, ORDER.length - 1)];
        per[h.id] = {
          state: s,
          elapsedSec: idx * 12,
          lastEvent:
            s === "downloading"
              ? `Fetching ${draft.version} (45 MiB)`
              : s === "installing"
                ? "Extracted to /usr/local/bin"
                : s === "writing_config"
                  ? "Wrote /etc/default/minio"
                  : s === "starting"
                    ? "systemctl start buckit"
                    : s === "healthy"
                      ? "Started buckit.service"
                      : "Queued",
        };
      });
      update({
        deploy: {
          startedAt,
          perNode: per,
          overallPct: Math.round((stage / ORDER.length) * 100),
          canceled: false,
        },
      });
      setTimeout(advance, 1500);
    };
    setTimeout(advance, 800);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
            Deploying {draft.name || "cluster"}
          </h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            ● Running · started just now
          </p>
        </div>
        <button className="btn btn--sm btn--danger">Cancel deploy</button>
      </header>

      <div className="hstack">
        <span className="subtle">Overall progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 320 }}>
          <div className="progress__bar" style={{ width: `${draft.deploy.overallPct}%` }} />
        </div>
        <span className="subtle">{draft.deploy.overallPct}%</span>
      </div>

      <div className="card" style={{ padding: 0 }}>
        {hosts.map((h) => {
          const ns = draft.deploy.perNode[h.id] ?? {
            state: "pending" as const,
            elapsedSec: 0,
            lastEvent: "—",
          };
          return (
            <div key={h.id} className="rolling-row">
              <div className="rolling-row__name">{h.hostname}</div>
              <div className="rolling-row__state">
                <span style={{ marginRight: 6 }}>{STATE_GLYPH[ns.state]}</span>
                {STATE_LABELS[ns.state]}
              </div>
              <div className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
                {ns.elapsedSec}s
              </div>
              <div className="rolling-row__event">{ns.lastEvent}</div>
            </div>
          );
        })}
      </div>

      <TaskLogStream taskId="new-cluster-deploy" />
    </div>
  );
}
