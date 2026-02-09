package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestFollowUpQueue tests the follow-up queue functionality.
func TestFollowUpQueue(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	// Test adding follow-up messages
	err := agent.FollowUp("First follow-up")
	if err != nil {
		t.Fatalf("Failed to add first follow-up: %v", err)
	}

	err = agent.FollowUp("Second follow-up")
	if err != nil {
		t.Fatalf("Failed to add second follow-up: %v", err)
	}

	// Verify queue has capacity (100 total)
	// Can't directly access channel, but we can verify by adding more
	for i := 0; i < 98; i++ {
		err = agent.FollowUp(fmt.Sprintf("Additional follow-up %d", i))
		if err != nil {
			t.Fatalf("Failed to add follow-up %d: %v", i, err)
		}
	}

	// Queue should be full now (capacity is 100)
	err = agent.FollowUp("Should fail")
	if err == nil {
		t.Error("Expected error when queue is full, got nil")
	}
}

// TestFollowUpConcurrency tests concurrent follow-up additions.
func TestFollowUpConcurrency(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	// Add follow-ups concurrently
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func(n int) {
			err := agent.FollowUp("Concurrent follow-up")
			if err != nil {
				t.Errorf("Goroutine %d failed: %v", n, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for concurrent follow-ups")
		}
	}
}

// TestAgentSteer tests steering functionality.
func TestAgentSteer(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	// Steer should not block
	agent.Steer("Steer message")

	// Verify context was reset
	ctx := agent.GetContext()
	if ctx == nil {
		t.Error("Context should not be nil after steer")
	}
}

// TestAgentAbort tests abort functionality.
func TestAgentAbort(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	// Abort should not block
	agent.Abort()

	// Verify we can prompt again after abort
	err := agent.Prompt("Test after abort")
	if err != nil {
		t.Errorf("Failed to prompt after abort: %v", err)
	}
}

// TestCompactorInterface tests the Compactor interface.
func TestCompactorInterface(t *testing.T) {
	// Create a mock compactor
	mockCompactor := &mockCompactor{
		shouldCompact: true,
	}

	agent := NewAgent(llm.Model{}, "test-key", "test")
	agent.SetCompactor(mockCompactor)

	// Trigger auto-compact check
	agent.tryAutoCompact()

	if !mockCompactor.called {
		t.Error("Expected compactor to be called")
	}
}

// mockCompactor is a test double for Compactor.
type mockCompactor struct {
	shouldCompact bool
	called        bool
}

func (m *mockCompactor) ShouldCompact(messages []AgentMessage) bool {
	m.called = true
	return m.shouldCompact
}

func (m *mockCompactor) Compact(messages []AgentMessage) ([]AgentMessage, error) {
	// Return simplified messages
	return []AgentMessage{
		NewUserMessage("[Summary]"),
	}, nil
}

// TestAgentEvents tests the event channel.
func TestAgentEvents(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	events := agent.Events()
	if events == nil {
		t.Fatal("Event channel should not be nil")
	}

	// Verify channel is readable (non-blocking)
	select {
	case <-events:
		// Channel has events (unlikely in this test, but ok)
	default:
		// Channel is empty, which is expected
	}
}

// TestAgentContext tests agent context operations.
func TestAgentContext(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	// Test initial state
	ctx := agent.GetContext()
	if ctx == nil {
		t.Fatal("Context should not be nil")
	}

	if len(ctx.Messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(ctx.Messages))
	}

	// Test setting context
	newCtx := NewAgentContext("new system prompt")
	agent.SetContext(newCtx)

	retrievedCtx := agent.GetContext()
	if retrievedCtx.SystemPrompt != "new system prompt" {
		t.Errorf("Expected system prompt 'new system prompt', got '%s'", retrievedCtx.SystemPrompt)
	}
}

// TestAgentWithTools tests adding tools to agent.
func TestAgentWithTools(t *testing.T) {
	agent := NewAgent(llm.Model{}, "test-key", "test")

	mockTool := &mockTool{
		name: "test_tool",
	}

	agent.AddTool(mockTool)

	ctx := agent.GetContext()
	if len(ctx.Tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(ctx.Tools))
	}
}

// mockTool is a test double for Tool.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return "Mock tool for testing"
}

func (m *mockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"param": map[string]interface{}{
			"type":        "string",
			"description": "Test parameter",
		},
	}
}

func (m *mockTool) Execute(ctx context.Context, args map[string]interface{}) ([]ContentBlock, error) {
	return []ContentBlock{
		TextContent{Type: "text", Text: "Mock result"},
	}, nil
}

// TestAgentState tests getting agent state.
func TestAgentState(t *testing.T) {
	agent := NewAgent(llm.Model{
		ID:       "test-model",
		Provider: "test-provider",
	}, "test-key", "test system prompt")

	state := agent.GetState()

	if state["model"] == nil {
		t.Error("Model should be in state")
	}

	if state["systemPrompt"] != "test system prompt" {
		t.Errorf("Expected system prompt 'test system prompt', got '%v'", state["systemPrompt"])
	}

	if state["messageCount"].(int) != 0 {
		t.Errorf("Expected 0 messages, got %d", state["messageCount"].(int))
	}

	if state["toolCount"].(int) != 0 {
		t.Errorf("Expected 0 tools, got %d", state["toolCount"].(int))
	}
}
