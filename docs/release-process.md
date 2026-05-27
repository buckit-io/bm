# bm release process

**Status:** Draft — not yet implemented.
**Owner:** TBD
**Last updated:** 2026-05-27

## Problem

`bm` needs a real release process before self-update can work.

Today there are two blockers:

1. `bm web` serves the frontend from `web/dist` on disk, so a released
   binary is not self-contained.
2. There is no stable, machine-readable "latest release for this
   platform" endpoint that `bm update` or the `/settings` page can
   consume.

The Buckit server repo already solved the second problem with a
GitHub-Releases-plus-GitHub-Pages model. `bm` should adopt the same
release discovery model, but only after the binary embeds the web UI.

## Goals

- Release tags use the Buckit server format:
  `RELEASE.YYYY-MM-DDTHH-MM-SSZ`
- `bm` ships as a single self-contained binary with embedded static UI
  assets.
- GitHub Actions builds release binaries for supported OS/arch targets.
- GitHub Releases hosts immutable per-platform artifacts.
- GitHub Pages hosts small per-platform pointer files that identify the
  latest stable release.
- The resulting layout is suitable for both `bm update` and web-based
  self-update in `/settings`.

## Non-goals

- **Installer packaging in this phase.** No `.pkg`, `.msi`, `.deb`,
  `.rpm`, or Homebrew/Scoop integration yet.
- **Automatic restart after update.** Update discovery and binary apply
  only; restart remains operator-controlled.
- **Releasing the current disk-layout model.** Shipping `bm` plus an
  adjacent `web/dist` tree is not the end state and should not be the
  long-term self-update contract.

## Why embed first

Right now `bm` expects frontend assets on disk:

- `cmd/bm/web.go` resolves `web/dist` via `defaultWebDist()`
- `internal/api/router.go` serves files from that path
- missing assets cause `bm web` to fail with the "no built UI assets"
  message

That is workable for local development, but it is the wrong shape for
release and self-update.

If we keep the current disk-based asset model, the released artifact for
one platform must be an archive containing:

```text
bm
web/dist/...
```

That complicates everything:

- self-update would need to download and unpack an archive, not replace a
  single executable
- the update pointer would have to point at archive assets
- `selfupdate.Apply(...)` would no longer be a direct fit

Embedding `web/dist` into the binary removes that complexity. After
embedding, the release unit becomes one file per platform: the `bm`
binary itself.

## Proposed release model

`bm` should match Buckit's release model in two layers:

1. **GitHub Releases**
   Holds immutable versioned artifacts.
2. **GitHub Pages**
   Holds small stable pointer files at fixed per-platform URLs.

The pointer file is not the binary. It is a checksum file whose second
field names the latest stable release asset identity.

## Tag format

Stable releases:

```sh
git tag RELEASE.2026-05-27T22-15-00Z
git push origin RELEASE.2026-05-27T22-15-00Z
```

Release candidates:

```sh
git tag RELEASE.2026-05-27T22-15-00Z.rc1
git push origin RELEASE.2026-05-27T22-15-00Z.rc1
```

Rules:

- stable tags update GitHub Pages pointers
- RC tags publish GitHub Release assets but do not move the stable
  pointer files

## Supported platforms

Match the current supported build matrix unless product requirements
change:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

## Release artifact names

Once `web/dist` is embedded, publish one versioned binary per platform:

- `bm-linux-amd64.RELEASE.2026-05-27T22-15-00Z`
- `bm-linux-arm64.RELEASE.2026-05-27T22-15-00Z`
- `bm-darwin-amd64.RELEASE.2026-05-27T22-15-00Z`
- `bm-darwin-arm64.RELEASE.2026-05-27T22-15-00Z`
- `bm-windows-amd64.exe.RELEASE.2026-05-27T22-15-00Z`

Each binary should also publish:

- `.sha256sum`
- `.minisig`

Example:

```text
bm-linux-amd64.RELEASE.2026-05-27T22-15-00Z
bm-linux-amd64.RELEASE.2026-05-27T22-15-00Z.sha256sum
bm-linux-amd64.RELEASE.2026-05-27T22-15-00Z.minisig
```

## GitHub Pages pointer layout

Use a fixed per-platform layout parallel to Buckit's:

```text
https://buckit-io.github.io/bm/manager/bm/release/linux-amd64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/linux-arm64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/darwin-amd64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/darwin-arm64/bm.sha256sum
https://buckit-io.github.io/bm/manager/bm/release/windows-amd64/bm.sha256sum
```

Each pointer file should contain exactly one line:

```text
<sha256hex>  bm.RELEASE.2026-05-27T22-15-00Z
```

For Windows:

```text
<sha256hex>  bm.exe.RELEASE.2026-05-27T22-15-00Z
```

That gives the updater enough information to:

- learn the latest stable release tag
- verify the expected checksum
- derive the real GitHub Releases asset URL

## How self-update should consume the pointer

The updater should follow the same pattern as Buckit server:

1. Fetch the platform-specific `bm.sha256sum` pointer file from GitHub
   Pages.
2. Parse:
   - checksum
   - release info (`bm.RELEASE...` or `bm.exe.RELEASE...`)
3. Extract the `RELEASE.*` tag.
4. Convert the pointer URL into the matching GitHub Releases asset URL.
5. Download the real binary asset from GitHub Releases.
6. Verify checksum and minisign signature.
7. Apply the binary update in place.

GitHub Pages stays small and stable. GitHub Releases stores the large,
immutable binaries.

## Embedding the frontend

This is the prerequisite release change.

### Build-time behavior

The release workflow should:

1. run `web/npm ci`
2. run `web/npm run typecheck`
3. run `web/npm run build`
4. compile the Go binary with the generated `web/dist` included via
   `embed.FS`

### Code changes

Proposed implementation:

- add a package such as `internal/uiassets`
- embed `web/dist/**`
- have `internal/api/router.go` serve from `http.FS(embedFS)` in release
  mode
- preserve an optional disk override for local development if useful

Example shape:

```go
// internal/uiassets/assets.go
package uiassets

import "embed"

//go:embed all:web/dist
var FS embed.FS
```

Then the router can serve embedded files by default and only use a disk
path for explicit dev overrides.

### Desired runtime behavior

- release binary works with no adjacent `web/dist` directory
- `bm web` serves the embedded UI assets automatically
- local developers can still point at a disk `web/dist` when iterating

## GitHub Actions workflow

The final release workflow should look more like Buckit's than the
current draft archive publisher.

### Trigger

```yaml
on:
  push:
    tags:
      - "RELEASE.*"
```

### Jobs

1. `web`
   Builds the React bundle once and uploads it as a workflow artifact.

2. `build`
   Cross-compiles `bm` for each supported platform with:
   - embedded `web/dist`
   - release metadata in `internal/version`
   - versioned artifact names based on `${{ github.ref_name }}`

3. `sign`
   Generates `.sha256sum` and `.minisig` files per binary.

4. `publish`
   Creates the GitHub Release and uploads all versioned binary assets.

5. `update-gh-pages`
   Stable releases only.
   Writes the per-platform `bm.sha256sum` pointer files and publishes
   them to `gh-pages`.

## Release metadata

The release build should inject:

- `internal/version.Version`
- `internal/version.Commit`
- `internal/version.Date`

For official releases, `Version` should align with the release tag so
the updater can compare official builds reliably.

Preferred behavior:

- release build: `Version=RELEASE.2026-05-27T22-15-00Z`
- local build: `Version=dev` or current Makefile default

## GitHub Pages content

In addition to the pointer files, Pages can publish a small human-facing
release index similar to Buckit's:

- latest stable release page
- archives/history page
- links to GitHub Release assets

That is optional for updater correctness, but useful operationally.

## Verification checklist

Before declaring the new release process ready:

1. `bm` built from a release tag starts `bm web` successfully with no
   adjacent `web/dist` directory.
2. GitHub Release contains all expected per-platform binaries,
   checksums, and signatures.
3. GitHub Pages stable pointers resolve for all supported platforms.
4. Pointer files reference the exact checksums of the published GitHub
   Release assets.
5. `bm update --check` can discover the latest stable release using the
   pointer file.
6. Web `/settings` can show the same latest-release status via the same
   backend update service.

## Rollout plan

### Phase 1 — embed static assets

- implement `embed.FS` serving
- keep disk override for local dev if needed
- stop treating `web/dist` as a required runtime release asset

### Phase 2 — Buckit-style release workflow

- switch tags from `v*` to `RELEASE.*`
- publish versioned binaries to GitHub Releases
- generate `.sha256sum` and `.minisig`
- write stable pointer files to GitHub Pages

### Phase 3 — shared self-update

- add `internal/update`
- add native `bm update`
- add `/settings` update endpoints
- use the GitHub Pages pointer as the default discovery source

## Open questions

- Should `bm` publish minisign signatures from the start, or land with
  checksums first and add signatures immediately after?
- Do we want a human-facing Pages path rooted at
  `/manager/bm/release/`, or should it live under a different prefix?
- Should macOS `amd64` remain supported long term, or only for the first
  release generation?
