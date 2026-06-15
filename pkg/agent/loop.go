package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
	"strings"
	"time"
)

const (
	defaultLoopMaxConsecutiveToolCalls = 6
	defaultLoopMaxToolCallsPerName     = 60
	defaultMalformedToolCallRecoveries = 2
	defaultEmptyResponseMaxRetries     = 2
	defaultRuntimeMetaHeartbeatTurns   = 6
	defaultLLMTotalTimeout             = 10 * time.Minute // Total timeout for LLM request
	defaultLLMFirstResponseTimeout     = 2 * time.Minute  // Timeout between streaming chunks (2min)
)

type LoopConfig struct {
	Model  llm.Model
	APIKey string
	// GetModel returns the current model. If nil, falls back to Model field.
	// This allows dynamic model switching without restarting the loop.
	GetModel func() llm.Model
	// GetAPIKey returns the current API key. If nil, falls back to APIKey field.
	// This allows dynamic API key switching without restarting the loop.
	GetAPIKey func() string
	// GetWorkingDir returns the current working directory for runtime_state telemetry.
	GetWorkingDir func() string
	// GetStartupPath returns the startup/root path for runtime_state telemetry.
	GetStartupPath func() string
	// GetSessionDir returns the session directory for checkpoint management.
	GetSessionDir func() string
	// RunID is the run ID assigned by the parent ai serve process.
	// Empty when running standalone (ai --mode rpc without ai serve).
	RunID      string
	Executor   ToolExecutor // agentctx.Tool executor with concurrency control
	Metrics    *Metrics     // Metrics collector
	ToolOutput ToolOutputLimits
	Compactors []agentctx.Compactor // Multiple compactors with priority control (array order determines priority)
	// PersistDeltaCompact records a delta_compact entry to the session journal.
	// When nil, delta compaction still updates in-memory RecentMessages but
	// skips journal persistence (e.g. in tests). summary is the LLM-generated
	// text; fromEntryID/toEntryID identify the compressed message interval.
	PersistDeltaCompact func(summary, fromEntryID, toEntryID string) error
	// ToolCallCutoff summarizes the oldest tool outputs when visible tool results exceed this.
	ToolCallCutoff int
	// ThinkingLevel: off, minimal, low, medium, high, xhigh.
	ThinkingLevel string
	// MaxLLMRetries is the maximum number of retries for LLM calls.
	MaxLLMRetries int
	// RetryBaseDelay is the base delay for exponential backoff.
	RetryBaseDelay time.Duration
	// MaxConsecutiveToolCalls is the maximum number of consecutive identical tool call signatures (0=default, <0=disabled).
	MaxConsecutiveToolCalls int
	// MaxToolCallsPerName is the maximum number of tool calls per tool name in one run (0=default, <0=disabled).
	MaxToolCallsPerName int
	// MaxTurns is the maximum number of conversation turns (0=default=unlimited).
	MaxTurns int
	// ContextWindow is the context window for the model (0=use default 128000).
	ContextWindow int
	// LLMTotalTimeout is the total timeout for an LLM request (default 10min).
	LLMTotalTimeout time.Duration
	// LLMFirstResponseTimeout is the timeout between streaming chunks (default 2min).
	LLMFirstResponseTimeout time.Duration
	// EnableCheckpoint enables automatic checkpoint creation (default true).
	EnableCheckpoint bool
	// MaxLoopGuardFeedback is the number of feedback rounds the loop guard gives
	// the LLM before escalating to a hard abort (0=default=2).
	MaxLoopGuardFeedback int
	// Hooks is the hook registry for BeforeModel/AfterTool/AfterAgent hooks.
	// Nil is safe — all Run* methods are no-ops.
	Hooks *HookRegistry
	// CacheMode controls how runtime_state telemetry is managed.
	// CacheModeAuto (default) auto-detects from model name.
	CacheMode CacheMode
	// AgentContextPrefix combines skills and project-level instructions (AGENTS.md)
	// into a single user message injected before the first user message on each LLM call.
	// Empty means no injection.
	// Merging into one message and placing it in the prefix maximizes provider
	// prefix cache hits — both skills and instructions are stable within a session.
	AgentContextPrefix string
}

// getEffectiveModel returns the current model, using GetModel callback if available.
func getEffectiveModel(config *LoopConfig) llm.Model {
	if config.GetModel != nil {
		return config.GetModel()
	}
	return config.Model
}

// getEffectiveAPIKey returns the current API key, using GetAPIKey callback if available.
func getEffectiveAPIKey(config *LoopConfig) string {
	if config.GetAPIKey != nil {
		return config.GetAPIKey()
	}
	return config.APIKey
}

// DefaultLoopConfig returns a default LoopConfig with sensible values.
func DefaultLoopConfig() *LoopConfig {
	return &LoopConfig{
		ToolCallCutoff:          10,
		ThinkingLevel:           "high",
		MaxLLMRetries:           defaultLLMMaxRetries,
		RetryBaseDelay:          defaultRetryBaseDelay,
		Executor:                NewToolExecutor(10, 60),
		ToolOutput:              DefaultToolOutputLimits(),
		LLMTotalTimeout:         defaultLLMTotalTimeout,
		LLMFirstResponseTimeout: defaultLLMFirstResponseTimeout,
		EnableCheckpoint:        true,
	}
}

var streamAssistantResponseFn = streamAssistantResponse

// RunLoop starts a new agent loop with the given prompts.
func RunLoop(
	ctx context.Context,
	prompts []agentctx.AgentMessage,
	agentCtx *agentctx.AgentContext,
	config *LoopConfig,
) *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	go func() {
		// Panic recovery must be registered after stream.End so it
		// executes BEFORE stream.End (defers are LIFO). If we recover
		// from a panic, we push error events before stream.End closes
		// the stream.
		defer stream.End(nil)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[Loop] Agent loop panic (recovered)",
					"panic", r,
					"turn", "unknown")
				stream.Push(NewErrorEvent(fmt.Errorf("agent loop panic (recovered): %v", r)))
				stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			}
		}()

		newMessages := append([]agentctx.AgentMessage{}, prompts...)
		currentCtx := &agentctx.AgentContext{
			SystemPrompt:   agentCtx.SystemPrompt,
			RecentMessages: append(agentCtx.RecentMessages, prompts...),
			Tools:          agentCtx.Tools,
			LLMContext:     agentCtx.LLMContext,
			AgentState:     agentCtx.AgentState,
		}

		stream.Push(NewAgentStartEvent())
		stream.Push(NewTurnStartEvent())

		for _, msg := range prompts {
			stream.Push(NewMessageStartEvent(msg))
			stream.Push(NewMessageEndEvent(msg))
		}

		runInnerLoop(ctx, currentCtx, newMessages, config, stream)
	}()

	return stream
}

// runInnerLoop contains the core loop logic.
func runInnerLoop(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	newMessages []agentctx.AgentMessage,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
) {
	span := traceevent.StartSpan(ctx, "runInnerLoop", traceevent.CategoryEvent)
	defer span.End()

	state := newLoopState(config, agentCtx, stream, newMessages)
	defer state.cleanup()

	for {
		if state.shouldStop(ctx) {
			return
		}
		state.advanceTurn()

		// Pre-LLM compaction: check thresholds and compact if needed.
		// Save checkpoint BEFORE compaction so progress is preserved if the
		// compaction LLM call crashes the process.
		state.savePreCompactionCheckpoint("pre_llm_threshold")
		compacted, _ := state.performCompaction(ctx, "pre_llm_threshold", true, false)
		if compacted != nil {
			saveCheckpointAfterCompaction(state.checkpointMgr, agentCtx, compacted.LLMContextUpdated, state.turnCount, "pre_llm_threshold")
		}

		// Run BeforeModel hooks: fan-out, inject additional messages before LLM call.
		config.Hooks.RunBeforeModel(HookContext{
			Ctx:      ctx,
			AgentCtx: agentCtx,
			Config:   config,
		}, agentCtx.RecentMessages)

		// Stream assistant response with retry logic.
		msg, err := streamAssistantResponseWithRetry(ctx, agentCtx, config, stream)
		if err != nil {
			if llm.IsContextLengthExceeded(err) && len(config.Compactors) > 0 && state.compactionRecs < maxCompactionRecoveries {
				recoveryResult, recoveryErr := state.performCompaction(ctx, "context_limit_recovery", false, true)
				if recoveryErr == nil {
					recoveryUpdated := recoveryResult != nil && recoveryResult.LLMContextUpdated
					saveCheckpointAfterCompaction(state.checkpointMgr, agentCtx, recoveryUpdated, state.turnCount, "context_limit_recovery")
					continue
				}
			}

			slog.Error("Error streaming response", "error", err)
			traceFields := []traceevent.Field{
				{Key: "error_message", Value: err.Error()},
			}
			if stack := ErrorStack(err); stack != "" {
				traceFields = append(traceFields, traceevent.Field{Key: "error_stack", Value: stack})
			}
			traceevent.Log(ctx, traceevent.CategoryEvent, "run_loop_error", traceFields...)
			stream.Push(NewErrorEvent(err))
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		if msg == nil {
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		agentCtx.RecentMessages = append(agentCtx.RecentMessages, *msg)
		state.newMessages = append(state.newMessages, *msg)

		// Update AgentState with token usage after successful LLM response.
		if msg.Usage != nil && msg.Usage.TotalTokens > 0 {
			const defaultContextWindow = 200000
			tokensMax := defaultContextWindow
			if config.ContextWindow > 0 {
				tokensMax = config.ContextWindow
			}
			agentCtx.AgentState.TokensUsed = msg.Usage.TotalTokens
			agentCtx.AgentState.TokensLimit = tokensMax
			agentCtx.AgentState.TotalTurns = len(agentCtx.RecentMessages)
		}

		// Check for error or abort (special cases that end the loop immediately).
		if msg.StopReason == "error" || msg.StopReason == "aborted" {
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		// Check for non-success stopReason and notify user.
		if sanitized := sanitizeMessageForNonSuccessStopReason(msg); sanitized {
			slog.Warn("[Loop] LLM request ended with non-success stopReason", "stopReason", msg.StopReason)
			traceevent.Log(ctx, traceevent.CategoryEvent, "non_success_stop_reason_detected",
				traceevent.Field{Key: "stopReason", Value: msg.StopReason})
			agentCtx.RecentMessages[len(agentCtx.RecentMessages)-1] = *msg
			state.newMessages[len(state.newMessages)-1] = *msg
		}

		// [Step 8] Delta compaction post-processing: if a compaction prompt
		// was injected last turn, parse the LLM response and execute delta
		// compaction when the decision is "yes" (or forced). This runs BEFORE
		// processToolCalls so the compressed context is in place, but tool
		// calls in the (protected) response are still processed normally.
		deltaHandledThisTurn := false
		if state.deltaPromptPending {
			state.deltaPromptPending = false
			state.processDeltaCompactionResponse(ctx, msg)
			deltaHandledThisTurn = true
		}

		hasMore, toolResults := state.processToolCalls(ctx, msg)

		stream.Push(NewTurnEndEvent(msg, toolResults))

		// If no more tool calls, end the conversation.
		if !hasMore {
			// The LLM responded to a delta compaction prompt (not to the
			// user's task). Resume the task in the next turn instead of
			// ending — the compaction response naturally has no tool calls.
			if deltaHandledThisTurn {
				continue
			}

			if maybeRecoverMalformedToolCall(ctx, agentCtx, &state.newMessages, stream, msg, &state.malformedRecs) {
				continue
			}

			// Loop guard hard-abort recovery: give the LLM one final turn
			// to produce a text response to the user instead of silently
			// terminating. The sanitizeMessageForToolLoopGuard has already
			// replaced tool calls with a textual explanation of the abort,
			// so the LLM will see it and can respond accordingly.
			if msg.StopReason == "aborted" && !state.guardAbortRecovery {
				state.guardAbortRecovery = true
				slog.Info("[Loop] loop guard hard abort — giving LLM one recovery turn")
				continue
			}

			// Check for empty response: stop_reason=stop but no actionable content.
			if msg.StopReason == "stop" && isEmptyActionableResponse(msg) && state.emptyRetries < defaultEmptyResponseMaxRetries {
				state.emptyRetries++
				slog.Warn("[Loop] LLM returned stop_reason=stop with empty actionable output (no text, no tool calls); retrying",
					"stopReason", msg.StopReason,
					"turn", state.turnCount,
					"retry_attempt", state.emptyRetries,
					"max_retries", defaultEmptyResponseMaxRetries,
				)
				continue
			}

			break
		}

		// [Step 11] Delta compaction trigger check: estimate the current
		// delta token count and, when a threshold + tool-call interval is
		// met, inject a compaction prompt for the next LLM turn.
		state.checkDeltaCompactionTrigger(ctx)
	}

	// Run AfterAgent hooks: sequential, no data passing, before AgentEndEvent.
	config.Hooks.RunAfterAgent(HookContext{
		Ctx:      ctx,
		AgentCtx: agentCtx,
		Config:   config,
	})

	stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
}

func hashAny(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// randFloat64 returns a random float64 in [0, 1)
func randFloat64() float64 {
	return rand.Float64()
}

// isEmptyActionableResponse returns true if the message contains no actionable
// content (no non-empty text blocks and no tool calls). Thinking-only content
// is NOT considered actionable.
func isEmptyActionableResponse(msg *agentctx.AgentMessage) bool {
	if msg == nil {
		return true
	}
	for _, block := range msg.Content {
		switch b := block.(type) {
		case agentctx.TextContent:
			// Non-empty text is actionable
			if strings.TrimSpace(b.Text) != "" {
				return false
			}
		case agentctx.ToolCallContent:
			// Tool calls are actionable
			return false
		}
	}
	// Only thinking content (or empty content) — not actionable
	return true
}
