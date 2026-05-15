import { TaskStep } from "../mock/data";
import { formatDuration } from "../mock/data";
import { taskStateGlyph } from "./TaskStateIcon";
import "./TaskStepsTimeline.css";

export function TaskStepsTimeline({ steps }: { steps: TaskStep[] }) {
  return <ul className="timeline">{steps.map((s) => renderStep(s, 0))}</ul>;
}

function renderStep(step: TaskStep, depth: number) {
  return (
    <li
      key={step.id}
      className={`timeline__item is-${step.state}`}
      style={{ paddingLeft: `${depth * 18 + 4}px` }}
    >
      <span className="timeline__glyph">{taskStateGlyph(step.state)}</span>
      <span className="timeline__name">{step.name}</span>
      <span className="timeline__dur subtle">
        {step.durationSec !== undefined ? formatDuration(step.durationSec) : ""}
      </span>
      {step.children && step.children.length > 0 && (
        <ul className="timeline__children">
          {step.children.map((c) => renderStep(c, depth + 1))}
        </ul>
      )}
    </li>
  );
}
