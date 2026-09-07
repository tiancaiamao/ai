package context

// LLMRequestFingerprint captures the cache-relevant shape of an LLM request
// (model, system prompt, per-message content hashes, tools) so consecutive
// requests can be compared to detect provider prefix-cache misses.
type LLMRequestFingerprint struct {
	Model      string
	SystemHash uint64
	ToolsHash  uint64
	MsgHashes  []uint64
}

// AgentState represents system-maintained metadata about the agent state.
// Most fields are recomputed from RecentMessages every turn
// (see injectRuntimeMeta); persistence is not required.
type AgentState struct {
	// Workspace
	WorkspaceRoot     string
	CurrentWorkingDir string

	// Statistics (recomputed each turn from RecentMessages)
	TotalTurns  int
	TokensUsed  int
	TokensLimit int

	// Tracking
	// ToolCallsSinceLastTrigger drives the LLMDecide ask interval.
	ToolCallsSinceLastTrigger int

	// Runtime metadata (cached snapshot, rebuilt on band heartbeat)
	RuntimeMetaTurns    int
	RuntimeMetaSnapshot string
	RuntimeMetaBand     string

	// Prefix-cache miss detection: fingerprint of the most recent LLM request
	// (nil = cold start). PendingCacheResetReason explains a cleared
	// fingerprint (e.g. compaction replaced history) so the next check
	// reports a known reset instead of a divergence.
	LastLLMRequest          *LLMRequestFingerprint
	PendingCacheResetReason string
}

// NewAgentState creates a new AgentState rooted at cwd.
func NewAgentState(cwd string) *AgentState {
	return &AgentState{
		WorkspaceRoot:             cwd,
		CurrentWorkingDir:         cwd,
		ToolCallsSinceLastTrigger: 0,
	}
}

// ResetLLMRequestFingerprint clears the last LLM request fingerprint, marking
// the next prefix-cache check as a known cold start with the given reason.
// Nil-safe.
func (s *AgentState) ResetLLMRequestFingerprint(reason string) {
	if s == nil {
		return
	}
	s.LastLLMRequest = nil
	s.PendingCacheResetReason = reason
}
