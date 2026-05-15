import "./Stepper.css";

export interface StepperStep {
  id: string;
  label: string;
}

interface StepperProps {
  steps: StepperStep[];
  currentIndex: number;
  onJump?: (index: number) => void;
}

export function Stepper({ steps, currentIndex, onJump }: StepperProps) {
  return (
    <ol className="stepper" aria-label="Wizard progress">
      {steps.map((step, i) => {
        const done = i < currentIndex;
        const active = i === currentIndex;
        const reachable = i <= currentIndex && onJump;
        return (
          <li
            key={step.id}
            className={
              "stepper__step" +
              (done ? " is-done" : "") +
              (active ? " is-active" : "")
            }
          >
            <button
              type="button"
              className="stepper__btn"
              disabled={!reachable && !active}
              onClick={() => reachable && onJump?.(i)}
            >
              <span className="stepper__num">{done ? "✓" : i + 1}</span>
              <span className="stepper__label">{step.label}</span>
            </button>
            {i < steps.length - 1 && <span className="stepper__sep" aria-hidden />}
          </li>
        );
      })}
    </ol>
  );
}
