import { NewClusterDraft } from "../state";
import { detectHostnamePattern } from "./hostnamePattern";

interface Props { draft: NewClusterDraft; }

const GiB = 1024 ** 3;
const TiB = 1024 ** 4;
const STORAGE_SUBDIR = "buckit";

function humanSize(bytes: number): string {
  if (bytes >= TiB) return `${(bytes / TiB).toFixed(1)} TiB`;
  if (bytes >= GiB) return `${(bytes / GiB).toFixed(1)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(0)} MiB`;
  return `${(bytes / 1024).toFixed(0)} KiB`;
}

function storagePathForMount(mount: string): string {
  const trimmed = mount.replace(/\/+$/, "");
  return `${trimmed || "/"}${trimmed === "/" ? "" : "/"}${STORAGE_SUBDIR}`;
}

function detectNumericPattern(values: string[]): string | null {
  const list = values.map((v) => v.trim()).filter(Boolean);
  if (list.length === 0) return null;
  if (list.length === 1) return list[0];

  const prefix = longestCommonPrefix(list);
  const suffix = longestCommonSuffix(list);
  const middles = list.map((value) => {
    if (prefix.length + suffix.length > value.length) return null;
    return value.slice(prefix.length, value.length - suffix.length);
  });
  if (middles.some((m) => !m || !/^\d+$/.test(m))) return null;

  const width = middles[0]!.length;
  const padded = middles[0]!.startsWith("0") && width > 1;
  if (padded && middles.some((m) => m!.length !== width)) return null;

  const nums = middles.map((m) => parseInt(m!, 10)).sort((a, b) => a - b);
  if (new Set(nums).size !== nums.length) return null;
  for (let i = 1; i < nums.length; i++) {
    if (nums[i] !== nums[i - 1] + 1) return null;
  }

  const lo = padded ? String(nums[0]).padStart(width, "0") : String(nums[0]);
  const hi = padded ? String(nums[nums.length - 1]).padStart(width, "0") : String(nums[nums.length - 1]);
  return `${prefix}{${lo}...${hi}}${suffix}`;
}

function longestCommonPrefix(values: string[]): string {
  if (values.length === 0) return "";
  let prefix = values[0];
  for (let i = 1; i < values.length; i++) {
    while (!values[i].startsWith(prefix)) {
      prefix = prefix.slice(0, -1);
      if (!prefix) return "";
    }
  }
  return prefix;
}

function longestCommonSuffix(values: string[]): string {
  if (values.length === 0) return "";
  let suffix = values[0];
  for (let i = 1; i < values.length; i++) {
    while (!values[i].endsWith(suffix)) {
      suffix = suffix.slice(1);
      if (!suffix) return "";
    }
  }
  return suffix;
}

function renderVolumes(hosts: string[], mounts: string[], port: number, scheme: "http" | "https"): string {
  const storagePaths = mounts.map(storagePathForMount);
  const drivePattern = detectNumericPattern(storagePaths);
  if (hosts.length <= 1) {
    return drivePattern ?? storagePaths.join(" ");
  }

  const hostPattern = detectHostnamePattern(hosts);
  if (hostPattern.fits && drivePattern) {
    return `${scheme}://${hostPattern.pattern}:${port}${drivePattern}`;
  }
  if (hostPattern.fits) {
    return storagePaths.map((path) => `${scheme}://${hostPattern.pattern}:${port}${path}`).join(" ");
  }
  if (drivePattern) {
    return hosts.map((host) => `${scheme}://${host}:${port}${drivePattern}`).join(" ");
  }
  return hosts.flatMap((host) => storagePaths.map((path) => `${scheme}://${host}:${port}${path}`)).join(" ");
}

export function Review({ draft }: Props) {
  const nodes = draft.hosts.filter((h) => h.hostname.trim());
  const t = draft.topology;
  const drivesPerNode = t.selectedMounts.length;
  const totalDrives = nodes.length * drivesPerNode;
  // Estimate drive size from the first discovered eligible drive (real
  // backend uses preflight-validated uniformity).
  const sampleSize =
    Object.values(draft.discovery)
      .flatMap((r) => r.drives ?? [])
      .find((d) => !d.isBoot && d.sizeBytes > 0)?.sizeBytes ?? 0;
  const rawBytes = totalDrives * sampleSize;
  const usableBytes = rawBytes - rawBytes * (t.parity / Math.max(t.setSize, 1));
  const tlsOn = draft.tls.mode === "byo";
  const scheme: "http" | "https" = tlsOn ? "https" : "http";
  const hostname = nodes[0]?.hostname || "node1";
  const serverUrl = draft.serverUrl.trim() || `${scheme}://${hostname}:${draft.api.port}`;
  const storageMounts =
    t.selectedMounts.length > 0 ? t.selectedMounts : ["/data/disk1"];
  const volumes = renderVolumes(nodes.map((h) => h.hostname.trim()), storageMounts, draft.api.port, scheme);
  const minioOpts = tlsOn
    ? `--address :${draft.api.port} --console-address :${draft.api.consolePort} --certs-dir /etc/minio/certs`
    : `--address :${draft.api.port} --console-address :${draft.api.consolePort}`;
  const primaryEnvFile = `MINIO_CONFIG_ENV_FILE="/etc/minio/config.env"
MINIO_VOLUMES="${volumes}"
MINIO_OPTS="${minioOpts}"
MINIO_REGION=${draft.region}
MINIO_SERVER_URL=${serverUrl}`;
  const secondaryEnvFile = `MINIO_ROOT_USER=${draft.credentials.rootUser}
MINIO_ROOT_PASSWORD=********    # the password you set in Basics`;

  return (
    <div className="vstack" style={{ gap: "var(--s-5)" }}>
      <header>
        <h2 style={{ fontSize: "var(--fs-xl)", fontWeight: 600 }}>Review</h2>
        <p className="muted" style={{ fontSize: "var(--fs-sm)", marginTop: 4 }}>
          Verify the plan before deploy.
        </p>
      </header>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Summary</h3>
        <div className="hstack" style={{ flexWrap: "wrap", gap: "var(--s-4)" }}>
          <div><div className="field-label">Cluster</div><div>{draft.name} ({draft.version})</div></div>
          <div><div className="field-label">Nodes</div><div>{nodes.length}</div></div>
          <div><div className="field-label">Pools</div><div>1</div></div>
          <div><div className="field-label">Drives</div><div>{totalDrives}</div></div>
          <div><div className="field-label">Parity</div><div>EC:{t.parity}</div></div>
          <div><div className="field-label">Drives / node</div><div>{drivesPerNode || "—"}</div></div>
          <div><div className="field-label">Usable / Raw</div><div>~{humanSize(usableBytes)} / {humanSize(rawBytes)}</div></div>
          <div>
            <div className="field-label">TLS</div>
            <div>{tlsOn ? "Enabled (HTTPS)" : "Disabled (HTTP)"}</div>
          </div>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Install method</h3>
        <p>
          <span className="mono">dnf install</span>{" "}
          <span className="subtle">(RHEL 9 detected on all nodes)</span>
        </p>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">Environment files</h3>
        <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
          Root credentials live in a separate reloadable file so they can be
          rotated in place without rewriting the cluster config.
        </p>
        <div className="vstack" style={{ gap: "var(--s-2)" }}>
          <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
            Filename: <span className="mono">/etc/default/minio</span>{" "}
            <span className="subtle">(cluster config, mode 600)</span>
          </p>
          <pre className="codeblock">{primaryEnvFile}</pre>
        </div>
        <div className="vstack" style={{ gap: "var(--s-2)" }}>
          <p className="subtle" style={{ fontSize: "var(--fs-xs)" }}>
            Filename: <span className="mono">/etc/minio/config.env</span>{" "}
            <span className="subtle">(root credentials, mode 600, owned by the service user)</span>
          </p>
          <pre className="codeblock">{secondaryEnvFile}</pre>
        </div>
      </div>

      <div className="card vstack" style={{ gap: "var(--s-2)" }}>
        <h3 className="card-stat__title">What will happen</h3>
        <ol style={{ paddingLeft: "1.2em", fontSize: "var(--fs-sm)" }}>
          <li>On each node, download <span className="mono">buckit-{draft.version}.rpm</span> from the GitHub Release to <span className="mono">/tmp/buckit.rpm</span> and verify its sha256.</li>
          <li><span className="mono">dnf install -y /tmp/buckit.rpm</span></li>
          <li>Detect the service user/group from <span className="mono">buckit.service</span>.</li>
          <li>Create a managed <span className="mono">buckit/</span> subdirectory inside each selected drive.</li>
          {tlsOn && (
            <li>
              Write <span className="mono">/etc/minio/certs/public.crt</span> and{" "}
              <span className="mono">/etc/minio/certs/private.key</span> (mode 600,
              owned by the service user).
            </li>
          )}
          <li>Write <span className="mono">/etc/minio/config.env</span> with the root credentials (mode 600).</li>
          <li>Write <span className="mono">/etc/default/minio</span> with cluster values, pointing at the credentials file.</li>
          <li><span className="mono">systemctl daemon-reload && enable --now buckit</span></li>
          <li>Wait for cluster health-ready (timeout 5m).</li>
        </ol>
      </div>

    </div>
  );
}
