export function ClusterSettings() {
  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 640 }}>
      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h2 className="card-stat__title">Configuration</h2>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">SSH credentials</div>
            <div className="subtle">Encrypted at rest</div>
          </div>
          <div className="hstack">
            <button className="btn btn--sm">View</button>
            <button className="btn btn--sm">Rotate</button>
          </div>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Buckit version pin</div>
            <div className="mono">v1.0.0</div>
          </div>
          <button className="btn btn--sm">Change…</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Health probe interval</div>
            <div>Every 30 s</div>
          </div>
          <button className="btn btn--sm">Edit</button>
        </div>
        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <div>
            <div className="field-label">Audit log retention</div>
            <div>30 days</div>
          </div>
          <button className="btn btn--sm">Edit</button>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h2 className="card-stat__title" style={{ color: "var(--c-danger)" }}>
          Danger zone
        </h2>
        <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          Tearing down a cluster stops every node, removes the buckit package,
          and detaches the cluster from this manager. Data drives are not
          erased.
        </p>
        <div>
          <button className="btn btn--danger">Tear down cluster…</button>
        </div>
      </div>
    </div>
  );
}
