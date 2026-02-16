package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// BenchmarkLoadSession compares lazy vs full loading performance
func BenchmarkLoadSession_Lazy(b *testing.B) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "large-session.jsonl")

	// Create a large session file with 500 messages
	createLargeSessionFile(filePath, 500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSessionLazy(filePath, DefaultLoadOptions())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Full(b *testing.B) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "large-session.jsonl")

	// Create a large session file with 500 messages
	createLargeSessionFile(filePath, 500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(filePath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Lazy_1000(b *testing.B) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "large-session.jsonl")

	// Create a large session file with 1000 messages
	createLargeSessionFile(filePath, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSessionLazy(filePath, DefaultLoadOptions())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Full_1000(b *testing.B) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "large-session.jsonl")

	// Create a large session file with 1000 messages
	createLargeSessionFile(filePath, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(filePath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// createLargeSessionFile creates a session file with n messages
func createLargeSessionFile(filePath string, n int) {
	sess := &Session{
		filePath: filePath,
		entries:  make([]*SessionEntry, 0),
		byID:     make(map[string]*SessionEntry),
		persist:  false,
	}
	sess.header = newSessionHeader("benchmark-session", "/test", "")

	for i := 0; i < n; i++ {
		msg := agent.AgentMessage{
			Role: "user",
			Content: []agent.ContentBlock{
				agent.TextContent{
					Type: "text",
					Text: "This is a longer message content to simulate realistic token usage. It contains enough text to represent a typical coding question or response.",
				},
			},
		}
		sess.AddMessages(msg)
	}

	data := serializeSessionForTest(sess)
	os.WriteFile(filePath, data, 0644)
}

func serializeSessionForBenchmark(s *Session) []byte {
	var data []byte

	headerLine := map[string]interface{}{
		"type":      EntryTypeSession,
		"version":   s.header.Version,
		"id":        s.header.ID,
		"timestamp": s.header.Timestamp,
		"cwd":       s.header.Cwd,
	}
	headerBytes, _ := json.Marshal(headerLine)
	data = append(data, headerBytes...)
	data = append(data, '\n')

	for _, entry := range s.entries {
		entryBytes, _ := json.Marshal(entry)
		data = append(data, entryBytes...)
		data = append(data, '\n')
	}

	return data
}