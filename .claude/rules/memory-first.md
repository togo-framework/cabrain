# Memory-first rules (cabrain brain)

This project has a memory organ exposed by the **cabrain** MCP server. The FlowOS /
OneStudio knowledge lives in the `flowos` namespace. These rules are **mandatory** —
follow them on every turn.

## R1 — Recall BEFORE you answer
For ANY question that touches FlowOS / OneStudio — a venture, portfolio, person,
domain-expert agent, issue, task, post, roadmap, release, goal, decision, learning,
or "who/what/why" about the studio — you **MUST** call `memory_recall` with
`namespace: "flowos"` **before** answering. Do this even if you think you already
know; the brain is the source of truth.

## R2 — Recall BEFORE you act
Before starting work on a venture or topic (writing code, planning, drafting), first
`memory_recall` the relevant context (the venture, its issues, related learnings) so
you act on what's already known instead of re-deriving it.

## R3 — Answer FROM memory, and cite it
Base the answer on what `memory_recall` returns. Quote/point to the specific
memories used. If recall returns nothing relevant, **say so explicitly** ("the brain
has no memory of X") — do NOT invent facts to fill the gap.

## R4 — Retain what's new
After you produce something durable — a decision ("we chose X over Y because Z"), a
correction, a learned constraint/gotcha, a new fact about a venture/person, an
interface/contract detail — call `memory_retain` (namespace `flowos`) so the brain
grows. The write-decision de-dupes automatically; when unsure, retain a short
distilled sentence rather than nothing.

## R5 — Prefer the brain over asking
If information is likely already in the brain (studio/venture/people facts), recall it
instead of asking the user to repeat it. Only ask the user for things the brain
genuinely doesn't have.

## R6 — Query style
Use concise, keyword-forward queries (`"Sentra"`, `"PDPL compliance kit"`,
`"OAuth login issue"`) — they rank cleaner than long sentences. Recall more than one
phrasing if the first is thin.

## R7 — Namespaces (pick the right brain)
- `flowos` — the FlowOS / OneStudio hub brain (ventures, people, agents, issues,
  posts, learnings). Default for studio/venture/people questions.
- `avo` — the **AVO "Founder Readiness Lab"** brain: the full markdown of the AVO
  repo (board, playbooks, kaizen engine, kickstart legal/finance, pitch/fundraising,
  research, theses, drills). Use for any question about AVO, founder readiness, the
  board pack, playbooks, or KSA/GCC founder prep.
- `cabrain` — this project's own dev knowledge: the CaBrain repo docs (SPEC, PLAN,
  DEPLOY, decisions, rules) + git commit history. Use when developing/following up
  on CaBrain itself ("how does the Redis L1 work?", "why the partition-index BM25?").

Pick the namespace that matches the question; never mix scopes in one query. If it's
ambiguous which brain, recall both (two calls) and merge.

## R8 — Keep the brain reachable
The tools need the app on `:8080`. If `memory_recall` fails to connect, tell the user
to run `bash /home/coder/run-cabrain.sh` (and confirm the workspace is on
`stack_stacknet`). See `docs/flowos-brain.md`.

## R9 — Keep the `cabrain` brain current (every update)
After committing a set of changes to this project, **refresh the `cabrain` dev brain**
so the next session can recall the latest state and history:

```bash
CABRAIN_MEMORY_FILE=~/.claude/projects/-home-coder-caBrain/memory/cabrain-project.md \
  python3 scripts/refresh-cabrain-brain.py
```

This re-ingests the repo docs + the full git build-log (+ the curated project memory
if the env var is set); the §4.1 write-decision dedupes, so only new/changed knowledge
is added. Treat it as part of "done": code committed → brain refreshed.

**Order of operations every turn: recall → answer/act → retain (and refresh `cabrain` after commits).**
