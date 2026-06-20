package agent

import (
	"context"
	"fmt"
	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const maxCompactionRecoveries = 1

// loopState holds shared mutable state for the inner agent loop.
// It replaces multiple local variables and repeated parameter passing
// between the loop body and its extracted helper functions.
type loopState struct {
	config         *LoopConfig
	agentCtx       *agentctx.AgentContext
	stream         *llm.EventStream[AgentEvent, []agentctx.AgentMessage]
	compactionRecs int
	turnCount      int
	loopGuard      *toolLoopGuard

	emptyRetries  int
	malformedRecs int
	newMessages   []agentctx.AgentMessage
	// guardAbortRecovery tracks whether we've already given the LLM a
	// recovery turn after a loop guard hard abort. Prevents re-triggering
	// if the LLM continues the loop.
	guardAbortRecovery bool
}

func newLoopState(
	config *LoopConfig,
	agentCtx *agentctx.AgentContext,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	newMessages []agentctx.AgentMessage,
) *loopState {
	return &loopState{
		config:    config,
		agentCtx:  agentCtx,
		stream:    stream,
		loopGuard: newToolLoopGuard(config),

		newMessages: newMessages,
	}
}

// cleanup is a no-op now that the checkpoint manager holds no resources.
func (s *loopState) cleanup() {}

// shouldStop checks for context cancellation and max turns limit.
// Returns true if the loop should terminate. Pushes AgentEndEvent on stop.
func (s *loopState) shouldStop(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		s.stream.Push(NewAgentEndEvent(s.agentCtx.RecentMessages))
		return true
	default:
	}

	if s.config.MaxTurns > 0 && s.turnCount >= s.config.MaxTurns {
		slog.Info("[Loop] max turns limit reached",
			"turns", s.turnCount,
			"maxTurns", s.config.MaxTurns)
		s.stream.Push(NewAgentEndEvent(s.agentCtx.RecentMessages))
		return true
	}

	return false
}

// advanceTurn increments the turn counter.
func (s *loopState) advanceTurn() {
	s.turnCount++
}

// performCompaction executes compaction using the configured compactors.
// It iterates compactors in priority order, emits trace and stream events,
// and updates agent context on success.
//
// Parameters:
//   - trigger: identifies the compaction source (e.g. "pre_llm_threshold", "context_limit_recovery")
//   - checkShouldCompact: if true, calls ShouldCompact before attempting Compact (pre-LLM path);
//     if false, unconditionally calls Compact on each compactor (recovery path)
//   - trackRecovery: if true, increments the compaction recovery counter on success
//
// Returns the CompactionResult (nil if no compactor triggered or all returned nil),
// and any error from the last attempted compaction.
func (s *loopState) performCompaction(
	ctx context.Context,
	trigger string,
	checkShouldCompact bool,
	trackRecovery bool,
) (*agentctx.CompactionResult, error) {
	var compacted *agentctx.CompactionResult
	var compactErr error
	var compactionStarted bool
	var before int
	var compactionSpan *traceevent.Span

	for _, c := range s.config.Compactors {
		if checkShouldCompact && !c.ShouldCompact(ctx, s.agentCtx) {
			continue
		}

		if !compactionStarted {
			before = len(s.agentCtx.RecentMessages)
			compactionSpan = traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
				traceevent.Field{Key: "source", Value: trigger},
				traceevent.Field{Key: "auto", Value: true},
				traceevent.Field{Key: "before_messages", Value: before},
				traceevent.Field{Key: "trigger", Value: trigger},
			)
			s.stream.Push(NewCompactionStartEvent(CompactionInfo{
				Auto:    true,
				Before:  before,
				Trigger: trigger,
			}))
			compactionStarted = true
		}

		slog.Info("[Loop] Compaction triggered", "trigger", trigger, "compactor", fmt.Sprintf("%T", c))
		compacted, compactErr = c.Compact(ctx, s.agentCtx)
		if compactErr == nil && compacted != nil {
			break // Compactor performed work; first real result wins
		}
		if compactErr == nil && compacted == nil {
			slog.Info("[Loop] Compactor returned nil result, trying next", "trigger", trigger, "compactor", fmt.Sprintf("%T", c))
			continue // No-op: try the next compactor
		}
		slog.Warn("[Loop] Compaction failed", "trigger", trigger, "compactor", fmt.Sprintf("%T", c), "error", compactErr)
	}

	if !compactionStarted {
		return nil, nil
	}

	if compactErr != nil {
		slog.Warn("[Loop] Compaction triggered but all compactors failed", "trigger", trigger, "error", compactErr)
		compactionSpan.AddField("error", true)
		compactionSpan.AddField("error_message", compactErr.Error())
		if stack := ErrorStack(compactErr); stack != "" {
			compactionSpan.AddField("error_stack", stack)
		}
		compactionSpan.End()
		s.stream.Push(NewCompactionEndEvent(CompactionInfo{
			Auto:    true,
			Before:  before,
			Error:   compactErr.Error(),
			Trigger: trigger,
		}))
		return nil, compactErr
	}

	if compacted == nil {
		slog.Warn("[Loop] Compaction triggered but returned nil result", "trigger", trigger)
		compactionSpan.End()
		s.stream.Push(NewCompactionEndEvent(CompactionInfo{
			Auto:    true,
			Before:  before,
			Trigger: trigger,
		}))
		return nil, nil
	}

	s.agentCtx.LastCompactionSummary = compacted.Summary
	after := len(s.agentCtx.RecentMessages)

	compactionSpan.AddField("after_messages", after)
	compactionSpan.End()

	// Carry the summary and a copy of post-compaction messages on the event.
	// The event consumer (rpc_app) persists these to a compaction snapshot file
	// and appends a compaction entry to messages.jsonl. We copy the slice to
	// avoid sharing the backing array with the loop goroutine.
	endEvent := NewCompactionEndEvent(CompactionInfo{
		Type:           compacted.Type,
		Auto:           true,
		Before:         before,
		After:          after,
		Trigger:        trigger,
		Summary:        compacted.Summary,
		TokensBefore:   compacted.TokensBefore,
		TokensAfter:    compacted.TokensAfter,
		TruncatedCount: compacted.TruncatedCount,
	})
	if len(s.agentCtx.RecentMessages) > 0 {
		msgs := make([]agentctx.AgentMessage, len(s.agentCtx.RecentMessages))
		copy(msgs, s.agentCtx.RecentMessages)
		endEvent.Messages = msgs
	}
	s.stream.Push(endEvent)

	if trackRecovery {
		s.compactionRecs++
	}

	return compacted, nil
}

// processToolCalls handles the full tool call lifecycle within a single turn:
// extract tool calls, check loop guard, dispatch to executor, collect results,
// append to conversation state, and save checkpoints.
func (s *loopState) processToolCalls(
	ctx context.Context,
	msg *agentctx.AgentMessage,
) (hasMore bool, toolResults []agentctx.AgentMessage) {
	toolCalls := msg.ExtractToolCalls()
	hasMore = len(toolCalls) > 0

	// Reset malformed recovery counter when we have valid tool calls.
	if hasMore {
		s.malformedRecs = 0
	}

	// Check loop guard for consecutive identical tool call patterns.
	if hasMore && s.loopGuard != nil {
		result := s.loopGuard.Observe(toolCalls)
		if result.Blocked {
			slog.Warn("[Loop] tool call loop guard triggered", "reason", result.Reason, "hardAbort", result.HardAbort, "feedbackAttempt", result.FeedbackAttempt)
			s.stream.Push(NewLoopGuardTriggeredEvent(LoopGuardInfo{Reason: result.Reason}))
			traceevent.Log(ctx, traceevent.CategoryEvent, "tool_loop_guard_triggered",
				traceevent.Field{Key: "reason", Value: result.Reason},
				traceevent.Field{Key: "call_count", Value: len(toolCalls)},
				traceevent.Field{Key: "hard_abort", Value: result.HardAbort},
				traceevent.Field{Key: "feedback_attempt", Value: result.FeedbackAttempt},
			)

			if result.HardAbort {
				// Escalation limit reached: sanitize the message and abort.
				sanitizeMessageForToolLoopGuard(msg, result.Reason)
				s.agentCtx.RecentMessages[len(s.agentCtx.RecentMessages)-1] = *msg
				s.newMessages = replaceLast(s.newMessages, *msg)
				hasMore = false
				return hasMore, nil
			}

			// Soft feedback: construct ToolResult messages for each blocked tool call
			// and return them to the LLM so it can self-correct.
			toolResults := buildLoopGuardToolResults(toolCalls, result, s.loopGuard.maxFeedbackAttempts)
			for _, tr := range toolResults {
				s.agentCtx.RecentMessages = append(s.agentCtx.RecentMessages, tr)
				s.newMessages = append(s.newMessages, tr)
			}
			// hasMore stays true — the LLM will see the feedback results and get another turn.
			return hasMore, toolResults
		}
	}

	if !hasMore {
		return hasMore, nil
	}

		// Dispatch tool calls to the executor.
	toolResults = executeToolCalls(ctx, s.agentCtx, s.agentCtx.Tools, s.agentCtx.GetAllowedToolsMap(), msg, s.stream, s.config.Executor, s.config.ToolOutput)

	// Run AfterTool hooks: chain-style, each hook's output feeds the next.
	hookCtx := HookContext{
		Ctx:      ctx,
		AgentCtx: s.agentCtx,
		Config:   s.config,
	}
	for i := range toolResults {
		toolResults[i] = s.config.Hooks.RunAfterTool(hookCtx, toolResults[i].ToolName, toolResults[i])
	}

	// Append results to conversation state.
	for _, result := range toolResults {
		s.agentCtx.RecentMessages = append(s.agentCtx.RecentMessages, result)
		s.newMessages = append(s.newMessages, result)
	}

	// Notify loop guard of tool output so it can detect polling patterns
	// (same tool+args but changing output = legitimate polling, not stuck loop).
	if s.loopGuard != nil {
		for _, result := range toolResults {
			outputHash := result.OutputHash()
			if outputHash != "" {
				s.loopGuard.NotifyToolOutput(outputHash)
			}
		}
	}

	// Increment tool call counter for compactor trigger intervals.
	s.agentCtx.AgentState.ToolCallsSinceLastTrigger += len(toolResults)

	return hasMore, toolResults
}

// replaceLast replaces the last element of a slice with the given message.
// Panics if the slice is empty.
func replaceLast(msgs []agentctx.AgentMessage, msg agentctx.AgentMessage) []agentctx.AgentMessage {
	msgs[len(msgs)-1] = msg
	return msgs
}
