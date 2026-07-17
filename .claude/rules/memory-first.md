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

## R7 — Namespaces
`flowos` = the FlowOS/OneStudio brain (default for studio questions). Use a different
namespace only when the user is clearly working in a separate project/scope; never
mix scopes in one query.

## R8 — Keep the brain reachable
The tools need the app on `:8080`. If `memory_recall` fails to connect, tell the user
to run `bash /home/coder/run-cabrain.sh` (and confirm the workspace is on
`stack_stacknet`). See `docs/flowos-brain.md`.

**Order of operations every turn: recall → answer/act → retain.**
