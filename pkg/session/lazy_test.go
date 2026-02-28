package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"encoding/json"
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

func TestLoadSessionLazyWithResumeOffset(t *testing.T) {
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
	for i := 0; i < 30; i++ {
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

	// Get file size
	stat, _ := os.Stat(filePath)
	fileSize := stat.Size()

	// Update header with ResumeOffset (pointing to middle of file)
	sess.header.ResumeOffset = fileSize / 2

	// Re-write with updated header
	data = serializeSessionForTest(sess)
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	// Test lazy loading - should use ResumeOffset
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should have loaded something (less than full)
	msgCount := len(loaded.GetMessages())
	assert.Less(t, msgCount, 30)
}

func TestLoadSessionLazyBackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "legacy-session")
	err := os.MkdirAll(sessionDir, 0755)
	require.NoError(t, err)

	// Create a session file without ResumeOffset
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

	// Test lazy loading - should work without ResumeOffset
	loaded, err := LoadSessionLazy(sessionDir, DefaultLoadOptions())
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "legacy-session", loaded.header.ID)
}

// TestLoadSessionLazyMessageChain tests that message chain is properly linked
// when lazy loading truncates the history (fix for /resume history not loading)
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

	// Test lazy loading with maxMessages = 10 to force truncation
	loaded, err := LoadSessionLazy(sessionDir, LoadOptions{
		MaxMessages:    10,
		IncludeSummary: false,
		Lazy:          true,
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
		Lazy:          true,
	})
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Should return: compaction summary + 10 recent messages = 11 messages
	messages := loaded.GetMessages()
	assert.Len(t, messages, 11, "Should return compaction summary + 10 recent messages")
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
	if s.header.ResumeOffset > 0 {
		headerLine["resumeOffset"] = s.header.ResumeOffset
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