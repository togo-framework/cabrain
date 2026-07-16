package brain

import "testing"

func TestWriteDecision(t *testing.T) {
	cases := []struct {
		name    string
		top     *neighbor
		content string
		wantDec string
		wantRel bool // expect a related id
	}{
		{"no neighbor → add", nil, "the sky is blue", "add", false},
		{"exact dup → noop", &neighbor{ID: "a", Content: "The sky is blue", Sim: 0.99}, "the sky   is blue", "noop", true},
		{"near-identical → noop", &neighbor{ID: "a", Content: "sky colour is blue today", Sim: 0.985}, "the sky is blue", "noop", true},
		{"evolved → update", &neighbor{ID: "a", Content: "we use pg on port 5432", Sim: 0.91}, "we use pg on port 55432 now", "update", true},
		{"distinct → add", &neighbor{ID: "a", Content: "unrelated topic entirely", Sim: 0.40}, "the sky is blue", "add", false},
		{"negation of similar → invalidate", &neighbor{ID: "a", Content: "the API key rotates monthly", Sim: 0.90}, "correction: the API key no longer rotates monthly", "invalidate", true},
		{"negation of distinct → add", &neighbor{ID: "a", Content: "unrelated", Sim: 0.30}, "that's not true anymore", "add", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dec, rel := writeDecision(c.top, c.content)
			if dec != c.wantDec {
				t.Fatalf("decision = %q, want %q", dec, c.wantDec)
			}
			if (rel != "") != c.wantRel {
				t.Fatalf("relatedID presence = %v, want %v (got %q)", rel != "", c.wantRel, rel)
			}
		})
	}
}

func TestNormText(t *testing.T) {
	if normText("  The  SKY\tis\nblue ") != "the sky is blue" {
		t.Fatalf("normText collapse failed: %q", normText("  The  SKY\tis\nblue "))
	}
}

func TestNegates(t *testing.T) {
	for _, s := range []string{"this is false", "no longer valid", "Correction: X", "disregard that"} {
		if !negates(s) {
			t.Errorf("expected negates(%q) = true", s)
		}
	}
	for _, s := range []string{"the sky is blue", "we deploy on friday"} {
		if negates(s) {
			t.Errorf("expected negates(%q) = false", s)
		}
	}
}
