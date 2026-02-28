package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"time"
)

// AgentEvent represents an event emitted during agent execution.
type AgentEvent struct {
	Type string `json:"type"` // Event type discriminator
	// EventAt is when the event was created (UnixNano).
	EventAt int64 `json:"eventAt,omitempty"`

	// error
	Error      string `json:"error,omitempty"`
	ErrorStack string `json:"errorStack,omitempty"`

	// Common fields
	Message  *agentctx.AgentMessage  `json:"message,omitempty"`
	Messages []agentctx.AgentMessage `json:"messages,omitempty"`

	// turn_start/turn_end
	ToolResults []agentctx.AgentMessage `json:"toolResults,omitempty"`

	// tool_execution_start/tool_execution_end
	ToolCallID string                 `json:"toolCallId,omitempty"`
	ToolName   string                 `json:"toolName,omitempty"`
	Args       map[string]interface{} `json:"args,omitempty"`
	Result     *agentctx.AgentMessage          `json:"result,omitempty"`
	IsError    bool                   `json:"isError,omitempty"`

	// message_update
	AssistantMessageEvent interface{} `json:"assistantMessageEvent,omitempty"`

	// compaction events
	Compaction *CompactionInfo `json:"compaction,omitempty"`

	// loop guard events
	LoopGuard *LoopGuardInfo `json:"loopGuard,omitempty"`

	// tool-call recovery events
	ToolCallRecovery *ToolCallRecoveryInfo `json:"toolCallRecovery,omitempty"`
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
	EventLoopGuardTriggered = "loop_guard_triggered"
	EventToolCallRecovery   = "tool_call_recovery"
	EventError              = "error"
)

// CompactionInfo describes a compaction event.
type CompactionInfo struct {
	Auto    bool   `json:"auto,omitempty"`
	Before  int    `json:"before,omitempty"`
	After   int    `json:"after,omitempty"`
	Error   string `json:"error,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// LoopGuardInfo describes why tool-loop protection interrupted a turn.
type LoopGuardInfo struct {
	Reason string `json:"reason,omitempty"`
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

// NewLoopGuardTriggeredEvent creates a loop_guard_triggered event.
func NewLoopGuardTriggeredEvent(info LoopGuardInfo) AgentEvent {
	return AgentEvent{
		Type:      EventLoopGuardTriggered,
		EventAt:   time.Now().UnixNano(),
		LoopGuard: &info,
	}
}

// NewErrorEvent creates an error event.
func NewErrorEvent(err error) AgentEvent {
	err = WithErrorStack(err)
	message := ""
	stack := ""
	if err != nil {
		message = err.Error()
		stack = ErrorStack(err)
	}
	return AgentEvent{
		Type:       EventError,
		EventAt:    time.Now().UnixNano(),
		Error:      message,
		ErrorStack: stack,
	}
}

// NewAgentStartEvent creates an agent_start event.
func NewAgentStartEvent() AgentEvent {
	return AgentEvent{Type: EventAgentStart, EventAt: time.Now().UnixNano()}
}

// NewAgentEndEvent creates an agent_end event.
func NewAgentEndEvent(messages []agentctx.AgentMessage) AgentEvent {
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
func NewTurnEndEvent(message *agentctx.AgentMessage, toolResults []agentctx.AgentMessage) AgentEvent {
	return AgentEvent{
		Type:        EventTurnEnd,
		EventAt:     time.Now().UnixNano(),
		Message:     message,
		ToolResults: toolResults,
	}
}

// NewMessageStartEvent creates a message_start event.
func NewMessageStartEvent(message agentctx.AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageStart,
		EventAt: time.Now().UnixNano(),
		Message: &message,
	}
}

// NewMessageEndEvent creates a message_end event.
func NewMessageEndEvent(message agentctx.AgentMessage) AgentEvent {
	return AgentEvent{
		Type:    EventMessageEnd,
		EventAt: time.Now().UnixNano(),
		Message: &message,
	}
}

// NewMessageUpdateEvent creates a message_update event.
func NewMessageUpdateEvent(message agentctx.AgentMessage, assistantEvent interface{}) AgentEvent {
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
func NewToolExecutionEndEvent(toolCallID, toolName string, result *agentctx.AgentMessage, isError bool) AgentEvent {
	return AgentEvent{
		Type:       EventToolExecutionEnd,
		EventAt:    time.Now().UnixNano(),
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Result:     result,
		IsError:    isError,
	}
}

// ToolCallRecoveryInfo describes a malformed tool-call recovery event.
type ToolCallRecoveryInfo struct {
	Reason  string `json:"reason,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

// NewToolCallRecoveryEvent creates a tool_call_recovery event.
func NewToolCallRecoveryEvent(info ToolCallRecoveryInfo) AgentEvent {
	return AgentEvent{
		Type:             EventToolCallRecovery,
		EventAt:          time.Now().UnixNano(),
		ToolCallRecovery: &info,
	}
}
