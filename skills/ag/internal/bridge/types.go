// Package bridge manages the lifecycle of a single ai --mode rpc process.
// Each agent gets a bridge process running in tmux that holds the ai RPC pipes
// and exposes a Unix domain socket for control-plane commands (steer, abort, etc.).
package bridge

// AgentStatus represents the current status of an agent.
type AgentStatus string

const (
	StatusRunning AgentStatus = "running"
	StatusDone    AgentStatus = "done"
	StatusFailed  AgentStatus = "failed"
	StatusKilled  AgentStatus = "killed"
	StatusIdle    AgentStatus = "idle" // agent was aborted but not terminated
)

// AgentActivity is written to activity.json by the bridge.
// It provides real-time status for ag agent status commands.
// All fields are populated incrementally as events arrive from ai --mode rpc.
type AgentActivity struct {
	Status     AgentStatus `json:"status"`
	Pid        int         `json:"pid,omitempty"`
	StartedAt  int64       `json:"startedAt,omitempty"`
	FinishedAt int64       `json:"finishedAt,omitempty"`

	// Incremental counters from event stream
	Turns       int   `json:"turns"`
	TokensIn    int64 `json:"tokensIn"`
	TokensOut   int64 `json:"tokensOut"`
	TokensTotal int64 `json:"tokensTotal"`

	// Last observed activity
	LastTool string `json:"lastTool,omitempty"`
	LastText string `json:"lastText,omitempty"`

	// Backend name (e.g., "ai", "codex", "claude")
	Backend string `json:"backend,omitempty"`

	// Error message (only set on failed status)
	Error string `json:"error,omitempty"`
}

// BridgeCommand is sent over the Unix socket by CLI tools (ag agent steer/abort/prompt).
type BridgeCommand struct {
	Type    string `json:"type"`              // steer, abort, prompt, get_state, shutdown
	Message string `json:"message,omitempty"` // for steer and prompt
}

// BridgeResponse is sent back over the Unix socket after processing a BridgeCommand.
type BridgeResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// SpawnConfig is stored in meta.json by the spawn command and read by the bridge.
type SpawnConfig struct {
	ID        string `json:"id"`
	System    string `json:"system,omitempty"`
	Input     string `json:"input,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Backend   string `json:"backend,omitempty"`
	StartedAt int64  `json:"startedAt"`
}

// Bridge command type constants
const (
	CmdSteer     = "steer"
	CmdAbort     = "abort"
	CmdPrompt    = "prompt"
	CmdGetState  = "get_state"
	CmdShutdown  = "shutdown"
)