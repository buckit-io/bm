import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { me } from "../api/client";
import "./NewClusterChoiceModal.css";

interface Props {
  onClose: () => void;
}

export function NewClusterChoiceModal({ onClose }: Props) {
  const navigate = useNavigate();
  const modalRef = useRef<HTMLDivElement | null>(null);
  const [goos, setGoos] = useState<string | undefined>();
  const localSupported = useMemo(
    () => goos === "darwin" || goos === "windows",
    [goos],
  );

  useEffect(() => {
    me().then((session) => setGoos(session.goos)).catch(() => {});
  }, []);

  useEffect(() => {
    const modal = modalRef.current;
    const first = modal?.querySelector<HTMLElement>(
      "button:not(:disabled), [href], input, select, textarea, [tabindex]:not([tabindex='-1'])",
    );
    first?.focus();
  }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const modal = modalRef.current;
      if (!modal) return;
      const focusable = Array.from(
        modal.querySelectorAll<HTMLElement>(
          "button:not(:disabled), [href], input, select, textarea, [tabindex]:not([tabindex='-1'])",
        ),
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const choose = (path: string) => {
    onClose();
    navigate(path);
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        ref={modalRef}
        className="card modal modal--lg new-cluster-choice"
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-cluster-choice-title"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="new-cluster-choice__header">
          <div>
            <h2 id="new-cluster-choice-title">Select deployment type</h2>
            <p className="muted">Choose where Buckit should be deployed.</p>
          </div>
          <button className="btn btn--ghost btn--sm" onClick={onClose}>
            Close
          </button>
        </header>

        <div className="new-cluster-choice__grid">
          <button
            className="new-cluster-choice__card"
            onClick={() => choose("/clusters/new")}
            data-testid="new-cluster-remote-choice"
          >
            <span className="new-cluster-choice__title">Remote servers</span>
            <span className="new-cluster-choice__body">
              Deploy Buckit as a managed cluster on one or more Linux servers over SSH.
            </span>
          </button>

          <button
            className={"new-cluster-choice__card" + (!localSupported ? " is-disabled" : "")}
            onClick={() => choose("/clusters/new/local")}
            disabled={!localSupported}
            data-testid="new-cluster-local-choice"
          >
            <span className="new-cluster-choice__title">This computer</span>
            <span className="new-cluster-choice__body">
              {localSupported
                ? "Download and configure Buckit as a single-node cluster on this computer."
                : goos === undefined
                  ? "Checking whether local setup is available on this computer."
                  : "Local single-node setup is available when bm is running on macOS or Windows."}
            </span>
          </button>
        </div>
      </div>
    </div>
  );
}
