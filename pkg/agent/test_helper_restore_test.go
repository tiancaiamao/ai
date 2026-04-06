package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// restoreConfig holds configuration for RestoreAgent.
type restoreConfig struct {
	turn         int
	mutateState  func(*agentctx.ContextSnapshot)
	script       *ScriptedLLM
	mockTools    []agentctx.Tool
	model        *llm.Model
	journalLen   int // expected journal length after reconstruction (0 = use all)
}

// RestoreAgentOption configures RestoreAgent behavior.
type RestoreAgentOption func(*restoreConfig)

// WithTurn specifies which checkpoint turn to restore from.
// Default: use the latest checkpoint.
func WithTurn(turn int) RestoreAgentOption {
	return func(c *restoreConfig) { c.turn = turn }
}

// WithMutateState applies a mutation to the snapshot after reconstruction.
func WithMutateState(fn func(*agentctx.ContextSnapshot)) RestoreAgentOption {
	return func(c *restoreConfig) { c.mutateState = fn }
}

// WithScript injects a ScriptedLLM as the LLM caller.
func WithScript(script *ScriptedLLM) RestoreAgentOption {
	return func(c *restoreConfig) { c.script = script }
}

// WithMockTools replaces the default tool set with mock tools.
func WithMockTools(tools []agentctx.Tool) RestoreAgentOption {
	return func(c *restoreConfig) { c.mockTools = tools }
}

// WithModel overrides the default test model.
func WithModel(model *llm.Model) RestoreAgentOption {
	return func(c *restoreConfig) { c.model = model }
}

// RestoreAgent creates an AgentNew ready for testing by restoring from a real session.
//
// It copies sessionDir to a temp directory (to avoid mutating test data),
// loads the checkpoint at the specified turn (or latest), replays the journal
// to reconstruct the snapshot, applies any mutations, and returns an agent
// with the injected callLLM and mock tools.
func RestoreAgent(t *testing.T, sessionDir string, opts ...RestoreAgentOption) *AgentNew {
	t.Helper()

	cfg := &restoreConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 1. Copy sessionDir to a temp directory
	tmpDir := t.TempDir()
	err := copyDir(sessionDir, tmpDir)
	if err != nil {
		t.Fatalf("RestoreAgent: failed to copy session dir: %v", err)
	}

	// 2. Load checkpoint
	var checkpoint *agentctx.CheckpointInfo
	if cfg.turn > 0 {
		checkpoint, err = agentctx.LoadCheckpointAtTurn(tmpDir, cfg.turn)
		if err != nil {
			t.Fatalf("RestoreAgent: failed to load checkpoint at turn %d: %v", cfg.turn, err)
		}
	} else {
		checkpoint, err = agentctx.LoadLatestCheckpoint(tmpDir)
		if err != nil {
			t.Fatalf("RestoreAgent: failed to load latest checkpoint: %v", err)
		}
	}

	// 3. Open journal and read entries
	journal, err := agentctx.OpenJournal(tmpDir)
	if err != nil {
		t.Fatalf("RestoreAgent: failed to open journal: %v", err)
	}

	entries, err := journal.ReadAll()
	if err != nil {
		t.Fatalf("RestoreAgent: failed to read journal: %v", err)
	}

	// 4. Reconstruct snapshot from checkpoint + journal
	snapshot, err := agentctx.ReconstructSnapshotWithCheckpoint(tmpDir, checkpoint, entries)
	if err != nil {
		t.Fatalf("RestoreAgent: failed to reconstruct snapshot: %v", err)
	}

	// 5. Apply mutations
	if cfg.mutateState != nil {
		cfg.mutateState(snapshot)
	}

	// By default, reset trigger-related state so tests start in normal mode.
	// Tests that specifically want to trigger context management should use
	// WithMutateState to set their own trigger conditions.
	if cfg.mutateState == nil {
		snapshot.AgentState.TokensUsed = 1000
		snapshot.AgentState.TurnsSinceLastTrigger = 0
		snapshot.AgentState.ToolCallsSinceLastTrigger = 0
		snapshot.AgentState.LastTriggerTurn = snapshot.AgentState.TotalTurns
	}

	// 6. Set up model
	model := cfg.model
	if model == nil {
		model = &llm.Model{
			ID:            "test-model",
			Provider:      "test",
			BaseURL:       "http://localhost:0",
			API:           "openai-completions",
			ContextWindow: 200000,
		}
	}

	// 7. Set up tools
	tools := cfg.mockTools
	if tools == nil {
		tools = defaultMockTools()
	}

	// 8. Create agent
	var modelSpec ModelSpec = *model
	ag := &AgentNew{
		snapshot:       snapshot,
		sessionDir:     tmpDir,
		sessionID:      "test-restore-session",
		journal:        journal,
		model:          &modelSpec,
		apiKey:         "test-api-key",
		allTools:       tools,
		triggerChecker: agentctx.NewTriggerChecker(),
		thinkingLevel:  "high",
		followUpQueue:  make(chan string, 100),
	}

	// 9. Inject script if provided
	if cfg.script != nil {
		ag.callLLM = cfg.script.Call
	}

	return ag
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0644)
	})
}

// --- Mock Tool ---

// MockTool is a simple tool implementation for testing.
type MockTool struct {
	name   string
	result string
}

// NewMockTool creates a mock tool that returns the given result string.
func NewMockTool(name, result string) *MockTool {
	return &MockTool{name: name, result: result}
}

// Name returns the tool name.
func (m *MockTool) Name() string { return m.name }

// Description returns a placeholder description.
func (m *MockTool) Description() string { return fmt.Sprintf("mock %s tool", m.name) }

// Parameters returns an empty parameter schema.
func (m *MockTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Execute returns the mock result.
func (m *MockTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: m.result},
	}, nil
}

// defaultMockTools returns a set of mock tools that cover the tools
// commonly found in test session data.
func defaultMockTools() []agentctx.Tool {
	return []agentctx.Tool{
		NewMockTool("bash", "mock bash output"),
		NewMockTool("read", "mock file content"),
		NewMockTool("write", "mock write success"),
		NewMockTool("grep", "mock grep results"),
		NewMockTool("glob", "mock glob results"),
		NewMockTool("edit", "mock edit success"),
	}
}

// waitWithTimeout waits for a goroutine to finish or times out.
// Returns an error if the timeout is exceeded.
func waitWithTimeout(done <-chan struct{}, timeout time.Duration) error {
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout after %v", timeout)
	}
}
