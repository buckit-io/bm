// Migration overview step. Lands the operator on a brief explanation of
// what the migration does, what it expects to find on each host, and lets
// them pick the Buckit version to install. The version catalog is fetched
// from the backend's /artifacts/versions endpoint (GitHub releases), so
// it's always the live list — the migrate wizard used to ship with a
// hardcoded "v1.0.0" default that no longer existed in the catalog and
// failed the artifact_reachable preflight check.

import { useEffect, useState } from "react";
import { listVersions } from "../../../../api/client";
import type { BuckitVersion } from "../../../../api/types";
import { MigrationDraft } from "../state";

interface Props {
  draft: MigrationDraft;
  update: (patch: Partial<MigrationDraft>) => void;
}

export function Overview({ draft, update }: Props) {
  const [versions, setVersions] = useState<BuckitVersion[]>([]);
  const [versionsError, setVersionsError] = useState<string | null>(null);

  useEffect(() => {
    listVersions()
      .then((vs) => {
        setVersions(vs);
        setVersionsError(null);
      })
      .catch((e) => {
        const msg = e instanceof Error ? e.message : "Failed to fetch versions.";
        setVersionsError(msg);
      });
  }, []);

  // Once the catalog arrives, seed draft.version if it's empty (first
  // mount) or stale (a previously-saved tag that no longer exists).
  useEffect(() => {
    if (versions.length === 0) return;
    if (draft.version && versions.some((v) => v.tag === draft.version)) return;
    update({ version: versions[0].tag });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versions, draft.version]);

  return (
    <div className="vstack" style={{ gap: "var(--s-5)", maxWidth: 720 }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>
          Migration overview
        </h2>
        <p
          className="muted"
          style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}
        >
          Buckit Manager will replace MinIO with Buckit in place on every
          host in this cluster. Object data stays on the existing drives;
          only the binary and the systemd unit change.
        </p>
      </header>

      <section className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">How migration works</h3>
        <p style={{ fontSize: "var(--fs-sm)" }}>
          One host at a time through SSH:
        </p>
        <ul
          className="vstack"
          style={{
            gap: "var(--s-1)",
            paddingLeft: "var(--s-5)",
            fontSize: "var(--fs-sm)",
          }}
        >
          <li>Stop MinIO systemd service</li>
          <li>Install Buckit systemd service</li>
          <li>Start systemd service</li>
          <li>Wait healthy, move to next host</li>
        </ul>
        <p style={{ fontSize: "var(--fs-sm)" }}>
          A systemd drop-in is added so Buckit runs as{" "}
          <span className="mono">minio-user</span> and reads the same env
          file as MinIO. Configurations, drives and object data are
          untouched. If a host fails, the wizard rolls every cut-over
          host back to MinIO.
        </p>
      </section>

      <section className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Prerequisites on every host</h3>
        <ul
          className="vstack"
          style={{
            gap: "var(--s-1)",
            paddingLeft: "var(--s-5)",
            fontSize: "var(--fs-sm)",
          }}
        >
          <li>
            SSH access as <span className="mono">root</span> or with
            passwordless <span className="mono">sudo</span>.
          </li>
          <li>
            <span className="mono">minio.service</span> registered with
            systemd and using <span className="mono">/etc/default/minio</span>{" "}
            (or <span className="mono">/etc/sysconfig/minio</span>).
          </li>
          <li>
            Same architecture across the cluster (
            <span className="mono">amd64</span> or{" "}
            <span className="mono">arm64</span>).
          </li>
          <li>Network access to download the Buckit RPM.</li>
        </ul>
      </section>

      <section className="card vstack" style={{ gap: "var(--s-3)" }}>
        <h3 className="card-stat__title">Buckit version</h3>
        <div className="field">
          <label className="field-label" htmlFor="migrate-version">
            Install this version on every host
          </label>
          <select
            id="migrate-version"
            className="select"
            value={draft.version}
            onChange={(e) => update({ version: e.target.value })}
            disabled={versions.length === 0}
          >
            {versions.length === 0 && !versionsError && (
              <option value="">Loading versions…</option>
            )}
            {versions.map((v) => (
              <option key={v.tag} value={v.tag}>
                {v.label}
              </option>
            ))}
          </select>
          {versionsError && (
            <span
              className="field-hint"
              style={{ color: "var(--c-danger)" }}
            >
              Couldn't load the version catalog — {versionsError}. Check
              network access to GitHub releases, then revisit this page.
            </span>
          )}
        </div>
      </section>
    </div>
  );
}
