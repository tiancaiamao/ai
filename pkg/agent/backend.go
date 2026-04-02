package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ---------------------------------------------------------------------------
// Command Registry — unified mechanism for registering commands
// ---------------------------------------------------------------------------

// CommandHandler processes a command and returns a result.
// The raw command payload is in cmd.Payload (json.RawMessage).
// The handler is responsible for unmarshaling its own payload type.
type CommandHandler func(ctx context.Context, cmd Command) (any, error)

// Command represents a generic command invocation.
type Command struct {
	Name    string          // Command name (e.g. "prompt", "steer", "fork")
	Payload json.RawMessage // Raw JSON payload
}

// CommandMeta provides metadata for a command (used for discovery/help).
type CommandMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // "builtin", "skill", "extension"
}

// CommandRegistry maps command names to handlers.
type CommandRegistry struct {
	mu       sync.RWMutex
	handlers map[string]CommandHandler
	meta     map[string]CommandMeta
}

// NewCommandRegistry creates a new command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		handlers: make(map[string]CommandHandler),
		meta:     make(map[string]CommandMeta),
	}
}

// Register adds a command handler with metadata.
func (r *CommandRegistry) Register(name string, handler CommandHandler, meta CommandMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = handler
	r.meta[name] = meta
}

// Lookup finds the handler for a command name.
func (r *CommandRegistry) Lookup(name string) (CommandHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

// Handle dispatches a command by name.
func (r *CommandRegistry) Handle(ctx context.Context, cmd Command) (any, error) {
	handler, ok := r.Lookup(cmd.Name)
	if !ok {
		return nil, ErrCommandNotFound{Command: cmd.Name}
	}
	return handler(ctx, cmd)
}

// ListCommands returns all registered command metadata, sorted by name.
func (r *CommandRegistry) ListCommands() []CommandMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]CommandMeta, 0, len(r.meta))
	for _, m := range r.meta {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListNames returns registered command names, sorted.
func (r *CommandRegistry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// Tool Registry — type alias to agentctx.ToolRegistry
// ---------------------------------------------------------------------------

// ToolRegistry is an alias for agentctx.ToolRegistry.
// The canonical definition lives in pkg/context/tool_registry.go.
type ToolRegistry = agentctx.ToolRegistry

// NewToolRegistry creates a new tool registry.
// Delegates to agentctx.NewToolRegistry.
var NewToolRegistry = agentctx.NewToolRegistry

// ---------------------------------------------------------------------------
// AgentBackend — transport-agnostic agent interface
//
// This is the primary interface for consuming ai as an SDK.
// Core operations (Prompt, Steer, Abort, session management, model control)
// are interface methods.
// Extended operations (fork, bash, export, custom commands) go through
// HandleCommand which delegates to the CommandRegistry.
// ---------------------------------------------------------------------------

// AgentBackend defines what an agent wrapper can do.
// Implementations wrap AgentNew (or Agent) and add session/command management.
type AgentBackend interface {
	// --- Core conversation ---
	Prompt(ctx context.Context, message string) error
	Steer(ctx context.Context, message string) error
	FollowUp(ctx context.Context, message string) error
	Abort() error

	// --- Session management ---
	NewSession(name, title string) (string, error)
	ClearSession() error
	ListSessions() ([]any, error)
	SwitchSession(id string) error
	DeleteSession(id string) error

	// --- Model ---
	GetAvailableModels() ([]BackendModelInfo, error)
	SetModel(provider, modelID string) (*BackendModelInfo, error)

	// --- State ---
	GetState() (*BackendState, error)
	GetMessages() []any

	// --- Compaction ---
	Compact() (*BackendCompactResult, error)
	SetAutoCompaction(enabled bool) error

	// --- Thinking ---
	SetThinkingLevel(level string) (string, error)

	// --- Extension point ---
	// HandleCommand dispatches arbitrary commands through the CommandRegistry.
	// This is the escape hatch for everything not in the interface:
	// fork, bash, export_html, trace_events, custom extensions, etc.
	HandleCommand(ctx context.Context, cmd Command) (any, error)

	// --- Discovery ---
	GetCommands() ([]CommandMeta, error)

	// --- Lifecycle ---
	Close() error
}

// BackendState is the transport-agnostic session state.
type BackendState struct {
	ModelID               string `json:"modelId"`
	ModelProvider         string `json:"modelProvider"`
	ThinkingLevel         string `json:"thinkingLevel"`
	IsStreaming           bool   `json:"isStreaming"`
	IsCompacting          bool   `json:"isCompacting"`
	SteeringMode          string `json:"steeringMode"`
	FollowUpMode          string `json:"followUpMode"`
	SessionID             string `json:"sessionId,omitempty"`
	SessionName           string `json:"sessionName,omitempty"`
	AutoCompactionEnabled bool   `json:"autoCompactionEnabled"`
	MessageCount          int    `json:"messageCount"`
}

// BackendModelInfo describes a model available to the backend.
type BackendModelInfo struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
}

// BackendCompactResult represents the result of a compaction operation.
type BackendCompactResult struct {
	TokensBefore int `json:"tokensBefore,omitempty"`
	TokensAfter  int `json:"tokensAfter,omitempty"`
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// ErrCommandNotFound is returned when a command is not registered.
type ErrCommandNotFound struct {
	Command string
}

func (e ErrCommandNotFound) Error() string {
	return fmt.Sprintf("command not found: %s", e.Command)
}
