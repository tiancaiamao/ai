package traceevent

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	enabledEvents       atomic.Uint64 // Per-event enablement (bit flags)
	dynamicEventsMu     sync.RWMutex
	enabledDynamicEvent = make(map[string]struct{})
)

func init() { ResetToDefaultEvents() }

// DefaultEvents returns the default enabled event names.
func DefaultEvents() []string {
	return append([]string(nil), defaultEnabledEvents...)
}

// KnownEvents returns all supported trace event names in stable order.
func KnownEvents() []string {
	out := make([]string, 0, len(eventNameToBit))
	for name := range eventNameToBit {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DisableAllEvents disables all known events.
func DisableAllEvents() {
	enabledEvents.Store(0)
	dynamicEventsMu.Lock()
	clear(enabledDynamicEvent)
	dynamicEventsMu.Unlock()
}

// ResetToDefaultEvents resets enablement to the default set and returns them.
func ResetToDefaultEvents() []string {
	DisableAllEvents()
	for _, eventName := range defaultEnabledEvents {
		EnableEvent(eventName)
	}
	return DefaultEvents()
}

// EnableEvent enables a specific event by name.
func EnableEvent(eventName string) {
	name := normalizeEventName(eventName)
	if bit, ok := eventNameToBit[name]; ok {
		mask := uint64(1) << bit
		for {
			old := enabledEvents.Load()
			if enabledEvents.CompareAndSwap(old, old|mask) {
				return
			}
		}
	}
	if isDynamicEventName(name) {
		dynamicEventsMu.Lock()
		enabledDynamicEvent[name] = struct{}{}
		dynamicEventsMu.Unlock()
	}
}

// DisableEvent disables a specific event by name.
func DisableEvent(eventName string) {
	name := normalizeEventName(eventName)
	if bit, ok := eventNameToBit[name]; ok {
		mask := uint64(1) << bit
		for {
			old := enabledEvents.Load()
			if enabledEvents.CompareAndSwap(old, old&^mask) {
				return
			}
		}
	}
	if isDynamicEventName(name) {
		dynamicEventsMu.Lock()
		delete(enabledDynamicEvent, name)
		dynamicEventsMu.Unlock()
	}
}

// IsEventEnabled checks if a specific event is enabled.
func IsEventEnabled(eventName string) bool {
	name := normalizeEventName(eventName)
	if bit, ok := eventNameToBit[name]; ok {
		return (enabledEvents.Load() & (uint64(1) << bit)) != 0
	}
	if isDynamicEventName(name) {
		dynamicEventsMu.RLock()
		_, ok := enabledDynamicEvent[name]
		dynamicEventsMu.RUnlock()
		return ok
	}
	return false
}

// GetEnabledEvents returns list of currently enabled event names.
func GetEnabledEvents() []string {
	var events []string
	enabled := enabledEvents.Load()
	for name, bit := range eventNameToBit {
		if (enabled & (uint64(1) << bit)) != 0 {
			events = append(events, name)
		}
	}
	dynamicEventsMu.RLock()
	for name := range enabledDynamicEvent {
		events = append(events, name)
	}
	dynamicEventsMu.RUnlock()
	sort.Strings(events)
	return events
}

// ExpandEventSelectors expands event selectors into concrete event names.
// Selectors may be specific event names or groups:
// - all
// - none
// - llm
// - tool
// - event
// - log
// - metrics
func ExpandEventSelectors(selectors []string) (expanded []string, unknown []string) {
	seen := make(map[string]struct{})
	add := func(name string) {
		if _, ok := eventNameToBit[name]; !ok && !isDynamicEventName(name) {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		expanded = append(expanded, name)
	}

	for _, selector := range selectors {
		key := strings.ToLower(strings.TrimSpace(selector))
		if key == "" {
			continue
		}

		if key == "all" {
			for _, name := range KnownEvents() {
				add(name)
			}
			continue
		}
		if key == "none" {
			continue
		}

		if group, ok := eventSelectorGroups[key]; ok {
			for _, name := range group {
				add(name)
			}
			continue
		}

		if _, ok := eventNameToBit[key]; ok || isDynamicEventName(key) {
			add(key)
			continue
		}

		unknown = append(unknown, selector)
	}

	return expanded, unknown
}

// EventNameToBit exports the event name to bit mapping for RPC handler use.
var EventNameToBit = eventNameToBit

// eventNameToBit maps event names to bit positions.
var eventNameToBit = map[string]int{
	// Core span lifecycle (canonical names).
	"prompt":         0,
	"prompt_start":   0, // legacy alias
	"prompt_end":     0, // legacy alias
	"llm_call":       1,
	"llm_call_start": 1, // legacy alias
	"llm_call_end":   1, // legacy alias
	"tool_execution": 2,
	"event_loop":     3,
	"tool_start":     4,
	"tool_end":       5,

	// Streaming / high-cardinality deltas.
	"text_delta":      6,
	"thinking_delta":  7,
	"tool_call_delta": 8,

	// Agent event stream lifecycle.
	"agent_start":          9,
	"agent_end":            10,
	"turn_start":           11,
	"turn_end":             12,
	"message_start":        13,
	"message_end":          14,
	"message_update":       15,
	"tool_execution_start": 2, // alias for tool_execution span + event stream marker
	"tool_execution_end":   2, // alias for tool_execution span + event stream marker
	"compaction":           18,
	"compaction_start":     18, // legacy alias
	"compaction_end":       18, // legacy alias
	"assistant_text":       20,
	"assistant_text_start": 20, // legacy alias
	"assistant_text_end":   20, // legacy alias
	"event_loop_start":     3,  // legacy alias
	"event_loop_end":       3,  // legacy alias

	// Log events.
	"log:info":                        24,
	"log:warn":                        25,
	"log:error":                       26,
	"trace_overflow":                  28,
	"tool_call_normalized":            29,
	"tool_call_unresolved":            30,
	"tool_call_invalid_args":          31,
	"assistant_tool_tag_parse_failed": 32,
	"llm_request_snapshot":            33,
	"llm_request_json":                34,
	"llm_response_json":               35,
	"tool_summary":                    36,
	"tool_summary_batch":              37,
	"llm_retry_scheduled":             38,
	"llm_retry_aborted":               39,
	"llm_retry_exhausted":             40,
}

var defaultEnabledEvents = []string{
	"prompt",
	"llm_call",
	"tool_execution",
	"event_loop",
	"tool_start",
	"tool_end",
	"tool_execution_start",
	"tool_execution_end",
	"turn_start",
	"turn_end",
	"agent_start",
	"agent_end",
	"message_start",
	"message_end",
	// "message_update",    // high frequency
	"assistant_text",
	// "text_delta",        // high frequency, enable via -trace or config
	// "thinking_delta",    // high frequency, enable via -trace or config
	// "tool_call_delta",   // high frequency, enable via -trace or config
	"compaction",
	"trace_overflow",
	"tool_call_normalized",
	"tool_call_unresolved",
	"tool_call_invalid_args",
	"assistant_tool_tag_parse_failed",
	"llm_request_snapshot",
	"llm_retry_scheduled",
	"llm_retry_aborted",
	"llm_retry_exhausted",
	"tool_summary",
	"tool_summary_batch",
	// Default log events
	"log:info",
	"log:warn",
	"log:error",
}

var eventSelectorGroups = map[string][]string{
	"llm": {
		"llm_call",
		"assistant_text",
		"text_delta",
		"thinking_delta",
		"tool_call_delta",
		"llm_request_snapshot",
		"llm_request_json",
		"llm_response_json",
		"llm_retry_scheduled",
		"llm_retry_aborted",
		"llm_retry_exhausted",
	},
	"tool": {
		"tool_execution",
		"tool_start",
		"tool_end",
		"tool_call_normalized",
		"tool_call_unresolved",
		"tool_call_invalid_args",
		"assistant_tool_tag_parse_failed",
		"tool_summary",
		"tool_summary_batch",
	},
	"event": {
		"prompt",
		"event_loop",
		"agent_start",
		"agent_end",
		"turn_start",
		"turn_end",
		"message_start",
		"message_end",
		"message_update",
		"compaction",
	},
	"log": {
		"log:info",
		"log:warn",
		"log:error",
	},
	"metrics": {
		"trace_overflow",
	},
}

func normalizeEventName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isDynamicEventName(name string) bool {
	return strings.HasPrefix(name, "log:")
}
