import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useCluster } from "../../api/hooks";
import {
  ApiError,
  getClusterAdminCreds,
  saveClusterAdminCreds,
} from "../../api/client";

export function ClusterSettings() {
  const { clusterId = "" } = useParams();
  const { data: cluster, isLoading: loadingCluster } = useCluster(clusterId);
  const [url, setURL] = useState("");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [insecure, setInsecure] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [loadingCreds, setLoadingCreds] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoadingCreds(true);
    setError(null);
    getClusterAdminCreds(clusterId)
      .then((creds) => {
        if (cancelled) return;
        setURL(creds?.url ?? "");
        setAccessKey(creds?.accessKey ?? "");
        setInsecure(creds?.insecure ?? false);
        setSecretKey("");
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : "failed to load admin credentials");
      })
      .finally(() => {
        if (!cancelled) setLoadingCreds(false);
      });
    return () => {
      cancelled = true;
    };
  }, [clusterId]);

  const canSave =
    url.trim() !== "" &&
    accessKey.trim() !== "" &&
    secretKey !== "" &&
    !saving;

  async function onSave() {
    if (!canSave) return;
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await saveClusterAdminCreds(clusterId, {
        url: url.trim(),
        accessKey: accessKey.trim(),
        secretKey,
        insecure,
      });
      setSecretKey("");
      setSaved(true);
    } catch (err) {
      const msg =
        err instanceof ApiError || err instanceof Error
          ? err.message
          : "failed to save admin credentials";
      setError(msg);
    } finally {
      setSaving(false);
    }
  }

  if (loadingCluster || loadingCreds) {
    return <div className="card">Loading settings...</div>;
  }

  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 760 }}>
      <div className="card vstack" style={{ gap: "var(--s-3)" }}>
        <div>
          <h2 className="card-stat__title">Cluster credentials</h2>
          <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
            Update the admin API credentials stored locally for{" "}
            <b>{cluster?.name ?? clusterId}</b>.
          </p>
        </div>

        {error && <div className="banner banner--danger">{error}</div>}
        {saved && (
          <div className="banner banner--success">
            Admin credentials saved.
          </div>
        )}

        <div className="field">
          <label className="field-label">Admin API URL</label>
          <input
            className="input"
            value={url}
            onChange={(e) => setURL(e.target.value)}
            placeholder="https://node1.example.com:9000"
          />
        </div>

        <div className="field">
          <label className="field-label">Root username</label>
          <input
            className="input"
            value={accessKey}
            onChange={(e) => setAccessKey(e.target.value)}
            placeholder="buckitadmin"
          />
        </div>

        <div className="field">
          <label className="field-label">Current root password</label>
          <div className="hstack" style={{ gap: "var(--s-2)" }}>
            <input
              type={showSecret ? "text" : "password"}
              className="input"
              value={secretKey}
              onChange={(e) => setSecretKey(e.target.value)}
              style={{ flex: 1 }}
            />
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => setShowSecret((v) => !v)}
            >
              {showSecret ? "Hide" : "Show"}
            </button>
          </div>
          <span className="field-hint">
            The stored password is encrypted and is never displayed.
          </span>
        </div>

        <label className="hstack" style={{ gap: "var(--s-2)" }}>
          <input
            type="checkbox"
            checked={insecure}
            onChange={(e) => setInsecure(e.target.checked)}
          />
          <span>Skip TLS certificate verification</span>
        </label>

        <div className="hstack" style={{ justifyContent: "space-between" }}>
          <Link className="btn btn--sm" to={`/clusters/${clusterId}/ssh`}>
            Configure SSH credentials
          </Link>
          <button
            className="btn btn--primary"
            disabled={!canSave}
            onClick={onSave}
          >
            {saving ? "Saving..." : "Save admin credentials"}
          </button>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h2 className="card-stat__title" style={{ color: "var(--c-danger)" }}>
          Danger zone
        </h2>
        <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          Removing a cluster stops tracking it in this manager. Hosts and data
          are not changed.
        </p>
        <div>
          <button className="btn btn--danger">Remove cluster definition</button>
        </div>
      </div>
    </div>
  );
}
