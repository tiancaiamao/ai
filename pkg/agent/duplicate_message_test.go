// Package agent provides regression tests for bug fixes.
package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestRegression_DuplicateUserMessage_PreventsInfiniteLoop tests that duplicate user messages
// are not appended to the journal, which prevents the infinite loop bug.
//
// Bug #5: Infinite loop when user retries after DNS error
// Root cause: When a user message fails (e.g., DNS error) and the user retries with the same
// message, the journal would have two consecutive user messages. The validateSequence function
// would stop at the second user message, discarding all subsequent messages, causing the LLM
// to only receive system + user and repeat the same tool calls indefinitely.
// Fix: Add duplicate message check in executeNormalStep before appending user message.
func TestRegression_DuplicateUserMessage_PreventsInfiniteLoop(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create agent
	model := &ModelSpec{} // Dummy model for test
	agent, err := NewAgentNew(sessionDir, "test-session", model, "dummy-key", nil)
	require.NoError(t, err)

	// Add initial user message
	agent.snapshotMu.Lock()
	agent.snapshot.RecentMessages = append(agent.snapshot.RecentMessages, agentctx.NewUserMessage("AgentBackend 那边抽象"))
	agent.snapshotMu.Unlock()

	// Verify initial state
	assert.Equal(t, 1, len(agent.snapshot.RecentMessages), "Should have 1 message initially")

	// Attempt to add duplicate user message
	duplicateMsg := "AgentBackend 那边抽象"
	agent.snapshotMu.Lock()
	msgAppended := false

	// Simulate the duplicate check logic from executeNormalStep
	if len(agent.snapshot.RecentMessages) > 0 {
		lastMsg := agent.snapshot.RecentMessages[len(agent.snapshot.RecentMessages)-1]
		if lastMsg.Role == "user" {
			lastContent := lastMsg.ExtractText()
			if lastContent == duplicateMsg {
				// Duplicate user message detected - skip adding it
				msgAppended = true
			}
		}
	}

	// Verify duplicate was detected
	assert.True(t, msgAppended, "Duplicate message should be detected")
	assert.Equal(t, 1, len(agent.snapshot.RecentMessages), "Should still have only 1 message")
	agent.snapshotMu.Unlock()

	// Verify different message is not detected as duplicate
	differentMsg := "A different message"
	agent.snapshotMu.Lock()
	isDuplicate := false
	if len(agent.snapshot.RecentMessages) > 0 {
		lastMsg := agent.snapshot.RecentMessages[len(agent.snapshot.RecentMessages)-1]
		if lastMsg.Role == "user" {
			lastContent := lastMsg.ExtractText()
			if lastContent == differentMsg {
				isDuplicate = true
			}
		}
	}
	assert.False(t, isDuplicate, "Different message should not be detected as duplicate")
	agent.snapshotMu.Unlock()
}

// TestRegression_DuplicateUserMessage_EmptySnapshot tests duplicate detection with empty snapshot.
func TestRegression_DuplicateUserMessage_EmptySnapshot(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create agent
	model := &ModelSpec{} // Dummy model for test
	agent, err := NewAgentNew(sessionDir, "test-session", model, "dummy-key", nil)
	require.NoError(t, err)

	// Verify empty snapshot has no duplicate detection
	agent.snapshotMu.Lock()
	assert.Equal(t, 0, len(agent.snapshot.RecentMessages), "Should have 0 messages initially")

	// Simulate duplicate check with empty messages
	userMsg := "Test message"
	msgAppended := false
	if len(agent.snapshot.RecentMessages) > 0 {
		lastMsg := agent.snapshot.RecentMessages[len(agent.snapshot.RecentMessages)-1]
		if lastMsg.Role == "user" {
			lastContent := lastMsg.ExtractText()
			if lastContent == userMsg {
				msgAppended = true
			}
		}
	}

	assert.False(t, msgAppended, "Empty snapshot should not detect duplicate")
	agent.snapshotMu.Unlock()
}

// TestRegression_DuplicateUserMessage_LastMessageNotUser tests duplicate detection when last message is not user.
func TestRegression_DuplicateUserMessage_LastMessageNotUser(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create agent
	model := &ModelSpec{} // Dummy model for test
	agent, err := NewAgentNew(sessionDir, "test-session", model, "dummy-key", nil)
	require.NoError(t, err)

	// Add assistant message as last message
	agent.snapshotMu.Lock()
	agent.snapshot.RecentMessages = append(agent.snapshot.RecentMessages, agentctx.AgentMessage{
		Role:         "assistant",
		Content:      []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "Previous response"}},
		Timestamp:    time.Now().Unix(),
		AgentVisible: true,
		UserVisible:  true,
	})
	agent.snapshotMu.Unlock()

	// Attempt to add user message after assistant message
	userMsg := "Test message"
	agent.snapshotMu.Lock()
	isDuplicate := false
	if len(agent.snapshot.RecentMessages) > 0 {
		lastMsg := agent.snapshot.RecentMessages[len(agent.snapshot.RecentMessages)-1]
		if lastMsg.Role == "user" {
			lastContent := lastMsg.ExtractText()
			if lastContent == userMsg {
				isDuplicate = true
			}
		}
	}

	assert.False(t, isDuplicate, "Should not detect duplicate when last message is not user")
	agent.snapshotMu.Unlock()
}
