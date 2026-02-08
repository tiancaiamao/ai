package agent

// AgentEvent represents an event emitted during agent execution.
type AgentEvent struct {
	Type string `json:"type"` // Event type discriminator

	// Common fields
	Message  *AgentMessage  `json:"message,omitempty"`
	Messages []AgentMessage `json:"messages,omitempty"`

	// turn_start/turn_end
	ToolResults []AgentMessage `json:"toolResults,omitempty"`

	// tool_execution_start/tool_execution_end
	ToolCallID string                 `json:"toolCallId,omitempty"`
	ToolName   string                 `json:"toolName,omitempty"`
	Args       map[string]interface{} `json:"args,omitempty"`
	Result     *AgentMessage          `json:"result,omitempty"`
	IsError    bool                   `json:"isError,omitempty"`

	// message_update
	AssistantMessageEvent interface{} `json:"assistantMessageEvent,omitempty"`
}

// AssistantMessageEvent provides a stable, json-tagged shape for streaming updates.
type AssistantMessageEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex,omitempty"`
	Delta        string `json:"delta,omitempty"`
	Content      string `json:"content,omitempty"`
}

// Event type constants
const (
	EventAgentStart         = "agent_start"
	EventAgentEnd           = "agent_end"
	EventTurnStart          = "turn_start"
	EventTurnEnd            = "turn_end"
	EventMessageStart       = "message_start"
	EventMessageEnd         = "message_end"
	EventMessageUpdate      = "message_update"
	EventToolExecutionStart = "tool_execution_start"
	EventToolExecutionEnd   = "tool_execution_end"
	EventTextDelta          = "text_delta"
	EventToolCallDelta      = "tool_call_delta"
	EventThinkingDelta      = "thinking_delta"
)

// NewAgentStartEvent creates an agent_start event.
func NewAgentStartEvent() AgentEvent {
	return AgentEvent{Type: EventAgentStart}
}

// NewAgentEndEvent creates an agent_end event.
func NewAgentEndEvent(messages []AgentMessage) AgentEvent {
	return AgentEvent{
		Type:     EventAgentEnd,
		Messages: messages,
	}
}

// NewTurnStartEvent creates a turn_start event.
func NewTurnStartEvent() AgentEvent {
	return AgentEvent{Type: EventTurnStart}
}

// NewTurnEndEvent creates a turn_end event.
func NewTurnEndEvent(message *AgentMessage, toolResults []AgentMessage) AgentEvent {
	return AgentEvent{
		Type:        EventTurnEnd,
		Message:     message,
		ToolResults: toolResults,
	}
}

// NewMessageStartEvent creates a message_start event.
func NewMessageStartEvent(message AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageStart,
		Message: &message,
	}
}

// NewMessageEndEvent creates a message_end event.
func NewMessageEndEvent(message AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageEnd,
		Message: &message,
	}
}

// NewMessageUpdateEvent creates a message_update event.
func NewMessageUpdateEvent(message AgentMessage, assistantEvent interface{}) AgentEvent {
	return AgentEvent{
		Type:                  EventMessageUpdate,
		Message:               &message,
		AssistantMessageEvent: assistantEvent,
	}
}

// NewToolExecutionStartEvent creates a tool_execution_start event.
func NewToolExecutionStartEvent(toolCallID, toolName string, args map[string]interface{}) AgentEvent {
	return AgentEvent{
		Type:       EventToolExecutionStart,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
	}
}

// NewToolExecutionEndEvent creates a tool_execution_end event.
func NewToolExecutionEndEvent(toolCallID, toolName string, result *AgentMessage, isError bool) AgentEvent {
	return AgentEvent{
		Type:       EventToolExecutionEnd,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Result:     result,
		IsError:    isError,
	}
}
