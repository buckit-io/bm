# Manager self-update in bm

**Status:** Draft — not yet implemented.
**Owner:** TBD
**Last updated:** 2026-05-27

## Problem

`bm` needs to support updating its own binary from two entrypoints:

- the native CLI command `bm update`
- the `/settings` page in the local web UI

Those two entrypoints should not duplicate release lookup, checksum
verification, permission checks, download behavior, or binary swap logic.

`bm` also differs from the existing sibling `bm-cli` updater in one
important way: `bm` often runs as a long-lived `bm web` process. We do
not want the update flow to force an immediate restart. The operator may
apply an update while `bm web` is still serving, and the new binary
should take effect the next time `bm web` starts.

Per [release-process.md](./release-process.md), self-update also depends
on one release prerequisite: the web UI must be embedded into the `bm`
binary. Until that lands, `bm` is not a single-file release unit and the
simple binary-replacement updater described here is the wrong shape.

## Goals

- `bm update` and the `/settings` page use the same update logic.
- Self-update consumes the same `RELEASE.*` artifacts and GitHub
  Pages pointer files described in [release-process.md](./release-process.md).
- Applying an update replaces the `bm` executable on disk using the same
  in-place swap model as `bm-cli` / `mc`.
- Updating does **not** restart `bm web`.
- The UI and CLI both report clearly that a restart is required before
  the new version is active.
- Source builds and non-writable installs fail clearly and safely.

## Non-goals

- **Immediate relaunch.** No attempt to restart `bm web` automatically
  after applying an update.
- **Hot code swap.** The running process continues serving the old code
  until the operator restarts it.
- **Separate web-only updater.** The browser should not implement its
  own release fetching or binary download logic.
- **Reusing `../bm-cli` directly.** We may borrow its design, but `bm`
  owns its own update package and command surface.
- **Archive-based self-update.** Once `web/dist` is embedded, the update
  unit is the binary itself, not a tarball/zip bundle.

## Background

The sibling `../bm-cli` repo already implements a client self-update
command in `cmd/update-main.go`. Its flow is:

1. Determine the current release identity.
2. Fetch the latest release metadata and checksum.
3. Download the target binary.
4. Verify checksum and optional signature.
5. Call `selfupdate.Apply(...)` to replace the executable on disk.

That updater is suitable as a model for `bm` because it updates the
local client binary, not Buckit/MinIO cluster nodes.

The Buckit server repo contributes the second half of the design:

- stable discovery via a GitHub Pages checksum pointer file
- immutable binaries hosted in GitHub Releases
- `RELEASE.YYYY-MM-DDTHH-MM-SSZ[.rcN]` tags as the release identity

`bm` should combine those two ideas:

- use the `bm-cli` / `selfupdate` in-place binary replacement model
- use the Buckit release-discovery model

The key behavior from `selfupdate.Apply(...)` is that it swaps files on
disk without relaunching the current process:

1. Write `.<binary>.new`
2. Rename the current executable to `.<binary>.old`
3. Rename `.<binary>.new` into place
4. Remove or hide `.<binary>.old`

That means a running `bm web` process can usually update the binary on
disk successfully, but it will continue executing the old image already
loaded in memory until the process exits.

## Product behavior

### CLI

Add a native `bm update` command to `cmd/bm/main.go`.

Supported behavior:

- `bm update`
  Checks for a newer release and applies it if available.
- `bm update --check`
  Checks only; does not mutate the binary.

CLI output after a successful apply must say that the operator needs to
restart `bm web` for the new version to take effect.

Example:

```text
Update installed successfully.
Restart bm web to use version RELEASE.2026-05-27T22-15-00Z.
```

### Web

The `/settings` page gets a manager-update section that:

- shows the current version
- checks whether a newer release exists
- offers an apply action when the install is writable
- reports that the update has been written to disk and will be active on
  the next `bm web` start

Example post-apply message:

```text
Update installed on disk. Restart bm web to use version RELEASE.2026-05-27T22-15-00Z.
```

### Restart semantics

No restart is triggered by the updater.

This is intentional. Replacing the file on disk is enough to support the
next process start, and avoids building relaunch/supervisor machinery
into Phase 1. The existing operator workflow remains:

1. apply update
2. finish current work if needed
3. stop `bm web`
4. start `bm web` again

## Architecture

All update behavior lives in a single shared package:

```text
internal/update/
```

Two thin entrypoints sit on top of it:

- native CLI command `bm update`
- HTTP handlers used by `/settings`

The browser and CLI must never each implement their own copy of release
lookup, verification, or apply logic.

### Proposed package shape

```go
package update

type Service struct {
    Source ReleaseSource
}

type Status struct {
    CurrentVersion  string `json:"currentVersion"`
    LatestVersion   string `json:"latestVersion,omitempty"`
    UpdateAvailable bool   `json:"updateAvailable"`
    DownloadURL     string `json:"downloadUrl,omitempty"`
    CanApply        bool   `json:"canApply"`
    Reason          string `json:"reason,omitempty"`
    RestartRequired bool   `json:"restartRequired"`
}

type ApplyResult struct {
    Applied         bool   `json:"applied"`
    CurrentVersion  string `json:"currentVersion"`
    LatestVersion   string `json:"latestVersion,omitempty"`
    RestartRequired bool   `json:"restartRequired"`
    Message         string `json:"message"`
}

func (s *Service) Check(ctx context.Context) (Status, error)
func (s *Service) Apply(ctx context.Context) (ApplyResult, error)
```

Suggested internal files:

- `internal/update/service.go`
  Shared orchestration and public API.
- `internal/update/source.go`
  Release metadata lookup.
- `internal/update/apply.go`
  `selfupdate.Apply(...)` wrapper plus permission handling.
- `internal/update/version.go`
  Helpers for interpreting build metadata and release tags.

## Release source

The update source should be pluggable, but the default production
implementation should follow the release process in
[release-process.md](./release-process.md):

- GitHub Pages hosts the stable per-platform pointer file
- GitHub Releases hosts the real binary, checksum sidecar, and minisign
  signature

Default pointer URLs:

```text
https://buckit-io.github.io/bm/manager/bm/release/linux-amd64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/linux-arm64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/darwin-amd64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/darwin-arm64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/windows-amd64/bm.sha256sum
```

The source needs to provide enough metadata for both check-only and
apply flows:

```go
type ReleaseInfo struct {
    Version      string
    PublishedAt  time.Time
    DownloadURL  string
    SHA256       []byte
    SignatureURL string
}
```

The update service should be responsible for:

- identifying the current OS/arch target
- parsing the pointer file payload of the form
  `<sha256>  bm.RELEASE...` or `<sha256>  bm.exe.RELEASE...`
- converting the pointer URL and parsed release tag into the final
  GitHub Releases download URL
- comparing the current version to the latest supported version

The updater should not treat GitHub Pages as the host for the actual
binary payload. Pages is discovery only.

## Current-version detection

`bm` already exposes build metadata through `internal/version`.

The update package should use that first. If the running build metadata
does not represent an official release, the updater can still support a
best-effort check, but `CanApply` should be conservative.

Preferred behavior:

- official release build: check and apply allowed
- local/dev/source build: check allowed, apply usually denied with a
  clear reason

Official release builds should carry the `RELEASE.*` tag identity in
`internal/version.Version`, not a `v1.2.3`-style semver string.

Example reason:

```text
This build was started from a local development binary; install a release build to use auto-update.
```

## Apply path

`Apply(ctx)` should:

1. call `Check(ctx)`
2. return early if no update is available
3. verify the install is writable
4. download the target binary from GitHub Releases
5. verify checksum
6. verify signature if configured
7. call `selfupdate.Apply(...)`
8. return a result that explicitly says restart is required

The actual swap on disk should remain delegated to the `selfupdate`
library so that both CLI and web use the same well-tested file
replacement behavior.

This flow assumes the release unit is a single binary. If `web/dist` is
still external, self-update should remain disabled until embedding lands.

## API surface

Expose update behavior over HTTP with small dedicated endpoints.

Proposed routes:

- `GET /api/v1/manager/update`
  Returns `update.Status`
- `POST /api/v1/manager/update/apply`
  Returns `update.ApplyResult`

These handlers should be very thin:

- construct the shared update service
- call `Check` or `Apply`
- translate errors to JSON API responses

No shelling out to `bm update` from the web handler.

## CLI surface

Add a native `update` subcommand in `cmd/bm/main.go`.

Why native:

- it keeps `bm` self-update behavior inside this repo
- it avoids coupling to the delegated `bm-cli` command tree
- it guarantees the CLI and web both hit the same `internal/update`
  package

The CLI command should be a thin wrapper around the shared service:

- parse flags
- call `Check` or `Apply`
- print a concise user-facing message

## Error handling

Expected failure cases should be surfaced clearly and consistently in
both CLI and web:

- network error while checking
- no writable permission for the executable path
- unsupported current build type
- checksum or signature mismatch
- failed rename during apply

The shared service should return typed errors or stable error codes so
CLI and HTTP handlers do not need to guess from strings.

## Testing

Most tests should live in `internal/update`.

Needed coverage:

- newer release available vs already current
- pointer-file parsing and GitHub Releases URL derivation
- source/dev build behavior
- permission-denied apply
- checksum mismatch
- apply success result includes `RestartRequired=true`
- HTTP handlers marshal the shared result shapes correctly
- CLI command delegates to the shared service correctly

Use a fake `ReleaseSource` and fake target paths in temp dirs. Avoid
network dependency in unit tests.

## Rollout plan

### Phase 1 — shared check-only service

- depend on embedded static assets from
  [release-process.md](./release-process.md)
- add `internal/update`
- implement `Check`
- add `bm update --check`
- add `GET /api/v1/manager/update`
- wire `/settings` to display real status

### Phase 2 — shared apply path

- implement `Apply`
- add `bm update`
- add `POST /api/v1/manager/update/apply`
- wire `/settings` apply action
- show explicit restart-required messaging in both CLI and UI

### Phase 3 — release-process refinements

- improve source-build messaging
- decide whether stable pointer pages should also publish a human-facing
  release index
- decide whether applied updates should preserve `.old` binaries in a
  known path for rollback/debugging

## Open questions

- Should `bm` use the exact GitHub Pages prefix proposed in
  [release-process.md](./release-process.md), or a different final path?
- Should `bm` use minisign from day one, or allow checksum-only apply in
  an earlier milestone?
- Should `bm update` keep a saved `.old` binary for manual rollback, or
  should it mirror the current `bm-cli` cleanup behavior?
