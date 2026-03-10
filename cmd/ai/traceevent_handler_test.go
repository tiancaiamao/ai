package main

import (
	"fmt"
	"strings"
	"testing"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// TestTraceEventOnHandler tests that the RPC handler correctly handles the "on" and "all" commands
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

	// Create the actual handler logic (same as in rpc_handlers.go)
	handler := func(events []string) ([]string, error) {
		if len(events) == 0 {
			return traceevent.ResetToDefaultEvents(), nil
		}

		normalized := make([]string, 0, len(events))
		for _, e := range events {
			e = strings.ToLower(strings.TrimSpace(e))
			if e == "" {
				continue
			}
			normalized = append(normalized, e)
		}
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
		case "on":
			// "on" enables the default working set (not all events, to avoid high-frequency noise)
			return traceevent.ResetToDefaultEvents(), nil
		case "all":
			// "all" enables ALL known events, including high-frequency ones
			expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
			return applyExpanded(expanded, true), nil
		case "default":
			return traceevent.ResetToDefaultEvents(), nil
		case "off", "none":
			traceevent.DisableAllEvents()
			return []string{}, nil
		case "enable":
			if len(normalized) == 1 {
				return nil, fmt.Errorf("trace-events enable requires at least one selector")
			}
			expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return applyExpanded(expanded, false), nil
		case "disable":
			if len(normalized) == 1 {
				return nil, fmt.Errorf("trace-events disable requires at least one selector")
			}
			expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			for _, eventName := range expanded {
				traceevent.DisableEvent(eventName)
			}
			return traceevent.GetEnabledEvents(), nil
		default:
			// Backward-compatible absolute set.
			expanded, unknown := traceevent.ExpandEventSelectors(normalized)
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return applyExpanded(expanded, true), nil
		}
	}

	// Test 1: "on" command should enable default events (not all events)
	t.Run("on_enables_default_events", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"on"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) == 0 {
			t.Error("expected 'on' to enable events")
		}

		// Verify default events are enabled
		defaultEvents := traceevent.DefaultEvents()
		if len(result) != len(defaultEvents) {
			t.Errorf("expected %d default events enabled, got %d", len(defaultEvents), len(result))
		}

		// Verify high-frequency events are NOT enabled
		enabled := traceevent.GetEnabledEvents()
		for _, e := range enabled {
			if e == "text_delta" || e == "thinking_delta" || e == "tool_call_delta" {
				t.Errorf("'on' should not enable high-frequency event %s", e)
			}
		}
	})

	// Test 2: "all" command should enable ALL events including high-frequency ones
	t.Run("all_enables_all_events", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"all"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(result) == 0 {
			t.Error("expected 'all' to enable events")
		}

		// Verify all known events are enabled
		allEvents, _ := traceevent.ExpandEventSelectors([]string{"all"})
		if len(result) != len(allEvents) {
			t.Errorf("expected %d events enabled, got %d", len(allEvents), len(result))
		}

		// Verify high-frequency events ARE enabled
		enabled := traceevent.GetEnabledEvents()
		hasTextDelta := false
		for _, e := range enabled {
			if e == "text_delta" {
				hasTextDelta = true
				break
			}
		}
		if !hasTextDelta {
			t.Error("expected 'all' to enable high-frequency event 'text_delta'")
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

	// Test 5: Unknown selectors should return proper error
	t.Run("unknown_selectors_return_error", func(t *testing.T) {
		traceevent.DisableAllEvents()
		result, err := handler([]string{"unknown_selector_xyz"})
		if err == nil {
			t.Error("expected error for unknown selector, got nil")
		}
		if result != nil {
			t.Errorf("expected nil result for error case, got %v", result)
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("expected error to mention 'unknown', got: %v", err)
		}
	})

	// Test 6: "on" and "all" produce different results
	t.Run("on_vs_all_different", func(t *testing.T) {
		traceevent.DisableAllEvents()
		onResult, _ := handler([]string{"on"})
		
		traceevent.DisableAllEvents()
		allResult, _ := handler([]string{"all"})

		if len(onResult) == len(allResult) {
			t.Errorf("'on' and 'all' should enable different number of events, but both enabled %d", len(onResult))
		}

		if len(onResult) > len(allResult) {
			t.Errorf("'all' should enable more events than 'on', but 'on' enabled %d and 'all' enabled %d", len(onResult), len(allResult))
		}
	})
}