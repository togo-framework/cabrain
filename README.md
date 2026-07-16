# CaBrain

A general, hardware-elastic **memory organ** for AI agents — built as a **ToGO plugin**
(`plugins/cabrain`) on `togo-postgres` (VectorChord + BM25 + pgvector) with hippocampal
*hot* / cortical *cold* tiers, salience-gated sleep consolidation, and reconsolidation-on-recall.
Exposed as MCP tools + a Claude Code Memory Tool backend. Full design in [`SPEC.md`](./SPEC.md);
sequenced build in [`PLAN.md`](./PLAN.md).

## Status — Phase 1

- **Track 0 (done, committed):** engine-agnostic source-of-truth artifacts —
  [`db/schema.sql`](./db/schema.sql) (§3 data model, partition PK/FK fixes),
  [`contracts/tools.md`](./contracts/tools.md) (§5.1 six MCP tools),
  [`docs/capture-mode.md`](./docs/capture-mode.md) (§6 capture hook).
- **Track A (in progress):** togo app scaffolded at repo root (`togo v0.2.31`, module
  `github.com/togo-framework/cabrain`, `--db togo-postgres`, features `queue,storage,cache`);
  `plugins/cabrain` + the five resources next.
- **Blocked (Track B):** `togo migrate`/`serve`/`db:up` and embedding benchmarks await the
  finalized infra bundle (SPEC §1.5). Postgres + MinIO are up on the box; all infra
  hostnames resolve only inside Docker net `stack_stacknet`, so CaBrain deploys as a
  container on that network (pending confirm). Local `go` is 1.22.2; the app targets 1.26
  via `GOTOOLCHAIN=auto`, and `sqlc`/`atlas` install via `togo doctor`.

## togo quickstart (once infra is live)

```bash
cp .env.example .env          # gets the SPEC §1.5 values from the infra bundle
togo doctor                   # ensure Go 1.26 toolchain + sqlc + atlas
togo make:resource ...        # model + controller + views (drives togo.resources.yaml)
togo generate                 # sqlc + gqlgen + atlas + openapi
togo migrate                  # apply schema (needs the live DB — Blocker B)
togo serve                    # backend (+ frontend)
```

## Layout

```
plugins/cabrain/      CaBrain plugin (provider + internal + web + .claude)   ← the brain
db/schema.sql         §3 data model — reference / direct-on-Postgres fallback
db/atlas/             Atlas desired-state schema + migrations
internal/db/          sqlc schema + queries + generated code
internal/{graph,rest,resources,server,app,plugins}/   generated API + wiring
cmd/{api,migrate,seed}/   entrypoints
contracts/tools.md    §5.1 MCP tool contracts        docs/capture-mode.md   §6 capture design
web/                  frontend (tanstack)            togo.yaml   project config
SPEC.md · PLAN.md     design + sequenced build
```

Secrets are never committed; they arrive via the app env from the infra agent.
