import { Pill, PillTone } from "./Pill";
import { TaskState } from "../mock/data";

const MAP: Record<TaskState, { tone: PillTone; icon: string; label: string }> = {
  pending: { tone: "neutral", icon: "·", label: "Pending" },
  running: { tone: "info", icon: "⟳", label: "Running" },
  succeeded: { tone: "success", icon: "✓", label: "Success" },
  failed: { tone: "danger", icon: "✗", label: "Failed" },
  canceled: { tone: "neutral", icon: "○", label: "Canceled" },
};

export function TaskStatePill({ state }: { state: TaskState }) {
  const m = MAP[state];
  return <Pill tone={m.tone} icon={m.icon}>{m.label}</Pill>;
}

export function taskStateGlyph(state: TaskState) {
  return MAP[state].icon;
}
