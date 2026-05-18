import { useEffect, useMemo, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { WizardShell } from "../../../layouts/WizardShell";
import { useCluster, useNodes } from "../../../api/hooks";
import { emptyMigration, HostRow, MigrationDraft, STEPS } from "./state";
import { SshCredentials } from "./steps/SshCredentials";
import { Review } from "./steps/Review";
import { Migrate } from "./steps/Migrate";

export function MigrationWizard() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const sourceId = params.get("from");

  // Migration only makes sense against an imported cluster — without a
  // source id, send the operator to /clusters to pick one (or import).
  if (!sourceId) return <Navigate to="/clusters" replace />;

  return <MigrationWizardInner sourceId={sourceId} navigate={navigate} />;
}

interface InnerProps {
  sourceId: string;
  navigate: ReturnType<typeof useNavigate>;
}

function MigrationWizardInner({ sourceId, navigate }: InnerProps) {
  const { data: cluster, isLoading: clusterLoading } = useCluster(sourceId);
  const { data: nodes, isLoading: nodesLoading } = useNodes(sourceId);

  const [draft, setDraft] = useState<MigrationDraft | null>(null);
  const [index, setIndex] = useState(0);

  useEffect(() => {
    if (draft) return;
    if (!cluster || !nodes) return;
    const hostRows: HostRow[] = nodes.map((n) => ({
      id: n.id,
      hostname: n.hostname,
      port: n.sshPort,
      probe: "idle",
    }));
    setDraft(
      emptyMigration({
        id: cluster.id,
        name: cluster.name,
        description: cluster.description,
        hosts: hostRows,
      }),
    );
  }, [cluster, nodes, draft]);

  const update = (patch: Partial<MigrationDraft>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  const next = () => setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const nextDisabled = useMemo(() => {
    if (!draft) return true;
    // Review step (index 1): block on any blocking preflight failure.
    // Preflight must have run at least once.
    if (index === 1) {
      if (draft.preflight.length === 0) return true;
      return draft.preflight.some(
        (p) => p.severity === "blocking" && p.result === "fail",
      );
    }
    // Migrate step (index 2): block Next while cutover is still running.
    // (It's the last step anyway, so onNext is undefined — included for
    // safety in case routing changes later.)
    if (index === 2) {
      const totalHosts = draft.hosts.filter((h) => h.hostname.trim()).length;
      return draft.cutover.overallNodesDone < totalHosts;
    }
    return false;
  }, [index, draft]);

  if (clusterLoading || nodesLoading || !draft) {
    return (
      <WizardShell
        title="Migrate to Buckit"
        steps={STEPS}
        currentIndex={0}
        nextDisabled
      >
        <p className="muted">Loading source cluster…</p>
      </WizardShell>
    );
  }

  if (!cluster) {
    return <Navigate to="/clusters" replace />;
  }

  const body = (() => {
    switch (index) {
      case 0: return <SshCredentials draft={draft} update={update} />;
      case 1: return <Review draft={draft} update={update} />;
      case 2: return (
        <Migrate
          draft={draft}
          update={update}
          onFinish={() => navigate(`/clusters/${draft.sourceClusterId}`)}
        />
      );
      default: return null;
    }
  })();

  return (
    <WizardShell
      title={`Migrate ${draft.name} to Buckit`}
      steps={STEPS}
      currentIndex={index}
      onJump={(i) => i <= index && setIndex(i)}
      onBack={index > 0 && index < STEPS.length - 1 ? back : undefined}
      onNext={index < STEPS.length - 1 ? next : undefined}
      nextDisabled={nextDisabled}
      nextLabel={
        // Review step: clicking Next here kicks off cutover.
        index === 1 ? "Start migration" : "Next →"
      }
    >
      <div className="wizard__inner">{body}</div>
    </WizardShell>
  );
}
