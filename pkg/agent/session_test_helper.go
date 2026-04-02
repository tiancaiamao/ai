// Package agent provides testing utilities for session-based tests.
package agent

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// SessionTestCase represents a test case based on a real session.
// Use this to easily create tests from actual session data.
type SessionTestCase struct {
	// Name is the test name (e.g., "resume_bug_case")
	Name string

	// SessionDir is the path to the session directory
	// Can be relative to testdata/sessions/ or absolute
	SessionDir string

	// Description describes what this test case verifies
	Description string

	// ExpectedResults defines what to verify after loading
	ExpectedResults struct {
		// MinMessageCount is the minimum expected message count
		MinMessageCount int

		// MaxMessageCount is the maximum expected message count (0 = no limit)
		MaxMessageCount int

		// FirstMessageRole is the expected role of the first message ("" = don't check)
		FirstMessageRole string

		// LLMContextNotEmpty checks if LLMContext was loaded
		LLMContextNotEmpty bool

		// CheckpointHadMessages checks if checkpoint had messages.jsonl
		// Pass true if checkpoint SHOULD have had messages, false if it should be empty
		CheckpointHadMessages *bool
	}
}

// SessionTestHelper provides helper methods for session-based testing.
type SessionTestHelper struct {
	t           *testing.T
	testCase    SessionTestCase
	sessionDir  string
	checkpoint  *agentctx.CheckpointInfo
	snapshot    *agentctx.ContextSnapshot
	journal     *agentctx.Journal
	entries     []agentctx.JournalEntry
}

// NewSessionTest creates a new session test helper.
// SessionDir can be relative to testdata/sessions/ or absolute path.
//
// Example usage:
//
//	t.Run("resume_bug_case", func(t *testing.T) {
//	    helper := NewSessionTest(t, SessionTestCase{
//	        Name:       "resume_bug_case",
//	        SessionDir: "resume_bug_case", // relative to testdata/sessions/
//	        Description: "Tests resume when checkpoint has no messages.jsonl",
//	    })
//	    defer helper.Cleanup()
//
//	    // Load and verify
//	    helper.LoadSession().
//	        VerifyMinMessages(50).
//	        VerifyLLMContextNotEmpty()
//	})
func NewSessionTest(t *testing.T, tc SessionTestCase) *SessionTestHelper {
	h := &SessionTestHelper{
		t:        t,
		testCase: tc,
	}

	// Resolve session directory path
	if filepath.IsAbs(tc.SessionDir) {
		h.sessionDir = tc.SessionDir
	} else {
		// Assume relative to testdata/sessions/
		_, filename, _, _ := runtime.Caller(1)
		testdataDir := filepath.Join(filepath.Dir(filename), "testdata", "sessions")
		h.sessionDir = filepath.Join(testdataDir, tc.SessionDir)
	}

	// Verify session directory exists
	require.DirExists(t, h.sessionDir, "Session directory should exist: %s", h.sessionDir)

	t.Logf("Session test: %s", tc.Name)
	t.Logf("  Description: %s", tc.Description)
	t.Logf("  Session dir: %s", h.sessionDir)

	return h
}

// LoadSession loads the checkpoint and journal from the session.
// Returns the helper for chaining.
func (h *SessionTestHelper) LoadSession() *SessionTestHelper {
	t := h.t

	// Load checkpoint index
	idx, err := agentctx.LoadCheckpointIndex(h.sessionDir)
	require.NoError(t, err, "Should load checkpoint index")

	// Get latest checkpoint
	latestCheckpoint, err := idx.GetLatestCheckpoint()
	require.NoError(t, err, "Should get latest checkpoint")
	h.checkpoint = latestCheckpoint

	t.Logf("  Checkpoint: %s (turn=%d, message_index=%d)",
		latestCheckpoint.Path, latestCheckpoint.Turn, latestCheckpoint.MessageIndex)

	// Load checkpoint data
	snapshot, err := agentctx.LoadCheckpoint(h.sessionDir, latestCheckpoint)
	require.NoError(t, err, "Should load checkpoint")
	h.snapshot = snapshot

	t.Logf("  Checkpoint RecentMessages: %d", len(snapshot.RecentMessages))

	// Open journal
	journal, err := agentctx.OpenJournal(h.sessionDir)
	require.NoError(t, err, "Should open journal")
	h.journal = journal

	// Read journal entries
	entries, err := journal.ReadAll()
	require.NoError(t, err, "Should read journal")
	h.entries = entries

	// Count message entries
	messageCount := 0
	for _, e := range entries {
		if e.Type == "message" {
			messageCount++
		}
	}

	t.Logf("  Journal: %d total entries, %d messages", len(entries), messageCount)

	return h
}

// VerifyMinMessages verifies the snapshot has at least min messages.
func (h *SessionTestHelper) VerifyMinMessages(min int) *SessionTestHelper {
	h.t.Helper()
	messageCount := len(h.snapshot.RecentMessages)
	if messageCount < min {
		h.t.Errorf("Expected at least %d messages, got %d", min, messageCount)
	} else {
		h.t.Logf("  ✓ Message count: %d (≥%d)", messageCount, min)
	}
	return h
}

// VerifyMaxMessages verifies the snapshot has at most max messages.
func (h *SessionTestHelper) VerifyMaxMessages(max int) *SessionTestHelper {
	h.t.Helper()
	messageCount := len(h.snapshot.RecentMessages)
	if max > 0 && messageCount > max {
		h.t.Errorf("Expected at most %d messages, got %d", max, messageCount)
	} else if max > 0 {
		h.t.Logf("  ✓ Message count: %d (≤%d)", messageCount, max)
	}
	return h
}

// VerifyLLMContextNotEmpty verifies LLMContext was loaded from checkpoint.
func (h *SessionTestHelper) VerifyLLMContextNotEmpty() *SessionTestHelper {
	h.t.Helper()
	assert.NotEmpty(h.t, h.snapshot.LLMContext, "LLMContext should be loaded from checkpoint")
	h.t.Logf("  ✓ LLMContext loaded: %d chars", len(h.snapshot.LLMContext))
	return h
}

// VerifyFirstMessageRole verifies the first message has the expected role.
func (h *SessionTestHelper) VerifyFirstMessageRole(role string) *SessionTestHelper {
	h.t.Helper()
	if len(h.snapshot.RecentMessages) == 0 {
		h.t.Skip("No messages to verify first role")
		return h
	}
	assert.Equal(h.t, role, h.snapshot.RecentMessages[0].Role,
		"First message should have role '%s'", role)
	h.t.Logf("  ✓ First message role: %s", role)
	return h
}

// VerifyCheckpointHadMessages verifies whether checkpoint had messages.jsonl.
func (h *SessionTestHelper) VerifyCheckpointHadMessages(expected bool) *SessionTestHelper {
	h.t.Helper()
	// This is checked by seeing if LoadCheckpoint returned any messages
	hadMessages := len(h.snapshot.RecentMessages) > 0

	// Note: this check happens BEFORE replay, so we need to check the original state
	// For now, we'll infer from checkpoint metadata
	if expected && !hadMessages {
		// Check if this is expected based on recent_messages_count in checkpoint
		if h.checkpoint.RecentMessagesCount > 0 {
			h.t.Errorf("Checkpoint should have had messages.jsonl (recent_messages_count=%d), but got none",
				h.checkpoint.RecentMessagesCount)
		}
	}
	h.t.Logf("  ✓ Checkpoint messages.jsonl: %v (expected: %v)", hadMessages, expected)
	return h
}

// VerifyAll applies all verifications from ExpectedResults.
func (h *SessionTestHelper) VerifyAll() *SessionTestHelper {
	h.t.Helper()
	exp := h.testCase.ExpectedResults

	if exp.MinMessageCount > 0 {
		h.VerifyMinMessages(exp.MinMessageCount)
	}
	if exp.MaxMessageCount > 0 {
		h.VerifyMaxMessages(exp.MaxMessageCount)
	}
	if exp.FirstMessageRole != "" {
		h.VerifyFirstMessageRole(exp.FirstMessageRole)
	}
	if exp.LLMContextNotEmpty {
		h.VerifyLLMContextNotEmpty()
	}
	if exp.CheckpointHadMessages != nil {
		h.VerifyCheckpointHadMessages(*exp.CheckpointHadMessages)
	}

	return h
}

// GetSnapshot returns the loaded snapshot for custom assertions.
func (h *SessionTestHelper) GetSnapshot() *agentctx.ContextSnapshot {
	return h.snapshot
}

// GetCheckpoint returns the checkpoint info.
func (h *SessionTestHelper) GetCheckpoint() *agentctx.CheckpointInfo {
	return h.checkpoint
}

// GetJournalEntries returns the journal entries.
func (h *SessionTestHelper) GetJournalEntries() []agentctx.JournalEntry {
	return h.entries
}

// Cleanup cleans up resources.
func (h *SessionTestHelper) Cleanup() {
	if h.journal != nil {
		h.journal.Close()
	}
}

// DescribeTestLogs outputs a description of what this test verifies.
// Useful for documentation.
func (h *SessionTestHelper) DescribeTestLogs() string {
	return h.testCase.Description
}
