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
	Executor      ToolExecutor // agentctx.Tool executor with concurrency control
	Metrics       *Metrics     // Metrics collector
	ToolOutput    ToolOutputLimits
	Compactors    []agentctx.Compactor // Multiple compactors with priority control (array order determines priority)
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
		defer stream.End(nil)

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

	const maxCompactionRecoveries = 1
	compactionRecoveries := 0
	loopGuard := newToolLoopGuard(config)
	malformedToolCallRecoveries := 0
	emptyResponseRetries := 0

	// Turn counter for MaxTurns limit
	turnCount := 0

	// Initialize checkpoint manager if enabled
	var checkpointMgr *AgentContextCheckpointManager
	var checkpointErr error
	if config.EnableCheckpoint && config.GetSessionDir != nil {
		checkpointMgr, checkpointErr = NewAgentContextCheckpointManager(config.GetSessionDir())
		if checkpointErr != nil {
			slog.Warn("[Loop] Failed to initialize checkpoint manager", "error", checkpointErr)
		}
		defer func() {
			if checkpointMgr != nil {
				_ = checkpointMgr.Close()
			}
		}()
	}

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		default:
		}

		// Check for max turns limit
		if config.MaxTurns > 0 && turnCount >= config.MaxTurns {
			slog.Info("[Loop] max turns limit reached",
				"turns", turnCount,
				"maxTurns", config.MaxTurns)
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		turnCount++

		// Try each compactor in order (first trigger wins).
		// Emit compaction_start BEFORE executing Compact() so the user sees
		// immediate feedback instead of waiting for the full compaction to complete.
		var compacted *agentctx.CompactionResult
		var compactErr error
		var compactionStarted bool
		var before int
		var compactionSpan *traceevent.Span
		for _, c := range config.Compactors {
			if c.ShouldCompact(ctx, agentCtx) {
				if !compactionStarted {
					before = len(agentCtx.RecentMessages)
					compactionSpan = traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
						traceevent.Field{Key: "source", Value: "pre_llm_threshold"},
						traceevent.Field{Key: "auto", Value: true},
						traceevent.Field{Key: "before_messages", Value: before},
						traceevent.Field{Key: "trigger", Value: "pre_llm_threshold"},
					)
					stream.Push(NewCompactionStartEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						Trigger: "pre_llm_threshold",
					}))
					compactionStarted = true
				}
				slog.Info("[Loop] Pre-LLM compaction triggered", "compactor", fmt.Sprintf("%T", c))
				compacted, compactErr = c.Compact(agentCtx)
				if compactErr == nil {
					break // First successful compaction wins
				} else {
					slog.Warn("[Loop] Pre-LLM compaction failed", "compactor", fmt.Sprintf("%T", c), "error", compactErr)
				}
			}
		}

		// Process compaction result
		if compactionStarted {
			if compactErr != nil {
				// Compaction triggered but all compactors failed
				slog.Warn("[Loop] Compaction triggered but all compactors failed", "error", compactErr)
				compactionSpan.AddField("error", true)
				compactionSpan.AddField("error_message", compactErr.Error())
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Error:   compactErr.Error(),
					Trigger: "pre_llm_threshold",
				}))
			} else if compacted == nil {
				// ShouldCompact returned true but Compact returned nil result without error
				slog.Warn("[Loop] Compaction triggered but returned nil result")
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Trigger: "pre_llm_threshold",
				}))
			} else {
				// Note: Compactor now directly modifies agentCtx.RecentMessages
				// We just need to update the summary.
				// Compact events are already appended via OnCompactEvent in the tools.
				agentCtx.LastCompactionSummary = compacted.Summary
				after := len(agentCtx.RecentMessages)
				compactionSpan.AddField("after_messages", after)
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Type:              compacted.Type,
					Auto:              true,
					Before:            before,
					After:             after,
					Trigger:           "pre_llm_threshold",
					TokensBefore:      compacted.TokensBefore,
					TokensAfter:       compacted.TokensAfter,
					TruncatedCount:    compacted.TruncatedCount,
					LLMContextUpdated: compacted.LLMContextUpdated,
				}))

				// Create checkpoint after compaction to preserve AgentState for resume
				// Only create checkpoint if LLM context was updated (meaningful state change)
				if checkpointMgr != nil && checkpointMgr.ShouldCheckpoint() {
					if compacted.LLMContextUpdated {
						llmContextContent := agentCtx.LLMContext
						if _, err := checkpointMgr.CreateSnapshot(agentCtx, llmContextContent, turnCount); err != nil {
							slog.Warn("[Loop] Failed to create checkpoint after compaction", "error", err, "turn", turnCount)
						} else {
							slog.Info("[Loop] Checkpoint created after compaction (LLM context updated)", "trigger", "pre_llm_threshold", "turn", turnCount)
						}
					} else {
						slog.Info("[Loop] Skipping checkpoint creation after compaction (LLM context not updated, resume will replay from last checkpoint)", "trigger", "pre_llm_threshold", "turn", turnCount)
					}
				}
			}
		}

		// Stream assistant response with retry logic
		msg, err := streamAssistantResponseWithRetry(ctx, agentCtx, config, stream)
		if err != nil {
			if llm.IsContextLengthExceeded(err) && len(config.Compactors) > 0 && compactionRecoveries < maxCompactionRecoveries {
				before := len(agentCtx.RecentMessages)
				compactionSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
					traceevent.Field{Key: "source", Value: "context_limit_recovery"},
					traceevent.Field{Key: "auto", Value: true},
					traceevent.Field{Key: "before_messages", Value: before},
					traceevent.Field{Key: "trigger", Value: "context_limit_recovery"},
				)
				stream.Push(NewCompactionStartEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Trigger: "context_limit_recovery",
				}))

				// Try each compactor in order (first success wins)
				var recoveryCompacted *agentctx.CompactionResult
				var recoveryErr error
				for _, c := range config.Compactors {
					recoveryCompacted, recoveryErr = c.Compact(agentCtx)
					if recoveryErr == nil {
						break // First successful compaction wins
					}
				}

				if recoveryErr != nil {
					slog.Error("All compactors failed for recovery", "error", recoveryErr)
					compactionSpan.AddField("error", true)
					compactionSpan.AddField("error_message", recoveryErr.Error())
					if stack := ErrorStack(recoveryErr); stack != "" {
						compactionSpan.AddField("error_stack", stack)
					}
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						Error:   recoveryErr.Error(),
						Trigger: "context_limit_recovery",
					}))
				} else {
					compactionRecoveries++
					if recoveryCompacted != nil {
						// Compactor directly modified agentCtx.RecentMessages
						agentCtx.LastCompactionSummary = recoveryCompacted.Summary
						// Compact events are already appended via OnCompactEvent in the tools.
					}
					compactionSpan.AddField("after_messages", len(agentCtx.RecentMessages))
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Type: func() string {
							if recoveryCompacted != nil {
								return recoveryCompacted.Type
							}
							return ""
						}(),
						Auto:    true,
						Before:  before,
						After:   len(agentCtx.RecentMessages),
						Trigger: "context_limit_recovery",
						TokensBefore: func() int {
							if recoveryCompacted != nil {
								return recoveryCompacted.TokensBefore
							}
							return 0
						}(),
						TokensAfter: func() int {
							if recoveryCompacted != nil {
								return recoveryCompacted.TokensAfter
							}
							return 0
						}(),
						TruncatedCount: func() int {
							if recoveryCompacted != nil {
								return recoveryCompacted.TruncatedCount
							}
							return 0
						}(),
						LLMContextUpdated: func() bool {
							if recoveryCompacted != nil {
								return recoveryCompacted.LLMContextUpdated
							}
							return false
						}(),
					}))

					// Create checkpoint after compaction to preserve AgentState for resume
					// Only create checkpoint if LLM context was updated (meaningful state change)
					if checkpointMgr != nil && checkpointMgr.ShouldCheckpoint() {
						if recoveryCompacted != nil && recoveryCompacted.LLMContextUpdated {
							if _, err := checkpointMgr.CreateSnapshot(agentCtx, agentCtx.LLMContext, turnCount); err != nil {
								slog.Warn("[Loop] Failed to create checkpoint after compaction", "error", err, "turn", turnCount)
							} else {
								slog.Info("[Loop] Checkpoint created after compaction (LLM context updated)", "trigger", "context_limit_recovery", "turn", turnCount)
							}
						} else {
							slog.Info("[Loop] Skipping checkpoint creation after compaction (LLM context not updated, resume will replay from last checkpoint)", "trigger", "context_limit_recovery", "turn", turnCount)
						}
					}
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
			// Message was nil (aborted)
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		agentCtx.RecentMessages = append(agentCtx.RecentMessages, *msg)
		newMessages = append(newMessages, *msg)

		// Update AgentState with token usage after successful LLM response
		if msg.Usage != nil && msg.Usage.TotalTokens > 0 {
			// Use context window from config if available, otherwise use a default
			const defaultContextWindow = 200000 // matches internal/winai/interpreter.go default
			tokensMax := defaultContextWindow
			if config.ContextWindow > 0 {
				tokensMax = config.ContextWindow
			}
			// Update AgentState with usage info
			agentCtx.AgentState.TokensUsed = msg.Usage.TotalTokens
			agentCtx.AgentState.TokensLimit = tokensMax
			agentCtx.AgentState.TotalTurns = len(agentCtx.RecentMessages)
		}

		// Check for error or abort (special cases that end the loop immediately)
		if msg.StopReason == "error" || msg.StopReason == "aborted" {
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.RecentMessages))
			return
		}

		// Check for non-success stopReason and notify user
		// This handles network_error, rate_limit_error, timeout, and any other
		// error conditions that should be reported to the user instead of silent failure.
		if sanitized := sanitizeMessageForNonSuccessStopReason(msg); sanitized {
			slog.Warn("[Loop] LLM request ended with non-success stopReason", "stopReason", msg.StopReason)
			traceevent.Log(ctx, traceevent.CategoryEvent, "non_success_stop_reason_detected",
				traceevent.Field{Key: "stopReason", Value: msg.StopReason})
			// Update the message in both arrays to include the error notification
			agentCtx.RecentMessages[len(agentCtx.RecentMessages)-1] = *msg
			newMessages[len(newMessages)-1] = *msg
		}

		// Check for tool calls
		toolCalls := msg.ExtractToolCalls()
		hasMoreToolCalls := len(toolCalls) > 0
		if hasMoreToolCalls {
			malformedToolCallRecoveries = 0
		}
		if hasMoreToolCalls && loopGuard != nil {
			if blocked, reason := loopGuard.Observe(toolCalls); blocked {
				slog.Warn("[Loop] tool call loop guard triggered", "reason", reason)
				stream.Push(NewLoopGuardTriggeredEvent(LoopGuardInfo{Reason: reason}))
				traceevent.Log(ctx, traceevent.CategoryEvent, "tool_loop_guard_triggered",
					traceevent.Field{Key: "reason", Value: reason},
					traceevent.Field{Key: "call_count", Value: len(toolCalls)},
				)
				sanitizeMessageForToolLoopGuard(msg, reason)
				agentCtx.RecentMessages[len(agentCtx.RecentMessages)-1] = *msg
				newMessages[len(newMessages)-1] = *msg
				hasMoreToolCalls = false
			}
		}

		var toolResults []agentctx.AgentMessage
		if hasMoreToolCalls {
			toolResults = executeToolCalls(ctx, agentCtx, agentCtx.Tools, agentCtx.GetAllowedToolsMap(), msg, stream, config.Executor, config.Metrics, config.ToolOutput)
			for _, result := range toolResults {
				agentCtx.RecentMessages = append(agentCtx.RecentMessages, result)
				newMessages = append(newMessages, result)
			}
		}

		stream.Push(NewTurnEndEvent(msg, toolResults))

		// Increment tool call counter for compactor trigger intervals
		if hasMoreToolCalls {
			agentCtx.AgentState.ToolCallsSinceLastTrigger += len(toolResults)
		}

		// Note: toolResult persistence is handled by the sessionWriter (via tool_execution_end
		// events) in all modes (RPC, headless, json). We do NOT write toolResults to the journal
		// here to avoid duplicating each message in messages.jsonl.

		// Create checkpoint after update_llm_context tool execution
		if checkpointMgr != nil && checkpointMgr.ShouldCheckpoint() && hasToolResultNamed(toolResults, "update_llm_context") {
			llmContextContent := agentCtx.LLMContext
			if _, err := checkpointMgr.CreateSnapshot(agentCtx, llmContextContent, turnCount); err != nil {
				slog.Warn("[Loop] Failed to create checkpoint after update_llm_context", "error", err, "turn", turnCount)
			} else {
				slog.Info("[Loop] Checkpoint created after update_llm_context", "turn", turnCount)
			}
		}

		// If no more tool calls, end the conversation
		if !hasMoreToolCalls {
			if maybeRecoverMalformedToolCall(ctx, agentCtx, &newMessages, stream, msg, &malformedToolCallRecoveries) {
				continue
			}

			// Check for empty response: stop_reason=stop but no actionable content
			// (no text, no tool calls — only thinking content). This can happen with
			// models like glm-5.1 that sometimes return only thinking and stop.
			if msg.StopReason == "stop" && isEmptyActionableResponse(msg) && emptyResponseRetries < defaultEmptyResponseMaxRetries {
				emptyResponseRetries++
				slog.Warn("[Loop] LLM returned stop_reason=stop with empty actionable output (no text, no tool calls); retrying",
					"stopReason", msg.StopReason,
					"turn", turnCount,
					"retry_attempt", emptyResponseRetries,
					"max_retries", defaultEmptyResponseMaxRetries,
				)
				continue
			}

			break
		}
	}

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
