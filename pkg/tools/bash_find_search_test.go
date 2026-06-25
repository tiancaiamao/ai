package tools

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestIsBroadFilesystemSearch(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Blocked: filesystem root
		{"find root basic", "find /", true},
		{"find root with flag", "find / -name '*.go'", true},
		{"find root with type", "find / -type f -name config.yaml", true},
		{"find root compound &&", "cd /tmp && find /", true},
		{"find root compound ;", "echo hi; find /", true},
		{"find root multiline", "echo hi\nfind /", true},
		{"find root pipe after", "find / | head", true},

		// Blocked: glob variants of root
		{"find root glob", "find /*", true},
		{"find root glob with flag", "find /* -name '*.go'", true},
		{"find root glob compound", "echo hi && find /*", true},

		// Blocked: double-dash separator
		{"find dashdash root", "find -- /", true},
		{"find dashdash root flag", "find -- / -name x", true},

		// Blocked: home via tilde
		{"find home tilde basic", "find ~", true},
		{"find home tilde with flag", "find ~ -name '*.go'", true},
		{"find home tilde compound", "echo hi && find ~ -type f", true},

		// Blocked: glob variants of home
		{"find home tilde glob", "find ~/*", true},
		{"find home tilde glob with flag", "find ~/* -name '*.go'", true},

		// Blocked: home via $HOME
		{"find $HOME basic", "find $HOME", true},
		{"find $HOME with flag", "find $HOME -name '*.go'", true},

		// Allowed: specific subdirectories
		{"find /tmp", "find /tmp -name '*.go'", false},
		{"find /usr/local", "find /usr/local/bin -type f", false},
		{"find ~/project", "find ~/project -name '*.go'", false},
		{"find current dir", "find . -name '*.go'", false},
		{"find relative", "find src -name '*.go'", false},
		{"find with path then root flag", "find /home/user/project -name x", false},

		// Allowed: not a find command at all
		{"grep find", "grep -r 'find /' file.txt", false},
		{"echo", "echo hello", false},
		{"ls", "ls -la", false},

		// Allowed: find with no path (uses current dir)
		{"find no path", "find -name '*.go'", false},
		{"find name only", "find -name x", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBroadFilesystemSearch(tt.command)
			assert.Equal(t, tt.blocked, result, "command: %q", tt.command)
		})
	}
}

func TestBashToolBlocksBroadFilesystemSearch(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blockedCmds := []string{
		"find /",
		"find ~",
		"find $HOME",
		"find / -name '*.go'",
		"echo hi && find /",
	}

	for _, cmd := range blockedCmds {
		t.Run(cmd, func(t *testing.T) {
			blocks, err := tool.Execute(ctx, map[string]any{"command": cmd})
			assert.NoError(t, err)
			assert.NotEmpty(t, blocks)
			result := blocks[0].(agentctx.TextContent)
			assert.Contains(t, result.Text, "⛔ Blocked")
		})
	}
}

func TestBashToolAllowsScopedFind(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// These should execute normally (not blocked)
	allowedCmds := []string{
		"find /tmp -maxdepth 1 -name '*.txt' 2>/dev/null | head -1 || true",
		"find . -maxdepth 1 -name '*.go' 2>/dev/null | head -1 || true",
	}

	for _, cmd := range allowedCmds {
		t.Run(cmd, func(t *testing.T) {
			blocks, err := tool.Execute(ctx, map[string]any{"command": cmd, "timeout": float64(3)})
			assert.NoError(t, err)
			assert.NotEmpty(t, blocks)
			result := blocks[0].(agentctx.TextContent)
			assert.NotContains(t, result.Text, "⛔ Blocked")
		})
	}
}
