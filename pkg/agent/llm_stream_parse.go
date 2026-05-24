package agent

import (
	"encoding/json"
	"sort"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// StreamChunkEventType classifies what a processed chunk produced.
type StreamChunkEventType int

const (
	// ChunkStart indicates an LLMStartEvent was processed.
	ChunkStart StreamChunkEventType = iota
	// ChunkTextDelta indicates a text content delta.
	ChunkTextDelta
	// ChunkThinkingDelta indicates a thinking/reasoning delta.
	ChunkThinkingDelta
	// ChunkToolCallDelta indicates a tool call delta.
	ChunkToolCallDelta
	// ChunkDone indicates stream completion.
	ChunkDone
	// ChunkError indicates an error event.
	ChunkError
	// ChunkIgnored means the event was received but produced no state change.
	ChunkIgnored
)

// toolCallState tracks incremental tool call assembly across stream deltas.
type toolCallState struct {
	id        string
	name      string
	callType  string
	arguments string
}

// StreamChunkState holds mutable accumulation state across stream chunks.
// It is updated by processStreamChunk and can be freely inspected after each call.
type StreamChunkState struct {
	TextBuilder     strings.Builder
	ThinkingBuilder strings.Builder
	ToolCalls       map[int]*toolCallState
	Started         bool // true after LLMStartEvent processed
	FirstTokenSeen  bool // true after first non-trivial delta
}

// NewStreamChunkState returns a zero-value state ready for chunk processing.
func NewStreamChunkState() *StreamChunkState {
	return &StreamChunkState{
		ToolCalls: make(map[int]*toolCallState),
	}
}

// StreamChunkResult describes the outcome of processing a single stream chunk.
type StreamChunkResult struct {
	EventType StreamChunkEventType

	// Fields populated for ChunkTextDelta / ChunkThinkingDelta
	Delta        string
	ContentIndex int

	// Fields populated for ChunkDone
	StopReason string
	Usage      llm.Usage
	DoneEvent  llm.LLMDoneEvent // full event for caller inspection

	// Fields populated for ChunkError
	Error error

	// Content is the current snapshot of accumulated content blocks
	// after processing this chunk. Always populated when Started is true.
	Content []agentctx.ContentBlock
}

// processStreamChunk updates state based on a single LLM event and returns
// a result describing what happened. It is a pure function: no side effects,
// no context, no stream pushing. The caller is responsible for trace logging
// and stream event emission based on the result.
//
// thinkingLevel "off" suppresses thinking delta accumulation.
func processStreamChunk(state *StreamChunkState, event llm.LLMEvent, thinkingLevel string) StreamChunkResult {
	switch e := event.(type) {
	case llm.LLMStartEvent:
		state.TextBuilder.Reset()
		state.ThinkingBuilder.Reset()
		state.ToolCalls = make(map[int]*toolCallState)
		state.Started = true
		state.FirstTokenSeen = false
		return StreamChunkResult{
			EventType: ChunkStart,
		}

	case llm.LLMTextDeltaEvent:
		if !state.Started {
			return StreamChunkResult{EventType: ChunkIgnored}
		}
		state.FirstTokenSeen = true
		state.TextBuilder.WriteString(e.Delta)
		content := buildContentBlocks(state.TextBuilder.String(), state.ThinkingBuilder.String(), state.ToolCalls)
		return StreamChunkResult{
			EventType:    ChunkTextDelta,
			Delta:        e.Delta,
			ContentIndex: e.Index,
			Content:      content,
		}

	case llm.LLMThinkingDeltaEvent:
		return processThinkingDelta(state, e.Delta, e.Index, thinkingLevel)

	case llm.LLMToolCallDeltaEvent:
		if !state.Started {
			return StreamChunkResult{EventType: ChunkIgnored}
		}
		state.FirstTokenSeen = true
		call, ok := state.ToolCalls[e.Index]
		if !ok {
			call = &toolCallState{}
			state.ToolCalls[e.Index] = call
		}
		if e.ToolCall.ID != "" {
			call.id = e.ToolCall.ID
		}
		if e.ToolCall.Type != "" {
			call.callType = e.ToolCall.Type
		}
		if e.ToolCall.Function.Name != "" {
			call.name = e.ToolCall.Function.Name
		}
		if e.ToolCall.Function.Arguments != "" {
			call.arguments += e.ToolCall.Function.Arguments
		}
		content := buildContentBlocks(state.TextBuilder.String(), state.ThinkingBuilder.String(), state.ToolCalls)
		return StreamChunkResult{
			EventType:    ChunkToolCallDelta,
			ContentIndex: e.Index,
			Content:      content,
		}

	case llm.LLMDoneEvent:
		return StreamChunkResult{
			EventType:  ChunkDone,
			StopReason: e.StopReason,
			Usage:      e.Usage,
			DoneEvent:  e,
			Content:    buildContentBlocks(state.TextBuilder.String(), state.ThinkingBuilder.String(), state.ToolCalls),
		}

	case llm.LLMErrorEvent:
		errVal := e.Error
		return StreamChunkResult{
			EventType: ChunkError,
			Error:     errVal,
		}

	default:
		return StreamChunkResult{EventType: ChunkIgnored}
	}
}

// processThinkingDelta handles a thinking/reasoning stream delta.
// It validates preconditions (stream started, thinking not suppressed),
// accumulates the delta into the thinking builder, and returns assembled
// content blocks. This is a pure, side-effect-free function (aside from
// mutating the provided state) extracted for independent testability.
func processThinkingDelta(state *StreamChunkState, delta string, contentIndex int, thinkingLevel string) StreamChunkResult {
	if !state.Started {
		return StreamChunkResult{EventType: ChunkIgnored}
	}
	if thinkingLevel == "off" {
		return StreamChunkResult{EventType: ChunkIgnored}
	}
	state.FirstTokenSeen = true
	state.ThinkingBuilder.WriteString(delta)
	content := buildContentBlocks(state.TextBuilder.String(), state.ThinkingBuilder.String(), state.ToolCalls)
	return StreamChunkResult{
		EventType:    ChunkThinkingDelta,
		Delta:        delta,
		ContentIndex: contentIndex,
		Content:      content,
	}
}

// buildContentBlocks assembles the current snapshot of content blocks from
// accumulated text, thinking, and tool call state.
func buildContentBlocks(text string, thinking string, calls map[int]*toolCallState) []agentctx.ContentBlock {
	content := make([]agentctx.ContentBlock, 0, 2+len(calls))
	if thinking != "" {
		content = append(content, agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: thinking,
		})
	}
	if text != "" {
		content = append(content, agentctx.TextContent{
			Type: "text",
			Text: text,
		})
	}
	if len(calls) == 0 {
		return content
	}

	indexes := make([]int, 0, len(calls))
	for idx := range calls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	for _, idx := range indexes {
		call := calls[idx]
		argsMap := make(map[string]any)
		if call.arguments != "" {
			if err := json.Unmarshal([]byte(call.arguments), &argsMap); err != nil {
				argsMap = make(map[string]any)
			}
		}
		content = append(content, agentctx.ToolCallContent{
			ID:        call.id,
			Type:      "toolCall",
			Name:      call.name,
			Arguments: argsMap,
		})
	}
	return content
}
