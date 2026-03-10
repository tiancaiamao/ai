package traceevent

import (
	"testing"
)

func TestTraceEventOnCommand(t *testing.T) {
	// Save current state
	originalEvents := GetEnabledEvents()
	defer func() {
		// Restore original state
		DisableAllEvents()
		for _, name := range originalEvents {
			EnableEvent(name)
		}
	}()

	// Test 1: Verify "on" is not recognized as an unknown selector
	t.Run("on_not_unknown", func(t *testing.T) {
		// "on" should not be in the unknown list when expanding selectors
		// This is because it's handled at the command level, not the selector level
		// But we can verify it's not a valid selector name
		_, unknown := ExpandEventSelectors([]string{"on"})
		if len(unknown) == 0 {
			t.Error("expected 'on' to be unknown as a selector (it's a command, not a selector)")
		}
	})

	// Test 2: Verify that "all" selector works correctly
	t.Run("all_selector_enables_all_events", func(t *testing.T) {
		DisableAllEvents()
		expanded, unknown := ExpandEventSelectors([]string{"all"})
		if len(unknown) != 0 {
			t.Errorf("expected no unknown selectors for 'all', got %v", unknown)
		}
		if len(expanded) == 0 {
			t.Error("expected 'all' to expand to multiple events")
		}

		// Enable all events
		for _, name := range expanded {
			EnableEvent(name)
		}

		enabled := GetEnabledEvents()
		if len(enabled) != len(expanded) {
			t.Errorf("expected %d enabled events, got %d", len(expanded), len(enabled))
		}
	})

	// Test 3: Verify other selectors work
	t.Run("other_selectors_work", func(t *testing.T) {
		tests := []struct {
			selector  string
			mustExist []string
		}{
			{"llm", []string{"llm_call", "llm_request_json"}},
			{"tool", []string{"tool_execution", "tool_start"}},
			{"event", []string{"turn_start", "agent_start"}},
			{"log", []string{"log:info", "log:warn", "log:error"}},
		}

		for _, tt := range tests {
			t.Run(tt.selector, func(t *testing.T) {
				DisableAllEvents()
				expanded, unknown := ExpandEventSelectors([]string{tt.selector})
				if len(unknown) != 0 {
					t.Errorf("expected no unknown selectors for '%s', got %v", tt.selector, unknown)
				}

				for _, name := range expanded {
					EnableEvent(name)
				}

				enabled := GetEnabledEvents()
				for _, mustExist := range tt.mustExist {
					found := false
					for _, e := range enabled {
						if e == mustExist {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected '%s' to be enabled when using selector '%s'", mustExist, tt.selector)
					}
				}
			})
		}
	})
}