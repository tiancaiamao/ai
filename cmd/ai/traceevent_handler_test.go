package main

import (
	"testing"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// TestTraceEventOnHandler tests that the RPC handler correctly handles the "on" command
func TestTraceEventOnHandler(t *testing.T) {
	// Save current state
	originalEvents := traceevent.GetEnabledEvents()
	defer func() {
		// Restore original state
		traceevent.DisableAllEvents()
		for _, name := range originalEvents {
			traceevent.EnableEvent(name)
		}
	}()

	// Simulate what the RPC handler does when receiving "on"
	handler := func(events []string) ([]string, error) {
		if len(events) == 0 {
			return traceevent.ResetToDefaultEvents(), nil
		}

		normalized := events
		if len(normalized) == 0 {
			return traceevent.ResetToDefaultEvents(), nil
		}

		applyExpanded := func(expanded []string, replace bool) []string {
			if replace {
				traceevent.DisableAllEvents()
			}
			for _, eventName := range expanded {
				traceevent.EnableEvent(eventName)
			}
			return traceevent.GetEnabledEvents()
		}

		op := normalized[0]
		switch op {
		case "default":
			return traceevent.ResetToDefaultEvents(), nil
		case "all", "on":
			expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
			return applyExpanded(expanded, true), nil
		case "off", "none":
			traceevent.DisableAllEvents()
			return []string{}, nil
		default:
			expanded, unknown := traceevent.ExpandEventSelectors(normalized)
			if len(unknown) > 0 {
				return nil, nil // error case, simplified for test
			}
			return applyExpanded(expanded, true), nil
		}
	}

	// Test 1: "on" command should enable all events
	t.Run("on_enables_all_events", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"on"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) == 0 {
			t.Error("expected 'on' to enable events")
		}

		// Verify all known events are enabled
		allEvents, _ := traceevent.ExpandEventSelectors([]string{"all"})
		if len(result) != len(allEvents) {
			t.Errorf("expected %d events enabled, got %d", len(allEvents), len(result))
		}
	})

	// Test 2: "all" command should still work
	t.Run("all_enables_all_events", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"all"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) == 0 {
			t.Error("expected 'all' to enable events")
		}
	})

	// Test 3: "off" command should disable all events
	t.Run("off_disables_all_events", func(t *testing.T) {
		// First enable some events
		_, _ = handler([]string{"all"})

		// Then disable them
		result, err := handler([]string{"off"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected 'off' to disable all events, got %d events", len(result))
		}
	})

	// Test 4: Selector commands should work
	t.Run("selectors_work", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"llm"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) == 0 {
			t.Error("expected 'llm' selector to enable events")
		}

		// Verify llm events are enabled
		enabled := traceevent.GetEnabledEvents()
		hasLLMCall := false
		for _, e := range enabled {
			if e == "llm_call" {
				hasLLMCall = true
				break
			}
		}
		if !hasLLMCall {
			t.Error("expected 'llm_call' event to be enabled")
		}
	})
}