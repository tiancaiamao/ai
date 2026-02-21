package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiancaiamao/ai/pkg/agent"
)

func TestSessionManager(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "ai-session-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sm := NewSessionManager(tempDir)

	var sessionIDs []string

	t.Run("CreateSession", func(t *testing.T) {
		sess, err := sm.CreateSession("test-session", "Test Session")
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if sess == nil {
			t.Fatal("Session is nil")
		}

		// Extract session ID from path
		sessPath := sess.GetPath()
		sessionID := filepath.Base(sessPath)
		sessionID = sessionID[:len(sessionID)-6] // Remove .jsonl extension
		sessionIDs = append(sessionIDs, sessionID)

		// Verify metadata file exists
		metaPath := sm.getMetaPath(sessionID)
		if _, err := os.Stat(metaPath); os.IsNotExist(err) {
			t.Errorf("Metadata file not created: %s", metaPath)
		}

		// Verify metadata content
		meta, err := sm.GetMeta(sessionID)
		if err != nil {
			t.Fatalf("Failed to get meta: %v", err)
		}

		if meta.Name != "test-session" {
			t.Errorf("Expected name 'test-session', got '%s'", meta.Name)
		}

		if meta.Title != "Test Session" {
			t.Errorf("Expected title 'Test Session', got '%s'", meta.Title)
		}
	})

	t.Run("ListSessions", func(t *testing.T) {
		// Create multiple sessions
		sess1, _ := sm.CreateSession("session-1", "Session 1")
		sess2, _ := sm.CreateSession("session-2", "Session 2")

		// Extract IDs
		sess1Path := sess1.GetPath()
		id1 := filepath.Base(sess1Path)
		id1 = id1[:len(id1)-6]
		sessionIDs = append(sessionIDs, id1)

		sess2Path := sess2.GetPath()
		id2 := filepath.Base(sess2Path)
		id2 = id2[:len(id2)-6]
		sessionIDs = append(sessionIDs, id2)

		sessions, err := sm.ListSessions()
		if err != nil {
			t.Fatalf("Failed to list sessions: %v", err)
		}

		if len(sessions) < 3 {
			t.Errorf("Expected at least 3 sessions, got %d", len(sessions))
		}
	})

	t.Run("SetCurrent and LoadCurrent", func(t *testing.T) {
		// Use the first session ID from sessionIDs
		if len(sessionIDs) < 1 {
			t.Skip("No session IDs available")
		}

		targetID := sessionIDs[1] // session-1

		// Set current session
		err := sm.SetCurrent(targetID)
		if err != nil {
			t.Fatalf("Failed to set current session: %v", err)
		}

		// Save current
		err = sm.SaveCurrent()
		if err != nil {
			t.Fatalf("Failed to save current: %v", err)
		}

		// Create new manager, set in-memory current, then load current
		sm2 := NewSessionManager(tempDir)
		if err := sm2.SetCurrent(targetID); err != nil {
			t.Fatalf("Failed to set current session on second manager: %v", err)
		}
		sess, sessionID, err := sm2.LoadCurrent()
		if err != nil {
			t.Fatalf("Failed to load current: %v", err)
		}

		if sessionID != targetID {
			t.Errorf("Expected session ID '%s', got '%s'", targetID, sessionID)
		}

		if sess == nil {
			t.Fatal("Session is nil")
		}
	})

	t.Run("GetMeta", func(t *testing.T) {
		// Use the second session ID from sessionIDs
		if len(sessionIDs) < 2 {
			t.Skip("Not enough session IDs available")
		}

		targetID := sessionIDs[1] // session-1

		meta, err := sm.GetMeta(targetID)
		if err != nil {
			t.Fatalf("Failed to get meta: %v", err)
		}

		if meta.Name != "session-1" {
			t.Errorf("Expected name 'session-1', got '%s'", meta.Name)
		}

		if meta.Title != "Session 1" {
			t.Errorf("Expected title 'Session 1', got '%s'", meta.Title)
		}
	})

	t.Run("DeleteSession", func(t *testing.T) {
		// Create a session to delete
		sess, _ := sm.CreateSession("to-delete", "To Delete")

		// Extract ID
		sessPath := sess.GetPath()
		deleteID := filepath.Base(sessPath)
		deleteID = deleteID[:len(deleteID)-6]

		// Set current to something else
		if len(sessionIDs) > 0 {
			sm.SetCurrent(sessionIDs[0])
		}

		// Delete session
		err := sm.DeleteSession(deleteID)
		if err != nil {
			t.Fatalf("Failed to delete session: %v", err)
		}

		// Verify session file is deleted (LoadSession returns empty session)
		deletedSess, err := sm.GetSession(deleteID)
		if err != nil {
			// Error is acceptable
		}
		if deletedSess != nil && len(deletedSess.GetMessages()) != 0 {
			t.Error("Deleted session should be empty")
		}

		// Verify metadata is deleted
		metaPath := sm.getMetaPath(deleteID)
		if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
			t.Error("Metadata file should be deleted")
		}

		// Verify session file is deleted
		sessFilePath := sm.getSessionPath(deleteID)
		if _, err := os.Stat(sessFilePath); !os.IsNotExist(err) {
			t.Error("Session file should be deleted")
		}
	})

	t.Run("CannotDeleteCurrentSession", func(t *testing.T) {
		if len(sessionIDs) < 1 {
			t.Skip("No session IDs available")
		}

		sm.SetCurrent(sessionIDs[0])

		err := sm.DeleteSession(sessionIDs[0])
		if err == nil {
			t.Error("Expected error when deleting current session")
		}
	})
}

func TestSessionManagerCreateDefaultSession(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "ai-session-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sm := NewSessionManager(tempDir)

	// Load current when no sessions exist (should create default)
	sess, sessionID, err := sm.LoadCurrent()
	if err != nil {
		t.Fatalf("Failed to load current: %v", err)
	}

	if sess == nil {
		t.Fatal("Session is nil")
	}

	if sessionID == "" {
		t.Error("Session ID should not be empty")
	}

	// Verify the session is set as current
	currentID := sm.GetCurrentID()
	if currentID != sessionID {
		t.Errorf("Expected current ID '%s', got '%s'", sessionID, currentID)
	}

	// No persisted pointer file. Current session is process-local state.
}

func TestCreateMetaFromSession(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "ai-session-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a session file manually
	sessID := uuid.New().String()
	sessPath := filepath.Join(tempDir, sessID+".jsonl")
	sess := NewSession(sessPath)

	// Add some messages
	sess.AddMessages(
		agent.AgentMessage{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "Hello"}}},
		agent.AgentMessage{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "Hi"}}},
	)

	// Create session manager and test createMetaFromSession
	sm := NewSessionManager(tempDir)
	meta, err := sm.createMetaFromSession(sessPath)
	if err != nil {
		t.Fatalf("Failed to create meta from session: %v", err)
	}

	if meta.ID != sessID {
		t.Errorf("Expected ID '%s', got '%s'", sessID, meta.ID)
	}

	if meta.MessageCount != 2 {
		t.Errorf("Expected 2 messages, got %d", meta.MessageCount)
	}

	if meta.Title != "Session" {
		t.Errorf("Expected title 'Session', got '%s'", meta.Title)
	}
}

func TestSessionManagerSaveCurrentUpdatesMeta(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "ai-session-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sm := NewSessionManager(tempDir)

	// Create a session
	sess, err := sm.CreateSession("test", "Test")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Extract session ID
	sessPath := sess.GetPath()
	sessionID := filepath.Base(sessPath)
	sessionID = sessionID[:len(sessionID)-6] // Remove .jsonl extension

	// Set as current
	sm.SetCurrent(sessionID)

	// Add some messages
	sess.AddMessages(
		agent.AgentMessage{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "Hello"}}},
	)

	// Save current (should update metadata)
	err = sm.SaveCurrent()
	if err != nil {
		t.Fatalf("Failed to save current: %v", err)
	}

	// Load metadata
	meta, err := sm.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("Failed to get meta: %v", err)
	}

	if meta.MessageCount != 1 {
		t.Errorf("Expected 1 message, got %d", meta.MessageCount)
	}

	// Check UpdatedAt is recent
	if time.Since(meta.UpdatedAt) > time.Second {
		t.Error("UpdatedAt should be recent")
	}
}
