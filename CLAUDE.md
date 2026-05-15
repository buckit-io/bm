# CLAUDE.md

Guidance for Claude Code (claude.ai/code) and other coding agents
working in this repository.

## Project overview

`bm` is the Buckit Manager — a personal desktop tool an operator runs
on their Mac/Windows machine to manage Buckit clusters. It ships as a
single binary that hosts a CLI plus a local web UI. Closer in spirit
to `mc`, `gh`, or `jupyter notebook` than to a centralised cluster
console (Rancher / Portainer / ArgoCD).

This repo is in **Phase 1**. What's landed today is the M0 module
scaffold and a clickable web UI prototype on top of an in-memory mock
data layer. The real backend (M1 onward) hasn't shipped yet — `bm
web` is a stub that prints "not yet implemented (M1)".

The product spec, implementation plan, and UI architecture all live in
the peer `buckit/` repo:

- High-level design — `../buckit/docs/manager/README.md`
- Phase 1 web UI wireframes — `../buckit/docs/manager/phase1-web-ui.md`
- Phase 1 implementation plan — `../buckit/docs/manager/phase1-implementation.md`
- UI architecture (data flow, REST contract, per-page details) — `../buckit/docs/manager/ui-architecture.md`
- Buckit metrics-tab spec (referenced for `/admin/info` shape) — `../buckit/docs/manager/metrics.md`

When in doubt about scope or behaviour, those docs are the source of
truth. Do not extend the prototype beyond what the design docs already
describe without checking with the operator.

## Common commands

```sh
# Go side
make build              # build the bm binary (./bm)
make build-all          # cross-compile for linux/darwin/windows × amd64/arm64
make test               # go test -race -count=1 ./...
make lint               # downloads golangci-lint into .bin/ if needed
make run                # ./bm web (currently a stub)

# Web side (run in web/)
cd web && npm run dev       # Vite dev server on :5173, proxies /api/* to :9443
cd web && npm run build     # tsc -b && vite build (output → web/dist/)
cd web && npm run typecheck # tsc -b --noEmit

# Both
make web                # npm install + npm run build, builds the embeddable bundle
```

## Repository layout

```
cmd/bm/                  CLI entry point + subcommand dispatch
internal/version/        build-time version metadata (-ldflags fed by Makefile)
internal/...             stub for app, api, store, tasks, ssh, deploy, cluster, auth, config, health
                         (most of these don't exist yet — they land in M1+)
web/                     React + Vite + TypeScript prototype
  src/api/hooks.ts       react-query hooks; today they wrap the mock client
  src/mock/data.ts       in-memory fixtures + computed health rules
  src/mock/api.ts        mock API client returning Promises with simulated latency
  src/layouts/           AppShell + WizardShell
  src/components/        Pill, Stepper, TaskLogStream, TaskStateIcon, TaskStepsTimeline
  src/pages/             one folder per route family (cluster/, wizards/new/, wizards/migrate/)
  src/routes.tsx         react-router config; matches the site map in phase1-web-ui.md
  src/styles/            tokens.css + global.css (CSS variables, light/dark)
scripts/lint.sh          installs golangci-lint locally + runs it
.github/workflows/ci.yml Go matrix build + web typecheck/build
packaging/               (planned for M9 — nfpm, install.sh, install.ps1)
```

## Stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go 1.25 | matches `buckit/`; no CGO so cross-compile is clean |
| HTTP router | `chi v5` (planned, M1) | not yet imported |
| Storage | `bbolt` with short-lived locks (planned, M1) | data dir at `~/.config/bm/bm.db` |
| Frontend | React 18 + Vite + TypeScript | `@tanstack/react-query`, `@tanstack/react-table`, `react-router-dom` |
| Embedding | `embed.FS` of `web/dist` (planned, M9) | single static binary at release |
| Event stream | SSE (planned, M2) | for task event streams only |

## Mock data layer

The web UI is fully self-contained today. `web/src/mock/data.ts`
defines the domain types and fixture data; `web/src/mock/api.ts`
exposes Promise-returning functions that mirror what the future
HTTP API will return. `web/src/api/hooks.ts` wraps those in react-query
hooks.

**The goal is for the real backend's REST shape to fall out of what
the UI actually fetches** — the `Cluster`, `Node`, `Task`, `HistoryEntry`,
`HealthSummary` types in `data.ts` should be the contract documented
under "REST API contract" in `ui-architecture.md`. When changing the
mock, keep the doc in sync.

When implementing the real Go backend, the natural seam is to swap
`web/src/mock/api.ts` for a real `fetch`-based client; everything
else stays the same.

## What's a stub today

| Surface | Status |
|---|---|
| `bm version`, `bm help` | landed |
| `bm web`, `bm server` (alias) | stubs — print "not yet implemented (M1)" |
| `internal/api`, `internal/store`, `internal/tasks`, `internal/ssh`, `internal/app`, `internal/cluster` | not implemented |
| Web UI (every screen + both wizards) | landed against the mock layer |
| Embedded binary | not yet — Vite build produces `web/dist/` but it's not embedded |
| Packaging (nfpm, install scripts) | not yet (M9) |

Don't claim a feature works end-to-end unless you've traced both
sides. If you're touching a UI screen and there's no real backend yet,
say so explicitly when reporting work done.

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

1. `make build` succeeds and the binary is in the ~10–12 MB target
   range (currently smaller because Go deps haven't landed yet).
2. `cd web && npm run build` succeeds with no TypeScript errors.
3. `make test` passes (currently a no-op — no Go tests yet).
4. Cross-compile sanity:
   ```sh
   for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
     GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/bm \
       && echo "ok $t" || echo "FAIL $t"
   done
   ```
5. If you touched the UI prototype, verify the pages still render in
   `npm run dev` — at minimum walk through `/clusters` →
   `/clusters/prod-east` → one node detail page → `/clusters/new`
   wizard step 1.
