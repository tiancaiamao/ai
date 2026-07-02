package agent

import (
	"encoding/json"

	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// ConvertLLMMessageToAgent converts an LLM message to an agent message.
//
// This is a fallback path for non-streaming LLM responses. In normal operation,
// the agent builds messages incrementally from streaming deltas (LLMTextDeltaEvent,
// LLMThinkingDeltaEvent, LLMToolCallDeltaEvent), which populates partialMessage.
// This function is only used when the LLM returns a complete message in a single
// event (LLMDoneEvent with Message field populated), which typically occurs with
// non-streaming APIs.
//
// Coverage: 0% in actual usage because streaming responses are the default.
// This code path is kept for compatibility with non-streaming providers.
func ConvertLLMMessageToAgent(llmMsg llm.LLMMessage) agentctx.AgentMessage {
	agentMsg := agentctx.NewAssistantMessage()
	agentMsg.Content = []agentctx.ContentBlock{}

	// Add thinking content first (for reasoning models)
	if llmMsg.Thinking != "" {
		agentMsg.Content = append(agentMsg.Content, agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: llmMsg.Thinking,
		})
	}

	// Add text content
	if llmMsg.Content != "" {
		agentMsg.Content = append(agentMsg.Content, agentctx.TextContent{
			Type: "text",
			Text: llmMsg.Content,
		})
	}

	// Add tool calls
	if len(llmMsg.ToolCalls) > 0 {
		for _, tc := range llmMsg.ToolCalls {
			// Parse arguments JSON string
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = make(map[string]any)
			}

			agentMsg.Content = append(agentMsg.Content, agentctx.ToolCallContent{
				ID:        tc.ID,
				Type:      "toolCall",
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	}

	return agentMsg
}
