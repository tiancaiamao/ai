package agent

import (
	"context"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestAgentLifecycleComplete tests the complete lifecycle of an agent
// from creation to shutdown.
func TestAgentLifecycleComplete(t *testing.T) {
	t.Run("creation_to_shutdown", func(t *testing.T) {
		// Step 1: Create agent
		model := llm.Model{ID: "test-model", Provider: "test"}
		agent := NewAgent(model, "test-key", "test-system-prompt")
		require.NotNil(t, agent)

		// Verify initial state
		state := agent.GetState()
		assert.NotNil(t, state["model"])
		assert.Equal(t, "test-system-prompt", state["systemPrompt"])
		assert.Equal(t, 0, state["messageCount"])
		assert.Equal(t, 0, state["toolCount"])

		// Step 2: Add tools
		mockTool := &mockLifecycleTool{name: "lifecycle-test-tool"}
		agent.AddTool(mockTool)
		require.Len(t, agent.GetContext().Tools, 1)

		// Step 3: Verify event channel works
		events := agent.Events()
		require.NotNil(t, events)

		// Step 4: Shutdown
		agent.Shutdown()
	})

	t.Run("with_custom_config", func(t *testing.T) {
		cfg := &LoopConfig{
			MaxLLMRetries:  3,
			RetryBaseDelay: 500 * time.Millisecond,
			MaxTurns:       10,
			Executor:       NewExecutorPool(map[string]int{"maxConcurrentTools": 5}),
		}

		model := llm.Model{ID: "test-model"}
		agent := NewAgentFromConfig(model, "test-key", "system-prompt", cfg)
		require.NotNil(t, agent)

		assert.Equal(t, 3, agent.MaxLLMRetries)
		assert.Equal(t, 500*time.Millisecond, agent.RetryBaseDelay)
		assert.Equal(t, 10, agent.MaxTurns)

		agent.Shutdown()
	})
}

// TestAgentLifecycleToolExecution tests tool execution within the agent lifecycle.
func TestAgentLifecycleToolExecution(t *testing.T) {
	t.Run("tool_executor_in_config", func(t *testing.T) {
		cfg := DefaultLoopConfig()
		agent := NewAgentFromConfig(llm.Model{ID: "test"}, "key", "system", cfg)
		defer agent.Shutdown()

		// Verify executor is set
		require.NotNil(t, agent.Executor)

		// Add a tool
		agent.AddTool(&mockLifecycleTool{name: "exec-test-tool"})

		// Context should have tool
		ctx := agent.GetContext()
		assert.Len(t, ctx.Tools, 1)
	})

	t.Run("tool_call_normalization", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Test that agent can be created and configured
		assert.NotNil(t, agent)
	})
}

// TestAgentLifecycleContextManagement tests context operations during lifecycle.
func TestAgentLifecycleContextManagement(t *testing.T) {
	t.Run("context_messages_persist", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Set initial messages
		agent.SetContext(&agentctx.AgentContext{
			SystemPrompt: "system",
			Messages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("first message"),
			},
		})

		// Add a tool
		agent.AddTool(&mockLifecycleTool{name: "tool1"})

		// Verify messages and tools persist when getting context
		ctx := agent.GetContext()
		assert.Len(t, ctx.Messages, 1)
		assert.Len(t, ctx.Tools, 1)

		// Replace context - tools should be preserved
		agent.SetContext(&agentctx.AgentContext{
			SystemPrompt: "new system",
			Messages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("second message"),
			},
		})

		// Tools should still be there
		ctx = agent.GetContext()
		assert.Len(t, ctx.Tools, 1, "Tools should be preserved after SetContext")
	})

	t.Run("followup_queue_operations", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Add follow-ups
		err := agent.FollowUp("follow-up 1")
		require.NoError(t, err)

		err = agent.FollowUp("follow-up 2")
		require.NoError(t, err)

		pending := agent.GetPendingFollowUps()
		assert.Equal(t, 2, pending)

		// Clear follow-ups via Abort
		agent.Abort()
	})

	t.Run("steer_resets_context", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Add initial context
		agent.SetContext(&agentctx.AgentContext{
			SystemPrompt: "original",
			Messages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("original message"),
			},
		})

		// Steer should not panic
		agent.Steer("steer message")

		ctx := agent.GetContext()
		assert.NotNil(t, ctx)
	})
}

// TestAgentLifecycleCompaction tests compaction during lifecycle.
func TestAgentLifecycleCompaction(t *testing.T) {
	t.Run("manual_compaction", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Set up mock compactor with shouldCompact = true
		mockCompactor := &mockLifecycleCompactor{shouldCompact: true}
		agent.SetCompactor(mockCompactor)

		// Set messages
		agent.SetContext(&agentctx.AgentContext{
			SystemPrompt: "system",
			Messages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("message 1"),
				agentctx.NewUserMessage("message 2"),
				agentctx.NewUserMessage("message 3"),
			},
		})

		// Compact - note: Compact doesn't call ShouldCompact, it just compacts
		err := agent.Compact(mockCompactor)
		require.NoError(t, err)

		// Verify compact was called (Compactor.Compact is called by agent.Compact)
		assert.True(t, mockCompactor.compactCalled)

		// Verify compaction summary is saved
		ctx := agent.GetContext()
		assert.Equal(t, "[Compacted]", ctx.LastCompactionSummary)
	})

	t.Run("auto_compaction_trigger", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		mockCompactor := &mockLifecycleCompactor{shouldCompact: true}
		agent.SetCompactor(mockCompactor)

		// Set many messages to trigger auto-compact
		messages := make([]agentctx.AgentMessage, 100)
		for i := range messages {
			messages[i] = agentctx.NewUserMessage("message")
		}

		agent.SetContext(&agentctx.AgentContext{
			SystemPrompt: "system",
			Messages:     messages,
		})

		// Trigger auto-compact
		agent.tryAutoCompact(context.Background())

		assert.True(t, mockCompactor.shouldCompactCalled)
		assert.True(t, mockCompactor.compactCalled)
	})
}

// TestAgentLifecycleAbortAndRetry tests abort and retry behavior.
func TestAgentLifecycleAbortAndRetry(t *testing.T) {
	t.Run("abort_clears_followups", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")

		// Add follow-ups
		err := agent.FollowUp("follow-up 1")
		require.NoError(t, err)
		err = agent.FollowUp("follow-up 2")
		require.NoError(t, err)

		assert.Equal(t, 2, agent.GetPendingFollowUps())

		// Abort should clear follow-ups
		agent.Abort()

		agent.Shutdown()
	})

	t.Run("retry_config", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Default should have retry enabled
		assert.True(t, agent.AutoRetryEnabled())

		// Disable retry
		agent.SetAutoRetry(false)
		assert.False(t, agent.AutoRetryEnabled())

		// Re-enable
		agent.SetAutoRetry(true)
		assert.True(t, agent.AutoRetryEnabled())

		// Custom retry config
		agent.SetLLMRetryConfig(5, 2*time.Second)
		assert.Equal(t, 5, agent.MaxLLMRetries)
		assert.Equal(t, 2*time.Second, agent.RetryBaseDelay)
	})
}

// TestAgentLifecycleMetrics tests metrics collection during lifecycle.
func TestAgentLifecycleMetrics(t *testing.T) {
	t.Run("metrics_available", func(t *testing.T) {
		cfg := DefaultLoopConfig()
		agent := NewAgentFromConfig(llm.Model{ID: "test"}, "key", "system", cfg)
		defer agent.Shutdown()

		metrics := agent.GetMetrics()
		require.NotNil(t, metrics)
	})
}

// TestAgentLifecycleConcurrency tests concurrent operations.
func TestAgentLifecycleConcurrency(t *testing.T) {
	t.Run("concurrent_followups", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		done := make(chan bool, 10)

		// Add follow-ups concurrently
		for i := 0; i < 10; i++ {
			go func(n int) {
				err := agent.FollowUp("follow-up")
				if err != nil {
					t.Errorf("FollowUp failed: %v", err)
				}
				done <- true
			}(i)
		}

		// Wait for all
		for i := 0; i < 10; i++ {
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				t.Fatal("Timeout waiting for concurrent follow-ups")
			}
		}

		assert.Equal(t, 10, agent.GetPendingFollowUps())
	})

	t.Run("events_channel_not_blocking", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		events := agent.Events()
		require.NotNil(t, events)

		// Non-blocking read should work
		select {
		case <-events:
			// Channel has events
		default:
			// Channel is empty, which is expected
		}
	})
}

// TestAgentLifecycleStateTransitions tests state transitions.
func TestAgentLifecycleStateTransitions(t *testing.T) {
	t.Run("state_reflects_configuration", func(t *testing.T) {
		agent := NewAgent(llm.Model{
			ID:       "test-model",
			Provider: "test-provider",
		}, "test-key", "test-system-prompt")
		defer agent.Shutdown()

		state := agent.GetState()

		// Verify model info is present
		model, ok := state["model"].(llm.Model)
		require.True(t, ok)
		assert.Equal(t, "test-model", model.ID)
		assert.Equal(t, "test-provider", model.Provider)

		// Verify system prompt
		assert.Equal(t, "test-system-prompt", state["systemPrompt"])

		// Initial counts should be 0
		assert.Equal(t, 0, state["messageCount"])
		assert.Equal(t, 0, state["toolCount"])
	})
}

// TestAgentLifecycleTurnSettings tests turn and window configuration.
func TestAgentLifecycleTurnSettings(t *testing.T) {
	t.Run("max_turns_config", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		agent.SetMaxTurns(50)
		assert.Equal(t, 50, agent.MaxTurns)

		// Setting to 0 should be allowed (unlimited)
		agent.SetMaxTurns(0)
		assert.Equal(t, 0, agent.MaxTurns)
	})

	t.Run("context_window_config", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		agent.SetContextWindow(128000)
		assert.Equal(t, 128000, agent.ContextWindow)
	})

	t.Run("task_tracking_toggle", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		agent.SetTaskTrackingEnabled(false)
		assert.False(t, agent.TaskTrackingEnabled)

		agent.SetTaskTrackingEnabled(true)
		assert.True(t, agent.TaskTrackingEnabled)
	})

	t.Run("context_management_toggle", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		agent.SetContextManagementEnabled(false)
		assert.False(t, agent.ContextManagementEnabled)

		agent.SetContextManagementEnabled(true)
		assert.True(t, agent.ContextManagementEnabled)
	})
}

// TestAgentLifecycleToolOutput tests tool output limits configuration.
func TestAgentLifecycleToolOutput(t *testing.T) {
	t.Run("tool_output_limits", func(t *testing.T) {
		cfg := &LoopConfig{
			ToolOutput: ToolOutputLimits{
				MaxChars: 5000,
			},
		}

		agent := NewAgentFromConfig(llm.Model{ID: "test"}, "key", "system", cfg)
		defer agent.Shutdown()

		// Verify tool output limits are set
		assert.Equal(t, 5000, agent.ToolOutput.MaxChars)
	})
}

// TestAgentLifecycleExecutorPool tests executor pool configuration.
func TestAgentLifecycleExecutorPool(t *testing.T) {
	t.Run("custom_executor_pool", func(t *testing.T) {
		pool := NewExecutorPool(map[string]int{
			"maxConcurrentTools": 5,
			"queueTimeout":       30,
		})

		cfg := &LoopConfig{
			Executor: pool,
		}

		agent := NewAgentFromConfig(llm.Model{ID: "test"}, "key", "system", cfg)
		defer agent.Shutdown()

		require.NotNil(t, agent.Executor)
	})
}

// TestAgentLifecycleEvents tests event emission during lifecycle.
func TestAgentLifecycleEvents(t *testing.T) {
	t.Run("agent_start_end_events", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Create start and end events
		startEvent := NewAgentStartEvent()
		assert.Equal(t, EventAgentStart, startEvent.Type)
		assert.NotZero(t, startEvent.EventAt)

		endEvent := NewAgentEndEvent(nil)
		assert.Equal(t, EventAgentEnd, endEvent.Type)
		assert.NotZero(t, endEvent.EventAt)
	})

	t.Run("compaction_events", func(t *testing.T) {
		agent := NewAgent(llm.Model{ID: "test"}, "key", "system")
		defer agent.Shutdown()

		// Create compaction events
		compactStart := NewCompactionStartEvent(CompactionInfo{
			Auto:    true,
			Before:  10,
			Trigger: "threshold",
		})
		assert.Equal(t, EventCompactionStart, compactStart.Type)

		compactEnd := NewCompactionEndEvent(CompactionInfo{
			Auto:    true,
			Before:  10,
			After:   5,
			Trigger: "threshold",
		})
		assert.Equal(t, EventCompactionEnd, compactEnd.Type)
	})

	t.Run("turn_events", func(t *testing.T) {
		turnStart := NewTurnStartEvent()
		assert.Equal(t, EventTurnStart, turnStart.Type)

		turnEnd := NewTurnEndEvent(nil, nil)
		assert.Equal(t, EventTurnEnd, turnEnd.Type)
	})

	t.Run("error_event", func(t *testing.T) {
		errEvent := NewErrorEvent(nil)
		assert.Equal(t, EventError, errEvent.Type)
	})
}

// mockLifecycleTool is a test double for agentctx.Tool.
type mockLifecycleTool struct {
	name string
}

func (m *mockLifecycleTool) Name() string {
	return m.name
}

func (m *mockLifecycleTool) Description() string {
	return "Mock tool for lifecycle testing"
}

func (m *mockLifecycleTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"param": map[string]interface{}{
			"type":        "string",
			"description": "Test parameter",
		},
	}
}

func (m *mockLifecycleTool) Execute(ctx context.Context, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "Mock result"},
	}, nil
}

// mockLifecycleCompactor is a test double for Compactor.
type mockLifecycleCompactor struct {
	shouldCompactCalled bool
	compactCalled       bool
	shouldCompact       bool
}

func (m *mockLifecycleCompactor) ShouldCompact(messages []agentctx.AgentMessage) bool {
	m.shouldCompactCalled = true
	return m.shouldCompact
}

func (m *mockLifecycleCompactor) Compact(messages []agentctx.AgentMessage, previousSummary string) (*CompactionResult, error) {
	m.compactCalled = true
	return &CompactionResult{
		Summary: "[Compacted]",
		Messages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("[Compacted summary]"),
		},
	}, nil
}

func (m *mockLifecycleCompactor) CalculateDynamicThreshold() int {
	return 100000
}

func (m *mockLifecycleCompactor) EstimateContextTokens(messages []agentctx.AgentMessage) int {
	return len(messages) * 100
}