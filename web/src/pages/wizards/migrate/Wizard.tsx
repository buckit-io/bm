import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { WizardShell } from "../../../layouts/WizardShell";
import { emptyMigration, MigrationDraft, STEPS } from "./state";
import { Basics } from "./steps/Basics";
import { AddNodes } from "./steps/AddNodes";
import { Discover } from "./steps/Discover";
import { Snapshot } from "./steps/Snapshot";
import { Plan } from "./steps/Plan";
import { Preflight } from "./steps/Preflight";
import { Cutover } from "./steps/Cutover";
import { Verify } from "./steps/Verify";
import { Finalize } from "./steps/Finalize";

export function MigrationWizard() {
  const navigate = useNavigate();
  const [draft, setDraft] = useState<MigrationDraft>(() => emptyMigration());
  const [index, setIndex] = useState(0);
  const update = (patch: Partial<MigrationDraft>) =>
    setDraft((d) => ({ ...d, ...patch }));

  const next = () => setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const nextDisabled = useMemo(() => {
    if (index === 0) return !draft.name.trim();
    if (index === 1)
      return draft.hosts.filter((h) => h.hostname.trim()).length < 2;
    if (index === 4) return !draft.plan.ack;
    return false;
  }, [index, draft]);

  const body = (() => {
    switch (index) {
      case 0: return <Basics draft={draft} update={update} />;
      case 1: return <AddNodes draft={draft} update={update} />;
      case 2: return <Discover draft={draft} update={update} />;
      case 3: return <Snapshot draft={draft} update={update} />;
      case 4: return <Plan draft={draft} update={update} />;
      case 5: return <Preflight draft={draft} update={update} />;
      case 6: return <Cutover draft={draft} update={update} onDone={next} />;
      case 7: return <Verify draft={draft} update={update} />;
      case 8: return <Finalize draft={draft} update={update} onFinish={() => navigate("/clusters/legacy-migrate")} />;
      default: return null;
    }
  })();

  return (
    <WizardShell
      title="Migrate from MinIO (in-place)"
      steps={STEPS}
      currentIndex={index}
      onJump={(i) => i <= index && setIndex(i)}
      onBack={index > 0 ? back : undefined}
      onNext={index < STEPS.length - 1 ? next : undefined}
      nextDisabled={nextDisabled}
      nextLabel={
        index === 5
          ? "Start cutover →"
          : index === STEPS.length - 1
            ? "Finalize migration"
            : "Next →"
      }
    >
      <div className="wizard__inner">{body}</div>
    </WizardShell>
  );
}
