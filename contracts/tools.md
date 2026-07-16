# CaBrain tool contracts — SPEC §5.1

The six MCP tools are the **only** consumer surface. They are engine-agnostic: the same
request/response holds whether the engine is Cognee or the direct-on-Postgres fallback
(SPEC §8). Field names map 1:1 to Go structs (`sqlc`) and to MCP JSON-Schema `inputSchema`.

## Conventions

- **Scoping (F5) is server-enforced, never client-trusted.** Every call carries an
  `agent_id` (from the MCP session identity, *not* a free request field) and a
  `namespace`. The server checks `namespace_grants(agent_id, namespace)` for `can_read`
  (recall/get) or `can_write` (retain/forget) before doing anything. A missing grant →
  `permission_denied`, not empty results. With >1 agent, `ontology` layers on top.
- **Never hard-delete.** `memory_forget` soft-invalidates; history stays queryable.
- **Latency budget (N1):** `memory_recall` p95 < 300 ms — no inline LLM, no DuckDB, no
  cold tier on that path. `memory_recall_archive` is the *only* tool allowed to touch cold.
- **Timestamps** are RFC-3339 UTC. **IDs** are UUID strings. `importance` ∈ [0,1].
- Every call emits one `memory_events` row (`op`, `latency_ms`, `namespace`, `agent_id`).

---

## `memory_retain` — write a memory (runs §4.1)

Embed → recall neighbors → ADD/UPDATE/INVALIDATE/NOOP decision → compute importance →
insert episodic/hot → populate entity graph → emit event. The write decision is internal;
callers do not choose it.

**Request**
| field | type | req | notes |
|---|---|---|---|
| `namespace` | string | ✓ | scope; write-checked against grants |
| `content` | string | ✓ | raw or distilled text (Arabic/English) |
| `source_kind` | enum | ✓ | `claude_code`\|`coder_run`\|`whatsapp`\|`slack`\|`chat`\|`manual` |
| `source_ref` | string |  | session/thread/run id (provenance) |
| `importance_hint` | number |  | [0,1] explicit salience flag; blended into the computed score, not authoritative |
| `visibility` | enum |  | `private`(default)\|`team`\|`global` |
| `metadata` | object |  | free-form jsonb |

**Response**
| field | type | notes |
|---|---|---|
| `id` | uuid | the affected memory (new, or the updated/superseding row) |
| `decision` | enum | `add`\|`update`\|`invalidate`\|`noop` — what the pipeline actually did |
| `importance` | number | the computed score (so callers can see salience) |
| `superseded_id` | uuid? | present on `update`/`invalidate`: the row that was retired |

---

## `memory_recall` — hybrid retrieval (runs §4.2)

Scoped hybrid (vector + BM25) fused with RRF + `0.15·importance`, top-20 → rerank
(`bge-reranker-v2-m3`) → optional 1-hop entity expansion → bump access stats. Hot tier only.

**Request**
| field | type | req | notes |
|---|---|---|---|
| `namespace` | string | ✓ | scope; read-checked against grants |
| `query` | string | ✓ | natural-language query, used for both vector + BM25 |
| `limit` | int |  | final N after rerank (default 8, max 50) |
| `expand_entities` | bool |  | 1-hop spreading activation (default true) |
| `min_importance` | number |  | optional floor filter |

**Response** — `results`: array, each:
| field | type | notes |
|---|---|---|
| `id` | uuid | |
| `content` | string | |
| `score` | number | fused + rerank score (ranking only, not calibrated) |
| `network` / `memory_type` | enum | classification |
| `source_kind` / `source_ref` | string | **provenance — required by Gate 1** |
| `importance` | number | |
| `valid_at` | timestamp | |
| `via_entity` | string? | set when the row came from 1-hop expansion, names the linking entity |

---

## `memory_recall_archive` — explicit cold-tier deep recall

The **only** tool that reads Iceberg/Parquet cold storage. Separate call, higher latency,
never folded into `memory_recall`. Same response shape as `memory_recall` plus
`tier: "cold"` on each row. (Phase 2 — stubbed until cold demotion exists.)

**Request** adds to `memory_recall`: `since` / `until` (timestamps) to bound the archive scan.

---

## `memory_get` — fetch by id (+ provenance)

**Request:** `namespace` (✓), `id` (✓).
**Response:** the full memory row incl. `source_kind`, `source_ref`, `valid_at`,
`invalid_at`, `superseded_by`, `access_count`, `metadata`. Read-checked. Returns from hot
or cold transparently (this is a point lookup, not the latency-bound path).

---

## `memory_forget` — soft-invalidate (F4/F, never hard-delete)

**Request:** `namespace` (✓), `id` (✓), `reason` (string, optional → `metadata`).
**Effect:** sets `invalid_at = now()`; row stays queryable via `memory_get` /
`memory_recall_archive`. Write-checked. Emits `op='forget'`.
**Response:** `id`, `invalid_at`.

---

## `memory_share` — grant a namespace to an agent

**Request:** `namespace` (✓), `grantee_agent_id` (✓), `can_read` (bool, default true),
`can_write` (bool, default false).
**Authorization:** caller must already hold a grant on `namespace` (bootstrap grant seeded
out-of-band). Upserts `namespace_grants`. Emits `op='share'`.
**Response:** the resulting grant row.

---

## Error model (shared)

`permission_denied` (no/insufficient grant) · `not_found` (id absent in scope) ·
`invalid_argument` (bad enum/range) · `unavailable` (embedding/rerank/engine down —
retriable). Scoping failures are **never** silently returned as empty result sets.
