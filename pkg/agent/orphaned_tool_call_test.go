// Package agent provides tests for the orphaned tool call fix.
//
// Bug: When context management truncates tool results, assistant messages still
// reference those tool calls. The LLM sees orphaned tool calls and either:
//   - Tries to use the IDs in context management → context_mgmt_invalid_id events
//   - Produces API-invalid sequences (tool calls without corresponding results)
//
// Fix: Three code paths filter orphaned tool calls:
//   1. ConvertMessagesToLLM: filters tool calls whose results have Truncated=true
//   2. buildNormalModeRequest: filters tool calls whose results are hidden (agent_visible=false)
//   3. buildContextMgmtMessages: filters tool calls without valid (non-truncated) results
package agent

import (
	"fmt"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertMessagesToLLM_FiltersTruncatedToolCalls verifies that ConvertMessagesToLLM
// does not include tool calls whose results have been truncated.
//
// This is the core fix for the orphaned tool call bug:
// Before the fix, the LLM received assistant messages with tool_calls that had no
// corresponding tool_result (because it was truncated). This caused:
// - Invalid API sequences (tool calls without results)
// - context_mgmt_invalid_id events when the LLM tried to reference those IDs
func TestConvertMessagesToLLM_FiltersTruncatedToolCalls(t *testing.T) {
	messages := []agentctx.AgentMessage{
		// User asks a question
		agentctx.NewUserMessage("List files"),
		// Assistant makes 3 tool calls
		newAssistantWithToolCalls([]agentctx.ToolCallContent{
			{ID: "call_aaa", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "ls"}},
			{ID: "call_bbb", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "ls -la"}},
			{ID: "call_ccc", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "find ."}},
		}),
		// Tool results for all 3
		agentctx.NewToolResultMessage("call_aaa", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "file1.txt\nfile2.txt"},
		}, false),
		agentctx.NewToolResultMessage("call_bbb", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "total 8\ndrwxr-xr-x ..."},
		}, false),
		agentctx.NewToolResultMessage("call_ccc", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Text: ".\n./file1.txt\n./file2.txt"},
		}, false),
	}

	// Before truncation: all 3 tool calls should be present
	result := ConvertMessagesToLLM(t.Context(), messages)
	assistantIdx := findAssistantIdx(t, result)
	require.Len(t, result[assistantIdx].ToolCalls, 3, "all tool calls should be present before truncation")

	// Now truncate call_bbb's result (simulating context management truncation)
	messages[3].Truncated = true
	messages[3].Content = []agentctx.ContentBlock{
		agentctx.TextContent{Text: "[truncated: 2 lines]"},
	}

	// After truncation: call_bbb should be filtered out
	result = ConvertMessagesToLLM(t.Context(), messages)
	assistantIdx = findAssistantIdx(t, result)
	require.Len(t, result[assistantIdx].ToolCalls, 2, "truncated tool call should be filtered")

	// Verify the remaining tool calls are the non-truncated ones
	remainingIDs := toolCallIDs(result[assistantIdx].ToolCalls)
	assert.Contains(t, remainingIDs, "call_aaa", "non-truncated call_aaa should remain")
	assert.Contains(t, remainingIDs, "call_ccc", "non-truncated call_ccc should remain")
	assert.NotContains(t, remainingIDs, "call_bbb", "truncated call_bbb should be filtered")
}

// TestConvertMessagesToLLM_MultipleTruncatedResults verifies filtering when
// multiple tool results are truncated (simulating the real bug scenario with 9+ truncations).
func TestConvertMessagesToLLM_MultipleTruncatedResults(t *testing.T) {
	// Build a conversation with 15 tool calls (similar to real buggy session)
	var messages []agentctx.AgentMessage

	messages = append(messages, agentctx.NewUserMessage("Do many things"))

	var toolCalls []agentctx.ToolCallContent
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("call_%02d", i)
		toolCalls = append(toolCalls, agentctx.ToolCallContent{
			ID:   id,
			Type: "toolCall",
			Name: "bash",
			Arguments: map[string]any{
				"command": fmt.Sprintf("echo %d", i),
			},
		})
	}
	messages = append(messages, newAssistantWithToolCalls(toolCalls))

	// Add tool results for all 15
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("call_%02d", i)
		messages = append(messages, agentctx.NewToolResultMessage(id, "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Text: fmt.Sprintf("output %d", i)},
		}, false))
	}

	// Before truncation
	result := ConvertMessagesToLLM(t.Context(), messages)
	assistantIdx := findAssistantIdx(t, result)
	require.Len(t, result[assistantIdx].ToolCalls, 15)

	// Truncate results 0-8 (9 truncations, like the real bug scenario)
	for i := 0; i < 9; i++ {
		messages[2+i].Truncated = true // results start at index 2
	}

	result = ConvertMessagesToLLM(t.Context(), messages)
	assistantIdx = findAssistantIdx(t, result)
	require.Len(t, result[assistantIdx].ToolCalls, 6, "9 truncated tool calls should be filtered, 6 remain")

	// Verify remaining are the non-truncated ones (9-14)
	remainingIDs := toolCallIDs(result[assistantIdx].ToolCalls)
	for i := 9; i < 15; i++ {
		id := fmt.Sprintf("call_%02d", i)
		assert.Contains(t, remainingIDs, id, "non-truncated call_%02d should remain", i)
	}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("call_%02d", i)
		assert.NotContains(t, remainingIDs, id, "truncated call_%02d should be filtered", i)
	}
}

// TestConvertMessagesToLLM_ToolResultOrderingPreserved verifies that tool results
// maintain correct ordering even when some are truncated.
func TestConvertMessagesToLLM_ToolResultOrderingPreserved(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("test"),
		newAssistantWithToolCalls([]agentctx.ToolCallContent{
			{ID: "call_1", Type: "toolCall", Name: "tool_a", Arguments: map[string]any{}},
			{ID: "call_2", Type: "toolCall", Name: "tool_b", Arguments: map[string]any{}},
		}),
		agentctx.NewToolResultMessage("call_1", "tool_a", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "result 1"},
		}, false),
		agentctx.NewToolResultMessage("call_2", "tool_b", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "result 2"},
		}, false),
	}

	// Truncate call_1's result
	messages[2].Truncated = true

	result := ConvertMessagesToLLM(t.Context(), messages)

	// call_2's result should still be present
	toolResults := filterByRole(result, "tool")
	require.Len(t, toolResults, 1, "only non-truncated tool result should appear")
	assert.Equal(t, "call_2", toolResults[0].ToolCallID)
	assert.Contains(t, toolResults[0].Content, "result 2")

	// Assistant should only have call_2's tool call
	assistantIdx := findAssistantIdx(t, result)
	require.Len(t, result[assistantIdx].ToolCalls, 1)
	assert.Equal(t, "call_2", result[assistantIdx].ToolCalls[0].ID)
}

// TestConvertMessagesToLLM_AllToolCallsTruncated verifies the edge case where
// ALL tool calls in an assistant message have truncated results.
func TestConvertMessagesToLLM_AllToolCallsTruncated(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("test"),
		newAssistantWithToolCalls([]agentctx.ToolCallContent{
			{ID: "call_1", Type: "toolCall", Name: "tool_a", Arguments: map[string]any{}},
			{ID: "call_2", Type: "toolCall", Name: "tool_b", Arguments: map[string]any{}},
		}),
		agentctx.NewToolResultMessage("call_1", "tool_a", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "result 1"},
		}, false),
		agentctx.NewToolResultMessage("call_2", "tool_b", []agentctx.ContentBlock{
			agentctx.TextContent{Text: "result 2"},
		}, false),
	}

	// Truncate ALL results
	messages[2].Truncated = true
	messages[3].Truncated = true

	result := ConvertMessagesToLLM(t.Context(), messages)

	// Assistant message should have no tool calls
	assistantIdx := findAssistantIdx(t, result)
	assert.Empty(t, result[assistantIdx].ToolCalls, "all tool calls should be filtered when all results truncated")

	// No tool results should be present (they're truncated and not sent)
	toolResults := filterByRole(result, "tool")
	assert.Empty(t, toolResults, "truncated tool results should not appear")
}

// TestHasValidToolResult verifies the hasValidToolResult helper used in
// buildContextMgmtMessages to filter orphaned tool calls.
func TestHasValidToolResult(t *testing.T) {
	agent := &AgentNew{
		snapshot: &agentctx.ContextSnapshot{
			RecentMessages: []agentctx.AgentMessage{
				agentctx.NewToolResultMessage("call_valid", "bash", []agentctx.ContentBlock{
					agentctx.TextContent{Text: "valid result"},
				}, false),
				// Truncated tool result
				func() agentctx.AgentMessage {
					m := agentctx.NewToolResultMessage("call_truncated", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Text: "[truncated]"},
					}, false)
					m.Truncated = true
					return m
				}(),
				agentctx.NewToolResultMessage("call_valid2", "bash", []agentctx.ContentBlock{
					agentctx.TextContent{Text: "another valid result"},
				}, false),
			},
		},
	}

	tests := []struct {
		toolCallID string
		expected   bool
	}{
		{"call_valid", true},
		{"call_valid2", true},
		{"call_truncated", false},
		{"call_nonexistent", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolCallID, func(t *testing.T) {
			result := agent.hasValidToolResult(tt.toolCallID)
			assert.Equal(t, tt.expected, result, "hasValidToolResult(%q)", tt.toolCallID)
		})
	}
}

// TestBuildContextMgmtMessages_FiltersOrphanedToolCalls verifies that
// buildContextMgmtMessages does not expose tool call IDs for truncated results.
// The LLM can only reference IDs it sees in "id=call_xxx" format in toolResult messages.
// Truncated results show as "(already truncated)" without their IDs.
func TestBuildContextMgmtMessages_FiltersOrphanedToolCalls(t *testing.T) {
	agent := &AgentNew{
		snapshot: &agentctx.ContextSnapshot{
			RecentMessages: []agentctx.AgentMessage{
				// User message
				agentctx.NewUserMessage("Do stuff"),
				// Assistant with 5 tool calls
				newAssistantWithToolCalls([]agentctx.ToolCallContent{
					{ID: "call_1", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "ls"}},
					{ID: "call_2", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "pwd"}},
					{ID: "call_3", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "whoami"}},
					{ID: "call_4", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "date"}},
					{ID: "call_5", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "uname"}},
				}),
				// Results: 1-3 truncated, 4-5 valid
				func() agentctx.AgentMessage {
					m := agentctx.NewToolResultMessage("call_1", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Text: "file1\nfile2\nfile3"},
					}, false)
					m.Truncated = true
					return m
				}(),
				func() agentctx.AgentMessage {
					m := agentctx.NewToolResultMessage("call_2", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Text: "/home/user"},
					}, false)
					m.Truncated = true
					return m
				}(),
				func() agentctx.AgentMessage {
					m := agentctx.NewToolResultMessage("call_3", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Text: "user"},
					}, false)
					m.Truncated = true
					return m
				}(),
				agentctx.NewToolResultMessage("call_4", "bash", []agentctx.ContentBlock{
					agentctx.TextContent{Text: "Mon Jan 1 00:00:00 UTC 2024"},
				}, false),
				agentctx.NewToolResultMessage("call_5", "bash", []agentctx.ContentBlock{
					agentctx.TextContent{Text: "Linux"},
				}, false),
			},
		},
	}

	messages := agent.buildContextMgmtMessages()

	// Collect all text from messages
	var allText string
	for _, msg := range messages {
		allText += msg.Content + "\n"
		for _, tc := range msg.ToolCalls {
			allText += tc.ID + ":" + tc.Function.Name + "\n"
		}
	}

	t.Logf("Context mgmt messages:\n%s", allText)

	// Truncated tool result IDs should NOT appear in "id=call_X" format.
	// The truncated results show as "[toolResult] (already truncated)" without ID.
	// This prevents the context management LLM from seeing and referencing these IDs.
	assert.NotContains(t, allText, "id=call_1", "truncated call_1 ID should not be exposed in context mgmt messages")
	assert.NotContains(t, allText, "id=call_2", "truncated call_2 ID should not be exposed in context mgmt messages")
	assert.NotContains(t, allText, "id=call_3", "truncated call_3 ID should not be exposed in context mgmt messages")

	// Non-truncated tool result IDs SHOULD appear in "id=call_X" format
	assert.Contains(t, allText, "id=call_4", "non-truncated call_4 ID should be visible")
	assert.Contains(t, allText, "id=call_5", "non-truncated call_5 ID should be visible")

	// Truncated results should show "(already truncated)" instead of their IDs
	assert.Contains(t, allText, "[toolResult] (already truncated)", "truncated results should show summary format")
}

// --- Helper functions ---

func newAssistantWithToolCalls(toolCalls []agentctx.ToolCallContent) agentctx.AgentMessage {
	msg := agentctx.NewAssistantMessage()
	for _, tc := range toolCalls {
		msg.Content = append(msg.Content, tc)
	}
	return msg
}

func findAssistantIdx(t *testing.T, messages []llm.LLMMessage) int {
	t.Helper()
	for i, msg := range messages {
		if msg.Role == "assistant" {
			return i
		}
	}
	t.Fatal("no assistant message found")
	return -1
}

func toolCallIDs(calls []llm.ToolCall) []string {
	ids := make([]string, len(calls))
	for i, tc := range calls {
		ids[i] = tc.ID
	}
	return ids
}

func filterByRole(messages []llm.LLMMessage, role string) []llm.LLMMessage {
	var filtered []llm.LLMMessage
	for _, msg := range messages {
		if msg.Role == role {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}
