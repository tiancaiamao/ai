package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// Session represents a conversation session.
type Session struct {
	mu       sync.Mutex
	messages []agent.AgentMessage
	filePath string
}

// NewSession creates a new session with the given file path.
func NewSession(filePath string) *Session {
	return &Session{
		messages: make([]agent.AgentMessage, 0),
		filePath: filePath,
	}
}

// LoadSession loads a session from the given file path.
func LoadSession(filePath string) (*Session, error) {
	sess := &Session{
		messages: make([]agent.AgentMessage, 0),
		filePath: filePath,
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// File doesn't exist, return empty session
		return sess, nil
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Parse JSONL (one JSON object per line)
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		var msg agent.AgentMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Log error but continue
			fmt.Fprintf(os.Stderr, "[Session] Failed to parse line: %v\n", err)
			continue
		}

		sess.messages = append(sess.messages, msg)
	}

	fmt.Fprintf(os.Stderr, "[Session] Loaded %d messages from %s\n", len(sess.messages), filePath)

	return sess, nil
}

// GetMessages returns all messages in the session.
func (s *Session) GetMessages() []agent.AgentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messages
}

// GetPath returns the file path of the session.
func (s *Session) GetPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filePath
}

// SaveMessages replaces all messages and saves to disk.
func (s *Session) SaveMessages(messages []agent.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = messages
	return s.save()
}

// AddMessages adds new messages to the session.
func (s *Session) AddMessages(messages ...agent.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, messages...)

	// Save to file
	if err := s.save(); err != nil {
		// Rollback
		s.messages = s.messages[:len(s.messages)-len(messages)]
		return err
	}

	return nil
}

// Clear clears all messages from the session.
func (s *Session) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]agent.AgentMessage, 0)

	// Delete the file
	if _, err := os.Stat(s.filePath); err == nil {
		if err := os.Remove(s.filePath); err != nil {
			return err
		}
	}

	return nil
}

// Save saves the current session to file.
func (s *Session) save() error {
	// Ensure directory exists
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Create temp file
	tmpPath := s.filePath + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write messages as JSONL
	encoder := json.NewEncoder(file)
	for _, msg := range s.messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, s.filePath); err != nil {
		return err
	}

	return nil
}

// GetDefaultSessionPath returns the default session file path.
func GetDefaultSessionPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".ai", "session.jsonl"), nil
}

// splitLines splits data into lines.
func splitLines(data []byte) [][]byte {
	lines := make([][]byte, 0)
	start := 0

	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}

	// Add last line if not empty
	if start < len(data) {
		lines = append(lines, data[start:])
	}

	return lines
}
