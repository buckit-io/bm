import { useState } from "react";
import { useApplyManagerUpdate, useManagerUpdateStatus, useSettings } from "../api/hooks";
import "./Settings.css";

export function Settings() {
  const [checkRequested, setCheckRequested] = useState(false);
  const { data: settings } = useSettings();
  const {
    data: updateStatus,
    error: updateError,
    isFetching: updateChecking,
    refetch: refetchUpdateStatus,
  } = useManagerUpdateStatus(checkRequested);
  const applyUpdate = useApplyManagerUpdate();
  const noUpdateMessage =
    checkRequested &&
    updateStatus &&
    !updateStatus.updateAvailable &&
    !updateStatus.restartRequired
      ? "No updates available."
      : null;

  async function handleCheckForUpdates() {
    setCheckRequested(true);
    const result = await refetchUpdateStatus();
    const status = result.data;
    if (!status || !status.updateAvailable || !status.canApply) {
      return;
    }
    if (!window.confirm(`Install ${status.latestVersion ?? "the latest update"} now? bm web will keep running until you restart it.`)) {
      return;
    }
    await applyUpdate.mutateAsync();
  }

  /*
  const [remoteOn, setRemoteOn] = useState(false);
  const [bindAddr, setBindAddr] = useState("0.0.0.0:9443");
  const [passcode, setPasscode] = useState("");
  const [certMode, setCertMode] = useState<"auto" | "custom">("auto");
  */

  return (
    <section className="settings">
      <h1>Settings</h1>

      {/* Remote access is planned for a future release.
      <div className="card settings__group">
        <h2 className="settings__group-title">Remote access</h2>
        <p className="subtle" style={{ fontSize: "var(--fs-sm)" }}>
          By default <span className="mono">bm web</span> only listens on{" "}
          <span className="mono">127.0.0.1</span>. Turn this on to allow
          access from other devices on your network. Requires a passcode and
          uses TLS.
        </p>
        <div className="settings__row">
          <div>
            <div className="field-label">Allow access from other devices</div>
          </div>
          <label className="hstack" style={{ gap: 6 }}>
            <input
              type="checkbox"
              checked={remoteOn}
              onChange={(e) => setRemoteOn(e.target.checked)}
            />
            {remoteOn ? "On" : "Off"}
          </label>
        </div>

        {remoteOn && (
          <>
            <div className="settings__row">
              <div>
                <div className="field-label">Bind address</div>
                <div className="subtle">e.g. 0.0.0.0:9443 or 192.168.1.50:9443</div>
              </div>
              <input
                className="input settings__sel"
                value={bindAddr}
                onChange={(e) => setBindAddr(e.target.value)}
              />
            </div>
            <div className="settings__row">
              <div>
                <div className="field-label">Passcode</div>
                <div className="subtle">Min 8 characters.</div>
              </div>
              <input
                className="input settings__sel"
                type="password"
                value={passcode}
                onChange={(e) => setPasscode(e.target.value)}
                placeholder="Set a passcode"
              />
            </div>
            <div className="settings__row">
              <div>
                <div className="field-label">TLS certificate</div>
              </div>
              <select
                className="select settings__sel"
                value={certMode}
                onChange={(e) =>
                  setCertMode(e.target.value as typeof certMode)
                }
              >
                <option value="auto">Auto-generated (self-signed)</option>
                <option value="custom">Use my own cert + key</option>
              </select>
            </div>
            {certMode === "custom" && (
              <>
                <div className="settings__row">
                  <div>
                    <div className="field-label">Certificate path</div>
                  </div>
                  <input
                    className="input settings__sel"
                    placeholder="/path/to/cert.pem"
                  />
                </div>
                <div className="settings__row">
                  <div>
                    <div className="field-label">Private key path</div>
                  </div>
                  <input
                    className="input settings__sel"
                    placeholder="/path/to/key.pem"
                  />
                </div>
              </>
            )}
          </>
        )}
      </div>
      */}

      <div className="card settings__group">
        <h2 className="settings__group-title">About</h2>
        <div className="settings__row">
          <div>
            <div className="field-label">Version</div>
            <div className="settings__value mono">{settings?.version ?? "unknown"}</div>
            {updateStatus?.installedVersion &&
              updateStatus.installedVersion !== updateStatus.currentVersion && (
                <div className="subtle settings__note">
                  Installed on disk: <span className="mono">{updateStatus.installedVersion}</span>
                </div>
              )}
            {updateStatus?.latestVersion &&
              (updateStatus.updateAvailable || updateStatus.restartRequired) && (
              <div className="subtle settings__note">
                Latest stable: <span className="mono">{updateStatus.latestVersion}</span>
              </div>
            )}
            {noUpdateMessage && <div className="subtle settings__note">{noUpdateMessage}</div>}
            {updateStatus?.reason && (
              <div className="subtle settings__note">{updateStatus.reason}</div>
            )}
            {updateError instanceof Error && (
              <div className="subtle settings__note">{updateError.message}</div>
            )}
            {applyUpdate.data?.message && (
              <div className="subtle settings__note">{applyUpdate.data.message}</div>
            )}
          </div>
          {!applyUpdate.isPending && (
            <div className="settings__actions">
              <button
                className="btn btn--sm"
                onClick={() => {
                  void handleCheckForUpdates();
                }}
                disabled={updateChecking}
              >
                {updateChecking ? "Checking..." : "Check for updates"}
              </button>
            </div>
          )}
        </div>
        <div className="settings__row">
          <div>
            <div className="field-label">Documentation</div>
          </div>
          <a
            className="btn btn--sm"
            href="https://github.com/buckit-io/buckit/tree/main/buckit/docs/manager"
            target="_blank"
            rel="noreferrer"
          >
            Open docs ↗
          </a>
        </div>
      </div>
    </section>
  );
}
