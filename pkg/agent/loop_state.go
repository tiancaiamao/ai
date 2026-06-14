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
	checkpointMgr  *AgentContextCheckpointManager
	emptyRetries   int
	malformedRecs  int
	newMessages    []agentctx.AgentMessage
	// guardAbortRecovery tracks whether we've already given the LLM a
	// recovery turn after a loop guard hard abort. Prevents re-triggering
	// if the LLM continues the loop.
	guardAbortRecovery bool
	// deltaPromptPending indicates a compaction prompt was injected and the
	// loop is awaiting the LLM's decision/summary response.
	deltaPromptPending bool
	// deltaPromptForced indicates the pending prompt was forced (no decision
	// requested, only a summary).
	deltaPromptForced bool
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
	// Guard: skip checkpoint when there are no messages to persist. An empty
	// message list means there is nothing useful to checkpoint. (LLMContext
	// may legitimately be empty in the delta-compaction design, so we must NOT
	// gate on it — CreateSnapshot carries forward any previous LLMContext.)
	if len(s.agentCtx.RecentMessages) == 0 {
		slog.Info("[Loop] Skipping pre-compaction checkpoint (no messages)", "trigger", trigger, "turn", s.turnCount)
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
	// Heavyweight compaction absorbs all prior delta summaries into a single
	// full summary. Reset the delta token counter so delta compaction restarts
	// from zero for the new compaction window.
	s.agentCtx.AgentState.TokensSinceLastDeltaCompaction = 0
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

// processDeltaCompactionResponse parses the LLM response to a previously
// injected compaction prompt and executes delta compaction when warranted.
// For decision mode: a "yes" with a summary triggers compaction; "no" or an
// unparseable response (D7) resets the tool-call interval counter. For forced
// mode: any valid summary triggers compaction.
func (s *loopState) processDeltaCompactionResponse(ctx context.Context, msg *agentctx.AgentMessage) {
	responseText := msg.ExtractText()

	if s.deltaPromptForced {
		s.deltaPromptForced = false
		summary, ok := ParseForcedCompactionResponse(responseText)
		if ok {
			s.executeDeltaCompaction(ctx, summary)
			return
		}
		// Unparseable forced response: treat as declined (D7).
		s.agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
		slog.Warn("[Loop] Forced delta compaction response unparseable", "turn", s.turnCount)
		return
	}

	decision := ParseCompactionResponse(responseText)
	if decision.Parsed && decision.ShouldCompact && decision.Summary != "" {
		s.executeDeltaCompaction(ctx, decision.Summary)
		return
	}
	// Explicit "no" or unparseable (D7): reset the interval counter so the
	// next ask waits a fresh tool-call interval.
	s.agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
	slog.Info("[Loop] Delta compaction declined",
		"parsed", decision.Parsed, "shouldCompact", decision.ShouldCompact, "turn", s.turnCount)
}

// executeDeltaCompaction performs an inline delta compaction using an
// LLM-generated summary. It replaces the delta message range
// [deltaStart, protected.StartIndex) with a single delta_summary message,
// keeping the protected (recent) messages verbatim. It resets the delta
// token and tool-call counters, persists a delta_compact entry (when a sink
// is configured and entry IDs are available), and emits trace + stream events.
func (s *loopState) executeDeltaCompaction(ctx context.Context, summary string) {
	messages := s.agentCtx.RecentMessages
	deltaStart := findDeltaStartIndex(messages)
	protected := CalculateProtectedBoundary(messages, deltaStart)

	// Nothing to compress (protected region covers the entire delta).
	if protected.StartIndex <= deltaStart {
		slog.Info("[Loop] Delta compaction skipped — nothing to compress",
			"deltaStart", deltaStart, "protectedStart", protected.StartIndex, "turn", s.turnCount)
		return
	}

	fromEntryID := ""
	if deltaStart < len(messages) {
		fromEntryID = messages[deltaStart].EntryID
	}
	toEntryID := ""
	if protected.StartIndex > 0 {
		toEntryID = messages[protected.StartIndex-1].EntryID
	}

	before := len(messages)

	compactionSpan := traceevent.StartSpan(ctx, "delta_compaction", traceevent.CategoryEvent,
		traceevent.Field{Key: "source", Value: "delta"},
		traceevent.Field{Key: "before_messages", Value: before},
		traceevent.Field{Key: "delta_start", Value: deltaStart},
		traceevent.Field{Key: "protected_start", Value: protected.StartIndex},
	)
	s.stream.Push(NewCompactionStartEvent(CompactionInfo{
		Auto:    true,
		Before:  before,
		Trigger: "delta",
		Source:  "delta",
	}))

	// Rebuild RecentMessages: prefix + delta_summary + protected tail.
	deltaSummaryMsg := newDeltaSummaryMessage(summary)
	newMessages := make([]agentctx.AgentMessage, 0, deltaStart+1+(len(messages)-protected.StartIndex))
	newMessages = append(newMessages, messages[:deltaStart]...)
	newMessages = append(newMessages, deltaSummaryMsg)
	newMessages = append(newMessages, messages[protected.StartIndex:]...)
	s.agentCtx.RecentMessages = newMessages

	// Reset counters.
	s.agentCtx.AgentState.TokensSinceLastDeltaCompaction = 0
	s.agentCtx.AgentState.ToolCallsSinceLastTrigger = 0

	// Persist best-effort: skip when entry IDs are unavailable (current-run
	// messages that haven't been assigned session entries yet).
	if s.config.PersistDeltaCompact != nil && fromEntryID != "" && toEntryID != "" {
		if err := s.config.PersistDeltaCompact(summary, fromEntryID, toEntryID); err != nil {
			slog.Warn("[Loop] Failed to persist delta_compact entry", "error", err, "turn", s.turnCount)
		}
	}

	after := len(s.agentCtx.RecentMessages)
	compactionSpan.AddField("after_messages", after)
	compactionSpan.End()

	s.stream.Push(NewCompactionEndEvent(CompactionInfo{
		Auto:    true,
		Before:  before,
		After:   after,
		Trigger: "delta",
		Source:  "delta",
	}))

	slog.Info("[Loop] Delta compaction completed",
		"before", before, "after", after, "turn", s.turnCount)

	// Save checkpoint so the compaction survives a crash.
	saveCheckpointAfterCompaction(s.checkpointMgr, s.agentCtx, false, s.turnCount, "delta")
}

// checkDeltaCompactionTrigger estimates the current delta token count and,
// when a threshold + tool-call interval is met, injects a compaction prompt
// message into RecentMessages for the next LLM turn. It resets the tool-call
// counter on injection so the interval restarts.
func (s *loopState) checkDeltaCompactionTrigger(ctx context.Context) {
	deltaTokens := EstimateDeltaTokens(s.agentCtx.RecentMessages)
	s.agentCtx.AgentState.TokensSinceLastDeltaCompaction = deltaTokens

	trigger := CheckDeltaCompactionTrigger(deltaTokens, s.agentCtx.AgentState.ToolCallsSinceLastTrigger)
	switch trigger {
	case TriggerNone:
		return
	case TriggerDecision:
		s.agentCtx.RecentMessages = append(s.agentCtx.RecentMessages, BuildDecisionMessage())
		s.agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
		s.deltaPromptPending = true
		s.deltaPromptForced = false
		traceevent.Log(ctx, traceevent.CategoryEvent, "delta_compaction_trigger",
			traceevent.Field{Key: "type", Value: "decision"},
			traceevent.Field{Key: "delta_tokens", Value: deltaTokens},
			traceevent.Field{Key: "turn", Value: s.turnCount},
		)
		slog.Info("[Loop] Delta compaction decision prompt injected",
			"delta_tokens", deltaTokens, "turn", s.turnCount)
	case TriggerForced:
		s.agentCtx.RecentMessages = append(s.agentCtx.RecentMessages, BuildForcedCompactionMessage())
		s.agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
		s.deltaPromptPending = true
		s.deltaPromptForced = true
		traceevent.Log(ctx, traceevent.CategoryEvent, "delta_compaction_trigger",
			traceevent.Field{Key: "type", Value: "forced"},
			traceevent.Field{Key: "delta_tokens", Value: deltaTokens},
			traceevent.Field{Key: "turn", Value: s.turnCount},
		)
		slog.Info("[Loop] Delta compaction forced prompt injected",
			"delta_tokens", deltaTokens, "turn", s.turnCount)
	}
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
