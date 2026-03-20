package compactcase

import (
	"encoding/json"
	"os"
	"strings"
)

type ContentBlock struct {
	Type string         `json:"type"`
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name,omitempty"`
	Text string         `json:"text,omitempty"`
	Args map[string]any `json:"arguments,omitempty"`
}

type Message struct {
	Role        string         `json:"role"`
	ToolCallID  string         `json:"toolCallId,omitempty"`
	ToolName    string         `json:"toolName,omitempty"`
	Content     []ContentBlock `json:"content,omitempty"`
	AgentVisible bool          `json:"agentVisible"`
}

func NewAssistantMessage(blocks ...ContentBlock) Message {
	return Message{Role: "assistant", Content: blocks, AgentVisible: true}
}

func NewToolResultMessage(toolCallID, toolName, text string) Message {
	return Message{
		Role:        "toolResult",
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		Content:     []ContentBlock{{Type: "text", Text: text}},
		AgentVisible: true,
	}
}

func ToolCall(id, name string, args map[string]any) ContentBlock {
	return ContentBlock{Type: "toolCall", ID: id, Name: name, Args: args}
}

func Text(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func (m Message) ExtractToolCalls() []ContentBlock {
	calls := make([]ContentBlock, 0)
	for _, block := range m.Content {
		if block.Type == "toolCall" {
			calls = append(calls, block)
		}
	}
	return calls
}

func ensureToolCallPairing(oldMessages, recentMessages []Message) []Message {
	if len(recentMessages) == 0 {
		return recentMessages
	}

	oldToolCallIDs := make(map[string]bool)
	for _, msg := range oldMessages {
		for _, tc := range msg.ExtractToolCalls() {
			if tc.ID != "" {
				oldToolCallIDs[tc.ID] = true
			}
		}
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			oldToolCallIDs[msg.ToolCallID] = true
		}
	}

	keptMessages := make([]Message, 0, len(recentMessages))
	for _, msg := range recentMessages {
		if msg.Role == "toolResult" && msg.ToolCallID != "" && oldToolCallIDs[msg.ToolCallID] {
			archived := msg
			archived.AgentVisible = false
			keptMessages = append(keptMessages, archived)
			continue
		}
		// BUG: assistant tool calls that belong to oldMessages are not filtered.
		keptMessages = append(keptMessages, msg)
	}

	return keptMessages
}

type traceDoc struct {
	TraceEvents []traceEvent `json:"traceEvents"`
}

type traceEvent struct {
	Name string         `json:"name"`
	Ts   float64        `json:"ts"`
	Args map[string]any `json:"args"`
}

func traceShowsCompactThenMismatch(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	var doc traceDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return false, err
	}

	compactTS := -1.0
	for _, ev := range doc.TraceEvents {
		if ev.Name == "tool_start" {
			if stringArg(ev.Args, "tool") != "llm_context_decision" {
				continue
			}
			if inner, ok := ev.Args["args"].(map[string]any); ok {
				if stringArg(inner, "decision") == "compact" {
					compactTS = ev.Ts
				}
			}
		}

		if ev.Name == "log:error" && compactTS >= 0 && ev.Ts > compactTS {
			errText := stringArg(ev.Args, "error")
			if strings.Contains(errText, "tool call result does not follow tool call") {
				return true, nil
			}
		}
	}

	return false, nil
}

func stringArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
