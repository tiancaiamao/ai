package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const (
	agentBusyTimeout = 60 * time.Second // Wait timeout for agent lock
	traceFlushEvery  = 256
	traceFlushWindow = 1 * time.Second
	traceFlushTick   = 200 * time.Millisecond
)

func shouldLogAgentEvent(eventType string) bool {
	if !traceevent.IsEventEnabled("log:agent_event_stream") {
		return false
	}
	switch eventType {
	case EventMessageUpdate, EventTextDelta, EventThinkingDelta, EventToolCallDelta:
		// Keep high-frequency agent stream logs additionally gated by event type switches.
		if eventType == EventMessageUpdate && !traceevent.IsEventEnabled("message_update") {
			return false
		}
		if eventType == EventTextDelta && !traceevent.IsEventEnabled("text_delta") {
			return false
		}
		if eventType == EventThinkingDelta && !traceevent.IsEventEnabled("thinking_delta") {
			return false
		}
		if eventType == EventToolCallDelta && !traceevent.IsEventEnabled("tool_call_delta") {
			return false
		}
		return true
	default:
		return true
	}
}

// ErrAgentBusy is returned when the agent is already processing a request.
var ErrAgentBusy = errors.New("agent is busy")

// Compactor interface for context compression.
type Compactor interface {
	ShouldCompact(messages []AgentMessage) bool
	Compact(messages []AgentMessage) ([]AgentMessage, error)
}

// Agent represents an AI agent.
type Agent struct {
	mu            chan struct{}
	model         llm.Model
	apiKey        string
	systemPrompt  string
	context       *AgentContext
	eventChan     chan AgentEvent
	currentStream *llm.EventStream[AgentEvent, []AgentMessage]
	streamMu      sync.RWMutex
	// 	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	compactor       Compactor     // Optional compactor for automatic context compression
	followUpQueue   chan string   // Queue for follow-up messages
	executor        *ExecutorPool // Tool executor with concurrency control
	metrics         *Metrics      // Performance and usage metrics
	toolOutput      ToolOutputLimits
	toolCallCutoff  int
	toolSummaryMode string
	thinkingLevel   string
	traceBuf        *traceevent.TraceBuf
	traceStop       chan struct{}
	traceDone       chan struct{}
	shutdownOnce    sync.Once
	traceSeq        atomic.Uint64
}

// NewAgent creates a new agent.
func NewAgent(model llm.Model, apiKey, systemPrompt string) *Agent {
	return NewAgentWithContext(model, apiKey, NewAgentContext(systemPrompt))
}

// NewAgentWithContext creates a new agent with a custom context.
func NewAgentWithContext(model llm.Model, apiKey string, agentCtx *AgentContext) *Agent {
	metrics := NewMetrics()
	traceBuf := traceevent.NewTraceBuf()
	traceBuf.SetTraceID(traceevent.GenerateTraceID("session", 0))
	traceBuf.SetFlushEvery(traceFlushEvery)
	traceBuf.SetFlushInterval(traceFlushWindow)
	traceBuf.AddSink(func(event traceevent.TraceEvent) {
		metrics.RecordTraceEvent(event)
	})
	traceevent.SetActiveTraceBuf(traceBuf)

	a := &Agent{
		mu:           make(chan struct{}, 1),
		model:        model,
		apiKey:       apiKey,
		systemPrompt: agentCtx.SystemPrompt,
		context:      agentCtx,
		eventChan:    make(chan AgentEvent, 100),
		// 		ctx:             ctx,
		// 		cancel:          cancel,
		followUpQueue:   make(chan string, 100), // Buffer up to 100 follow-up messages (increased from 10)
		executor:        NewExecutorPool(map[string]int{"maxConcurrentTools": 3, "toolTimeout": 30, "queueTimeout": 60}),
		metrics:         metrics,
		toolOutput:      DefaultToolOutputLimits(),
		toolCallCutoff:  10,
		toolSummaryMode: "llm",
		thinkingLevel:   "high",
		traceBuf:        traceBuf,
		traceStop:       make(chan struct{}),
		traceDone:       make(chan struct{}),
	}

	go a.runTraceFlusher()
	return a
}

// Prompt sends a user message to the agent and waits for completion.
// Waits up to agentBusyTimeout for the agent to become available.
func (a *Agent) Prompt(message string) error {
	timer := time.NewTimer(agentBusyTimeout)
	defer timer.Stop()

	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := traceevent.WithTraceBuf(baseCtx, a.traceBuf)
	a.cancel = cancel

	select {
	case a.mu <- struct{}{}:
		a.wg.Add(1)
		go func() {
			defer func() { <-a.mu }()
			defer a.wg.Done()
			slog.Info("[Agent] Starting prompt", "message", message)

			a.processPrompt(ctx, message)

			// Check for follow-up messages
			for {
				select {
				case followUpMsg := <-a.followUpQueue:
					slog.Info("[Agent] Processing follow-up", "message", followUpMsg)
					a.processPrompt(ctx, followUpMsg)
				default:
					// No more follow-up messages
					return
				}
			}
		}()
		return nil
	case <-timer.C:
		return fmt.Errorf("agent busy timeout after %v", agentBusyTimeout)
	}
}

// processPrompt handles a single prompt (shared by Prompt and follow-up).
func (a *Agent) processPrompt(ctx context.Context, message string) {
	hadError := false

	a.rotateTraceForPrompt(ctx)
	defer a.finalizeTraceForPrompt(ctx)

	// Create span for prompt processing - auto-records begin/end
	span := traceevent.StartSpan(ctx, "prompt", traceevent.CategoryEvent,
		traceevent.Field{Key: "message", Value: message})
	defer span.End()

	// Create child span for event iteration
	eventLoopSpan := span.StartChild("event_loop",
		traceevent.Field{Key: "turn_count", Value: len(a.context.Messages)})
	defer eventLoopSpan.End()

	prompts := []AgentMessage{NewUserMessage(message)}

	config := &LoopConfig{
		Model:               a.model,
		APIKey:              a.apiKey,
		Executor:            a.executor,
		Metrics:             a.metrics,
		ToolOutput:          a.toolOutput,
		Compactor:           a.compactor,
		ToolCallCutoff:      a.toolCallCutoff,
		ToolSummaryStrategy: a.toolSummaryMode,
		ThinkingLevel:       a.thinkingLevel,
	}

	slog.Info("[Agent] Starting RunLoop")
	stream := RunLoop(ctx, prompts, a.context, config)
	a.setCurrentStream(stream)
	defer a.setCurrentStream(nil)

	// Emit events to channel
	slog.Info("[Agent] Starting event iteration")
	eventCount := 0
	for event := range stream.Iterator(ctx) {
		if event.Done {
			slog.Info("[Agent] Event stream done", "totalEvents", eventCount)
			break
		}

		traceFields := []traceevent.Field{
			{Key: "event_at", Value: event.Value.EventAt},
		}
		if event.Value.Message != nil {
			traceFields = append(traceFields,
				traceevent.Field{Key: "role", Value: event.Value.Message.Role},
				traceevent.Field{Key: "stop_reason", Value: event.Value.Message.StopReason},
			)
		}
		if event.Value.ToolName != "" {
			traceFields = append(traceFields,
				traceevent.Field{Key: "tool_name", Value: event.Value.ToolName},
				traceevent.Field{Key: "tool_call_id", Value: event.Value.ToolCallID},
			)
		}

		traceevent.Log(ctx, traceevent.CategoryEvent, event.Value.Type, traceFields...)

		if update, ok := event.Value.AssistantMessageEvent.(AssistantMessageEvent); ok {
			traceevent.Log(ctx, traceevent.CategoryEvent, "message_update",
				traceevent.Field{Key: "update_type", Value: update.Type},
				traceevent.Field{Key: "content_index", Value: update.ContentIndex},
			)
			switch update.Type {
			case "text_start":
				traceevent.Log(ctx, traceevent.CategoryLLM, "assistant_text",
					traceevent.Field{Key: "state", Value: "start"})
			case "text_end":
				traceevent.Log(ctx, traceevent.CategoryLLM, "assistant_text",
					traceevent.Field{Key: "state", Value: "end"})
			}
		}

		eventCount++
		if shouldLogAgentEvent(event.Value.Type) {
			slog.Debug("[Agent] Got event", "type", event.Value.Type)
		}

		switch event.Value.Type {
		case EventToolExecutionEnd:
			if event.Value.IsError {
				hadError = true
			}
		case EventTurnEnd:
			if event.Value.Message == nil {
				hadError = true
			} else if event.Value.Message.StopReason == "error" || event.Value.Message.StopReason == "aborted" {
				hadError = true
			}
		}

		// Update context with new messages
		if event.Value.Type == EventMessageEnd {
			msg := event.Value.Message
			if msg != nil && msg.Role == "user" {
				a.context.AddMessage(*msg)
			}
		}
		if event.Value.Type == EventTurnEnd {
			msg := event.Value.Message
			if msg != nil {
				a.context.AddMessage(*msg)
			}
			for _, tr := range event.Value.ToolResults {
				a.context.AddMessage(tr)
			}
			// Try automatic compression after each turn
			a.tryAutoCompact(ctx)
		}

		// Send to event channel
		a.emitEvent(event.Value)
	}
	span.AddField("error", hadError)
	if hadError {
		span.AddField("error_message", errors.New("prompt failed").Error())
	}

	slog.Info("[Agent] Prompt completed")
}

func (a *Agent) rotateTraceForPrompt(ctx context.Context) {
	if a.traceBuf == nil {
		return
	}
	if err := a.traceBuf.DiscardOrFlush(ctx); err != nil {
		slog.Error("[Agent] Failed to rotate trace file", "error", err)
	}
	seq := int(a.traceSeq.Add(1))
	a.traceBuf.SetTraceID(traceevent.GenerateTraceID("session", seq))
}

func (a *Agent) finalizeTraceForPrompt(ctx context.Context) {
	if a.traceBuf == nil {
		return
	}
	if err := a.traceBuf.DiscardOrFlush(ctx); err != nil {
		slog.Error("[Agent] Failed to finalize trace file", "error", err)
	}
	// Move to a fresh trace ID so post-prompt logs don't overwrite finalized prompt traces.
	seq := int(a.traceSeq.Add(1))
	a.traceBuf.SetTraceID(traceevent.GenerateTraceID("session", seq))
}

func (a *Agent) runTraceFlusher() {
	ticker := time.NewTicker(traceFlushTick)
	defer ticker.Stop()
	defer close(a.traceDone)
	for {
		select {
		case <-a.traceStop:
			return
		case <-ticker.C:
			if a.traceBuf != nil {
				_ = a.traceBuf.FlushIfNeeded(context.Background())
			}
		}
	}
}

func (a *Agent) shutdownTracing() {
	a.shutdownOnce.Do(func() {
		if a.traceStop != nil {
			close(a.traceStop)
			<-a.traceDone
		}
		if a.traceBuf != nil {
			if err := a.traceBuf.DiscardOrFlush(context.Background()); err != nil {
				slog.Error("[Agent] Failed to flush trace", "error", err)
			}
			traceevent.ClearActiveTraceBuf(a.traceBuf)
		}
	})
}

// Wait waits for all agent operations to complete.
func (a *Agent) Wait() {
	a.wg.Wait()
}

// Shutdown flushes and finalizes trace output.
func (a *Agent) Shutdown() {
	a.shutdownTracing()
}

// Steer interrupts the current execution and sends a new message.
func (a *Agent) Steer(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		slog.Warn("[Agent] Steer called with empty message")
		return
	}

	// Cancel current execution
	if a.cancel != nil {
		a.cancel()
	}

	// Create new context
	ctx, cancel := context.WithCancel(context.Background())
	if a.traceBuf != nil {
		ctx = traceevent.WithTraceBuf(ctx, a.traceBuf)
	}
	// 	a.ctx = ctx
	a.cancel = cancel

	// Send prompt with steering message
	if err := a.Prompt(message); err != nil {
		if errors.Is(err, ErrAgentBusy) {
			if followErr := a.FollowUp(message); followErr != nil {
				slog.Error("[Agent] Steer follow-up failed", "error", followErr)
			}
			return
		}
		slog.Error("[Agent] Steer prompt failed", "error", err)
	}
}

// Abort stops the current execution.
func (a *Agent) Abort() {
	slog.Info("[Agent] Abort called, canceling context")
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	aborted := a.abortCurrentStream()
	if aborted {
		a.clearFollowUps()
	}
	// 	ctx, cancel := context.WithCancel(context.Background())
	// 	if a.traceBuf != nil {
	// 		ctx = traceevent.WithTraceBuf(ctx, a.traceBuf)
	// 	}
	// 	a.ctx = ctx
	// 	a.cancel = cancel
	slog.Info("[Agent] Context canceled, waiting for agent to finish")
}

// FollowUp adds a message to be processed after the current prompt completes.
func (a *Agent) FollowUp(message string) error {
	select {
	case a.followUpQueue <- message:
		slog.Debug("[Agent] Follow-up queued", "message", message)
		return nil
	default:
		return fmt.Errorf("follow-up queue full")
	}
}

// Events returns the event channel.
func (a *Agent) Events() <-chan AgentEvent {
	return a.eventChan
}

// GetState returns the current agent state.
func (a *Agent) GetState() map[string]any {
	return map[string]any{
		"model":        a.model,
		"systemPrompt": a.systemPrompt,
		"messageCount": len(a.context.Messages),
		"toolCount":    len(a.context.Tools),
	}
}

// SetModel updates the active model configuration.
func (a *Agent) SetModel(model llm.Model) {
	a.model = model
}

// SetAPIKey updates the API key for the active model.
func (a *Agent) SetAPIKey(apiKey string) {
	a.apiKey = apiKey
}

// GetModel returns the active model configuration.
func (a *Agent) GetModel() llm.Model {
	return a.model
}

// GetMessages returns all messages in the context.
func (a *Agent) GetMessages() []AgentMessage {
	return a.context.Messages
}

// AddTool adds a tool to the agent.
func (a *Agent) AddTool(tool Tool) {
	a.context.AddTool(tool)
}

// SetContext sets the agent context.
func (a *Agent) SetContext(ctx *AgentContext) {
	if ctx != nil && len(ctx.Tools) == 0 && a.context != nil && len(a.context.Tools) > 0 {
		ctx.Tools = append(ctx.Tools, a.context.Tools...)
	}
	a.context = ctx
}

// GetContext returns the agent context.
func (a *Agent) GetContext() *AgentContext {
	return a.context
}

// SetCompactor sets the compactor for automatic context compression.
func (a *Agent) SetCompactor(compactor Compactor) {
	a.compactor = compactor
}

// SetExecutor sets the tool executor pool for concurrency control.
func (a *Agent) SetExecutor(executor *ExecutorPool) {
	a.executor = executor
}

// GetExecutor returns the current tool executor.
func (a *Agent) GetExecutor() *ExecutorPool {
	return a.executor
}

// SetToolOutputLimits sets truncation limits for tool output.
func (a *Agent) SetToolOutputLimits(limits ToolOutputLimits) {
	a.toolOutput = limits
}

// SetToolCallCutoff sets threshold for automatic tool output summarization.
func (a *Agent) SetToolCallCutoff(cutoff int) {
	if cutoff < 0 {
		cutoff = 0
	}
	a.toolCallCutoff = cutoff
}

// SetToolSummaryStrategy controls automatic tool summarization behavior.
// Accepted values: llm, heuristic, off.
func (a *Agent) SetToolSummaryStrategy(strategy string) {
	a.toolSummaryMode = normalizeToolSummaryStrategy(strategy)
}

// SetThinkingLevel controls reasoning depth instructions sent to the model.
// Accepted values: off, minimal, low, medium, high, xhigh.
func (a *Agent) SetThinkingLevel(level string) {
	a.thinkingLevel = prompt.NormalizeThinkingLevel(level)
}

// GetPendingFollowUps returns the number of queued follow-up messages.
func (a *Agent) GetPendingFollowUps() int {
	return len(a.followUpQueue)
}

// Compact compacts the agent's context messages using the provided compactor.
func (a *Agent) Compact(compactor Compactor) error {
	messages := a.context.Messages
	compacted, err := compactor.Compact(messages)
	if err != nil {
		return fmt.Errorf("failed to compact: %w", err)
	}

	// Replace messages with compacted version
	a.context.Messages = compacted
	return nil
}

// tryAutoCompact attempts automatic compression if thresholds exceeded.
func (a *Agent) tryAutoCompact(ctx context.Context) {
	if a.compactor == nil {
		return
	}

	messages := a.context.Messages
	if a.compactor.ShouldCompact(messages) {
		before := len(messages)
		slog.Info("[Agent] Auto-compacting", "beforeCount", before)
		compactSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
			traceevent.Field{Key: "source", Value: "auto_threshold"},
			traceevent.Field{Key: "auto", Value: true},
			traceevent.Field{Key: "before_messages", Value: before},
			traceevent.Field{Key: "trigger", Value: "threshold"},
		)
		a.emitEvent(NewCompactionStartEvent(CompactionInfo{
			Auto:    true,
			Before:  before,
			Trigger: "threshold",
		}))
		if err := a.Compact(a.compactor); err != nil {
			slog.Error("[Agent] Auto-compact failed", "error", err)
			compactSpan.AddField("error", true)
			compactSpan.AddField("error_message", err.Error())
			compactSpan.End()
			a.emitEvent(NewCompactionEndEvent(CompactionInfo{
				Auto:    true,
				Before:  before,
				Error:   err.Error(),
				Trigger: "threshold",
			}))
		} else {
			after := len(a.context.Messages)
			slog.Info("[Agent] Auto-compact successful", "before", before, "after", after)
			compactSpan.AddField("after_messages", after)
			compactSpan.End()
			a.emitEvent(NewCompactionEndEvent(CompactionInfo{
				Auto:    true,
				Before:  before,
				After:   after,
				Trigger: "threshold",
			}))
		}
	}
}

func (a *Agent) emitEvent(event AgentEvent) {
	select {
	case a.eventChan <- event:
	default:
		slog.Warn("[Agent] Event channel full, dropping event")
	}
}

func (a *Agent) setCurrentStream(stream *llm.EventStream[AgentEvent, []AgentMessage]) {
	a.streamMu.Lock()
	a.currentStream = stream
	a.streamMu.Unlock()
}

func (a *Agent) getCurrentStream() *llm.EventStream[AgentEvent, []AgentMessage] {
	a.streamMu.RLock()
	stream := a.currentStream
	a.streamMu.RUnlock()
	return stream
}

func (a *Agent) abortCurrentStream() bool {
	stream := a.getCurrentStream()
	if stream == nil || stream.IsDone() {
		return false
	}
	// Force an agent_end event so UI state can reset even if the iterator stops on ctx cancel.
	stream.Push(NewAgentEndEvent(a.context.Messages))
	a.emitEvent(NewAgentEndEvent(a.context.Messages))
	return true
}

func (a *Agent) clearFollowUps() {
	for {
		select {
		case <-a.followUpQueue:
		default:
			return
		}
	}
}

// GetMetrics returns the agent's metrics collector.
func (a *Agent) GetMetrics() *Metrics {
	return a.metrics
}
