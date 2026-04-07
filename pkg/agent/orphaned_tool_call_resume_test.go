// Package agent provides resume-based integration tests for the orphaned tool call fix.
//
// These tests load a real buggy session fixture, reconstruct the snapshot,
// and verify that the fix correctly handles truncated tool calls at all code paths:
//   - ConvertMessagesToLLM (normal mode LLM API format)
//   - buildContextMgmtMessages (context management text format)
//   - hasValidToolResult (helper used by context management)
//
// The fixture was extracted from a real session that exhibited the bug:
// trace pid22907-sess56cc9e3b showed 44 context_mgmt_invalid_id events.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResume_RealBuggySession_VerifiesFix loads the real buggy session fixture
// and verifies the orphaned tool call fix works correctly at the snapshot level.
//
// The fixture contains 61 journal entries from session 56cc9e3b:
//   - 50 messages from turns 1-2 (user, assistant with tool calls, tool results)
//   - 9 truncate events from context management round 1 (turn 3)
//   - 1 session entry + 1 session_info entry
//
// After reconstruction, 9 tool results should be marked as Truncated.
// The fix ensures:
// 1. ConvertMessagesToLLM does not include tool calls whose results are truncated
// 2. hasValidToolResult returns false for truncated tool call IDs
// 3. buildContextMgmtMessages does not expose truncated tool call IDs to the LLM
func TestResume_RealBuggySession_VerifiesFix(t *testing.T) {
	fixtureDir := filepath.Join("testdata", "sessions", "orphaned_tool_call_bug")
	require.DirExists(t, fixtureDir, "fixture directory must exist")

	// Step 1: Load and reconstruct the snapshot (simulates resume)
	snapshot := reconstructSnapshotFromFixture(t, fixtureDir)
	require.NotNil(t, snapshot)

	// Step 2: Verify the fixture has the expected structure
	// Should have messages with tool calls and truncated results
	var totalMessages, toolResults, truncatedResults, assistantWithToolCalls int
	var allToolCallIDs, truncatedIDs []string
	for _, msg := range snapshot.RecentMessages {
		if !msg.IsAgentVisible() {
			continue
		}
		totalMessages++

		if msg.Role == "toolResult" {
			toolResults++
			if msg.Truncated {
				truncatedResults++
				truncatedIDs = append(truncatedIDs, msg.ToolCallID)
			}
			allToolCallIDs = append(allToolCallIDs, msg.ToolCallID)
		}

		if msg.Role == "assistant" {
			tcs := msg.ExtractToolCalls()
			if len(tcs) > 0 {
				assistantWithToolCalls++
			}
		}
	}

	t.Logf("Snapshot stats: %d messages, %d tool results, %d truncated, %d assistants with tool calls",
		totalMessages, toolResults, truncatedResults, assistantWithToolCalls)
	t.Logf("Truncated IDs: %v", truncatedIDs)

	require.Greater(t, truncatedResults, 0, "fixture must contain truncated tool results")
	require.Greater(t, assistantWithToolCalls, 0, "fixture must contain assistant messages with tool calls")

	// Step 3: Create agent with the reconstructed snapshot
	agent := &AgentNew{
		snapshot:       snapshot,
		triggerChecker: agentctx.NewTriggerChecker(),
	}

	// Step 4: Verify hasValidToolResult returns false for truncated IDs
	t.Run("hasValidToolResult_returns_false_for_truncated", func(t *testing.T) {
		for _, id := range truncatedIDs {
			assert.False(t, agent.hasValidToolResult(id),
				"hasValidToolResult should return false for truncated tool call %s", id)
		}
	})

	// Step 5: Verify hasValidToolResult returns true for non-truncated IDs
	t.Run("hasValidToolResult_returns_true_for_valid", func(t *testing.T) {
		validIDs := difference(allToolCallIDs, truncatedIDs)
		for _, id := range validIDs {
			if id == "" {
				continue
			}
			assert.True(t, agent.hasValidToolResult(id),
				"hasValidToolResult should return true for non-truncated tool call %s", id)
		}
	})

	// Step 6: Verify ConvertMessagesToLLM filters orphaned tool calls
	t.Run("ConvertMessagesToLLM_filters_truncated", func(t *testing.T) {
		llmMessages := ConvertMessagesToLLM(t.Context(), snapshot.RecentMessages)

		// Collect all tool call IDs from LLM messages
		var visibleToolCallIDs []string
		for _, msg := range llmMessages {
			for _, tc := range msg.ToolCalls {
				visibleToolCallIDs = append(visibleToolCallIDs, tc.ID)
			}
		}

		// Verify no truncated IDs appear in LLM messages
		for _, truncatedID := range truncatedIDs {
			assert.NotContains(t, visibleToolCallIDs, truncatedID,
				"ConvertMessagesToLLM should not include truncated tool call %s", truncatedID)
		}

		// Verify non-truncated IDs still appear
		validIDs := difference(allToolCallIDs, truncatedIDs)
		for _, validID := range validIDs {
			if validID == "" {
				continue
			}
			assert.Contains(t, visibleToolCallIDs, validID,
				"ConvertMessagesToLLM should include non-truncated tool call %s", validID)
		}
	})

	// Step 7: Verify buildContextMgmtMessages does not expose truncated IDs
	t.Run("buildContextMgmtMessages_hides_truncated_ids", func(t *testing.T) {
		messages := agent.buildContextMgmtMessages()

		var allText string
		for _, msg := range messages {
			allText += msg.Content + "\n"
		}

		// Verify truncated IDs don't appear in "id=<ID>" format
		for _, truncatedID := range truncatedIDs {
			assert.NotContains(t, allText, "id="+truncatedID,
				"buildContextMgmtMessages should not expose truncated ID %s", truncatedID)
		}
	})
}

// reconstructSnapshotFromFixture reconstructs a ContextSnapshot from a test fixture directory.
// This simulates what happens when ResumeAgentForE2E loads a session.
func reconstructSnapshotFromFixture(t *testing.T, fixtureDir string) *agentctx.ContextSnapshot {
	t.Helper()

	// Load latest checkpoint
	latestCheckpoint, err := agentctx.LoadLatestCheckpoint(fixtureDir)
	require.NoError(t, err, "failed to load latest checkpoint")

	// Read journal entries
	entries := readJournalEntries(t, filepath.Join(fixtureDir, "messages.jsonl"))
	require.NotEmpty(t, entries, "journal must not be empty")

	// Reconstruct snapshot
	snapshot, err := agentctx.ReconstructSnapshotWithCheckpoint(fixtureDir, latestCheckpoint, entries)
	require.NoError(t, err, "failed to reconstruct snapshot")

	return snapshot
}

// readJournalEntries reads all journal entries from a file without opening it in append mode.
func readJournalEntries(t *testing.T, journalPath string) []agentctx.JournalEntry {
	t.Helper()

	data, err := os.ReadFile(journalPath)
	require.NoError(t, err)

	var entries []agentctx.JournalEntry
	lines := splitLines(string(data))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry agentctx.JournalEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip invalid lines
		}
		entries = append(entries, entry)
	}

	return entries
}

// splitLines splits text into lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// difference returns elements in a that are not in b.
func difference(a, b []string) []string {
	bSet := make(map[string]bool)
	for _, s := range b {
		bSet[s] = true
	}
	var result []string
	for _, s := range a {
		if !bSet[s] {
			result = append(result, s)
		}
	}
	return result
}
