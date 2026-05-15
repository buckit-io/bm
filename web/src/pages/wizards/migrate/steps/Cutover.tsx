import { useEffect, useRef } from "react";
import { MigrationDraft, CutoverNodeState } from "../state";
import { TaskLogStream } from "../../../../components/TaskLogStream";
import { Pill } from "../../../../components/Pill";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
  onDone: () => void;
}

const STAGES: CutoverNodeState["state"][] = [
  "stopping_minio",
  "uploading_pkg",
  "installing",
  "switching_unit",
  "waiting_health",
  "waiting_cluster",
  "done",
];
const LABEL: Record<CutoverNodeState["state"], string> = {
  pending: "Pending",
  stopping_minio: "Stopping minio",
  uploading_pkg: "Uploading package",
  installing: "Installing buckit",
  switching_unit: "Switching unit",
  waiting_health: "Waiting node-healthy",
  waiting_cluster: "Waiting cluster-healthy",
  done: "Buckit healthy",
  rolled_back: "Rolled back",
  failed: "Failed",
};

function statePill(s: CutoverNodeState["state"]) {
  if (s === "done") return <Pill tone="success" icon="✓">{LABEL[s]}</Pill>;
  if (s === "pending") return <Pill tone="neutral" icon="·">{LABEL[s]}</Pill>;
  if (s === "rolled_back") return <Pill tone="warning" icon="↺">{LABEL[s]}</Pill>;
  if (s === "failed") return <Pill tone="danger" icon="✗">{LABEL[s]}</Pill>;
  return <Pill tone="info" icon="⟳">{LABEL[s]}</Pill>;
}

export function Cutover({ draft, update, onDone }: Props) {
  const hosts = draft.hosts.filter((h) => h.hostname.trim());
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const per: Record<string, CutoverNodeState> = {};
    hosts.forEach((h) => (per[h.id] = { state: "pending" }));
    update({ cutover: { perNode: per, overallNodesDone: 0, paused: false } });

    let nodeIdx = 0;
    let stageIdx = 0;
    const tick = () => {
      if (nodeIdx >= hosts.length) {
        setTimeout(onDone, 600);
        return;
      }
      const h = hosts[nodeIdx];
      if (stageIdx === 0) per[h.id] = { state: STAGES[0], cutoverStartedAt: new Date().toISOString() };
      else per[h.id] = { ...per[h.id], state: STAGES[stageIdx] };
      if (stageIdx === STAGES.length - 1) {
        per[h.id].durationSec = 60 + Math.round(Math.random() * 20);
      }
      update({
        cutover: {
          perNode: { ...per },
          overallNodesDone: stageIdx === STAGES.length - 1 ? nodeIdx + 1 : nodeIdx,
          paused: false,
        },
      });
      if (stageIdx >= STAGES.length - 1) {
        stageIdx = 0;
        nodeIdx++;
      } else {
        stageIdx++;
      }
      setTimeout(tick, 350);
    };
    setTimeout(tick, 400);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const done = draft.cutover.overallNodesDone;
  const pct = Math.round((done / hosts.length) * 100);

  return (
    <div className="vstack" style={{ gap: "var(--s-4)" }}>
      <header className="hstack" style={{ justifyContent: "space-between" }}>
        <div>
          <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
            Migrating {draft.name} → Buckit
          </h2>
          <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
            ● Running · {done}/{hosts.length} nodes migrated
          </p>
        </div>
        <div className="hstack">
          <button className="btn btn--sm">Pause after current node</button>
          <button className="btn btn--sm btn--danger">Rollback all completed</button>
        </div>
      </header>

      <div className="hstack">
        <span className="subtle">Overall progress</span>
        <div className="progress" style={{ flex: 1, maxWidth: 320 }}>
          <div className="progress__bar" style={{ width: `${pct}%` }} />
        </div>
        <span className="subtle">{done} / {hosts.length}</span>
      </div>

      <div className="card" style={{ padding: 0 }}>
        {hosts.map((h) => {
          const s = draft.cutover.perNode[h.id] ?? { state: "pending" as const };
          return (
            <div key={h.id} className="rolling-row" style={{ gridTemplateColumns: "200px 1fr 90px" }}>
              <div className="rolling-row__name">{h.hostname}</div>
              <div>{statePill(s.state)}</div>
              <div className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
                {s.durationSec ? `${s.durationSec}s` : ""}
              </div>
            </div>
          );
        })}
      </div>

      <div className="cards-row">
        <div className="card card-stat">
          <div className="card-stat__title">Cluster health (live)</div>
          <div className="card-stat__sub">Quorum ✓ maintained throughout cutover</div>
          <div className="card-stat__sub">Read availability 100%</div>
          <div className="card-stat__sub">
            Write availability {Math.max(50, 100 - hosts.length * 2)}% (recovers between nodes)
          </div>
        </div>
        <div className="card card-stat" style={{ gridColumn: "span 2" }}>
          <div className="card-stat__title">Live log</div>
          <TaskLogStream taskId="migrate-cutover" />
        </div>
      </div>
    </div>
  );
}
