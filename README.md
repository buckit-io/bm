# bm — Buckit Manager

`bm` is the operational control plane for Buckit deployments. It is a
single binary that ships with a CLI, an HTTP API, and an embedded web
UI. Phase 1 delivers `bm server` and the two operator wizards required
to (a) deploy a new Buckit cluster onto fresh hosts over SSH, and
(b) migrate an existing MinIO deployment to Buckit in place.

The high-level design lives in the buckit repo under
[`buckit/docs/manager/README.md`](../buckit/docs/manager/README.md).
The Phase 1 UI spec is in
[`buckit/docs/manager/phase1-web-ui.md`](../buckit/docs/manager/phase1-web-ui.md).
The implementation plan that drives this directory is at
[`buckit/docs/manager/phase1-implementation.md`](../buckit/docs/manager/phase1-implementation.md).

## Install

`bm` is a single per-user binary — no sudo, no system-wide install.

macOS / Linux:

```sh
curl -fsSL https://buckit-io.github.io/bm/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://buckit-io.github.io/bm/install.ps1 | iex
```

The installer downloads the latest stable build for your OS/arch,
verifies its SHA-256 against the published release pointer, and installs
to `~/.local/bin` (`%LOCALAPPDATA%\Programs\bm` on Windows). Set
`BM_INSTALL_DIR` to override. You can also grab a signed binary directly
from the [download site](https://buckit-io.github.io/bm/manager/bm/release/)
or the [GitHub releases](https://github.com/buckit-io/bm/releases).

Once installed, `bm update` self-updates to the latest stable release
(verifying SHA-256 + minisign signature).

## Requirements

- Go 1.25+
- Node 20+ and npm (for building the web UI)

## Build

```sh
make web      # build the React frontend into web/dist
make build    # build the bm binary
./bm version
```

Cross-compile for all supported platforms:

```sh
make build-all
ls dist/
```

## Run (development)

```sh
# Terminal 1 — Go server (will be wired in M1)
make run

# Terminal 2 — Vite dev server with API proxy
cd web && npm install && npm run dev
```

The frontend dev server proxies `/api/*` to `http://localhost:9443`.

## Layout

| Path | Purpose |
|---|---|
| `cmd/bm/` | Entry point and CLI dispatch |
| `internal/app/` | Shared application services (the "core") |
| `internal/api/` | HTTP layer (chi router, handlers, SSE) |
| `internal/store/` | bbolt-backed persistent state |
| `internal/tasks/` | Task engine for long-running workflows |
| `internal/ssh/` | SSH execution and file transfer |
| `internal/deploy/` | Install / upgrade / config / systemd workflows |
| `internal/cluster/` | Domain models and topology planning |
| `internal/auth/` | Local admin auth |
| `internal/config/` | Server configuration |
| `internal/version/` | Build-time version metadata |
| `web/` | React + Vite + TypeScript frontend |
| `packaging/` | Per-user install scripts (`install.sh`, `install.ps1`) |

## Status

This directory is being built milestone-by-milestone per
[`phase1-implementation.md`](../buckit/docs/manager/phase1-implementation.md).
M0 (module bootstrap) is the first landing.
