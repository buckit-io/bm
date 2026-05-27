# Debian package support in bm

**Status:** Draft — not yet implemented.
**Owner:** TBD
**Last updated:** 2026-05-24

## Problem

`bm` today only manages clusters whose hosts use RPM-based package managers
(RHEL / Rocky / Alma / CentOS via `dnf` or `yum`). Every install path is
hard-coded against `rpm` query commands, `/tmp/buckit.rpm`, and `dnf` verbs.

A few places hint at Debian awareness — the GitHub-release catalog in
`internal/deploy/versions.go` classifies `.deb` assets and populates a
`BuckitVersion.DebURL` field, the install-command picker in `install.go`
probes `apt-get`, and the preflight + SSH test mock both reference
`dpkg`. But those hints are unfinished: `PickInstallCmd` actually hands
`/tmp/buckit.rpm` to `apt-get install`, which fails immediately, and
nothing downstream consumes the `DebURL` field.

This document proposes a refactor that adds full Debian / Ubuntu support
alongside the existing RPM path, behind a small package-manager abstraction.

## Goals

- Initial deploy of a Debian/Ubuntu cluster works end-to-end.
- Migration cutover (`internal/migration/installer.go`) installs `.deb`
  artifacts on Debian targets.
- Per-host operations (`redeploy_software`, `cluster_upgrade_by_systemctl`)
  detect the host's package manager and use the right install verbs and
  version-inspection commands.
- Preflight rejects clusters whose hosts mix package formats, and rejects
  hosts without a supported package manager.
- The downgrade-rejection guard added for RPM (see
  [`internal/operations/orchestrated.go`](../internal/operations/orchestrated.go))
  applies uniformly to Debian.

## Non-goals

- **Heterogeneous clusters.** A single cluster must have one package
  format across all nodes. Mixing dnf and apt-get hosts is rejected
  during preflight rather than supported.
- **Repo-based installs.** Only direct `.deb` file download + local-file
  install (`apt-get install ./buckit.deb`). No `apt repositories`,
  `add-apt-repository`, or `deb.buckit.io` repo configuration.
- **Alpine (`.apk`).** The Buckit release ships `.apk` artifacts for
  container image builds; bm doesn't manage container deployments, so
  this is out of scope. See "Alpine" in the open questions section if
  that ever changes.

## Background: today's RPM-only flow

Three call sites currently hard-code the package format:

1. **Initial deploy** — `internal/deploy/install.go:Installer.Execute`
   downloads the RPM, verifies its checksum, picks an install command via
   `PickInstallCmd`, and runs it. Everything assumes `/tmp/buckit.rpm`.
2. **Migration cutover** — `internal/migration/installer.go` has its own
   parallel install flow with the same RPM-only assumptions.
3. **Orchestrated runtime ops** — `redeployExecutor` and
   `clusterUpgradeBySystemctlExecutor` in
   `internal/operations/orchestrated.go` use
   `resolveRpmArtifactForNode`, `determineRpmInstallAction`, and dnf
   verbs (`dnf upgrade -y`, `dnf reinstall -y`).

The host-side commands they share:

| Purpose | Command today |
|---|---|
| Download to host | `curl -fSL -o /tmp/buckit.rpm <url>` |
| Verify checksum | `sha256sum -c -` against `/tmp/buckit.rpm` |
| Query installed package EVR | `rpm -q --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}' buckit` |
| Query candidate package EVR | `rpm -qp --qf '...' /tmp/buckit.rpm` |
| Compare versions | `rpm --eval "%{lua: print(rpm.vercmp(...))}"` |
| Install fresh | `dnf install -y /tmp/buckit.rpm` |
| Reinstall same version | `dnf reinstall -y /tmp/buckit.rpm` |
| Upgrade to newer version | `dnf upgrade -y /tmp/buckit.rpm` |

## Approach

A `PackageManager` interface in `internal/deploy` abstracts every
per-distro shell command. Two implementations live alongside it:
`rpmManager` (wraps today's commands verbatim) and `debManager` (new).

```go
type PackageManager interface {
    // Kind matches BuckitArtifact.Kind: "rpm" or "deb".
    Kind() string

    // LocalFile is the absolute path on the host where the package is
    // staged after download (e.g. /tmp/buckit.rpm or /tmp/buckit.deb).
    LocalFile() string

    // DownloadCommand fetches a URL to LocalFile via curl.
    DownloadCommand(url string) string

    // VerifyChecksumCommand reads the expected sha256 on stdin and
    // verifies the local file. Returns exit 0 on match.
    VerifyChecksumCommand(expectedSHA256 string) string

    // InspectScript is the shell script that emits three lines
    //   installed=<EVR or empty if not installed>
    //   candidate=<EVR of the local file>
    //   cmp=<-1 | 0 | 1>
    // where cmp follows rpm.vercmp semantics: -1 if installed < candidate,
    // 0 if equal (or no install), 1 if installed > candidate.
    InspectScript() string

    // InstallCommand returns the install command for one of the actions.
    // Downgrade is never expected here — callers reject it before invoking.
    InstallCommand(action InstallAction) string
}

type InstallAction string

const (
    InstallActionFresh     InstallAction = "fresh"
    InstallActionReinstall InstallAction = "reinstall"
    InstallActionUpgrade   InstallAction = "upgrade"
    InstallActionDowngrade InstallAction = "downgrade"
)
```

### Inspect script shape (Debian)

```sh
installed=$(dpkg-query -W -f='${Version}' buckit 2>/dev/null || true)
candidate=$(dpkg-deb -f /tmp/buckit.deb Version)
cmp=0
if [ -n "$installed" ] && [ "$installed" != "$candidate" ]; then
  if dpkg --compare-versions "$installed" lt "$candidate"; then
    cmp=-1
  elif dpkg --compare-versions "$installed" gt "$candidate"; then
    cmp=1
  fi
fi
printf 'installed=%s\ncandidate=%s\ncmp=%s\n' "$installed" "$candidate" "$cmp"
```

The Go-side parser (currently inside `determineRpmInstallAction`) becomes
generic: it consumes the three lines and decides the `InstallAction`
without knowing which manager produced them.

### Install verbs (Debian)

| Action | Command |
|---|---|
| Fresh | `apt-get install -y /tmp/buckit.deb` |
| Upgrade | `apt-get install -y /tmp/buckit.deb` (apt detects direction) |
| Reinstall | `apt-get install -y --reinstall /tmp/buckit.deb` |
| Downgrade | *(never invoked — rejected by caller)* |

### Detection

A single helper does the probe:

```go
func DetectPackageManager(
    ctx context.Context,
    runShell func(context.Context, string) (string, error),
) (PackageManager, error)
```

The probe is `command -v dnf; command -v yum; command -v apt-get`. Preference
order: **dnf > yum > apt-get** (preserves today's behavior on RHEL hosts
that have apt-get installed as a side effect of something else).

Caching: the result lives on `runCtx` for the duration of an operation
(roughly one probe per host per op). Not persisted across operations.
Rationale: probing is cheap (~50 ms over SSH), and persisted state risks
drift if an operator manually changes the host's package manager between
operations. The cost-benefit clearly favors re-detection.

## Phased rollout

### Phase 1 — abstraction (no behavior change)

Carve out the interface without changing what runs on the host. Pure
refactor; easy to review, easy to revert if the abstraction shape proves
wrong.

**Files touched:**

- `internal/deploy/pkgmgr.go` *(new)* — interface, `InstallAction` enum,
  `rpmManager` that wraps today's commands verbatim.
- `internal/deploy/checksum.go` — rename `RPMArtifact` → `Artifact`.
  Keep RPM URL helpers but route the local-file path through the manager.
  `DownloadRPMCommand` and friends become thin wrappers around
  `rpmManager{}.DownloadCommand` to limit blast radius.
- `internal/operations/orchestrated.go` — `determineRpmInstallAction`
  becomes `inspectInstallAction(ctx, deps, rc, n, mgr)`. Both executors
  pass `mgr = rpmManager{}`.
- `internal/deploy/pkgmgr_test.go` *(new)* — asserts
  `rpmManager{}.InspectScript()` produces the exact bytes today's
  hardcoded script does.

**Risk:** very low. No new shell behavior.

### Phase 2 — Debian implementation + detection

Add the second backend and the auto-detect path. After this phase, deploy
and migration cutover work on Debian.

**Files touched:**

- `internal/deploy/pkgmgr.go` — add `debManager`, add
  `DetectPackageManager`.
- `internal/deploy/install.go` — replace `PickInstallCmd` with detection +
  `mgr.InstallCommand(...)`. Removes the currently-broken apt-get branch
  that hands a `.rpm` to apt.
- `internal/deploy/params.go` — `ArtifactURL()` learns to pick `RpmURL` or
  `DebURL` from `BuckitVersion` based on a `Kind` field threaded from
  detection. Likely refactored to `ArtifactURL(kind string)`.
- `internal/deploy/versions.go` — add `DebArtifactForArch` and
  `DebURLForArch` mirroring the RPM helpers. `BuckitArtifact` and
  `BuckitVersion` already carry the `Kind` discrimination and `DebURL`
  field; no domain change needed.
- `internal/migration/installer.go` — mirror the same detect-and-use
  flow as `deploy.Installer.Execute`.
- `internal/api/m7_cases_test.go` — add deb-host variants for the
  existing M7 SSH mocks.
- `internal/sshtest/server.go` — extend the existing `dpkg -s` and
  `apt-get install` scaffolding to handle the new commands
  (`dpkg-query -W -f`, `dpkg-deb -f`, `dpkg --compare-versions`).

**Risk:** medium. Deploy and migration touch real hosts. The
downgrade-rejection guard already in place remains a backstop.

### Phase 3 — runtime ops on Debian

Wire the orchestrated ops to the manager.

**Files touched:**

- `internal/operations/orchestrated.go` — in
  `redeployExecutor.Execute` and `clusterUpgradeBySystemctlExecutor.Execute`,
  call `DetectPackageManager` per host, then drive the existing
  download / verify / inspect / install sequence through the manager.
- `internal/operations/helpers.go` — cache the detected
  `PackageManager` on `runCtx` so we don't re-probe between hosts.
- `internal/operations/rotate_root_creds.go` — doesn't deal with
  packages, no change.
- `internal/api/m7_cases_test.go` — extend the upgrade test family with
  a `Deb` variant.

**Risk:** low. Phase 2 proves the script shape on real hosts; this is
plumbing.

### Phase 4 — preflight + wizard polish

User-visible improvements that prevent confusing failures up front.

**Files touched:**

- `internal/preflight/checks.go` — tighten the existing dnf/yum/apt-get
  probe to report which one was found, and reject if none match. Add a
  check that the selected `BuckitVersion` has an artifact matching the
  detected manager (e.g. user picked a release that only ships `.rpm`,
  but the hosts are Debian → fail preflight with a clear message rather
  than fail mid-deploy).
- `internal/preflight/preflight_test.go` — coverage for the new checks.
- `web/src/pages/wizards/new/steps/Preflight.tsx` *(or wherever the
  preflight host card lives)* — surface "Detected: Debian / Ubuntu (apt)"
  so the operator sees what bm decided.
- Heterogeneous-cluster guard: refuse to proceed when probes report
  different managers across hosts in the same cluster. Error message:
  *"Cluster has mixed package managers (dnf on host-a, apt-get on host-b).
  bm requires all nodes to use the same package format."*

**Risk:** low. UI/UX work plus fail-fast guards.

## Files touched per phase

| Phase | Files |
|---|---|
| 1 | `internal/deploy/pkgmgr.go` *(new)*, `internal/deploy/checksum.go`, `internal/operations/orchestrated.go`, `internal/deploy/pkgmgr_test.go` *(new)* |
| 2 | `internal/deploy/pkgmgr.go`, `internal/deploy/install.go`, `internal/deploy/params.go`, `internal/deploy/versions.go`, `internal/migration/installer.go`, `internal/api/m7_cases_test.go`, `internal/sshtest/server.go` |
| 3 | `internal/operations/orchestrated.go`, `internal/operations/helpers.go`, `internal/api/m7_cases_test.go` |
| 4 | `internal/preflight/checks.go`, `internal/preflight/preflight_test.go`, `web/src/pages/wizards/new/steps/Preflight.tsx` *(or equivalent)* |

## Test strategy

The existing M7 test family (`internal/api/m7_cases_test.go`) already
mocks SSH command outputs for the RPM path. The same shape extends to
Debian: each upgrade / redeploy test gets a parallel `…OnDeb` variant
that swaps the command pattern matches:

- `rpm -q --qf …` → `dpkg-query -W -f='${Version}' buckit`
- `rpm -qp --qf …` → `dpkg-deb -f /tmp/buckit.deb Version`
- `rpm --eval "%{lua: print(rpm.vercmp(…))}"` → `dpkg --compare-versions …`
- `dnf upgrade -y` / `dnf reinstall -y` → `apt-get install -y [--reinstall]`

The mock harness in `internal/sshtest/server.go` already has scaffolding
for `dpkg -s` and `apt-get install`, so the additions are incremental.

A new `internal/deploy/pkgmgr_test.go` covers:

- `rpmManager.InspectScript()` byte-for-byte equivalence with today's
  hardcoded script (locks Phase 1's "no behavior change" guarantee).
- `debManager.InspectScript()` produces a valid script that, when run
  against a stub host, emits the documented three-line output.
- `DetectPackageManager` returns the right impl for each of the four
  scenarios: dnf-only, yum-only, apt-get-only, none-found.

## Open questions

1. **Persist `cluster.PackageKind`?** Right now per-cluster state lives
   in bbolt. Detecting once during deploy and persisting `PackageKind`
   on the cluster row would save probes on every op. But it adds drift
   risk (operator manually switches package manager between ops —
   vanishingly rare in production, possible in dev environments).
   **Recommendation:** detect-and-cache in-memory per `runCtx`, don't
   persist. One probe per operation is ~50 ms.

2. **Surface package kind in the operation UI?** When the user selects
   a version in the dropdown (e.g. "Redeploy software"), we could note
   "(installs .deb on this cluster)" once detection is wired. Optional
   polish. **Recommendation:** defer until Phase 4 user testing tells
   us whether the implicit behavior is confusing.

3. **`/tmp/buckit.rpm` vs `/tmp/buckit.deb` lifetime.** Today the file
   is left on disk after install. Should the manager clean up? Currently
   no cleanup. **Recommendation:** keep parity — no behavior change.
   Disk usage is small (single RPM/DEB ≈ tens of MB) and the file is
   useful for post-mortem inspection.

4. **Custom URL flow.** `CustomRPMArtifact` lets the user paste a URL
   directly. Debian needs a `CustomDebArtifact` equivalent and a way to
   specify the kind, or auto-detect from URL suffix. **Recommendation:**
   auto-detect from URL suffix (`.rpm` / `.deb`). Reject anything else
   with a clear error.

5. **dnf vs yum.** The current code prefers dnf but `PickInstallCmd`
   probes yum too. Keep yum support (RHEL 7 / CentOS 7) or drop as
   out-of-support? **Recommendation:** keep yum because it costs us
   almost nothing — `rpmManager` stores `"dnf"` or `"yum"` as the verb
   prefix and the rest of the logic is identical.

6. **Alpine.** Buckit releases ship `.apk` artifacts. Those are
   recognized by the catalog (`classifyReleaseAsset` in `versions.go`)
   but bm doesn't manage Alpine clusters. If that ever becomes a goal
   (containerized clusters managed via SSH?), an `apkManager` slots into
   this same interface; the only complication is that `.apk` has no
   equivalent of `rpm -qp` for inspecting a local file — the candidate
   version has to be read by extracting `.PKGINFO` from the tarball.

## References

- Initial diagnosis of the version-handling issues that motivated this
  doc lives in the commit history around
  `internal/operations/orchestrated.go` (the `redeploy_software`
  hardcoded-version fix and the `dnf reinstall`-vs-upgrade fix).
- The downgrade-rejection guard added in the same change pair is what
  this doc preserves and extends to Debian.
- Existing `BuckitArtifact` / `BuckitVersion` shape:
  [`internal/domain/wizard.go`](../internal/domain/wizard.go).
- GitHub-release classifier (already deb-aware):
  [`internal/deploy/versions.go`](../internal/deploy/versions.go),
  function `classifyReleaseAsset`.
