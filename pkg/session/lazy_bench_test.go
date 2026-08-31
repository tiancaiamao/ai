package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkLoadSession compares lazy vs full loading performance
func BenchmarkLoadSession_Lazy(b *testing.B) {
	tmpDir := b.TempDir()
	sessionDir := filepath.Join(tmpDir, "large-session")

	// Create a large session file with 500 messages
	createLargeSessionFile(sessionDir, 500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(sessionDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Full(b *testing.B) {
	tmpDir := b.TempDir()
	sessionDir := filepath.Join(tmpDir, "large-session")

	// Create a large session file with 500 messages
	createLargeSessionFile(sessionDir, 500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(sessionDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Lazy_1000(b *testing.B) {
	tmpDir := b.TempDir()
	sessionDir := filepath.Join(tmpDir, "large-session")

	// Create a large session file with 1000 messages
	createLargeSessionFile(sessionDir, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(sessionDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadSession_Full_1000(b *testing.B) {
	tmpDir := b.TempDir()
	sessionDir := filepath.Join(tmpDir, "large-session")

	// Create a large session file with 1000 messages
	createLargeSessionFile(sessionDir, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadSession(sessionDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// createLargeSessionFile creates a session file with n messages
func createLargeSessionFile(sessionDir string, n int) {
	os.MkdirAll(sessionDir, 0755)

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		persist:    false,
	}
	sess.header = newSessionHeader("benchmark-session", "/test", "")

	for i := 0; i < n; i++ {
		msg := agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: "This is a longer message content to simulate realistic token usage. It contains enough text to represent a typical coding question or response.",
				},
			},
		}
		sess.AddMessages(msg)
	}

	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data := serializeSessionForTest(sess)
	os.WriteFile(filePath, data, 0644)
}
