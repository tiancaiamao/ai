package traceevent

import "testing"

func TestExpandEventSelectorsGroups(t *testing.T) {
	expanded, unknown := ExpandEventSelectors([]string{"event", "llm", "log", "metrics"})
	if len(unknown) != 0 {
		t.Fatalf("expected no unknown selectors, got %v", unknown)
	}

	// Spot-check key events from each group.
	required := []string{
		"turn_start",       // event
		"llm_call",         // llm
		"llm_request_json", // llm
		"log:info",         // log
		"trace_overflow",   // metrics
	}
	for _, name := range required {
		found := false
		for _, got := range expanded {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected selector expansion to include %q, got %v", name, expanded)
		}
	}
}

func TestExpandEventSelectorsUnknown(t *testing.T) {
	expanded, unknown := ExpandEventSelectors([]string{"event", "no_such_selector"})
	if len(expanded) == 0 {
		t.Fatal("expected known selector to expand")
	}
	if len(unknown) != 1 || unknown[0] != "no_such_selector" {
		t.Fatalf("expected unknown selector to be returned, got %v", unknown)
	}
}

func TestExpandEventSelectorsNone(t *testing.T) {
	expanded, unknown := ExpandEventSelectors([]string{"none"})
	if len(unknown) != 0 {
		t.Fatalf("expected no unknown selectors, got %v", unknown)
	}
	if len(expanded) != 0 {
		t.Fatalf("expected none selector to expand to empty set, got %v", expanded)
	}
}
