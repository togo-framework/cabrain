# cabrain

A general, hardware-elastic **memory organ** for AI agents — built as togo plugins on
`togo-postgres` (VectorChord + BM25 + pgvector) with hippocampal *hot* / cortical *cold*
tiers, a Redis L1 cache, salience-gated sleep consolidation, and reconsolidation-on-recall.
Exposed as MCP tools + a Claude Code Memory Tool backend.

`cabrain` is the **project** (this monorepo / dev harness). The memory organ itself is the
**`brain`** plugin (`github.com/togo-framework/brain`), installable like any togo plugin.
Each provider is its own plugin (`brain-tei`, `brain-cognee`, …). Full design in
[`SPEC.md`](./SPEC.md); sequenced build in [`PLAN.md`](./PLAN.md); running decisions in
[`docs/decisions.md`](./docs/decisions.md).

## Status — Phase 1

- **Green:** `go build ./...`, standalone `brain` build/vet, and `togo generate`
  (sqlc → gqlgen → openapi) all pass.
- **`brain` plugin** (`plugins/brain`, module `github.com/togo-framework/brain`): schema +
  data layer + provider seams in place —
  [`internal/brain/schema.sql`](./plugins/brain/internal/brain/schema.sql) (§3 data model,
  `go:embed`ed + `Migrate`), `store.go` (retain/recall skeletons + hybrid recall SQL),
  `providers.go` (`Embedder`/`Reranker`/`Engine` seams for the driver plugins).
- **Contracts:** [`contracts/tools.md`](./contracts/tools.md) (§5.1 six MCP tools),
  [`docs/capture-mode.md`](./docs/capture-mode.md) (§6 capture hook).
- **Blocked (Blocker B):** `migrate`/`serve` and retain/recall execution await the finalized
  infra bundle. The workspace-reachable Postgres is a vanilla PG16 without the required
  extensions; the provisioned `cabrain` DB (vchord stack) is not yet reachable. Redis + NATS
  are reachable. See [`docs/decisions.md`](./docs/decisions.md) D5.

## Layout

```
cabrain/                         project / dev harness (module github.com/togo-framework/cabrain)
├── plugins/brain/               THE PLUGIN — module github.com/togo-framework/brain
│   ├── plugin.go                RegisterProviderFunc: /api/brain/ping (+ retain/recall as they land)
│   ├── togo.plugin.yaml
│   └── internal/brain/
│       ├── schema.sql           §3 data model (embedded, applied by Migrate)
│       ├── schema.go            go:embed + Migrate + recallSQL (§4.2)
│       ├── store.go             Store: Retain (§4.1) / Recall (§4.2) + provider registration
│       ├── providers.go         Embedder / Reranker / Engine seams (brain-tei, brain-cognee)
│       └── service.go           HTTP surface + provider wiring
├── internal/plugins/local.go    blank-imports the brain plugin (out of the DO-NOT-EDIT gen file)
├── go.mod                       require + replace github.com/togo-framework/brain => ./plugins/brain
├── internal/, cmd/, web/        harness (togo-generated API + serve/migrate entrypoints)
├── SPEC.md · PLAN.md            design + sequenced build
├── docs/decisions.md            running decision log (deltas on SPEC)
├── docs/capture-mode.md         §6 capture design
└── contracts/tools.md           §5.1 MCP tool contracts
```

## Dev loop (once the cabrain DB is live)

```bash
cp .env.example .env          # SPEC §1.5 / infra §4 values (secrets injected, never committed)
togo generate                 # sqlc + gqlgen + openapi  (green today)
togo migrate                  # apply schema — needs the live cabrain DB (Blocker B)
togo serve                    # backend + frontend
```

Toolchain is pinned to the cached go1.26.5 (`GOTOOLCHAIN=go1.26.5`); `sqlc` + `atlas` are in
`~/go/bin`. Secrets are never committed; they arrive via the app env from the infra agent.
