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
| `packaging/` | nfpm config, systemd unit, install scripts |

## Status

This directory is being built milestone-by-milestone per
[`phase1-implementation.md`](../buckit/docs/manager/phase1-implementation.md).
M0 (module bootstrap) is the first landing.
