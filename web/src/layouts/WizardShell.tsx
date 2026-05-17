import { ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Stepper, StepperStep } from "../components/Stepper";
import "./WizardShell.css";

interface WizardShellProps {
  title: string;
  steps: StepperStep[];
  currentIndex: number;
  onJump?: (i: number) => void;
  onBack?: () => void;
  onNext?: () => void;
  nextLabel?: string;
  nextDisabled?: boolean;
  children: ReactNode;
}

export function WizardShell({
  title,
  steps,
  currentIndex,
  onJump,
  onBack,
  onNext,
  nextLabel = "Next →",
  nextDisabled,
  children,
}: WizardShellProps) {
  const navigate = useNavigate();
  return (
    <div className="wizard">
      <header className="wizard__topbar">
        <h1 className="wizard__title">{title}</h1>
        <button
          className="btn btn--ghost btn--sm"
          onClick={() => navigate("/clusters")}
        >
          ✕ Exit
        </button>
      </header>

      <div className="wizard__stepper">
        <Stepper steps={steps} currentIndex={currentIndex} onJump={onJump} />
      </div>

      <main className="wizard__content">{children}</main>

      <footer className="wizard__footer">
        {onBack && (
          <button
            className="btn"
            onClick={onBack}
            disabled={currentIndex === 0}
          >
            ← Back
          </button>
        )}
        <div className="grow" />
        <button
          className="btn btn--primary"
          onClick={onNext}
          disabled={nextDisabled}
        >
          {nextLabel}
        </button>
      </footer>
    </div>
  );
}
