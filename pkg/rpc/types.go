package rpc

import "encoding/json"

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

// Command type constants
const (
	CommandPrompt               = "prompt"
	CommandSteer                = "steer"
	CommandFollowUp             = "follow_up"
	CommandAbort                = "abort"
	CommandNewSession           = "new_session"
	CommandClearSession         = "clear_session"
	CommandListSessions         = "list_sessions"
	CommandSwitchSession        = "switch_session"
	CommandDeleteSession        = "delete_session"
	CommandGetState             = "get_state"
	CommandGetMessages          = "get_messages"
	CommandCompact              = "compact"
	CommandGetAvailableModels   = "get_available_models"
	CommandSetModel             = "set_model"
	CommandCycleModel           = "cycle_model"
	CommandGetCommands          = "get_commands"
	CommandGetSessionStats      = "get_session_stats"
	CommandSetAutoCompaction    = "set_auto_compaction"
	CommandSetThinkingLevel     = "set_thinking_level"
	CommandCycleThinkingLevel   = "cycle_thinking_level"
	CommandGetLastAssistantText = "get_last_assistant_text"
	CommandGetForkMessages      = "get_fork_messages"
	CommandFork                 = "fork"
	CommandGetTree              = "get_tree"
	CommandResumeOnBranch       = "resume_on_branch"
	CommandSetSteeringMode      = "set_steering_mode"
	CommandSetFollowUpMode      = "set_follow_up_mode"
	CommandSetSessionName       = "set_session_name"
	CommandSetAutoRetry         = "set_auto_retry"
	CommandAbortRetry           = "abort_retry"
	CommandBash                 = "bash"
	CommandAbortBash            = "abort_bash"
	CommandExportHTML           = "export_html"
	CommandPing                 = "ping"
)

// SessionState represents the current session state.
type SessionState struct {
	Model                 *ModelInfo       `json:"model,omitempty"`
	ThinkingLevel         string           `json:"thinkingLevel"`
	IsStreaming           bool             `json:"isStreaming"`
	IsCompacting          bool             `json:"isCompacting"`
	SteeringMode          string           `json:"steeringMode"`
	FollowUpMode          string           `json:"followUpMode"`
	SessionFile           string           `json:"sessionFile,omitempty"`
	SessionID             string           `json:"sessionId,omitempty"`
	SessionName           string           `json:"sessionName,omitempty"`
	AIPid                 int              `json:"aiPid,omitempty"`
	AILogPath             string           `json:"aiLogPath,omitempty"`
	AIWorkingDir          string           `json:"aiWorkingDir,omitempty"`
	AutoCompactionEnabled bool             `json:"autoCompactionEnabled"`
	MessageCount          int              `json:"messageCount"`
	PendingMessageCount   int              `json:"pendingMessageCount"`
	Compaction            *CompactionState `json:"compaction,omitempty"`
}

// CompactionState represents compaction thresholds and settings.
type CompactionState struct {
	MaxMessages      int    `json:"maxMessages,omitempty"`
	MaxTokens        int    `json:"maxTokens,omitempty"`
	KeepRecent       int    `json:"keepRecent,omitempty"`
	KeepRecentTokens int    `json:"keepRecentTokens,omitempty"`
	ReserveTokens    int    `json:"reserveTokens,omitempty"`
	ContextWindow    int    `json:"contextWindow,omitempty"`
	TokenLimit       int    `json:"tokenLimit,omitempty"`
	TokenLimitSource string `json:"tokenLimitSource,omitempty"`
}

// ModelInfo represents a model description for RPC clients.
type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	API           string   `json:"api"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
}

// SlashCommand describes an available slash command for clients.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Location    string `json:"location,omitempty"`
	Path        string `json:"path,omitempty"`
}

// SessionTokenStats represents token usage statistics.
type SessionTokenStats struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
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
	Tokens            SessionTokenStats `json:"tokens"`
	Cost              float64           `json:"cost"`
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
	Model          ModelInfo `json:"model"`
	ThinkingLevel  string    `json:"thinkingLevel,omitempty"`
	IsScoped       bool      `json:"isScoped,omitempty"`
	ScopedProvider string    `json:"scopedProvider,omitempty"`
}

// BashResult represents a shell execution result.
type BashResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// CompactResult represents the result of a compaction operation.
type CompactResult struct {
	Summary          string `json:"summary,omitempty"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`
	TokensAfter      int    `json:"tokensAfter,omitempty"`
}
