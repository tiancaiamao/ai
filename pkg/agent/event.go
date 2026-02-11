package agent

import "time"

// AgentEvent represents an event emitted during agent execution.
type AgentEvent struct {
	Type string `json:"type"` // Event type discriminator
	// EventAt is when the event was created (UnixNano).
	EventAt int64 `json:"eventAt,omitempty"`

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

	// compaction events
	Compaction *CompactionInfo `json:"compaction,omitempty"`
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
	EventCompactionStart    = "compaction_start"
	EventCompactionEnd      = "compaction_end"
)

// CompactionInfo describes a compaction event.
type CompactionInfo struct {
	Auto    bool   `json:"auto,omitempty"`
	Before  int    `json:"before,omitempty"`
	After   int    `json:"after,omitempty"`
	Error   string `json:"error,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// NewCompactionStartEvent creates a compaction_start event.
func NewCompactionStartEvent(info CompactionInfo) AgentEvent {
	return AgentEvent{
		Type:       EventCompactionStart,
		EventAt:    time.Now().UnixNano(),
		Compaction: &info,
	}
}

// NewCompactionEndEvent creates a compaction_end event.
func NewCompactionEndEvent(info CompactionInfo) AgentEvent {
	return AgentEvent{
		Type:       EventCompactionEnd,
		EventAt:    time.Now().UnixNano(),
		Compaction: &info,
	}
}

// NewAgentStartEvent creates an agent_start event.
func NewAgentStartEvent() AgentEvent {
	return AgentEvent{Type: EventAgentStart, EventAt: time.Now().UnixNano()}
}

// NewAgentEndEvent creates an agent_end event.
func NewAgentEndEvent(messages []AgentMessage) AgentEvent {
	return AgentEvent{
		Type:     EventAgentEnd,
		EventAt:  time.Now().UnixNano(),
		Messages: messages,
	}
}

// NewTurnStartEvent creates a turn_start event.
func NewTurnStartEvent() AgentEvent {
	return AgentEvent{Type: EventTurnStart, EventAt: time.Now().UnixNano()}
}

// NewTurnEndEvent creates a turn_end event.
func NewTurnEndEvent(message *AgentMessage, toolResults []AgentMessage) AgentEvent {
	return AgentEvent{
		Type:        EventTurnEnd,
		EventAt:     time.Now().UnixNano(),
		Message:     message,
		ToolResults: toolResults,
	}
}

// NewMessageStartEvent creates a message_start event.
func NewMessageStartEvent(message AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageStart,
		EventAt: time.Now().UnixNano(),
		Message: &message,
	}
}

// NewMessageEndEvent creates a message_end event.
func NewMessageEndEvent(message AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageEnd,
		EventAt: time.Now().UnixNano(),
		Message: &message,
	}
}

// NewMessageUpdateEvent creates a message_update event.
func NewMessageUpdateEvent(message AgentMessage, assistantEvent interface{}) AgentEvent {
	return AgentEvent{
		Type:                  EventMessageUpdate,
		EventAt:               time.Now().UnixNano(),
		Message:               &message,
		AssistantMessageEvent: assistantEvent,
	}
}

// NewToolExecutionStartEvent creates a tool_execution_start event.
func NewToolExecutionStartEvent(toolCallID, toolName string, args map[string]interface{}) AgentEvent {
	return AgentEvent{
		Type:       EventToolExecutionStart,
		EventAt:    time.Now().UnixNano(),
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
	}
}

// NewToolExecutionEndEvent creates a tool_execution_end event.
func NewToolExecutionEndEvent(toolCallID, toolName string, result *AgentMessage, isError bool) AgentEvent {
	return AgentEvent{
		Type:       EventToolExecutionEnd,
		EventAt:    time.Now().UnixNano(),
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Result:     result,
		IsError:    isError,
	}
}
