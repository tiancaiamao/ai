package rpc

import (
	"encoding/json"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
)

// RPCCommand represents a command received on stdin.
type RPCCommand struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Message string          `json:"message,omitempty"` // For convenience, direct message field
	Data    json.RawMessage `json:"data,omitempty"`
}

// PromptRequest captures prompt fields beyond the message body.
type PromptRequest struct {
	Message           string            `json:"message"`
	StreamingBehavior string            `json:"streamingBehavior,omitempty"`
	Images            []json.RawMessage `json:"images,omitempty"`
}

// RPCResponse represents a response sent to stdout.
type RPCResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Protocol command type constants.
// "prompt" is the only protocol-level command.
// All other commands (state queries, settings, actions, session management)
// are handled as slash commands via the prompt channel.
const (
	CommandPrompt = "prompt"
)

// SessionState represents the current session state.
type SessionState struct {
	Model                 *config.ModelInfo        `json:"model,omitempty"`
	ThinkingLevel         string                   `json:"thinkingLevel"`
	IsStreaming           bool                     `json:"isStreaming"`
	IsCompacting          bool                     `json:"isCompacting"`
	SteeringMode          string                   `json:"steeringMode"`
	FollowUpMode          string                   `json:"followUpMode"`
	SessionFile           string                   `json:"sessionFile,omitempty"`
	SessionID             string                   `json:"sessionId,omitempty"`
	SessionName           string                   `json:"sessionName,omitempty"`
	AIPid                 int                      `json:"aiPid,omitempty"`
	AILogPath             string                   `json:"aiLogPath,omitempty"`
	AIWorkingDir          string                   `json:"aiWorkingDir,omitempty"`  // Current working directory
	AIStartupPath         string                   `json:"aiStartupPath,omitempty"` // Git repository root (where session started)
	AutoCompactionEnabled bool                     `json:"autoCompactionEnabled"`
	MessageCount          int                      `json:"messageCount"`
	PendingMessageCount   int                      `json:"pendingMessageCount"`
	Compaction            *compact.CompactionState `json:"compaction,omitempty"`
}

// SlashCommand represents an available slash command for clients.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Location    string `json:"location,omitempty"`
	Path        string `json:"path,omitempty"`
}

// SessionTokenStats represents token usage statistics.
type SessionTokenStats struct {
	Input              int `json:"input"`
	Output             int `json:"output"`
	CacheRead          int `json:"cacheRead"`
	CacheWrite         int `json:"cacheWrite"`
	Total              int `json:"total"`
	ActiveWindowTokens int `json:"activeWindowTokens"` // Active turn window tokens (for % calculation)
	SystemPromptTokens int `json:"systemPromptTokens"` // Estimated system prompt tokens
	SystemToolsTokens  int `json:"systemToolsTokens"`  // Estimated system tools tokens
}

// SessionStats represents usage statistics for a session.
type SessionStats struct {
	SessionFile       string            `json:"sessionFile"`
	SessionID         string            `json:"sessionId"`
	UserMessages      int               `json:"userMessages"`
	AssistantMessages int               `json:"assistantMessages"`
	ToolCalls         int               `json:"toolCalls"`
	ToolResults       int               `json:"toolResults"`
	TotalMessages     int               `json:"totalMessages"`
	CompactionCount   int               `json:"compactionCount"`
	Tokens            SessionTokenStats `json:"tokens"`
	Cost              float64           `json:"cost"`
	// Workspace is the git repo root path (the path bound at startup)
	Workspace string `json:"workspace,omitempty"`
	// CurrentWorkdir is the current working directory path
	CurrentWorkdir string `json:"currentWorkdir,omitempty"`
}

// ForkMessage represents a message candidate for forking.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// TreeEntry represents a session entry in tree order for navigation.
type TreeEntry struct {
	EntryID   string  `json:"entryId"`
	ParentID  *string `json:"parentId,omitempty"`
	Type      string  `json:"type"`
	Role      string  `json:"role,omitempty"`
	Text      string  `json:"text,omitempty"`
	Timestamp string  `json:"timestamp,omitempty"`
	Depth     int     `json:"depth"`
	Leaf      bool    `json:"leaf,omitempty"`
}

// ForkResult represents the result of a fork operation.
type ForkResult struct {
	Cancelled bool   `json:"cancelled"`
	Text      string `json:"text,omitempty"`
}

// CycleModelResult represents a cycle_model response payload.
type CycleModelResult struct {
	Model          config.ModelInfo `json:"model"`
	ThinkingLevel  string           `json:"thinkingLevel,omitempty"`
	IsScoped       bool             `json:"isScoped,omitempty"`
	ScopedProvider string           `json:"scopedProvider,omitempty"`
}

// FormattedMessage represents a summarized message for display purposes.
type FormattedMessage struct {
	Index     int      `json:"index"`
	Role      string   `json:"role"`
	Preview   string   `json:"preview"`
	ToolCalls []string `json:"toolCalls,omitempty"`
	ToolName  string   `json:"toolName,omitempty"`
	IsError   bool     `json:"isError,omitempty"`
}

// MessagesResult represents the result of the /messages slash command.
type MessagesResult struct {
	Total    int                `json:"total"`
	Showing  int                `json:"showing"`
	Messages []FormattedMessage `json:"messages"`
}

// BashResult represents a shell execution result.
type BashResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// CompactResult represents the result of a compaction operation.
type CompactResult struct {
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`
	TokensAfter      int    `json:"tokensAfter,omitempty"`
}

// WorkflowTask represents a single task in a workflow.
type WorkflowTask struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"` // pending, in_progress, done, failed
}

// WorkflowState represents the current workflow execution state.
type WorkflowState struct {
	Phase          string        `json:"phase"` // init, worker, completed, error
	TasksFile      string        `json:"tasksFile,omitempty"`
	TotalTasks     int           `json:"totalTasks"`
	PendingTasks   int           `json:"pendingTasks"`
	DoneTasks      int           `json:"doneTasks"`
	FailedTasks    int           `json:"failedTasks"`
	InProgressTask *WorkflowTask `json:"inProgressTask,omitempty"`
	StartedAt      string        `json:"startedAt,omitempty"`
	LastUpdate     string        `json:"lastUpdate,omitempty"`
	Error          string        `json:"error,omitempty"`
}
