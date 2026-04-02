package testutil

// This file provides the bridge between the test framework and the real agent.
// It allows injecting mock LLM calls and mock tools into AgentNew for testing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// MockAgentConfig configures how the mock agent behaves.
type MockAgentConfig struct {
	// SessionID is the session identifier
	SessionID string
	// TempDir is the base temporary directory
	TempDir string
	// Model is the LLM model configuration
	Model llm.Model
	// APIKey is the API key (only used in record mode)
	APIKey string
	// Mode is the VCR mode (record or replay)
	Mode Mode
	// CassetteDir is the directory for VCR cassettes
	CassetteDir string
	// CassetteName is the name of the cassette file
	CassetteName string
	// MockTools optionally replaces the standard tool set
	MockTools map[string]*MockTool
}

// MockAgent wraps an AgentNew with VCR-based LLM interception and mock tools.
//
// It works by:
// 1. Replacing the global http.DefaultTransport with VCR RoundTripper
// 2. Providing mock tools instead of real ones
// 3. Managing session lifecycle
type MockAgent struct {
	agent       *AgentWrapper
	env         *ScenarioTestEnv
	snapshot    *agentctx.ContextSnapshot
	journal     *agentctx.Journal
	messages    []agentctx.AgentMessage
	mockTools   map[string]*MockTool
	toolResults map[string]string // tool_call_id -> recorded result (for replay)
}

// AgentWrapper wraps the agent's internal state for testing.
// It mimics the AgentNew behavior but with injected dependencies.
type AgentWrapper struct {
	snapshot    *agentctx.ContextSnapshot
	journal     *agentctx.Journal
	sessionDir  string
	sessionID   string
	model       *llm.Model
	apiKey      string
	tools       []agentctx.Tool
	vcrMode     Mode
	llmCalls    int
	toolCalls   int
}

// NewMockAgent creates a mock agent for testing.
func NewMockAgent(t *testing.T, cfg MockAgentConfig) *MockAgent {
	t.Helper()

	sessionDir := filepath.Join(cfg.TempDir, "sessions", cfg.SessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	// Create checkpoints dir
	if err := os.MkdirAll(filepath.Join(sessionDir, "checkpoints"), 0755); err != nil {
		t.Fatalf("Failed to create checkpoints dir: %v", err)
	}

	// Create snapshot
	cwd, _ := os.Getwd()
	snapshot := agentctx.NewContextSnapshot(cfg.SessionID, cwd)
	snapshot.AgentState.TokensLimit = cfg.Model.ContextWindow

	// Open journal
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		t.Fatalf("Failed to open journal: %v", err)
	}

	// Build tool list
	var tools []agentctx.Tool
	mockToolsMap := make(map[string]*MockTool)
	if cfg.MockTools != nil {
		for _, mt := range cfg.MockTools {
			tools = append(tools, mt)
			mockToolsMap[mt.Name()] = mt
		}
	} else {
		registry := SetupStandardTools(t)
		for _, tool := range registry.All() {
			tools = append(tools, tool)
		}
		for name, mt := range registry.tools {
			mockToolsMap[name] = mt
		}
	}

	wrapper := &AgentWrapper{
		snapshot:   snapshot,
		journal:    journal,
		sessionDir: sessionDir,
		sessionID:  cfg.SessionID,
		model:      &cfg.Model,
		apiKey:     cfg.APIKey,
		tools:      tools,
		vcrMode:    cfg.Mode,
	}

	return &MockAgent{
		agent:       wrapper,
		snapshot:    snapshot,
		journal:     journal,
		mockTools:   mockToolsMap,
		toolResults: make(map[string]string),
	}
}

// ExecuteTurn executes a user message through the agent with VCR-backed LLM calls.
//
// In record mode: makes real LLM calls, records HTTP + tool results.
// In replay mode: uses saved responses, returns mock tool results.
func (ma *MockAgent) ExecuteTurn(ctx context.Context, userMessage string) error {
	w := ma.agent

	// Append user message
	userMsg := agentctx.AgentMessage{
		Role:         "user",
		Content:      []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: userMessage}},
		Timestamp:    time.Now().Unix(),
		AgentVisible: true,
		UserVisible:  true,
	}
	w.snapshot.RecentMessages = append(w.snapshot.RecentMessages, userMsg)
	if err := w.journal.AppendMessage(userMsg); err != nil {
		return fmt.Errorf("failed to append user message: %w", err)
	}

	// Conversation loop
	const maxCycles = 20
	for cycle := 0; cycle < maxCycles; cycle++ {
		// Build LLM request
		llmCtx := ma.buildLLMContext()

		// Call LLM (via VCR)
		w.llmCalls++
		stream := llm.StreamLLM(ctx, *w.model, llmCtx, w.apiKey, 2*time.Minute)

		// Process response
		assistantMsg, toolResults, err := ma.processResponse(ctx, stream)
		if err != nil {
			return fmt.Errorf("LLM call %d failed: %w", w.llmCalls, err)
		}

		// Append assistant message
		w.snapshot.RecentMessages = append(w.snapshot.RecentMessages, *assistantMsg)
		if err := w.journal.AppendMessage(*assistantMsg); err != nil {
			return fmt.Errorf("failed to append assistant message: %w", err)
		}

		// Process tool results
		for _, result := range toolResults {
			w.snapshot.RecentMessages = append(w.snapshot.RecentMessages, result)
			if err := w.journal.AppendMessage(result); err != nil {
				return fmt.Errorf("failed to append tool result: %w", err)
			}
		}

		// If no tool calls, we're done
		if len(toolResults) == 0 {
			break
		}
	}

	// Update turn count
	w.snapshot.AgentState.TotalTurns++
	w.snapshot.AgentState.UpdatedAt = time.Now()

	return nil
}

// buildLLMContext builds the LLM context from the current snapshot.
func (ma *MockAgent) buildLLMContext() llm.LLMContext {
	w := ma.agent

	// Convert tools to LLM format
	var llmTools []llm.LLMTool
	for _, tool := range w.tools {
		llmTools = append(llmTools, llm.LLMTool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}

	// Convert messages
	var llmMessages []llm.LLMMessage
	for _, msg := range w.snapshot.RecentMessages {
		if !msg.IsAgentVisible() {
			continue
		}
		if msg.IsTruncated() {
			continue
		}

		content := msg.ExtractText()
		toolCalls := msg.ExtractToolCalls()
		role := msg.Role
		if role == "toolResult" {
			role = "tool"
		}

		llmMsg := llm.LLMMessage{
			Role:    role,
			Content: content,
		}

		if len(toolCalls) > 0 {
			for _, tc := range toolCalls {
				argsJSON := "{}"
				if tc.Arguments != nil {
					if bytes, err := json.Marshal(tc.Arguments); err == nil {
						argsJSON = string(bytes)
					}
				}
				llmMsg.ToolCalls = append(llmMsg.ToolCalls, llm.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: llm.FunctionCall{
						Name:      tc.Name,
						Arguments: argsJSON,
					},
				})
			}
		}

		if msg.Role == "toolResult" {
			llmMsg.ToolCallID = msg.ToolCallID
		}

		llmMessages = append(llmMessages, llmMsg)
	}

	return llm.LLMContext{
		SystemPrompt: BuildSystemPrompt(agentctx.ModeNormal),
		Messages:     llmMessages,
		Tools:        llmTools,
	}
}

// processResponse processes the LLM streaming response.
func (ma *MockAgent) processResponse(
	ctx context.Context,
	stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage],
) (*agentctx.AgentMessage, []agentctx.AgentMessage, error) {
	var textContent string
	var thinkingContent string
	var toolCalls []agentctx.ToolCallContent
	var usage *agentctx.Usage
	var stopReason string

	for item := range stream.Iterator(ctx) {
		if item.Done {
			break
		}

		switch e := item.Value.(type) {
		case llm.LLMTextDeltaEvent:
			textContent += e.Delta
		case llm.LLMThinkingDeltaEvent:
			thinkingContent += e.Delta
		case llm.LLMToolCallDeltaEvent:
			if e.ToolCall != nil {
				// Accumulate tool calls by index
				// Simple approach: create a new entry each time
				argsMap := make(map[string]any)
				if e.ToolCall.Function.Arguments != "" {
					json.Unmarshal([]byte(e.ToolCall.Function.Arguments), &argsMap)
				}
				toolCalls = append(toolCalls, agentctx.ToolCallContent{
					ID:        e.ToolCall.ID,
					Type:      "toolCall",
					Name:      e.ToolCall.Function.Name,
					Arguments: argsMap,
				})
			}
		case llm.LLMDoneEvent:
			stopReason = e.StopReason
			usage = &agentctx.Usage{
				InputTokens:  e.Usage.InputTokens,
				OutputTokens: e.Usage.OutputTokens,
				TotalTokens:  e.Usage.TotalTokens,
			}
		case llm.LLMErrorEvent:
			return nil, nil, e.Error
		}
	}

	// Build assistant message
	content := make([]agentctx.ContentBlock, 0)
	if thinkingContent != "" {
		content = append(content, agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: thinkingContent,
		})
	}
	if textContent != "" {
		content = append(content, agentctx.TextContent{
			Type: "text",
			Text: textContent,
		})
	}
	// Convert tool calls to ContentBlock
	for _, tc := range toolCalls {
		content = append(content, tc)
	}

	assistantMsg := agentctx.AgentMessage{
		Role:       "assistant",
		Content:    content,
		StopReason: stopReason,
		Usage:      usage,
		Timestamp:  time.Now().UnixMilli(),
		API:        ma.agent.model.API,
		Provider:   ma.agent.model.Provider,
		Model:      ma.agent.model.ID,
	}

	// Update token usage
	if usage != nil {
		ma.agent.snapshot.AgentState.TokensUsed = usage.TotalTokens
	}

	// Execute tool calls with mock tools
	var results []agentctx.AgentMessage
	toolCallsByID := make(map[string]bool)
	for _, tc := range toolCalls {
		if toolCallsByID[tc.ID] {
			continue // Skip duplicate IDs
		}
		toolCallsByID[tc.ID] = true

		ma.agent.toolCalls++

		// Find the mock tool
		mockTool, ok := ma.mockTools[tc.Name]
		if !ok {
			// Tool not found - return error
			results = append(results, agentctx.NewToolResultMessage(
				tc.ID, tc.Name,
				[]agentctx.ContentBlock{agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Tool '%s' not found", tc.Name),
				}},
				true,
			))
			continue
		}

		// Execute the mock tool
		toolResult, err := mockTool.Execute(ctx, tc.Arguments)
		if err != nil {
			results = append(results, agentctx.NewToolResultMessage(
				tc.ID, tc.Name,
				[]agentctx.ContentBlock{agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Tool execution failed: %v", err),
				}},
				true,
			))
			continue
		}

		results = append(results, agentctx.NewToolResultMessage(tc.ID, tc.Name, toolResult, false))
	}

	return &assistantMsg, results, nil
}

// GetSnapshot returns the current agent snapshot.
func (ma *MockAgent) GetSnapshot() *agentctx.ContextSnapshot {
	return ma.agent.snapshot
}

// LLMCallCount returns the number of LLM calls made.
func (ma *MockAgent) LLMCallCount() int {
	return ma.agent.llmCalls
}

// ToolCallCount returns the number of tool calls made.
func (ma *MockAgent) ToolCallCount() int {
	return ma.agent.toolCalls
}

// GetMockTool returns a mock tool by name for inspection.
func (ma *MockAgent) GetMockTool(name string) (*MockTool, bool) {
	mt, ok := ma.mockTools[name]
	return mt, ok
}

// Close cleans up the mock agent.
func (ma *MockAgent) Close() {
	if ma.journal != nil {
		ma.journal.Close()
	}
}
