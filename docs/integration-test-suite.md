# bm integration test suite

**Status:** Draft
**Owner:** TBD
**Last updated:** 2026-05-28

## Problem

`bm` has strong unit and package-level integration coverage, but it does
not yet have an end-to-end suite that exercises the real operator flows
through the shipped web UI against real SSH targets and real Buckit or
MinIO services.

That leaves a gap in three places:

1. the React wizard flow and its contract with the real HTTP API
2. the long-running task and SSE flows that drive deploy and migration
3. the runtime assumptions around SSH, systemd, mounted drives, and
   real Buckit or MinIO processes

We need a suite that runs both locally and in GitHub Actions with the
same topology and without depending on a sibling checkout.

## Goals

- Provide a reproducible local and CI test lab for major `bm` workflows.
- Exercise the real `bm` binary, not an in-process `httptest.Server`.
- Drive the product through the real browser UI.
- Use real SSH targets for deploy and migration.
- Use real MinIO and Buckit containers where those flows need them.
- Keep the test harness self-contained inside this repository.
- Make failures debuggable with retained logs, screenshots, video, and
  Playwright traces.

## Non-goals

- **Replacing fast package tests.** Existing Go integration tests remain
  the main fast feedback layer.
- **Exhaustive UI coverage.** The suite should cover major operator
  workflows, not every route and edge case.
- **Production remote access.** The required non-loopback bind support is
  test infrastructure, not the full future passcode + TLS feature.
- **Mobile coverage.** Phase 1 remains desktop-first.

## Existing baseline

Today `bm` already has useful package-level integration tests:

- deploy flow wiring in `internal/api/m6_integration_test.go`
- migration cutover wiring in `internal/api/m8_integration_test.go`
- earlier API milestone coverage in `internal/api/m3` through `m7`

Those tests validate handler, task, and executor behavior with in-memory
or in-process fixtures. They should stay in place because they are much
faster and narrower than a full end-to-end lab.

The missing layer is:

- real `bm web`
- real built frontend
- real browser automation
- real containerized hosts and services

## Recommended stack

### Browser framework

Use Playwright as the browser test runner.

Reasons:

- It is already used by the sibling `console` web app.
- It handles auto-waiting well for async UI flows.
- It has good support for traces, screenshots, video, retries, and CI.
- It supports a clean TypeScript-based test model that fits `web/`.

For the first version of the suite, run Chromium only. Cross-browser
coverage can be added later if the suite proves stable and useful.

### Container orchestration

Use Docker Compose for the test lab.

Compose should manage:

- the `bm` container
- blank SSH target hosts
- MinIO source clusters
- optional Buckit fixture clusters for import smoke tests
- the Playwright runner
- optional seeding or verification helpers

### Fixture source

Create a new repo-local test harness under:

```text
integration-test/
```

Do not depend directly on `../buckit/testing/cluster/` at runtime.
Instead, selectively copy the pieces we need and make `bm` the owner of
its own lab.

That copied code should be minimized to the parts that matter:

- Dockerfile generation for systemd-capable hosts
- entrypoint logic for loopback XFS drives
- MinIO fixture image bits
- compose-generation patterns

## High-level architecture

The end-to-end lab has five layers:

1. a built `bm` binary and built `web/dist`
2. a Compose-managed container network
3. target host fixtures with SSH and systemd
4. service fixtures such as MinIO and optional Buckit
5. a Playwright runner that drives the UI and performs assertions

### Topology

All services should share one Docker bridge network.

`bm` runs as a normal container in that network and reaches fixtures by
hostname.

The browser test runner reaches `bm` through an exposed HTTP port:

- local: `http://127.0.0.1:9443`
- in-container runner: `http://bm:9443`

### Non-loopback bind support

Today `bm web` rejects non-loopback listen addresses. For integration
tests we should add an explicit opt-in flag:

```text
bm web --allow-non-loopback --addr 0.0.0.0:9443
```

Rules:

- default behavior stays unchanged
- the new flag is required whenever `addr` is non-loopback
- the flag is documented as test or lab infrastructure, not the remote
  access feature

This keeps the product safe by default while letting the containerized
lab expose the UI without a proxy sidecar.

## Fixture design

The `integration-test/` area should define three fixture families.

### 1. Target hosts

Purpose:

- deploy destination for the new-cluster wizard
- migration destination for MinIO cutover

Behavior:

- run `systemd` as PID 1
- provide SSH access
- create loopback XFS drives mounted at predictable paths
- start empty, with no Buckit already installed

Suggested layout:

```text
integration-test/targets/
  Dockerfile
  entrypoint.sh
  compose.sh
```

These hosts are the critical fixture that the old Buckit harness does
not provide directly.

### 2. MinIO source cluster

Purpose:

- migration source for the migrate wizard
- import source for MinIO import coverage if needed

Behavior:

- support single-node and distributed modes
- expose API and console ports
- provide SSH when useful for debugging

Suggested layout:

```text
integration-test/minio/
  Dockerfile
  entrypoint.sh
  compose.sh
```

### 3. Buckit fixture cluster

Purpose:

- import existing Buckit cluster
- operation smoke tests against an already-running cluster

This fixture is the highest-priority first service fixture because
existing-cluster import is the simplest end-to-end browser flow to land
first.

Suggested layout:

```text
integration-test/buckit/
  dockerfile-gen.sh
  entrypoint.sh
  compose.sh
```

## Compose model

The repository should own a top-level lab compose file plus generated
service fragments.

Suggested layout:

```text
integration-test/
  compose.yml
  env/
  scripts/
  targets/
  minio/
  buckit/
  playwright/
```

### Core services

`compose.yml` should define at least:

- `bm`
- `playwright`

The fixture scripts should generate or include:

- `targets-node1..N`
- `minio-node1..N`
- `buckit-node1..N` when enabled

### bm container

The `bm` service should:

- mount a disposable test data dir
- run the built binary from this repo
- serve the built `web/dist`
- listen on `0.0.0.0:9443` with `--allow-non-loopback`

Example command shape:

```sh
./bm web \
  --addr 0.0.0.0:9443 \
  --allow-non-loopback \
  --no-browser \
  --data-dir /var/lib/bm \
  --web-dist /app/web/dist
```

### Playwright container

The Playwright service should:

- depend on `bm`
- use a fixed `BASE_URL`
- mount a results directory for artifacts
- run the browser suite headlessly

## Test scenarios

Start with a small number of high-value flows.

### Scenario 1: Import existing Buckit cluster

Fixtures:

- already-running Buckit fixture cluster

Assertions:

- import discover succeeds
- import commit succeeds
- imported cluster appears in the list
- cluster detail and nodes pages render expected data

This is the recommended first end-to-end scenario because it avoids the
blank-host deploy bootstrap path while still validating the real UI, API,
persistence, and cluster-detail rendering.

### Scenario 2: Deploy new Buckit cluster

Fixtures:

- 4 blank target hosts

Assertions:

- wizard completes successfully
- deploy task reaches terminal success
- cluster appears on `/clusters`
- cluster detail page renders expected health summary
- node list shows all expected hosts

This is the highest-value first scenario because it validates the full
new-cluster path across UI, API, SSH, install, verify, persistence, and
post-deploy rendering. It should land after the import path establishes
the browser harness and Buckit fixture cluster.

### Scenario 3: Migrate MinIO to Buckit

Fixtures:

- 1-node or 4-node MinIO source
- 4 blank target hosts
- seeded buckets and objects

Assertions:

- snapshot and preflight succeed in the wizard
- cutover task reaches terminal success
- migrated cluster renders as Buckit in the UI
- expected buckets or objects are present after cutover

Use a separate verification helper, likely `mc`, to validate data
outside the browser when object-level correctness matters.

### Optional later scenarios

- cluster refresh from detail page
- one or two high-value operations from the detail page
- delete imported cluster
- rollback path for a forced migration failure

## Test structure

Suggested layout:

```text
integration-test/playwright/
  package.json
  playwright.config.ts
  tests/
    import.spec.ts
    deploy.spec.ts
    migrate.spec.ts
  fixtures/
    lab.ts
  pom/
    ClustersPage.ts
    ImportFlow.ts
    DeployWizard.ts
    MigrationWizard.ts
```

Guidelines:

- use page-object helpers for the wizards
- use stable selectors, preferably `data-testid`
- keep API polling and task-status waits inside helper methods
- record traces on retry and retain screenshots on failure

## Required product changes

The integration suite requires a small amount of product work.

### CLI flag for non-loopback listen

Add a new `bm web` flag:

```text
--allow-non-loopback
```

Behavior:

- if `addr` is loopback, no flag is needed
- if `addr` is non-loopback and the flag is absent, keep failing
- if `addr` is non-loopback and the flag is present, allow startup

This is intentionally narrower than the future remote-access milestone.

### Stable UI selectors

Add `data-testid` attributes to the major controls used in the deploy,
migrate, and import flows.

Do not rely on CSS classes or fragile text-only selectors where the
intent can be expressed directly.

### Test-friendly logs

Ensure the `bm` container writes useful logs to stdout and stderr so
Compose log capture is enough for most failures.

## Local developer workflow

Local usage should be one command from the repo root.

Suggested targets:

```text
make e2e
make e2e-deploy
make e2e-migrate
make e2e-import
```

Suggested flow:

1. build `web/dist`
2. build the `bm` binary
3. build fixture images
4. start Compose
5. run Playwright
6. tear down on success
7. preserve artifacts on failure

There should also be an escape hatch for interactive debugging:

```text
make e2e-up
make e2e-down
```

That allows a developer to keep the lab running and attach manually with
the browser, SSH, or `docker compose logs`.

## CI workflow

Add a separate GitHub Actions job for end-to-end coverage.

Suggested properties:

- run on `ubuntu-latest`
- install Docker Compose v2 if needed
- build `web/dist`
- build the host `bm` binary
- build or pull the Playwright image
- run the target scenario suite
- always upload artifacts

Artifacts to retain:

- Playwright HTML report
- Playwright traces
- screenshots
- videos if enabled
- `docker compose logs`

The end-to-end job should be separate from the current fast `go` and
`web` jobs so failures are easier to triage and do not block all
feedback behind the slowest layer.

## Implementation plan

Implement this in phases so the harness becomes useful early.

### Phase 1: product and scaffolding

1. Add `bm web --allow-non-loopback`.
2. Create `integration-test/` with initial folder structure.
3. Add a minimal `compose.yml`.
4. Add Playwright config and one smoke test that opens `/clusters` and
   checks the app shell renders.

Exit criteria:

- `bm` can run in Docker on `0.0.0.0:9443`
- Playwright can reach the real UI in CI

### Phase 2: Buckit import end-to-end

1. Copy and trim the fixture logic needed for an already-running Buckit
   cluster.
2. Build Buckit fixture image and compose generation.
3. Add stable `data-testid` hooks for the import flow where needed.
4. Implement the import spec and one cluster-detail smoke assertion.
5. Wire a focused `make e2e-import` and CI job path.

Exit criteria:

- import flow runs green end to end in local and CI environments
- the imported cluster detail and nodes views render expected data

### Phase 3: target-host fixture

1. Copy and trim the systemd and loopback-drive logic needed for blank
   SSH hosts.
2. Build target-host image and compose generation.
3. Add helper scripts to bring up 4 hosts predictably.

Exit criteria:

- `bm` can SSH from its container into the target hosts by hostname
- the drives and systemd assumptions match deploy requirements

### Phase 4: deploy end-to-end

1. Add stable `data-testid` hooks for the new-cluster wizard.
2. Implement page objects and the deploy spec.
3. Add log and artifact retention on failure.
4. Wire a focused `make e2e-deploy` and CI job path.

Exit criteria:

- deploy wizard runs green end to end in local and CI environments

### Phase 5: MinIO migration end-to-end

1. Copy and trim the MinIO fixture image bits.
2. Add data-seeding and verification helpers.
3. Add stable selectors for the migration wizard where needed.
4. Implement the migrate spec.

Exit criteria:

- migration wizard runs green end to end in local and CI environments

### Phase 6: hardening

1. Reduce fixture startup time where possible.
2. Add selective scenario entrypoints.
3. Improve artifact capture and teardown behavior.
4. Evaluate whether one or two cluster operations should be covered.

## Risks and mitigations

### Slow and flaky lab startup

Risk:

- systemd containers and loopback mounts are heavier than ordinary test
  containers

Mitigation:

- keep the scenario count small
- start with Chromium only
- separate fast Go tests from slow E2E
- retain artifacts so failures are diagnosable

### CI environment differences

Risk:

- privileged or systemd-oriented containers can behave differently on
  GitHub-hosted runners than on developer machines

Mitigation:

- validate the fixture design early on `ubuntu-latest`
- keep the harness Linux-first for CI
- document any macOS Docker Desktop caveats for local runs

### Overcoupling tests to presentation details

Risk:

- browser tests become brittle if they depend on layout and wording too
  tightly

Mitigation:

- add stable `data-testid` hooks
- put wizard flow knowledge in page objects
- assert operator-visible outcomes, not incidental markup

### Harness drift from product reality

Risk:

- copied fixture code diverges and stops matching the real deployment
  assumptions

Mitigation:

- keep the copied harness small
- document provenance in the copied files
- prefer explicit `bm`-owned fixtures over a large generic lab

## Decision summary

- Use Playwright for browser E2E.
- Add `bm web --allow-non-loopback` for lab use.
- Create a repo-local `integration-test/` harness under `bm`.
- Copy only the required fixture logic from the temporary Buckit test
  cluster harness.
- Prioritize import first, deploy second, and migration third.
