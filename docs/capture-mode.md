# Capture mode — SPEC §6 (design)

Phase 1's goal is **memory mass**, not a demo. Capture mode passively records meaningful
Claude Code turns as `memory_retain` calls so the brain accumulates high-fidelity episodics
across every session. No consolidation yet (that's Phase 2) — just accumulate.

> Status: **design only.** Wiring is inert until a live `memory_retain` endpoint exists
> (needs Blocker A: ToGO, and Blocker B: infra). This doc pins *what* gets captured and
> *how* it's redacted/scoped so the hook is a thin, decided shim when the endpoint lands.

## Mechanism: a Claude Code `Stop` hook

Claude Code fires lifecycle hooks the harness executes (not the model). Capture rides a
`Stop` hook (end of each assistant turn): the hook reads the turn's transcript, extracts the
memory-worthy spans, redacts, and POSTs them to `memory_retain`. Fire-and-forget so it never
adds latency to the interactive loop; failures log and drop (capture is best-effort, the
session is authoritative).

- **`source_kind`** = `claude_code`
- **`source_ref`** = the Claude Code **session id** (provenance; lets recall cite the origin)
- **`namespace`** = the project namespace (derived from the repo/workstream: `sentra`,
  `freshup`, `orchestra`, `togo`, `cabrain`, …) — one grant per project, no cross-leak (F5)

## What is memory-worthy (capture) vs noise (skip)

Capture the *why*, which compaction throws away — not the raw byte stream.

| Capture (one `retain` each) | Skip |
|---|---|
| **Decisions** — "we chose X over Y because Z" | routine file listings / greps |
| **Rejected approaches** — "tried A, it failed because B" | tool output already on disk |
| **Constraints / gotchas learned** — "the DB PK must include the partition key" | model chit-chat, acks |
| **Interface/contract facts** — endpoints, schemas, env shapes | verbatim large code blocks (store a pointer + summary, not the blob) |
| **User corrections / preferences** | transient status ("running tests…") |

Heuristic for the extraction step: keep turns that state a *conclusion, choice, correction,
or durable fact*. When unsure, prefer a short distilled sentence over the raw turn — the
store stays clean and `retain`'s ADD/UPDATE/NOOP decision (§4.1) dedupes the rest.

## `<private>` redaction (hard requirement)

Before anything leaves the session, strip every `<private>…</private>` span from `content`.
Redaction happens **in the hook, pre-POST** — private text must never reach the wire or the
DB. If a turn is entirely private, drop it. (Secrets/credentials are treated as implicitly
private and never captured regardless of tags.)

## Importance at capture time

The hook does **not** set final importance — `memory_retain` computes it (§4.1: novelty +
`source_kind` weight + recency). The hook may pass `importance_hint` when a turn is
explicitly flagged high-signal (an explicit decision, a user "remember this"), blended in,
not authoritative. Leaving salience flat is the top quality risk (SPEC §8) — so the hint is
a nudge, and the real formula lives server-side where it can be tuned.

## Rollout

1. Land the hook against one consumer (this Claude Code) once `memory_retain` is live.
2. Run it across all active workstreams so the brain sees varied real signal.
3. **Only then** evaluate recall quality (Gate 1) — you cannot tune recall before there is
   captured mass to recall from.

## Open decisions (resolve when wiring)

- **Extraction: inline heuristic vs a cheap model call.** A tiny model gives cleaner
  distillation but adds cost/latency to the hook. Start heuristic (regex/rules on turn
  structure); upgrade to a cheap `EXTRACTION_LLM` pass if signal-to-noise is poor.
- **Batching.** One `retain` per worthy span vs a batched end-of-session flush. Start
  per-span (simpler provenance); batch later if `retain` volume pressures the write path.
- **Namespace derivation.** Map repo path → namespace via a small config table so a session
  never guesses scope.
