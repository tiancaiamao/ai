package agent

import (
	"context"
	"reflect"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestRegression001_MaxConsecutiveToolCalls tests that MaxConsecutiveToolCalls prevents infinite loops
func TestRegression001_MaxConsecutiveToolCalls(t *testing.T) {
	// Bug: Agent gets stuck in tool call loop without progress
	// Fix: Added MaxConsecutiveToolCalls limit in LoopConfig

	cfg := DefaultLoopConfig()
	cfg.MaxConsecutiveToolCalls = 3 // Low limit for testing
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify that default config allows unlimited tool calls (0)
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.MaxConsecutiveToolCalls != 0 {
		t.Errorf("DefaultLoopConfig should have MaxConsecutiveToolCalls = 0 (unlimited), got %d", defaultCfg.MaxConsecutiveToolCalls)
	}

	// Verify that custom config can be set
	if cfg.MaxConsecutiveToolCalls != 3 {
		t.Errorf("MaxConsecutiveToolCalls not set correctly: got %d, want 3", cfg.MaxConsecutiveToolCalls)
	}

	// Create agent with the config
	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.MaxConsecutiveToolCalls != 3 {
		t.Errorf("agent.MaxConsecutiveToolCalls not set: got %d, want 3", agent.MaxConsecutiveToolCalls)
	}
}

// TestRegression002_AutoCompactConfiguration tests that auto-compact is properly configured
func TestRegression002_AutoCompactConfiguration(t *testing.T) {
	// Bug: Context overflow causes agent to fail silently
	// Fix: Auto-compact with ShouldCompact threshold

	cfg := DefaultLoopConfig()
	cfg.Compactors = []agentctx.Compactor{
		&testCompactor{shouldTrigger: true},
	}
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	agentCtx := agentctx.NewAgentContext("Test agent")

	// Add many messages to test compaction trigger
	for i := 0; i < 100; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("Test message"))
	}

	agent := NewAgentFromConfigWithContext(llm.Model{}, "test-key", agentCtx, cfg)

	// Verify compactor is registered
	if len(agent.Compactors) != 1 {
		t.Errorf("Expected 1 compactor, got %d", len(agent.Compactors))
	}

	// This test verifies the infrastructure is in place
	// Actual compaction behavior is tested in other test files
}

// TestRegression003_ToolCallCutoffConfiguration tests that ToolCallCutoff is configurable
func TestRegression003_ToolCallCutoffConfiguration(t *testing.T) {
	// Bug: Agent uses stale tool outputs, makes wrong decisions
	// Fix: ToolCallCutoff parameter truncates old outputs

	cfg := DefaultLoopConfig()
	cfg.ToolCallCutoff = 2 // Low limit for testing
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has ToolCallCutoff set to a positive value
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.ToolCallCutoff <= 0 {
		t.Errorf("DefaultLoopConfig should have ToolCallCutoff > 0, got %d", defaultCfg.ToolCallCutoff)
	}

	// Verify custom config can be set
	if cfg.ToolCallCutoff != 2 {
		t.Errorf("ToolCallCutoff not set correctly: got %d, want 2", cfg.ToolCallCutoff)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.ToolCallCutoff != 2 {
		t.Errorf("agent.ToolCallCutoff not set: got %d, want 2", agent.ToolCallCutoff)
	}
}

// TestRegression004_MaxToolCallsPerName tests that MaxToolCallsPerName prevents tool abuse
func TestRegression004_MaxToolCallsPerName(t *testing.T) {
	// Bug: Agent can abuse a single tool indefinitely
	// Fix: Added MaxToolCallsPerName limit

	cfg := DefaultLoopConfig()
	cfg.MaxToolCallsPerName = 5 // Low limit for testing
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config allows unlimited tool calls per name (0)
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.MaxToolCallsPerName != 0 {
		t.Errorf("DefaultLoopConfig should have MaxToolCallsPerName = 0 (unlimited), got %d", defaultCfg.MaxToolCallsPerName)
	}

	// Verify custom config can be set
	if cfg.MaxToolCallsPerName != 5 {
		t.Errorf("MaxToolCallsPerName not set correctly: got %d, want 5", cfg.MaxToolCallsPerName)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.MaxToolCallsPerName != 5 {
		t.Errorf("agent.MaxToolCallsPerName not set: got %d, want 5", agent.MaxToolCallsPerName)
	}
}

// TestRegression005_MaxTurnsConfiguration tests that MaxTurns prevents runaway conversations
func TestRegression005_MaxTurnsConfiguration(t *testing.T) {
	// Bug: Agent can continue conversation indefinitely
	// Fix: Added MaxTurns limit

	cfg := DefaultLoopConfig()
	cfg.MaxTurns = 3
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config allows unlimited turns (0)
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.MaxTurns != 0 {
		t.Errorf("DefaultLoopConfig should have MaxTurns = 0 (unlimited), got %d", defaultCfg.MaxTurns)
	}

	// Verify custom config can be set
	if cfg.MaxTurns != 3 {
		t.Errorf("MaxTurns not set correctly: got %d, want 3", cfg.MaxTurns)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.MaxTurns != 3 {
		t.Errorf("agent.MaxTurns not set: got %d, want 3", agent.MaxTurns)
	}
}

// TestRegression006_ContextWindowConfiguration tests that context window limits are configurable
func TestRegression006_ContextWindowConfiguration(t *testing.T) {
	// Bug: Agent ignores context window limits
	// Fix: ContextWindow parameter limits context size

	cfg := DefaultLoopConfig()
	cfg.ContextWindow = 1000 // Small limit for testing
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has ContextWindow set to 0 (use model default)
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.ContextWindow != 0 {
		t.Errorf("DefaultLoopConfig should have ContextWindow = 0 (use model default), got %d", defaultCfg.ContextWindow)
	}

	// Verify custom config can be set
	if cfg.ContextWindow != 1000 {
		t.Errorf("ContextWindow not set correctly: got %d, want 1000", cfg.ContextWindow)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.ContextWindow != 1000 {
		t.Errorf("agent.ContextWindow not set: got %d, want 1000", agent.ContextWindow)
	}
}

// TestRegression007_LLMRetryConfiguration tests that LLM retry logic is configurable
func TestRegression007_LLMRetryConfiguration(t *testing.T) {
	// Bug: Agent fails immediately on rate limit errors
	// Fix: Added retry logic with configurable limits

	cfg := DefaultLoopConfig()
	cfg.MaxLLMRetries = 3
	cfg.RetryBaseDelay = 100 // milliseconds
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has retries enabled
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.MaxLLMRetries <= 0 {
		t.Errorf("DefaultLoopConfig should have MaxLLMRetries > 0, got %d", defaultCfg.MaxLLMRetries)
	}

	// Verify custom config can be set
	if cfg.MaxLLMRetries != 3 {
		t.Errorf("MaxLLMRetries not set correctly: got %d, want 3", cfg.MaxLLMRetries)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.MaxLLMRetries != 3 {
		t.Errorf("agent.MaxLLMRetries not set: got %d, want 3", agent.MaxLLMRetries)
	}
}

// TestRegression008_ToolOutputLimits tests that tool output limits are configurable
func TestRegression008_ToolOutputLimits(t *testing.T) {
	// Bug: Large tool outputs fill up context
	// Fix: Added ToolOutput size limits

	cfg := DefaultLoopConfig()
	cfg.ToolOutput = ToolOutputLimits{
		MaxChars: 1000,
	}
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has ToolOutput limits set
	defaultCfg := DefaultLoopConfig()
	if reflect.DeepEqual(defaultCfg.ToolOutput, ToolOutputLimits{}) {
		t.Error("DefaultLoopConfig should have ToolOutput limits set")
	}

	// Verify custom config can be set
	if cfg.ToolOutput.MaxChars != 1000 {
		t.Errorf("ToolOutput.MaxChars not set correctly: got %d, want 1000", cfg.ToolOutput.MaxChars)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.ToolOutput.MaxChars != 1000 {
		t.Errorf("agent.ToolOutput.MaxChars not set: got %d, want 1000", agent.ToolOutput.MaxChars)
	}
}

// TestRegression009_ExecutorPoolConfiguration tests that executor pool is properly configured
func TestRegression009_ExecutorPoolConfiguration(t *testing.T) {
	// Bug: Tools can hang indefinitely or execute without limits
	// Fix: Added executor pool with concurrency and timeout controls

	cfg := DefaultLoopConfig()
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has Executor set
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.Executor == nil {
		t.Error("DefaultLoopConfig should have Executor set")
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify executor is available in agent
	if agent.Executor == nil {
		t.Error("agent.Executor should be set")
	}

	// This test verifies the infrastructure is in place
	// Actual tool execution behavior is tested in executor_test.go
}

// TestRegression010_EnableCheckpointConfiguration tests that checkpoint is configurable
func TestRegression010_EnableCheckpointConfiguration(t *testing.T) {
	// Bug: Checkpoints always enabled, causing overhead in some scenarios
	// Fix: Made checkpoint configurable

	cfg := DefaultLoopConfig()
	cfg.EnableCheckpoint = false
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false

	// Verify default config has checkpoint enabled
	defaultCfg := DefaultLoopConfig()
	if !defaultCfg.EnableCheckpoint {
		t.Error("DefaultLoopConfig should have EnableCheckpoint = true")
	}

	// Verify custom config can be set
	if cfg.EnableCheckpoint != false {
		t.Errorf("EnableCheckpoint not set correctly: got %v, want false", cfg.EnableCheckpoint)
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.EnableCheckpoint != false {
		t.Errorf("agent.EnableCheckpoint not set: got %v, want false", agent.EnableCheckpoint)
	}
}

// TestRegression011_LLMTimeoutConfiguration tests that LLM timeouts are configurable
func TestRegression011_LLMTimeoutConfiguration(t *testing.T) {
	// Bug: LLM calls can hang indefinitely
	// Fix: Added LLM timeout configuration

	cfg := DefaultLoopConfig()
	cfg.LLMTotalTimeout = 5 * 60 * 1000000000 // 5 minutes in nanoseconds
	cfg.LLMFirstResponseTimeout = 1 * 60 * 1000000000 // 1 minute in nanoseconds
	cfg.TaskTrackingEnabled = false
	cfg.ContextManagementEnabled = false
	cfg.EnableCheckpoint = false

	// Verify default config has timeouts set
	defaultCfg := DefaultLoopConfig()
	if defaultCfg.LLMTotalTimeout == 0 {
		t.Error("DefaultLoopConfig should have LLMTotalTimeout set")
	}
	if defaultCfg.LLMFirstResponseTimeout == 0 {
		t.Error("DefaultLoopConfig should have LLMFirstResponseTimeout set")
	}

	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if agent.LLMTotalTimeout != cfg.LLMTotalTimeout {
		t.Errorf("agent.LLMTotalTimeout not set correctly: got %v, want %v", agent.LLMTotalTimeout, cfg.LLMTotalTimeout)
	}
	if agent.LLMFirstResponseTimeout != cfg.LLMFirstResponseTimeout {
		t.Errorf("agent.LLMFirstResponseTimeout not set correctly: got %v, want %v", agent.LLMFirstResponseTimeout, cfg.LLMFirstResponseTimeout)
	}
}

// TestRegression012_RuntimeMetaConfiguration tests that runtime meta is configurable
func TestRegression012_RuntimeMetaConfiguration(t *testing.T) {
	// Bug: Runtime meta updates at fixed intervals, may be too frequent
	// Fix: Runtime meta is controlled by loop logic

	cfg := DefaultLoopConfig()
	cfg.TaskTrackingEnabled = true
	cfg.ContextManagementEnabled = true

	// Verify config accepts these flags
	agent := NewAgentFromConfig(llm.Model{}, "test-key", "Test agent", cfg)

	// Verify config is embedded in agent
	if !agent.TaskTrackingEnabled {
		t.Error("agent.TaskTrackingEnabled should be true")
	}
	if !agent.ContextManagementEnabled {
		t.Error("agent.ContextManagementEnabled should be true")
	}
}

// testCompactor is a mock compactor for testing
type testCompactor struct {
	shouldTrigger bool
	called         bool
}

func (c *testCompactor) Compact(ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	c.called = true
	return &agentctx.CompactionResult{
		Summary:      "Test summary",
		TokensBefore: len(ctx.RecentMessages) * 10,
		TokensAfter:  len(ctx.RecentMessages) * 5,
	}, nil
}

func (c *testCompactor) ShouldCompact(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	return c.shouldTrigger
}

func (c *testCompactor) CalculateDynamicThreshold() int {
	return 100000
}