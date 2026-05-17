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
	config        *LoopConfig
	agentCtx      *agentctx.AgentContext
	stream        *llm.EventStream[AgentEvent, []agentctx.AgentMessage]
	compactionRecs int
	turnCount     int
	loopGuard     *toolLoopGuard
	checkpointMgr *AgentContextCheckpointManager
	emptyRetries  int
	malformedRecs int
	newMessages   []agentctx.AgentMessage
}

func newLoopState(
	config *LoopConfig,
	agentCtx *agentctx.AgentContext,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	newMessages []agentctx.AgentMessage,
) *loopState {
	return &loopState{
		config:        config,
		agentCtx:      agentCtx,
		stream:        stream,
		loopGuard:     newToolLoopGuard(config),
		checkpointMgr: initCheckpointManager(config),
		newMessages:   newMessages,
	}
}

// cleanup closes the checkpoint manager if present.
func (s *loopState) cleanup() {
	if s.checkpointMgr != nil {
		_ = s.checkpointMgr.Close()
	}
}

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

// savePreCompactionCheckpoint saves a checkpoint before compaction modifies
// agent context. This ensures progress is preserved if the compaction LLM
// call crashes the process. Only saves when at least one compactor indicates
// it should compact, to avoid unnecessary I/O on every turn.
func (s *loopState) savePreCompactionCheckpoint(trigger string) {
	if s.checkpointMgr == nil || !s.checkpointMgr.ShouldCheckpoint() {
		return
	}
	// Check if any compactor would trigger before saving checkpoint.
	shouldCompact := false
	for _, c := range s.config.Compactors {
		if c.ShouldCompact(context.Background(), s.agentCtx) {
			shouldCompact = true
			break
		}
	}
	if !shouldCompact {
		return
	}
	if _, err := s.checkpointMgr.CreateSnapshot(s.agentCtx, s.agentCtx.LLMContext, s.turnCount); err != nil {
		slog.Warn("[Loop] Failed to save pre-compaction checkpoint", "error", err, "trigger", trigger, "turn", s.turnCount)
	} else {
		slog.Info("[Loop] Pre-compaction checkpoint saved", "trigger", trigger, "turn", s.turnCount)
	}
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
		compacted, compactErr = c.Compact(s.agentCtx)
		if compactErr == nil {
			break // First successful compaction wins
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
	s.stream.Push(NewCompactionEndEvent(CompactionInfo{
		Type:              compacted.Type,
		Auto:              true,
		Before:            before,
		After:             after,
		Trigger:           trigger,
		TokensBefore:      compacted.TokensBefore,
		TokensAfter:       compacted.TokensAfter,
		TruncatedCount:    compacted.TruncatedCount,
		LLMContextUpdated: compacted.LLMContextUpdated,
	}))

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
	toolResults = executeToolCalls(ctx, s.agentCtx, s.agentCtx.Tools, s.agentCtx.GetAllowedToolsMap(), msg, s.stream, s.config.Executor, s.config.Metrics, s.config.ToolOutput)

	// Append results to conversation state.
	for _, result := range toolResults {
		s.agentCtx.RecentMessages = append(s.agentCtx.RecentMessages, result)
		s.newMessages = append(s.newMessages, result)
	}

	// Increment tool call counter for compactor trigger intervals.
	s.agentCtx.AgentState.ToolCallsSinceLastTrigger += len(toolResults)

	// Create checkpoint after update_llm_context tool execution.
	if hasToolResultNamed(toolResults, "update_llm_context") {
		saveCheckpointAfterToolExecution(s.checkpointMgr, s.agentCtx, s.turnCount, "update_llm_context")
	}

	return hasMore, toolResults
}

// replaceLast replaces the last element of a slice with the given message.
// Panics if the slice is empty.
func replaceLast(msgs []agentctx.AgentMessage, msg agentctx.AgentMessage) []agentctx.AgentMessage {
	msgs[len(msgs)-1] = msg
	return msgs
}