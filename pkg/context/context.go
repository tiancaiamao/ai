package context

import (
	"context"
	"sync"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// AgentContext represents the context for agent execution.
type AgentContext struct {
	// Core components
	SystemPrompt string         `json:"systemPrompt,omitempty"`
	Tools        []Tool         `json:"tools,omitempty"`
	Skills       []skill.Skill  `json:"skills,omitempty"` // Loaded skills

	// Unified context state
	LLMContext     string         `json:"llmContext,omitempty"` // Structured LLM context content (not file manager)
	RecentMessages []AgentMessage `json:"recentMessages"`         // Recent messages (not full history)
	AgentState     AgentState     `json:"agentState"`            // System-maintained metadata

	// Compaction state
	LastCompactionSummary string `json:"lastCompactionSummary,omitempty"` // Last compaction summary for incremental updates

	// PostCompactRecovery indicates that overview.md should be injected for recovery after compact.
	// Set after compact completes, reset after injection.
	PostCompactRecovery bool `json:"-"`

	// OnMessagesChanged is called when messages are modified (e.g., after compact).
	// This allows persistence to session storage.
	OnMessagesChanged func() error `json:"-"`

	// allowedTools restricts which tools can be executed.
	// nil means all tools are allowed, non-nil is a whitelist.
	allowedTools map[string]bool `json:"-"`

	// contextMgmtMu serializes mutations to shared turn state
	// and assistant message tool-call arguments.
	contextMgmtMu sync.Mutex `json:"-"`
}

// Tool represents a tool that can be called by the agent.
type Tool interface {
	// Name returns the tool name.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// Parameters returns the JSON Schema for the tool parameters.
	Parameters() map[string]any

	// Execute executes the tool with the given arguments.
	Execute(ctx context.Context, args map[string]any) ([]ContentBlock, error)
}

type toolExecutionAgentContextKey struct{}
type toolExecutionCallIDKey struct{}

// WithToolExecutionAgentContext stores the current loop AgentContext in ctx so
// tools can mutate the active turn state instead of stale outer pointers.
func WithToolExecutionAgentContext(ctx context.Context, agentCtx *AgentContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolExecutionAgentContextKey{}, agentCtx)
}

// ToolExecutionAgentContext returns the active loop AgentContext for the
// current tool execution when available.
func ToolExecutionAgentContext(ctx context.Context) *AgentContext {
	if ctx == nil {
		return nil
	}
	agentCtx, _ := ctx.Value(toolExecutionAgentContextKey{}).(*AgentContext)
	return agentCtx
}

// WithToolExecutionCallID stores the current tool call ID in ctx so tools can
// access their own call metadata without sharing mutable state across goroutines.
func WithToolExecutionCallID(ctx context.Context, toolCallID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolExecutionCallIDKey{}, toolCallID)
}

// ToolExecutionCallID returns the current tool call ID for the running tool
// execution, when available.
func ToolExecutionCallID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	toolCallID, _ := ctx.Value(toolExecutionCallIDKey{}).(string)
	return toolCallID
}

// NewAgentContext creates a new AgentContext with the given system prompt.
func NewAgentContext(systemPrompt string) *AgentContext {
	return &AgentContext{
		SystemPrompt:   systemPrompt,
		LLMContext:     "",
		RecentMessages: make([]AgentMessage, 0),
		Tools:          make([]Tool, 0),
		Skills:         make([]skill.Skill, 0),
		AgentState:     *NewAgentState("", ""),
	}
}

// NewAgentContextWithSessionID creates a new AgentContext with session ID and working directory.
func NewAgentContextWithSessionID(systemPrompt, sessionID, cwd string) *AgentContext {
	ctx := &AgentContext{
		SystemPrompt:   systemPrompt,
		LLMContext:     "",
		RecentMessages: make([]AgentMessage, 0),
		Tools:          make([]Tool, 0),
		Skills:         make([]skill.Skill, 0),
		AgentState:     *NewAgentState(sessionID, cwd),
	}
	return ctx
}

// NewAgentContextWithSkills creates a new AgentContext with skills.
func NewAgentContextWithSkills(systemPrompt string, skills []skill.Skill) *AgentContext {
	ctx := NewAgentContext(systemPrompt)
	ctx.Skills = skills

	// Append skills to system prompt
	if len(skills) > 0 {
		skillsText := skill.FormatForPrompt(skills)
		if skillsText != "" {
			ctx.SystemPrompt = systemPrompt + skillsText
		}
	}

	return ctx
}

// AddRecentMessage adds a message to the recent messages.
func (c *AgentContext) AddRecentMessage(message AgentMessage) {
	c.RecentMessages = append(c.RecentMessages, message)
}

// AddMessage adds a message to the recent messages (alias for AddRecentMessage).
func (c *AgentContext) AddMessage(message AgentMessage) {
	c.AddRecentMessage(message)
}

// EstimateTokens provides a rough token estimate based on message text length.
// Uses ~4 chars per token heuristic.
func (c *AgentContext) EstimateTokens() int {
	total := len(c.LLMContext)
	for _, msg := range c.RecentMessages {
		total += len(msg.ExtractText())
	}
	return total / 4
}

// EstimateTokenPercent returns token usage as a fraction of the limit.
func (c *AgentContext) EstimateTokenPercent() float64 {
	if c.AgentState.TokensLimit <= 0 {
		return 0
	}
	return float64(c.EstimateTokens()) / float64(c.AgentState.TokensLimit)
}

// CountStaleOutputs counts tool result messages older than maxAge turns.
func (c *AgentContext) CountStaleOutputs(maxAge int) int {
	count := 0
	currentTurn := c.AgentState.TotalTurns
	for _, msg := range c.RecentMessages {
		if msg.Role == "toolResult" && currentTurn-msg.TruncatedAt > maxAge {
			count++
		}
	}
	return count
}

// LockContextManagement serializes context mutations on this context.
func (c *AgentContext) LockContextManagement() {
	if c == nil {
		return
	}
	c.contextMgmtMu.Lock()
}

// UnlockContextManagement releases the context mutation serialization lock.
func (c *AgentContext) UnlockContextManagement() {
	if c == nil {
		return
	}
	c.contextMgmtMu.Unlock()
}

// AddTool adds a tool to the context.
func (c *AgentContext) AddTool(tool Tool) {
	if tool == nil {
		return
	}
	name := tool.Name()
	for _, existing := range c.Tools {
		if existing == nil {
			continue
		}
		if existing.Name() == name {
			return
		}
	}
	c.Tools = append(c.Tools, tool)
}

// GetTool returns a tool by name, or nil if not found.
func (c *AgentContext) GetTool(name string) Tool {
	for _, tool := range c.Tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

// SetAllowedTools sets the whitelist of allowed tools.
// Pass nil to allow all tools (default behavior).
func (c *AgentContext) SetAllowedTools(tools []string) {
	if tools == nil {
		c.allowedTools = nil
		return
	}
	c.allowedTools = make(map[string]bool)
	for _, name := range tools {
		c.allowedTools[name] = true
	}
}

// IsToolAllowed checks if a tool is allowed to be executed.
// Returns true if allowedTools is nil (all allowed) or if the tool is in the whitelist.
func (c *AgentContext) IsToolAllowed(name string) bool {
	if c.allowedTools == nil {
		return true
	}
	return c.allowedTools[name]
}

// GetAllowedTools returns the list of allowed tool names.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedTools() []string {
	if c.allowedTools == nil {
		return nil
	}
	result := make([]string, 0, len(c.allowedTools))
	for name := range c.allowedTools {
		result = append(result, name)
	}
	return result
}

// GetAllowedToolsMap returns the internal allowed tools map for efficient lookup.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedToolsMap() map[string]bool {
	return c.allowedTools
}
