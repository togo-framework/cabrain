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
