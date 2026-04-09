package context

// ContextSnapshot represents the current state of the conversation context.
// It is the in-memory structure reconstructed from checkpoint + journal replay,
// analogous to a database snapshot rebuilt from redo logs.
type ContextSnapshot struct {
	// LLMContext - LLM-maintained structured context
	LLMContext string

	// RecentMessages - Recent conversation history
	RecentMessages []AgentMessage

	// AgentState - System-maintained metadata
	AgentState *AgentState
}

// NewContextSnapshot creates a new empty snapshot.
func NewContextSnapshot(sessionID, cwd string) *ContextSnapshot {
	return &ContextSnapshot{
		LLMContext:     "",
		RecentMessages: []AgentMessage{},
		AgentState:     NewAgentState(sessionID, cwd),
	}
}

// Clone creates a deep copy of the snapshot.
func (s *ContextSnapshot) Clone() *ContextSnapshot {
	if s == nil {
		return nil
	}

	messages := make([]AgentMessage, len(s.RecentMessages))
	for i, msg := range s.RecentMessages {
		contentBlocks := make([]ContentBlock, len(msg.Content))
		copy(contentBlocks, msg.Content)

		messages[i] = AgentMessage{
			Role:         msg.Role,
			Content:      contentBlocks,
			Timestamp:    msg.Timestamp,
			Metadata:     msg.Metadata,
			API:          msg.API,
			Provider:     msg.Provider,
			Model:        msg.Model,
			Usage:        msg.Usage,
			StopReason:   msg.StopReason,
			ToolCallID:   msg.ToolCallID,
			ToolName:     msg.ToolName,
			IsError:      msg.IsError,
			Truncated:    msg.Truncated,
			TruncatedAt:  msg.TruncatedAt,
			OriginalSize: msg.OriginalSize,
		}
	}

		return &ContextSnapshot{
		LLMContext:     s.LLMContext,
		RecentMessages: messages,
		AgentState:     s.AgentState.Clone(),
	}
}
