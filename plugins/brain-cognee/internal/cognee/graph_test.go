package cognee

import "testing"

func TestParseGraphAndEntityNames(t *testing.T) {
	body := []byte(`{
	  "nodes": [
	    {"id":"n1","label":"Postgres","type":"Entity","properties":{}},
	    {"id":"n2","label":"pgvector","type":"Entity","properties":{}},
	    {"id":"n3","label":"mem-123.txt","type":"TextDocument","properties":{}},
	    {"id":"n4","label":"chunk-0","type":"DocumentChunk","properties":{}},
	    {"id":"n5","label":"Postgres","type":"Entity","properties":{}}
	  ],
	  "edges": [
	    {"source":"n3","target":"n1","label":"mentions"},
	    {"source":"n1","target":"n2","label":"uses"}
	  ]
	}`)
	g, err := ParseGraph(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g.Nodes) != 5 || len(g.Edges) != 2 {
		t.Fatalf("expected 5 nodes / 2 edges, got %d/%d", len(g.Nodes), len(g.Edges))
	}
	names := g.EntityNames()
	// Entity nodes only, deduped, structural nodes (TextDocument/DocumentChunk) dropped.
	want := []string{"Postgres", "pgvector"}
	if len(names) != len(want) {
		t.Fatalf("EntityNames = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("EntityNames[%d] = %q, want %q (full %v)", i, names[i], want[i], names)
		}
	}
}

func TestEntityNamesUntypedFallback(t *testing.T) {
	// Older graphs may not tag node types → keep all labeled nodes.
	g := &GraphDTO{Nodes: []GraphNode{{ID: "a", Label: "Alice"}, {ID: "b", Label: "Bob"}, {ID: "c", Label: ""}}}
	if got := g.EntityNames(); len(got) != 2 || got[0] != "Alice" || got[1] != "Bob" {
		t.Fatalf("untyped fallback = %v, want [Alice Bob]", got)
	}
}

func TestParseGraphInvalid(t *testing.T) {
	if _, err := ParseGraph([]byte("not json")); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestMemoryEntityLinks(t *testing.T) {
	const mem = "d9925e40-9e60-4755-9ecf-559e00744caa"
	// Document node carries the mem-<uuid>.txt name → chunk → two entities (2 hops).
	// A far entity (3 hops) must NOT link; an unrelated doc contributes nothing.
	g := &GraphDTO{
		Nodes: []GraphNode{
			{ID: "doc", Label: "mem-" + mem + ".txt", Type: "TextDocument"},
			{ID: "chunk", Label: "chunk-0", Type: "DocumentChunk"},
			{ID: "e1", Label: "Redis", Type: "Entity"},
			{ID: "e2", Label: "Postgres", Type: "Entity"},
			{ID: "far", Label: "FarEntity", Type: "Entity"},
			{ID: "doc2", Label: "other.txt", Type: "TextDocument"},
		},
		Edges: []GraphEdge{
			{Source: "doc", Target: "chunk", Label: "has_chunk"},
			{Source: "chunk", Target: "e1", Label: "mentions"},
			{Source: "chunk", Target: "e2", Label: "mentions"},
			{Source: "e2", Target: "far", Label: "related"}, // 3 hops from doc → excluded
		},
	}
	links := g.MemoryEntityLinks()
	got := map[string]bool{}
	for _, l := range links {
		if l.MemoryID != mem {
			t.Fatalf("unexpected memory id %q", l.MemoryID)
		}
		got[l.EntityName] = true
	}
	if !got["Redis"] || !got["Postgres"] {
		t.Fatalf("expected Redis+Postgres links, got %v", got)
	}
	if got["FarEntity"] {
		t.Fatal("FarEntity is 3 hops away and must not link")
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d: %+v", len(links), links)
	}
}

func TestNodeMemoryIDFromProperties(t *testing.T) {
	// The uuid may ride in a property value rather than the label.
	n := GraphNode{ID: "d", Label: "document", Properties: map[string]any{
		"name": "mem-25a34e66-1292-4962-b880-cb00ef028ec1.txt",
	}}
	if got := nodeMemoryID(n); got != "25a34e66-1292-4962-b880-cb00ef028ec1" {
		t.Fatalf("nodeMemoryID from properties = %q", got)
	}
	if got := nodeMemoryID(GraphNode{Label: "Postgres", Type: "Entity"}); got != "" {
		t.Fatalf("entity node should have no memory id, got %q", got)
	}
}
