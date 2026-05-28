# integration-test

This directory contains the Docker-backed end-to-end test harness for
`bm`.

Current scope:

- Phase 1: `bm web` non-loopback lab support plus basic Docker scaffold
- Phase 2: Buckit import end-to-end

The first end-to-end slice uses:

- a `bm` container built from this repo
- a small Buckit fixture cluster
- a host-run Playwright suite that drives the real UI through the
  ephemeral host port published for the `bm` container

## Requirements

- Docker with Compose v2
- Node 20+ and npm
- `curl`
- `jq`

The Buckit import fixture image is built from the latest Buckit GitHub
release RPM by default. You can override this with:

- `BM_E2E_BUCKIT_RELEASE_TAG` to pin a specific Buckit release tag
- `BM_E2E_BUCKIT_RPM_URL` to pin a specific RPM asset URL
- `BM_E2E_BUCKIT_ARCH` to force `amd64` or `arm64`

## Common commands

```sh
make e2e-import
make e2e-up
make e2e-down
```

`make e2e-import` builds `web/dist`, builds the fixture image, starts the
lab, runs the Playwright import spec, and tears the lab down.
