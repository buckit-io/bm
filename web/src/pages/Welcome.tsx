import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useClusters, useMe } from "../api/hooks";
import { NewClusterChoiceModal } from "./NewClusterChoiceModal";
import "./Welcome.css";

export function Welcome() {
  const { data: clusters } = useClusters();
  const { data: session, isLoading: sessionLoading } = useMe();
  const navigate = useNavigate();
  const hasClusters = (clusters?.length ?? 0) > 0;
  const [deployOpen, setDeployOpen] = useState(false);
  const showDeployChoice =
    sessionLoading || session?.goos === "darwin" || session?.goos === "windows";

  const startDeploy = () => {
    if (showDeployChoice) {
      setDeployOpen(true);
      return;
    }
    navigate("/clusters/new");
  };

  return (
    <div className="welcome">
      <header className="welcome__header">
        <h1>{hasClusters ? "Buckit Manager wizards" : "Welcome to Buckit Manager"}</h1>
        <p className="muted">
          {hasClusters
            ? "Let's add another cluster."
            : "Let's get your first cluster running."}
        </p>
      </header>

      <div className="welcome__choices">
        <button
          className="card welcome__card"
          onClick={startDeploy}
          data-testid="welcome-deploy-new"
        >
          <div className="welcome__icon welcome__icon--blue">🟦</div>
          <span className="welcome__card-title">Deploy a new cluster</span>
          <span className="muted">
            Install Buckit on fresh hosts and form a new cluster over SSH.
          </span>
          <span className="welcome__cta">Get started →</span>
        </button>

        <Link to="/clusters/import" className="card welcome__card">
          <div className="welcome__icon welcome__icon--orange">🟧</div>
          <h2>Import existing Buckit or MinIO cluster</h2>
          <p className="muted">
            Register a running deployment to monitor and operate it from
            here. MinIO Community Edition clusters can be upgraded to the
            latest Buckit in place — same disks, same data.
          </p>
          <span className="welcome__cta">Get started →</span>
        </Link>
      </div>

      <p className="welcome__hint">
        Need help? See the{" "}
        <a href="https://buckit.sh/docs/operations/deployments/guided-deployment-using-bucket-manager" target="_blank" rel="noreferrer">
          install guide ↗
        </a>
      </p>
      {deployOpen && <NewClusterChoiceModal onClose={() => setDeployOpen(false)} />}
    </div>
  );
}
