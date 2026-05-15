import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { WizardShell } from "../../../layouts/WizardShell";
import { emptyDraft, NewClusterDraft, STEPS } from "./state";
import { Basics } from "./steps/Basics";
import { AddNodes } from "./steps/AddNodes";
import { Discover } from "./steps/Discover";
import { Topology } from "./steps/Topology";
import { Preflight } from "./steps/Preflight";
import { Review } from "./steps/Review";
import { Deploy } from "./steps/Deploy";
import { Done } from "./steps/Done";

export function NewClusterWizard() {
  const navigate = useNavigate();
  const [draft, setDraft] = useState<NewClusterDraft>(() => emptyDraft());
  const [index, setIndex] = useState(0);
  const update = (patch: Partial<NewClusterDraft>) =>
    setDraft((d) => ({ ...d, ...patch }));

  const next = () => setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const nextDisabled = useMemo(() => {
    if (index === 0) return !draft.name.trim();
    if (index === 1)
      return draft.hosts.filter((h) => h.hostname.trim()).length < 2;
    return false;
  }, [index, draft]);

  const stepBody = (() => {
    switch (index) {
      case 0: return <Basics draft={draft} update={update} />;
      case 1: return <AddNodes draft={draft} update={update} />;
      case 2: return <Discover draft={draft} update={update} />;
      case 3: return <Topology draft={draft} update={update} />;
      case 4: return <Preflight draft={draft} update={update} />;
      case 5: return <Review draft={draft} />;
      case 6: return <Deploy draft={draft} update={update} onDone={next} />;
      case 7: return <Done draft={draft} update={update} onFinish={() => navigate("/clusters/prod-east")} />;
      default: return null;
    }
  })();

  return (
    <WizardShell
      title="Deploy a new Buckit cluster"
      steps={STEPS}
      currentIndex={index}
      onJump={(i) => i <= index && setIndex(i)}
      onBack={index > 0 ? back : undefined}
      onNext={index < STEPS.length - 1 ? next : undefined}
      nextDisabled={nextDisabled}
      nextLabel={
        index === STEPS.length - 2
          ? "Deploy →"
          : index === STEPS.length - 1
            ? "Finish"
            : "Next →"
      }
      onSaveDraft={index < STEPS.length - 2 ? () => navigate("/clusters") : undefined}
    >
      <div className="wizard__inner">{stepBody}</div>
    </WizardShell>
  );
}
