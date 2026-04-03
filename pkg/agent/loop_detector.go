package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

const defaultMaxDuplicateCalls = 7

// loopDetector tracks tool call patterns to detect infinite loops.
// Unlike a local-map approach, this is persistent on the AgentNew struct,
// so it survives context management interruptions and trampoline re-entries.
//
// Detection strategy:
//   - Track tool calls by NAME (not exact arguments), because LLMs often retry
//     with slight parameter variations (different paths, slight rephrasings).
//   - Count consecutive occurrences of the same tool name.
//   - Reset the counter when a different tool is called.
//   - Also detect oscillation between a small set of tools (A→B→A→B→...).
type loopDetector struct {
	// maxDuplicates is the threshold for consecutive same-name tool calls.
	maxDuplicates int

	// toolNameCount tracks consecutive occurrences of each tool name.
	// Reset when a different tool name appears.
	toolNameCount map[string]int

	// lastToolNames is the ordered list of tool names from the last LLM response.
	// Used to detect oscillation patterns.
	lastToolNames []string

	// oscillationCount tracks how many consecutive rounds have the same
	// set of tool names (in any order). This catches A→B→A→B patterns.
	oscillationCount int
}

// newLoopDetector creates a new loopDetector.
func newLoopDetector(maxDuplicates int) *loopDetector {
	if maxDuplicates <= 0 {
		maxDuplicates = defaultMaxDuplicateCalls
	}
	return &loopDetector{
		maxDuplicates:    maxDuplicates,
		toolNameCount:    make(map[string]int),
		lastToolNames:    nil,
		oscillationCount: 0,
	}
}

// check analyzes tool calls from an assistant message and returns an error
// if an infinite loop is detected.
//
// It returns the list of tool names found (for logging) and an error if stuck.
func (ld *loopDetector) check(assistantMsg *agentctx.AgentMessage) ([]string, error) {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		// No tool calls: reset tracking (pure text response = progress)
		for k := range ld.toolNameCount {
			delete(ld.toolNameCount, k)
		}
		ld.lastToolNames = nil
		ld.oscillationCount = 0
		return nil, nil
	}

	// Extract current tool names
	currentNames := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		currentNames = append(currentNames, tc.Name)
	}

	// --- Check 1: Consecutive same-name tool calls ---
	ld.updateToolNameCounters(currentNames)

	for name, count := range ld.toolNameCount {
		if count >= ld.maxDuplicates {
			return currentNames, fmt.Errorf(
				"agent appears to be stuck in a loop: tool '%s' called %d times consecutively (limit: %d). "+
					"Consider using a different approach or providing a final text response",
				name, count, ld.maxDuplicates,
			)
		}
	}

	// --- Check 2: Oscillation detection (A→B→A→B pattern) ---
	// Only check for oscillation when there are 2+ distinct tools.
	// Single-tool oscillation is already caught by the name counter above.
	if len(currentNames) >= 2 && ld.sameToolSet(ld.lastToolNames, currentNames) {
		ld.oscillationCount++
		halfMax := ld.maxDuplicates / 2
		if halfMax < 3 {
			halfMax = 3
		}
		if ld.oscillationCount >= halfMax {
			return currentNames, fmt.Errorf(
				"agent appears to be oscillating between tools: same tool set {%s} repeated %d times. "+
					"Consider using a different approach or providing a final text response",
				strings.Join(uniqueSorted(currentNames), ", "),
				ld.oscillationCount,
			)
		}
	} else {
		ld.oscillationCount = 0
	}

	ld.lastToolNames = currentNames

	slog.Info("[AgentNew] Loop detector check passed",
		"tool_names", currentNames,
		"name_counts", ld.toolNameCount,
		"oscillation", ld.oscillationCount,
	)

	return currentNames, nil
}

// reset clears all loop detector state.
// Called when the agent makes meaningful progress (e.g., produces a text response).
// Nil-safe: no-op if the receiver is nil.
func (ld *loopDetector) reset() {
	if ld == nil {
		return
	}
	for k := range ld.toolNameCount {
		delete(ld.toolNameCount, k)
	}
	ld.lastToolNames = nil
	ld.oscillationCount = 0
}

// updateToolNameCounters increments counters for present names and resets absent ones.
func (ld *loopDetector) updateToolNameCounters(currentNames []string) {
	present := make(map[string]bool, len(currentNames))
	for _, name := range currentNames {
		present[name] = true
		ld.toolNameCount[name]++
	}

	for name := range ld.toolNameCount {
		if !present[name] {
			delete(ld.toolNameCount, name)
		}
	}
}

// sameToolSet checks if two slices contain the same set of tool names.
func (ld *loopDetector) sameToolSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	setA := make(map[string]bool, len(a))
	for _, s := range a {
		setA[s] = true
	}
	for _, s := range b {
		if !setA[s] {
			return false
		}
	}
	return true
}

// uniqueSorted returns unique sorted strings from a slice.
func uniqueSorted(s []string) []string {
	seen := make(map[string]bool, len(s))
	for _, v := range s {
		seen[v] = true
	}
	result := make([]string, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	return result
}

// buildToolCallSignature creates a unique signature for a tool call.
// The signature is based on tool name and normalized parameters.
func buildToolCallSignature(tc agentctx.ToolCallContent) string {
	argsJSON := "{}"
	if tc.Arguments != nil {
		if bytes, err := json.Marshal(tc.Arguments); err == nil {
			argsJSON = string(bytes)
		}
	}
	return tc.Name + ":" + argsJSON
}
