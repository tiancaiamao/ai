package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// TestSaveMessages tests saving messages to session.
func TestSaveMessages(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test_session")
	sessionPath := filepath.Join(sessionDir, "messages.jsonl")

	sess := NewSession(sessionDir)

	// Create test messages
	messages := []agent.AgentMessage{
		agent.NewUserMessage("Hello"),
		agent.NewAssistantMessage(),
	}

	// Save messages
	err := sess.SaveMessages(messages)
	if err != nil {
		t.Fatalf("Failed to save messages: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("Session file was not created")
	}

	// Load and verify
	loadedSess, err := LoadSession(sessionDir)
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}

	loadedMessages := loadedSess.GetMessages()
	if len(loadedMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(loadedMessages))
	}
}

// TestLoadEmptySession tests loading a non-existent session.
func TestLoadEmptySession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "non_existent")

	sess, err := LoadSession(sessionDir)
	if err != nil {
		t.Fatalf("Failed to load non-existent session: %v", err)
	}

	if sess == nil {
		t.Fatal("Session should not be nil")
	}

	if len(sess.GetMessages()) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(sess.GetMessages()))
	}
}

// TestAddMessages tests adding messages to session.
func TestAddMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test_add")
	sessionPath := filepath.Join(sessionDir, "messages.jsonl")

	sess := NewSession(sessionDir)

	// Add messages
	messages := []agent.AgentMessage{
		agent.NewUserMessage("First message"),
	}

	err := sess.AddMessages(messages...)
	if err != nil {
		t.Fatalf("Failed to add messages: %v", err)
	}

	// Verify messages were added
	savedMessages := sess.GetMessages()
	if len(savedMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(savedMessages))
	}

	// Verify file was created
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("Session file was not created")
	}
}

// TestClearSession tests clearing a session.
func TestClearSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test_clear")
	sessionPath := filepath.Join(sessionDir, "messages.jsonl")

	sess := NewSession(sessionDir)

	// Add some messages first
	messages := []agent.AgentMessage{
		agent.NewUserMessage("Test"),
	}
	_ = sess.AddMessages(messages...)

	// Clear session
	err := sess.Clear()
	if err != nil {
		t.Fatalf("Failed to clear session: %v", err)
	}

	// Verify messages are cleared
	if len(sess.GetMessages()) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(sess.GetMessages()))
	}

	// Verify file was deleted
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatal("Session file should be deleted after clear")
	}
}

// TestSaveMessagesOverwrite tests that SaveMessages overwrites existing data.
func TestSaveMessagesOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test_overwrite")

	sess := NewSession(sessionDir)

	// Save initial messages
	initialMessages := []agent.AgentMessage{
		agent.NewUserMessage("Initial"),
	}
	err := sess.SaveMessages(initialMessages)
	if err != nil {
		t.Fatalf("Failed to save initial messages: %v", err)
	}

	// Save different messages (should overwrite)
	newMessages := []agent.AgentMessage{
		agent.NewUserMessage("New 1"),
		agent.NewUserMessage("New 2"),
		agent.NewAssistantMessage(),
	}
	err = sess.SaveMessages(newMessages)
	if err != nil {
		t.Fatalf("Failed to save new messages: %v", err)
	}

	// Verify only new messages exist
	loadedMessages := sess.GetMessages()
	if len(loadedMessages) != 3 {
		t.Errorf("Expected 3 messages after overwrite, got %d", len(loadedMessages))
	}

	// Verify first message is "New 1"
	if loadedMessages[0].ExtractText() != "New 1" {
		t.Errorf("Expected first message to be 'New 1', got '%s'", loadedMessages[0].ExtractText())
	}
}

// TestSessionPersistence tests session persistence across loads.
func TestSessionPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test_persist")

	// Create and save session
	sess1 := NewSession(sessionDir)
	messages := []agent.AgentMessage{
		agent.NewUserMessage("Message 1"),
		agent.NewAssistantMessage(),
		agent.NewUserMessage("Message 2"),
	}
	err := sess1.SaveMessages(messages)
	if err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// Load session in a new instance
	sess2, err := LoadSession(sessionDir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	// Verify messages match
	loadedMessages := sess2.GetMessages()
	if len(loadedMessages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(loadedMessages))
	}

	// Verify message contents
	if loadedMessages[0].ExtractText() != "Message 1" {
		t.Errorf("Expected 'Message 1', got '%s'", loadedMessages[0].ExtractText())
	}

	if loadedMessages[2].ExtractText() != "Message 2" {
		t.Errorf("Expected 'Message 2', got '%s'", loadedMessages[2].ExtractText())
	}
}

// TestGetDefaultSessionPath tests getting default session path.
func TestGetDefaultSessionPath(t *testing.T) {
	cwd := filepath.Join(os.TempDir(), "ai-session-test", "project")
	path, err := GetDefaultSessionPath(cwd)
	if err != nil {
		t.Fatalf("Failed to get default session path: %v", err)
	}

	if path == "" {
		t.Error("Path should not be empty")
	}

	// Should contain .ai/sessions
	expected := filepath.Join(".ai", "sessions")
	if !strings.Contains(path, expected) {
		t.Errorf("Expected path to contain %s, got %s", expected, path)
	}
}
