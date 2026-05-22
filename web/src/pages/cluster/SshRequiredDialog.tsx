interface Props {
  clusterName: string;
  onClose: () => void;
  onConfigure: () => void;
}

export function SshRequiredDialog({
  clusterName,
  onClose,
  onConfigure,
}: Props) {
  return (
    <div className="modal-backdrop" onClick={(e) => e.stopPropagation()}>
      <div
        className="card modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <header style={{ paddingBottom: "var(--s-2)" }}>
          <h2 style={{ fontSize: "var(--fs-lg)", fontWeight: 600 }}>
            SSH credentials required{" "}
            <span className="subtle" style={{ fontWeight: 400 }}>
              · {clusterName}
            </span>
          </h2>
        </header>
        <div className="vstack" style={{ gap: "var(--s-3)" }}>
          <div className="banner banner--danger">
            <span>✗</span>
            <span>
              This action requires SSH credentials for the cluster, but none are
              configured yet.
            </span>
          </div>
          <p className="muted" style={{ fontSize: "var(--fs-sm)" }}>
            Open the SSH Credentials page first, save the cluster SSH user and
            auth method, then retry the action.
          </p>
        </div>
        <div
          className="hstack"
          style={{ justifyContent: "flex-end", marginTop: "var(--s-3)" }}
        >
          <button className="btn" onClick={onClose}>
            Close
          </button>
          <button className="btn btn--primary" onClick={onConfigure}>
            Go to SSH Credentials
          </button>
        </div>
      </div>
    </div>
  );
}
