# CaBrain brains — FlowOS & AVO

CaBrain hosts multiple namespaces ("brains"):
- **`flowos`** — the FlowOS / OneStudio hub (below).
- **`avo`** (~2,490 memories) — the AVO "Founder Readiness Lab" repo
  (`onestudio-exp/AVO`) markdown: 359 files chunked by heading (board pack,
  playbooks, kaizen engine, kickstart legal/finance, pitch/fundraising, research,
  theses, drills). Re-run with `scratchpad/avoingest.py` (needs the repo cloned to
  `scratchpad/AVO` via `gh repo clone onestudio-exp/AVO`). Query with
  `memory_recall(namespace="avo", query="…")`.

---

# The FlowOS brain — querying CaBrain about ventures & people

CaBrain has ingested the FlowOS (OneStudio hub) knowledge into the `flowos`
namespace so any MCP-connected session can ask about a venture, agent, person,
learning, or lean canvas and get the answer back from memory.

## What's ingested (namespace `flowos`, ~1,780 memories)

Two passes: the FlowOS **MCP** (structured API) and the **full hub Postgres DB**
(`onestudio_hub`, via an SSH tunnel) — everything except high-frequency telemetry
(feature/transition/claude-activity events, github metrics, views, tokens).

| Type | Source |
|---|---|
| Ventures (66, full records + team) | DB `ventures` + MCP `get_venture` |
| Domain-expert agents (40, en+ar, skills) | DB `agents` + MCP `get_agent` |
| People (46, names/emails/roles + venture memberships) | DB `users` + `venture_members` |
| Issues (597 — title, description, status, priority) | DB `issues` |
| Posts / status feed (930) | DB `posts` |
| Learnings (178) | MCP `list_learnings` + DB |
| Lean canvases (57) | MCP `get_lean_canvas` |
| Roadmaps (178), Releases (69), Goals (222), Harvested research (82), Tasks (68) | DB |

Near-duplicate/templated rows collapse via the §4.1 write-decision (that's why the
row total is ~1,780, not the raw ~2,600). Each memory embeds via TEI (bge-m3) and is
recallable by vector + BM25 + rerank. Re-run / extend with `scratchpad/ingest.py`,
`enrich.py`, and `dbingest.py` (the DB pass needs the SSH tunnel:
`ssh -L 15432:<DB_HOST>:5432 <USER>@<JUMP_HOST>` — real host/creds held by the owner).

## How to query it (from a new Claude Code session)

The **cabrain** MCP server is wired in `.mcp.json` (stdio, `brain-mcp`, talks to the
app on `localhost:8080`). After restarting Claude Code so it loads, the agent has
these tools: `memory_recall`, `memory_retain`, `memory_get`, `memory_forget`,
`memory_share`, `memory_recall_archive`.

Ask with `memory_recall`, namespace `flowos`:

```
memory_recall(namespace="flowos", query="what is the Sentra venture about?")
memory_recall(namespace="flowos", query="which agent handles Saudi HR / recruitment?")
memory_recall(namespace="flowos", query="PDPL compliance kit")
```

Verified live: "Sentra" → Sentra, "Zeedly embedded Salla app" → Zeedly,
"PDPL Saudi data protection" → PDPL Starter Kit, "football scouting in Saudi
Arabia" → Akhdar, "permission-check learning" → the exact learning.

## Running / restarting the app

The app must be up for the brain MCP to reach it. It runs detached:

```bash
bash /home/coder/run-cabrain.sh   # sources ~/.env.cabrain, serves :8080 (Redis L1)
```

`run-cabrain.sh` sets `DATABASE_URL` (from `CABRAIN_DATABASE_URL`), the TEI/Cognee
URLs, `CACHE_DRIVER=redis`, and `BRAIN_BM25_TOKENIZER`. The workspace must be on
`stack_stacknet` (`sudo docker network connect stack_stacknet coder-…` on the WSL
host) so `pg`/`tei-embed`/`cognee`/`redis` resolve.

## Optionally add the live FlowOS MCP (not committed — has a token)

To also give the session live FlowOS hub tools, add this to a **non-committed**
config (`~/.claude.json` user scope), NOT the repo `.mcp.json`:

```jsonc
{"mcpServers":{"onestudio-hub":{"type":"http",
  "url":"https://flowos.one-studio.co/api/mcp",
  "headers":{"Authorization":"Bearer ${FLOWOS_MCP_TOKEN}"}}}}
```

## Two follow-ups that sharpen the brain (infra)

1. **Production BM25 tokenizer** — name/keyword recall is strongest with a real
   multilingual tokenizer. A superuser runs `infra/grant-bm25.sql` §3 to create
   `cabrain_ml` (llmlingua2), then set `BRAIN_BM25_TOKENIZER=cabrain_ml`.
2. **Cognee graph** — cognify currently fails with *"Missing required pgvector
   credentials"* (Cognee's own vector store isn't configured). Once infra sets
   Cognee's pgvector creds, `brainctl mirror flowos` populates the entity graph +
   Graph Explorer.
