import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { WizardShell } from "../../../layouts/WizardShell";
import {
  validateRootPassword,
  validateRootUser,
} from "../../../lib/credentials";
import { looksLikePemCert, looksLikePemKey } from "../../../lib/tls";
import { CUSTOM_VERSION, emptyDraft, NewClusterDraft, STEPS } from "./state";
import { topologyErrors } from "./steps/topologyValidation";
import { Basics } from "./steps/Basics";
import { AddNodes } from "./steps/AddNodes";
import { Discover } from "./steps/Discover";
import { Topology } from "./steps/Topology";
import { Preflight } from "./steps/Preflight";
import { Review } from "./steps/Review";
import { Deploy } from "./steps/Deploy";
import { Done } from "./steps/Done";

function clusterRouteForDraft(draft: NewClusterDraft): string {
  const slug = draft.done.clusterId || draft.name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32) || "cluster";
  return `/clusters/${encodeURIComponent(slug)}`;
}

export function NewClusterWizard() {
  const navigate = useNavigate();
  const [draft, setDraft] = useState<NewClusterDraft>(() => emptyDraft());
  const [index, setIndex] = useState(0);
  const update = (patch: Partial<NewClusterDraft>) =>
    setDraft((d) => ({ ...d, ...patch }));

  const next = () => setIndex((i) => Math.min(STEPS.length - 1, i + 1));
  const back = () => setIndex((i) => Math.max(0, i - 1));

  const nextDisabled = useMemo(() => {
    if (index === 0) {
      if (!draft.name.trim()) return true;
      // Custom URL path: require a URL whose validation has resolved to
      // valid or warn. Block on error, blank, or in-flight check.
      if (draft.version === CUSTOM_VERSION) {
        if (!draft.customUrl.trim()) return true;
        const s = draft.customUrlCheck.state;
        if (s !== "valid" && s !== "warn") return true;
      }
      // Root credentials: same rules as the rotate_root_creds operation —
      // see web/src/lib/credentials.ts.
      if (!validateRootUser(draft.credentials.rootUser).ok) return true;
      if (!validateRootPassword(draft.credentials.rootPassword).ok) return true;
      // Ports: valid 1-65535 and not equal to each other.
      const p = draft.api.port;
      const c = draft.api.consolePort;
      if (!Number.isInteger(p) || p < 1 || p > 65535) return true;
      if (!Number.isInteger(c) || c < 1 || c > 65535) return true;
      if (p === c) return true;
      // TLS (when BYO selected): require PEM cert + key that at least
      // look like PEM blocks. Backend re-validates parse / pairing /
      // expiry / SAN coverage in preflight.
      if (draft.tls.mode === "byo") {
        if (!looksLikePemCert(draft.tls.certPem)) return true;
        if (!looksLikePemKey(draft.tls.keyPem)) return true;
        // If the operator typed a serverUrl, it must use https when TLS
        // is on (otherwise MinIO advertises a URL it doesn't actually serve).
        const u = draft.serverUrl.trim();
        if (u && !/^https:\/\//i.test(u)) return true;
      }
      return false;
    }
    if (index === 1) {
      const enteredHosts = draft.hosts.filter((h) => h.hostname.trim());
      if (enteredHosts.length < 1) return true;
      return enteredHosts.some((h) => h.probe !== "reachable");
    }
    if (index === 3) return topologyErrors(draft).length > 0;
    if (index === 4) {
      // Block on any blocking-severity preflight failure. Advisory
      // warnings are surfaced but don't gate Next.
      return draft.preflight.some(
        (p) => p.severity === "blocking" && p.result === "fail",
      );
    }
    if (index === 6) {
      // Deploy step: enable Next only after deploy completes, so the
      // operator can review the final state before continuing.
      return draft.deploy.overallPct < 100;
    }
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
      case 6: return <Deploy draft={draft} update={update} />;
      case 7: return <Done draft={draft} update={update} onFinish={() => navigate(clusterRouteForDraft(draft))} />;
      default: return null;
    }
  })();

  return (
    <WizardShell
      title="Deploy a new Buckit cluster"
      steps={STEPS}
      currentIndex={index}
      onJump={(i) => i <= index && setIndex(i)}
      onBack={index > 0 && index < STEPS.length - 1 ? back : undefined}
      onNext={index < STEPS.length - 1 ? next : undefined}
      nextDisabled={nextDisabled}
      nextLabel={
        // Review step: clicking Next here kicks off the deploy.
        index === STEPS.length - 3
          ? "Start deploying"
          : index === STEPS.length - 1
            ? "Finish"
            : "Next →"
      }
    >
      <div className="wizard__inner">{stepBody}</div>
    </WizardShell>
  );
}
