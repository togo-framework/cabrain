package main

// toolDefs is the MCP tools/list payload — the six CaBrain memory tools (SPEC
// §5.1 / contracts/tools.md). inputSchema is JSON Schema; field names are
// snake_case to match the contract and are translated to the REST body in
// callTool. agent_id is NOT a field — it comes from the session identity (F5).

type prop = map[string]any

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

var toolDefs = []map[string]any{
	{
		"name": "memory_retain",
		"description": "Write a memory (runs the §4.1 pipeline: embed → neighbor recall → " +
			"ADD/UPDATE/INVALIDATE/NOOP decision → importance → insert hot/episodic → entity graph). " +
			"The write decision is internal; callers do not choose it.",
		"inputSchema": obj(prop{
			"namespace":       prop{"type": "string", "description": "memory scope (write-checked against grants)"},
			"content":         prop{"type": "string", "description": "raw or distilled text (Arabic/English)"},
			"source_kind":     prop{"type": "string", "enum": []string{"claude_code", "coder_run", "whatsapp", "slack", "chat", "manual"}},
			"source_ref":      prop{"type": "string", "description": "session/thread/run id (provenance)"},
			"importance_hint": prop{"type": "number", "description": "[0,1] explicit salience flag, blended not authoritative"},
			"visibility":      prop{"type": "string", "enum": []string{"private", "team", "global"}, "default": "private"},
		}, "namespace", "content", "source_kind"),
	},
	{
		"name": "memory_recall",
		"description": "Hybrid retrieval (runs §4.2): scoped vector + BM25 fused with RRF + salience, " +
			"reranked (bge-reranker-v2-m3), optional 1-hop entity expansion. Hot tier only; p95 < 300ms.",
		"inputSchema": obj(prop{
			"namespace":            prop{"type": "string", "description": "memory scope (read-checked against grants)"},
			"query":                prop{"type": "string", "description": "natural-language query (vector + BM25)"},
			"limit":                prop{"type": "integer", "description": "final N after rerank (default 8, max 50)"},
			"expand_entities":      prop{"type": "boolean", "description": "1-hop spreading activation (default true)"},
			"min_importance":       prop{"type": "number", "description": "optional floor filter"},
			"types":                prop{"type": "array", "items": prop{"type": "string"}, "description": "narrow to these memory_type values (e.g. [\"venture\",\"spec\",\"goal\"]) — cuts noise from bulk ingest types like git-activity"},
			"exclude_source_kinds": prop{"type": "array", "items": prop{"type": "string"}, "description": "drop candidates from these source_kind streams (e.g. [\"flowos_github_activity\"]) to muffle high-volume noise"},
		}, "namespace", "query"),
	},
	{
		"name": "memory_recall_archive",
		"description": "Explicit cold-tier deep recall (the ONLY tool that reads Iceberg/Parquet cold " +
			"storage). Higher latency, never folded into memory_recall. Phase 2 — stubbed until cold demotion exists.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"query":     prop{"type": "string"},
			"since":     prop{"type": "string", "description": "RFC-3339 lower bound for the archive scan"},
			"until":     prop{"type": "string", "description": "RFC-3339 upper bound for the archive scan"},
		}, "namespace", "query"),
	},
	{
		"name":        "memory_get",
		"description": "Fetch a single memory by id with full provenance (source_kind, source_ref, valid_at, invalid_at, access_count, metadata). Point lookup; read-checked.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"id":        prop{"type": "string", "description": "UUID of the memory"},
		}, "namespace", "id"),
	},
	{
		"name":        "memory_dedup",
		"description": "Soft-invalidate duplicate memories in a namespace (same source_ref → keep newest only). Optional sourceKind narrows to one ingest stream (e.g. \"flowos_github_activity\"). Returns the count of rows invalidated. Write-checked.",
		"inputSchema": obj(prop{
			"namespace":  prop{"type": "string"},
			"sourceKind": prop{"type": "string", "description": "optional; empty = all source_kinds"},
		}, "namespace"),
	},
	{
		"name":        "memory_forget",
		"description": "Soft-invalidate a memory (sets invalid_at=now(); never hard-deletes — history stays queryable). Write-checked.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"id":        prop{"type": "string", "description": "UUID of the memory"},
			"reason":    prop{"type": "string", "description": "optional; recorded in metadata"},
		}, "namespace", "id"),
	},
	{
		"name":        "memory_share",
		"description": "Grant a namespace to another agent (upserts namespace_grants). Caller must already hold a grant on the namespace.",
		"inputSchema": obj(prop{
			"namespace":        prop{"type": "string"},
			"grantee_agent_id": prop{"type": "string", "description": "the agent receiving access"},
			"can_read":         prop{"type": "boolean", "default": true},
			"can_write":        prop{"type": "boolean", "default": false},
		}, "namespace", "grantee_agent_id"),
	},
	{
		"name": "graph_traverse",
		"description": "Walk the entity graph outward from a named entity (multi-hop). Answers relational " +
			"questions similarity search cannot, e.g. 'which people work on ventures in the Turif portfolio'. " +
			"Returns each reachable entity with its type, depth and the path walked to reach it.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"entity":    prop{"type": "string", "description": "exact entity name to start from"},
			"depth":     prop{"type": "integer", "description": "hops, default 2, max 6"},
			"relations": prop{"type": "array", "items": prop{"type": "string"}, "description": "restrict to these relation types (see graph_ontology)"},
			"types":     prop{"type": "array", "items": prop{"type": "string"}, "description": "only return these entity types"},
			"direction": prop{"type": "string", "enum": []string{"out", "in", "both"}},
			"asOf":      prop{"type": "string", "description": "RFC3339 — walk the graph as it stood then"},
			"limit":     prop{"type": "integer"},
		}, "namespace", "entity"),
	},
	{
		"name": "graph_spine",
		"description": "EVERYTHING about one venture (or portfolio, repo, person) in ONE call, grouped by role — " +
			"repos, members, recent activity, feed, goals, OKRs, roadmap, code modules, docs, learnings, issues, " +
			"meetings, channels. Each group reports the TRUE total next to a capped sample, so a short list is " +
			"never mistaken for the whole population. Use `window` (\"7d\", \"2w\") for time-boxed questions such as " +
			"'what happened on venture X last week' — the window binds the event-bearing roles (activity, meeting, " +
			"channel, feed) and deliberately leaves structural roles alone. Prefer this over several graph_traverse calls.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"entity":    prop{"type": "string", "description": "venture/portfolio name (or any entity name or id)"},
			"depth":     prop{"type": "integer", "description": "hops, default 2 (use 3 for a portfolio), max 4"},
			"hubs":      prop{"type": "array", "items": prop{"type": "string"}, "description": "entity types expanded past hop 1; default repo,venture,feed,portfolio"},
			"roles":     prop{"type": "array", "items": prop{"type": "string"}, "description": "only return these role groups"},
			"perGroup":  prop{"type": "integer", "description": "cap per group, default 10, max 200 (total is always the true count)"},
			"window":    prop{"type": "string", "description": "relative window: 24h, 7d, 2w, 3m"},
			"since":     prop{"type": "string", "description": "RFC3339 lower bound (overrides window)"},
			"until":     prop{"type": "string", "description": "RFC3339 upper bound"},
			"timeRoles": prop{"type": "array", "items": prop{"type": "string"}, "description": "roles the window applies to; [\"*\"] = all"},
		}, "namespace", "entity"),
	},
	{
		"name": "graph_neighbors",
		"description": "Immediate typed relationships of one entity, each with its relation, direction and a " +
			"human-readable fact. Use to explain HOW something is connected.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"entity":    prop{"type": "string"},
			"asOf":      prop{"type": "string", "description": "RFC3339 — relationships valid at that instant"},
		}, "namespace", "entity"),
	},
	{
		"name":        "graph_path",
		"description": "Shortest relationship path between two entities — answers 'how are these two connected?'.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"from":      prop{"type": "string"},
			"to":        prop{"type": "string"},
			"maxDepth":  prop{"type": "integer", "description": "default 4, max 6"},
		}, "namespace", "from", "to"),
	},
	{
		"name": "graph_ontology",
		"description": "The brain's declared shape: entity types and relation types with live counts. Call this " +
			"FIRST to discover which entity/relation names graph_traverse will accept.",
		"inputSchema": obj(prop{"namespace": prop{"type": "string"}}, "namespace"),
	},
	{
		"name": "memory_gaps",
		"description": "List knowledge gaps — questions the brain couldn't answer (recall came back empty), " +
			"deduped and counted. See what's missing, then index it (memory_retain) and memory_resolve_gap.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string", "description": "optional scope filter"},
			"status":    prop{"type": "string", "enum": []string{"open", "indexed", "dismissed", "all"}, "description": "default: open+indexed"},
			"limit":     prop{"type": "integer", "description": "default 100"},
		}),
	},
	{
		"name":        "memory_resolve_gap",
		"description": "Resolve a knowledge gap after indexing the missing knowledge (status=indexed) or to drop it (dismissed).",
		"inputSchema": obj(prop{
			"id":         prop{"type": "integer", "description": "the gap id from memory_gaps"},
			"status":     prop{"type": "string", "enum": []string{"indexed", "dismissed", "open"}},
			"resolution": prop{"type": "string", "description": "optional note"},
		}, "id", "status"),
	},
	{
		"name":        "brain_list",
		"description": "List the brains (namespaces) with their memory counts — the top level of what's stored.",
		"inputSchema": obj(prop{}),
	},
	{
		"name":        "brain_details",
		"description": "Detailed view of one brain (namespace): memory count, breakdown by type and source, open gaps, recall activity, first/last dates.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
		}, "namespace"),
	},
	{
		"name":        "memory_edit",
		"description": "Edit an existing memory by id — change its content (re-embeds), importance, or metadata. Namespace + id required.",
		"inputSchema": obj(prop{
			"namespace":  prop{"type": "string"},
			"id":         prop{"type": "string", "description": "memory UUID"},
			"content":    prop{"type": "string", "description": "new content (optional; re-embeds)"},
			"importance": prop{"type": "number", "description": "0..1 (optional)"},
			"metadata":   prop{"type": "object", "description": "replacement metadata (optional)"},
		}, "namespace", "id"),
	},
	{
		"name":        "brain_delete",
		"description": "DELETE a whole brain (namespace) and all its memories — destructive. `confirm` MUST equal the namespace.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"confirm":   prop{"type": "string", "description": "must equal namespace to proceed"},
		}, "namespace", "confirm"),
	},
	{
		"name":        "brain_grant",
		"description": "ACL (admin): grant an agent/token read and/or write access to a brain. Upserts the grant.",
		"inputSchema": obj(prop{
			"agentId":   prop{"type": "string", "description": "the agent identity the token maps to"},
			"namespace": prop{"type": "string", "description": "the brain to grant"},
			"canRead":   prop{"type": "boolean", "default": true},
			"canWrite":  prop{"type": "boolean", "default": false},
		}, "agentId", "namespace"),
	},
	{
		"name":        "brain_revoke_grant",
		"description": "ACL (admin): remove an agent's access to a brain.",
		"inputSchema": obj(prop{
			"agentId":   prop{"type": "string"},
			"namespace": prop{"type": "string"},
		}, "agentId", "namespace"),
	},
	{
		"name":        "brain_create_token",
		"description": "ACL (admin): mint an access token for an agent identity (optionally admin). Grant it brains with brain_grant; the holder sets CABRAIN_TOKEN.",
		"inputSchema": obj(prop{
			"agentId": prop{"type": "string"},
			"label":   prop{"type": "string"},
			"isAdmin": prop{"type": "boolean", "default": false},
		}, "agentId"),
	},
	{
		"name":        "brain_tokens",
		"description": "ACL (admin): list access tokens with their per-brain grants.",
		"inputSchema": obj(prop{
			"includeRevoked": prop{"type": "boolean", "default": false},
		}),
	},
	{
		"name":        "brain_chat",
		"description": "Live agent: ask a selected brain a question. It recalls the brain's relevant memories and answers grounded ONLY in them, returning the answer + the memories it cited (footprint). Says the brain has no memory of it rather than guessing.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"message":   prop{"type": "string"},
			"topK":      prop{"type": "integer", "description": "memories to ground on (default 8)"},
		}, "namespace", "message"),
	},
	{
		"name":        "secret_list",
		"description": "Secrets vault: list secret names + masked hints for a brain (NO values). Needs read on the brain.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
		}, "namespace"),
	},
	{
		"name":        "secret_store",
		"description": "Secrets vault: store/update an encrypted secret under a name for a brain. Needs write on the brain. Secrets found in retained content are ALSO auto-captured + redacted; use this for explicit stores.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"name":      prop{"type": "string"},
			"value":     prop{"type": "string"},
			"kind":      prop{"type": "string", "description": "api_key|password|env|token|private_key|connection_string|generic"},
		}, "namespace", "name", "value"),
	},
	{
		"name":        "secret_reveal",
		"description": "Secrets vault: decrypt and return a secret's value. Requires WRITE/admin on the brain (stricter than read). Use to recover a key between sessions.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"name":      prop{"type": "string"},
		}, "namespace", "name"),
	},
	{
		"name":        "secret_delete",
		"description": "Secrets vault: delete a secret from a brain. Needs write on the brain.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"name":      prop{"type": "string"},
		}, "namespace", "name"),
	},
	{
		"name": "datasource_list",
		"description": "Data sources: list a brain's configured connectors (text/markdown/crawler/github/sql/webhook) " +
			"with status, doc counts and last sync. Needs read on the brain.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
		}, "namespace"),
	},
	{
		"name": "datasource_create",
		"description": "Data sources: add a connector to a brain. kind ∈ text|markdown|crawler|github|sql|webhook. " +
			"config is per-kind JSON (text:{content,title}, crawler:{url}, github:{repo,branch,path,ext,token}, " +
			"sql:{driver,dsn,query,refColumn,titleColumn}, webhook: a shared secret is auto-generated). Needs write.",
		"inputSchema": obj(prop{
			"namespace": prop{"type": "string"},
			"kind":      prop{"type": "string", "enum": []string{"text", "markdown", "crawler", "github", "sql", "webhook"}},
			"name":      prop{"type": "string"},
			"config":    prop{"type": "object", "description": "per-kind connector config"},
		}, "namespace", "kind", "name"),
	},
	{
		"name":        "datasource_sync",
		"description": "Data sources: run a connector now — Fetch its documents, chunk and retain them into the brain. Returns ingested count. Webhook sources are push-only (cannot be synced). Needs write.",
		"inputSchema": obj(prop{
			"id": prop{"type": "string", "description": "datasource id from datasource_list"},
		}, "id"),
	},
	{
		"name":        "datasource_delete",
		"description": "Data sources: delete a connector from a brain (its already-ingested memories stay). Needs write.",
		"inputSchema": obj(prop{
			"id": prop{"type": "string", "description": "datasource id from datasource_list"},
		}, "id"),
	},
}
