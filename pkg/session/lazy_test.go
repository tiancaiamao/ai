package session

import (
	"encoding/json"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLoadOptions(t *testing.T) {
	opts := DefaultLoadOptions()
	assert.Equal(t, 0, opts.MaxMessages)
	assert.True(t, opts.IncludeSummary)
	assert.True(t, opts.Lazy)
}

func TestFullLoadOptions(t *testing.T) {
	opts := FullLoadOptions()
	assert.Equal(t, -1, opts.MaxMessages)
	assert.True(t, opts.IncludeSummary)
	assert.False(t, opts.Lazy)
}

func TestLoadSessionLazyEmptyPath(t *testing.T) {
	opts := DefaultLoadOptions()
	sess, err := LoadSessionLazy("", opts)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotEmpty(t, sess.header.ID)
}

func TestLoadSessionLazyNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "nonexistent")

	opts := DefaultLoadOptions()
	sess, err := LoadSessionLazy(sessionDir, opts)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotEmpty(t, sess.header.ID)
	assert.True(t, sess.persist)
}

func TestLoadSessionLazyEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "empty")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	err = os.WriteFile(filePath, []byte{}, 0644)
	require.NoError(t, err)

	opts := DefaultLoadOptions()
	sess, err := LoadSessionLazy(sessionDir, opts)
	require.NoError(t, err)
	require.NotNil(t, sess)
}

func TestLoadSessionLazyWithMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file with header and messages
	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add 60 messages
	for i := 0; i < 60; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "message content"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file using actual session format
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading with default options (should load all messages)
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should have loaded all messages (no limit by default)
	msgCount := len(loaded.GetMessages())
	assert.Equal(t, 60, msgCount, "Should load all messages when MaxMessages=0")
}

func TestLoadSessionLazyWithCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file with header, compaction, and messages
	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add some messages
	for i := 0; i < 30; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Add compaction entry using the existing structure
	compaction := &SessionEntry{
		Type:      EntryTypeCompaction,
		ID:        "compaction-1",
		Timestamp: "2024-01-01T00:00:00Z",
		Summary:   "Previous conversation summary",
	}
	sess.addEntry(compaction)

	// Add more recent messages
	for i := 0; i < 20; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "recent message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should have compaction + recent messages
	hasCompaction := false
	for _, entry := range loaded.entries {
		if entry.Type == EntryTypeCompaction {
			hasCompaction = true
			assert.Equal(t, "Previous conversation summary", entry.Summary)
			break
		}
	}
	assert.True(t, hasCompaction, "Should have loaded compaction entry")
}

func TestLoadSessionLazyFullLoad(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file
	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add messages
	for i := 0; i < 100; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test full loading (non-lazy)
	loaded, err := LoadSessionLazy(sessionDir, FullLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should have all 100 messages
	msgCount := len(loaded.GetMessages())
	assert.Equal(t, 100, msgCount)
}

// TestLoadSessionLazyWithoutCompaction tests lazy loading when there's no compaction.
func TestLoadSessionLazyWithoutCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "legacy-session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file
	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("legacy-session", "/test", "")

	// Add messages
	for i := 0; i < 20; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading - should work by scanning from end
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "legacy-session", loaded.header.ID)
}

// TestLoadSessionLazyMessageChain tests that message chain is properly linked
// when lazy loading loads all messages (no compaction case)
func TestLoadSessionLazyMessageChain(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file with many messages (> 50)
	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add 100 messages
	for i := 0; i < 100; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading with maxMessages = 10
	// Since there's no compaction, this should return the last 10 messages
	loaded, err := LoadSessionLazy(sessionDir, LoadOptions{
		MaxMessages:    10,
		IncludeSummary: false,
		Lazy:           true,
	})
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// GetMessages should return exactly 10 messages (not 0 or 1)
	// This tests that the message chain is properly linked
	messages := loaded.GetMessages()
	assert.Len(t, messages, 10, "Should return exactly 10 messages with proper chain linking")
}

// TestLoadSessionLazyMessageChainWithCompaction tests message chain with compaction
func TestLoadSessionLazyMessageChainWithCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add 30 old messages
	for i := 0; i < 30; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Add compaction entry
	compaction := &SessionEntry{
		Type:      EntryTypeCompaction,
		ID:        "compaction-1",
		Timestamp: "2024-01-01T00:00:00Z",
		Summary:   "Previous conversation summary",
	}
	sess.addEntry(compaction)

	// Add 30 recent messages
	for i := 0; i < 30; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "recent message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading with maxMessages = 10
	loaded, err := LoadSessionLazy(sessionDir, LoadOptions{
		MaxMessages:    10,
		IncludeSummary: true,
		Lazy:           true,
	})
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should return: compaction summary + 10 recent messages = 11 messages
	messages := loaded.GetMessages()
	assert.Len(t, messages, 11, "Should return compaction summary + 10 recent messages")
}

// TestRewindPreCompactionEntry tests that rewind works for pre-compaction entries
// after lazy loading via EnsureFullyLoaded.
func TestRewindPreCompactionEntry(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add 10 old messages (pre-compaction)
	for i := 0; i < 10; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Record the ID of the 5th entry (a pre-compaction entry)
	preCompactionID := sess.entries[4].ID
	require.NotEmpty(t, preCompactionID)

	// Add compaction entry
	compaction := &SessionEntry{
		Type:      EntryTypeCompaction,
		ID:        "compaction-1",
		Timestamp: "2024-01-01T00:00:00Z",
		Summary:   "Previous conversation summary",
	}
	sess.addEntry(compaction)

	// Add 10 recent messages (post-compaction)
	for i := 0; i < 10; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "recent message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Load lazily — pre-compaction entries should NOT be in byID
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Confirm the pre-compaction entry is NOT directly accessible via GetEntry
	// (this is the bug: lazy load doesn't include it)
	_, found := loaded.GetEntry(preCompactionID)
	assert.False(t, found, "Pre-compaction entry should not be in byID after lazy load")

	// Now call EnsureFullyLoaded (what the rewind handler will do)
	err = loaded.EnsureFullyLoaded()
	require.NoError(t, err)

	// After full load, the pre-compaction entry should be accessible
	entry, found := loaded.GetEntry(preCompactionID)
	require.True(t, found, "Pre-compaction entry should be found after EnsureFullyLoaded")
	require.NotNil(t, entry)
	assert.Equal(t, preCompactionID, entry.ID)

	// Branch (rewind) to the pre-compaction entry should succeed
	err = loaded.Branch(preCompactionID)
	require.NoError(t, err, "Branch to pre-compaction entry should succeed")
}

// TestRewindPostCompactionEntry tests that rewind still works for post-compaction entries.
func TestRewindPostCompactionEntry(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add old messages
	for i := 0; i < 10; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Add compaction entry
	compaction := &SessionEntry{
		Type:      EntryTypeCompaction,
		ID:        "compaction-1",
		Timestamp: "2024-01-01T00:00:00Z",
		Summary:   "Previous conversation summary",
	}
	sess.addEntry(compaction)

	// Add recent messages
	for i := 0; i < 10; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "recent message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Record the ID of the 5th post-compaction entry
	postCompactionID := sess.entries[12].ID
	require.NotEmpty(t, postCompactionID)

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Load lazily
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)

	// EnsureFullyLoaded + Branch should work for post-compaction too
	err = loaded.EnsureFullyLoaded()
	require.NoError(t, err)
	err = loaded.Branch(postCompactionID)
	require.NoError(t, err, "Branch to post-compaction entry should succeed")
}

// TestRewindRoot tests that rewind "root" (ResetLeaf) still works.
func TestRewindRoot(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    true,
	}
	sess.header = newSessionHeader("test-session", "/test", "")

	// Add some messages
	for i := 0; i < 5; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "message"},
			},
		}
		err := sess.AddMessages(msg)
		require.NoError(t, err)
	}

	// Persist to file
	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Load lazily
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)

	// ResetLeaf should work (this is what "root" rewind does)
	loaded.ResetLeaf()
	assert.Nil(t, loaded.leafID)
}

// Helper to serialize session to bytes (matches actual file format)
func serializeSessionForTest(s *Session) []byte {
	var data []byte

	// Write header line (type: session + header fields)
	headerLine := map[string]interface{}{
		"type":      EntryTypeSession,
		"version":   s.header.Version,
		"id":        s.header.ID,
		"timestamp": s.header.Timestamp,
		"cwd":       s.header.Cwd,
	}
	if s.header.LastCompactionID != "" {
		headerLine["lastCompactionId"] = s.header.LastCompactionID
	}
	headerBytes, _ := json.Marshal(headerLine)
	data = append(data, headerBytes...)
	data = append(data, '\n')

	// Write entries
	for _, entry := range s.entries {
		entryBytes, _ := json.Marshal(entry)
		data = append(data, entryBytes...)
		data = append(data, '\n')
	}

	return data
}
