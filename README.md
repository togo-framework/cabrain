# CaBrain

A general, hardware-elastic **memory organ** for AI agents — a ToGO plugin on `togo-postgres`
(VectorChord + BM25 + pgvector) with hippocampal *hot* / cortical *cold* tiers, salience-gated
sleep consolidation, and reconsolidation-on-recall. Exposed as MCP tools + a Claude Code Memory
Tool backend. See [`SPEC.md`](./SPEC.md) for the full design and [`PLAN.md`](./PLAN.md) for the
sequenced build.

## Status — Phase 1, Track 0 (pre-ToGO source-of-truth artifacts)

The ToGO framework and the finalized infra bundle are not yet on this box, so the ToGO scaffold
(`togo new` / `make:plugin`) and any live-DB work are still blocked. What lands first are the
**engine- and infra-independent contracts** that everything else reconciles against — and that
double as the direct-on-Postgres fallback (SPEC §8):

| Path | What it is |
|---|---|
| [`db/schema.sql`](./db/schema.sql) | Data model (SPEC §3), runnable DDL with the partitioning/FK fixes the literal spec DDL needs |
| [`contracts/tools.md`](./contracts/tools.md) | The six MCP tool contracts (SPEC §5.1) — request/response, engine-agnostic |
| [`docs/capture-mode.md`](./docs/capture-mode.md) | The Claude Code capture-hook design (SPEC §6) |

## Blockers before the next tracks run

- **A — ToGO pointer:** repo URL + `togo` install command (+ target app). *Still open.*
- **B — infra bundle:** finalized `.env` (SPEC §1.5) + `INFRA-CaBrain.md`. Postgres + MinIO live;
  TEI/Cognee/Ollama coming up. All infra hostnames resolve only inside Docker net `stack_stacknet`
  → CaBrain app should run as a container on that network (pending confirm).

Secrets are never committed; they arrive via the app env from the infra agent.
