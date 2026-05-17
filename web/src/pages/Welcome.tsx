import { Link } from "react-router-dom";
import "./Welcome.css";

export function Welcome() {
  return (
    <div className="welcome">
      <header className="welcome__header">
        <h1>Welcome to Buckit Manager</h1>
        <p className="muted">Let's get your first cluster running.</p>
      </header>

      <div className="welcome__choices">
        <Link to="/clusters/new" className="card welcome__card">
          <div className="welcome__icon welcome__icon--blue">🟦</div>
          <h2>Deploy a new cluster</h2>
          <p className="muted">
            Install Buckit on fresh hosts and form a new cluster over SSH.
          </p>
          <span className="welcome__cta">Get started →</span>
        </Link>

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
        <a href="https://github.com/buckit-io/buckit" target="_blank" rel="noreferrer">
          install guide ↗
        </a>
      </p>
    </div>
  );
}
