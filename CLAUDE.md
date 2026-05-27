# CLAUDE.md

Guidance for Claude Code (claude.ai/code) and other coding agents
working in this repository.

## Project overview

`bm` is the Buckit Manager — a personal desktop tool an operator runs
on their Mac/Windows machine to manage Buckit clusters. It ships as a
single binary that hosts a CLI plus a local web UI. Closer in spirit
to `mc`, `gh`, or `jupyter notebook` than to a centralised cluster
console (Rancher / Portainer / ArgoCD).

Most of Phase 1 (M1–M8) has landed: the real Go backend, real cluster
import/discovery via the admin API, real deploy + cutover orchestration,
and a React UI that fetches against the real HTTP surface. Still
outstanding: M9 (embed the React bundle into the binary, packaging,
install scripts) and the optional remote-access mode (passcode + TLS).

The product spec, implementation plan, and UI architecture all live in
the peer `buckit/` repo:

- High-level design — `../buckit/docs/manager/README.md`
- Phase 1 web UI wireframes — `../buckit/docs/manager/phase1-web-ui.md`
- Phase 1 implementation plan — `../buckit/docs/manager/phase1-implementation.md`
- UI architecture (data flow, REST contract, per-page details) — `../buckit/docs/manager/ui-architecture.md`
- Buckit metrics-tab spec (referenced for `/admin/info` shape) — `../buckit/docs/manager/metrics.md`

When in doubt about scope or behaviour, those docs are the source of
truth. Don't extend the surface beyond what the design docs already
describe without checking with the operator.

## Common commands

```sh
# Go side
make build              # build the bm binary (./bm)
make build-all          # cross-compile for linux/darwin/windows × amd64/arm64
make test               # go test -race -count=1 ./...
make lint               # downloads golangci-lint into .bin/ if needed
make run                # ./bm web (binds 127.0.0.1:9443, opens browser)

# Web side (run in web/)
cd web && npm run dev       # Vite dev server on :5173, proxies /api/* to :9443
cd web && npm run build     # tsc -b && vite build (output → web/dist/)
cd web && npm run typecheck # tsc -b --noEmit

# Both
make web                # npm install + npm run build, builds the bundle into web/dist
```

`bm web` serves `web/dist/` from disk via a chi static handler
(`internal/api/router.go`). Embedding into the binary is an M9 task.

## Repository layout

```
cmd/bm/main.go           CLI entry point + subcommand dispatch (web, version,
                         help; everything else is delegated to bm-cli)
cmd/bm/web.go            `bm web` subcommand — wires the api router to bbolt
                         + the task orchestrator, opens the browser
internal/admin/          madmin-go wrapper. Maps the wire shapes into bm's
                         domain types (ServerInfo, HealthInfo)
internal/alias/          mc-style aliases (also persisted in bbolt)
internal/api/            chi v5 HTTP surface. Handlers split by milestone:
                         m4_handlers.go (clusters/import/discover),
                         m5_migrate_handlers.go (snapshot/preflight),
                         m6_handlers.go (operations + SSE event stream)
internal/app/            top-level wire-up shared by cmd/bm/web.go
internal/bmcli/          delegates non-native subcommands to the forked bm-cli
internal/clusters/       cluster repo over bbolt
internal/clusteradmin/   per-cluster admin creds, sealed with AES-GCM
internal/config/         resolves ~/.config/bm/{config.json,bm.db,data.key}
internal/credentials/    SSH key + admin creds helpers
internal/deploy/         deploys a new Buckit cluster across hosts
internal/discovery/      cluster import flow — turns URL+creds into an
                         ImportCandidate. engine.go classifies MinIO vs Buckit
internal/domain/         shared domain types (mirrored by web/src/api/types.ts)
internal/migration/      MinIO → Buckit cutover: snapshot, preflight, executor,
                         installer, rollback, verify
internal/nodes/          node repo + per-node facts
internal/operations/     orchestrated cluster operations (restart, upgrade,
                         redeploy). Validates engine compatibility before
                         dispatch
internal/preflight/      generic preflight harness (used by deploy + migrate)
internal/ssh/            SSH connection pool
internal/sshconfig/      per-cluster SSH config (user, key, jump host)
internal/sshtest/        in-process SSH server for tests
internal/store/          bbolt wrapper. Buckets: clusters, nodes, node_facts,
                         cluster_ssh, cluster_admin, history, settings.
                         AES-GCM PutEncrypted/GetEncrypted for sensitive rows
internal/tasks/          long-running operation orchestrator. Each Executor
                         (DeployExecutor, CutoverExecutor, RollbackExecutor,
                         …) is registered here; runs surface over SSE
internal/version/        build-time version metadata (-ldflags fed by Makefile)
web/                     React 18 + Vite + TypeScript
  src/api/client.ts      real HTTP client (every UI fetch funnels through)
  src/api/sse.ts         SSE streaming endpoints (discover, operation events)
  src/api/hooks.ts       react-query hooks wrapping client.ts
  src/api/types.ts       wire types — mirrors internal/domain/
  src/layouts/           AppShell + WizardShell
  src/components/        Pill, Stepper, TaskLogStream, TaskStateIcon, TaskStepsTimeline
  src/pages/             one folder per route family (cluster/, wizards/new/, wizards/migrate/)
  src/routes.tsx         react-router config; matches the site map in phase1-web-ui.md
  src/styles/            tokens.css + global.css (CSS variables, light/dark)
scripts/lint.sh          installs golangci-lint locally + runs it
.github/workflows/ci.yml Go matrix build + web typecheck/build
```

The `web/src/mock/` directory referenced by older notes no longer exists —
`api/client.ts` replaced it.

## Stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go 1.25 | matches `buckit/`; no CGO so cross-compile is clean |
| HTTP router | `chi v5` | landed |
| Storage | `bbolt` with short-lived locks | data dir at `~/.config/bm/bm.db`, AES-GCM seal for sensitive buckets |
| Admin client | `buckit-io/madmin-go/v3` (forked) | wrapped by `internal/admin/` |
| Frontend | React 18 + Vite + TypeScript | `@tanstack/react-query`, `@tanstack/react-table`, `react-router-dom` |
| Embedding | `embed.FS` of `web/dist` | planned, M9 — today the binary serves dist from disk |
| Event stream | SSE | landed — discover stream, operation event stream |

## Wire contract

The browser talks to `bm` over HTTP at `127.0.0.1:9443` (loopback only;
non-loopback binds are explicitly rejected by `AssertLoopback` in
`internal/api/router.go` until the remote-access milestone). Every UI
fetch goes through `web/src/api/client.ts`; SSE endpoints go through
`web/src/api/sse.ts`. Wire types are mirrored in two places:

- Go: `internal/domain/`
- TS: `web/src/api/types.ts`

These need to stay in lockstep. When you change a domain type on either
side, update the other in the same change. The UI's data needs are still
the source of truth for what the REST contract looks like — keep
`ui-architecture.md` in the peer `buckit/` repo in sync when the shape
changes.

## What's still outstanding

| Surface | Status |
|---|---|
| Embedded binary | not yet — `bm web` serves `web/dist/` from disk (`router.go` `staticHandler`). M9 swaps to `//go:embed`. |
| Packaging (nfpm, install scripts) | not yet (M9). No `packaging/` directory exists yet. |
| Remote-access mode (passcode + TLS) | not yet — `AssertLoopback` enforces 127.0.0.1-only binds. |

Don't claim a feature works end-to-end unless you've traced both sides
of a change (Go + TS). If you only touch one side, say so.

## Notes for agents

- **Mobile is out of scope** for Phase 1. The cluster detail page is
  an 11-column monitoring grid that doesn't fit on a phone. Don't
  responsive-ify it; do make sure tables use `.card--table` so they
  scroll horizontally rather than clip.
- **Personal-tool framing.** No multi-user auth in default mode (the
  listener is `127.0.0.1` only). Optional remote-access mode adds
  passcode + TLS — see `ui-architecture.md` § "Optional remote access".
  Don't introduce session/RBAC/audit-retention plumbing without it.
- **Default sort on the node table** is pool asc, hostname asc within
  pool. The tiebreaker in `compareNodes` makes that automatic for
  every other sort key too — keep it.
- **Pool card severity ordering** (`POOL_RANK`) ensures problems are
  never hidden in the collapsed view. Don't change it to numeric
  ordering without a follow-up plan to surface problems some other way.

## Verification before saying "done"

1. `make build` succeeds.
2. `cd web && npm run build` succeeds with no TypeScript errors.
3. `make test` passes (`go test -race -count=1 ./...`). The migration,
   api, operations, discovery, store, tasks, ssh packages all have real
   tests — don't skip them.
4. Cross-compile sanity:
   ```sh
   for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
     GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/bm \
       && echo "ok $t" || echo "FAIL $t"
   done
   ```
5. If you touched the UI, walk through the affected flow in
   `npm run dev` (Vite proxies `/api/*` to `:9443`, so keep `bm web`
   running alongside). Remember `bm web` is long-running — Go changes
   need a rebuild + restart before the browser sees them.
