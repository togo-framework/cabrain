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
			"namespace":       prop{"type": "string", "description": "memory scope (read-checked against grants)"},
			"query":           prop{"type": "string", "description": "natural-language query (vector + BM25)"},
			"limit":           prop{"type": "integer", "description": "final N after rerank (default 8, max 50)"},
			"expand_entities": prop{"type": "boolean", "description": "1-hop spreading activation (default true)"},
			"min_importance":  prop{"type": "number", "description": "optional floor filter"},
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
}
