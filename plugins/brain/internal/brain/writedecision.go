package brain

import "strings"

// Write-decision (SPEC §4.1, Mem0-style). On retain the brain compares the new
// content against its nearest existing memory in the namespace and decides whether
// to ADD a new memory, UPDATE (supersede) an evolved one, treat it as a NOOP
// duplicate, or INVALIDATE a now-false one. The decision is a pure function of the
// nearest neighbor so it is unit-testable without the DB or the embedder.
//
// Thresholds are cosine similarity in [0,1] (1 = identical embedding):
//   - ≥ simDuplicate  → NOOP    (essentially the same memory; don't store a dup)
//   - ≥ simSupersede  → UPDATE  (same subject, changed content → supersede the old)
//   - otherwise       → ADD     (a distinct memory)
//
// A byte-identical (normalized) content is always a NOOP regardless of score.
// INVALIDATE is driven by an explicit negation signal in the new content (a full
// contradiction detector is an extraction-LLM job, Phase 2).
const (
	simDuplicate = 0.97
	simSupersede = 0.88
)

// neighbor is the nearest existing memory to a candidate write.
type neighbor struct {
	ID      string
	Content string
	Sim     float64 // cosine similarity to the candidate, [0,1]
}

// writeDecision returns the action and, for UPDATE/NOOP/INVALIDATE, the id of the
// existing memory involved (superseded on UPDATE, matched on NOOP, retired on
// INVALIDATE).
func writeDecision(top *neighbor, newContent string) (decision, relatedID string) {
	if top == nil {
		return "add", ""
	}
	if normText(top.Content) == normText(newContent) {
		return "noop", top.ID // exact (normalized) duplicate
	}
	// An explicit negation of a highly-similar memory retires it.
	if top.Sim >= simSupersede && negates(newContent) {
		return "invalidate", top.ID
	}
	switch {
	case top.Sim >= simDuplicate:
		return "noop", top.ID
	case top.Sim >= simSupersede:
		return "update", top.ID
	default:
		return "add", ""
	}
}

func normText(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// negates flags content that explicitly retracts/corrects a prior fact. Deliberately
// conservative — false negatives just fall back to UPDATE, never a wrong deletion.
var negationCues = []string{
	"no longer", "not anymore", "is wrong", "was wrong", "incorrect",
	"actually not", "never mind", "disregard", "retract", "correction:",
	"that's not true", "this is false",
}

func negates(content string) bool {
	c := strings.ToLower(content)
	for _, cue := range negationCues {
		if strings.Contains(c, cue) {
			return true
		}
	}
	return false
}
