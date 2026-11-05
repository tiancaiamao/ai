package agent

import (
	"context"
	"errors"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
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

	// eventDrainTimeout bounds how long processPrompt waits for the loop to
	// finish pushing its tail events after the run's ctx was canceled
	// (abort) before draining them for context writeback. Salvage
	// pushes happen within milliseconds of cancellation; the bound keeps
	// Abort responsive when a producer is wedged in a ctx-unaware tool.
	eventDrainTimeout = 500 * time.Millisecond

	// defaultSteerQueueDepth bounds queued steering messages when no
	// explicit depth is configured (steering modes "all"/"immediate").
	defaultSteerQueueDepth = 64

	// defaultEventQueueDepth bounds queued machine events (background
	// operation results). Delivered events would otherwise accumulate for the
	// whole run and burst at the end.
	defaultEventQueueDepth = 64

	// steerTickInterval delays the one-shot recheck that closes the
	// enqueue-vs-run-exit race (see scheduleSteerTick): short enough that
	// an orphaned steer is picked up promptly, long enough that the
	// re-arm polling while a run is busy stays cheap.
	steerTickInterval = 100 * time.Millisecond
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

// Compactor interface is defined in pkg/context package.
// Agent uses agentctx.Compactor for context compression.
type Compactor = agentctx.Compactor

// CompactionResult is an alias to agentctx.CompactionResult.
type CompactionResult = agentctx.CompactionResult

// Agent represents an AI agent.
type Agent struct {
	mu            chan struct{}
	model         llm.Model
	apiKey        string
	systemPrompt  string
	context       *agentctx.AgentContext
	ctxMu         sync.RWMutex // Protects context.RecentMessages access
	eventChan     chan AgentEvent
	currentStream *llm.EventStream[AgentEvent, []agentctx.AgentMessage]
	streamMu      sync.RWMutex
	// 	ctx             context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	followUpQueue chan string // Queue for follow-up messages
	// steerMu guards steerQueue and steerDepth.
	steerMu sync.Mutex
	// steerQueue holds steering messages waiting for the next model-call
	// boundary. Steer never cancels the in-flight prompt; the run loop
	// drains this queue at the top of each iteration.
	steerQueue []QueuedMessage
	// steerDepth caps steerQueue (0 = defaultSteerQueueDepth).
	steerDepth int
	// steerTickMu guards steerTick (the one-shot drain-retry timer). It is
	// separate from steerMu so Steer can schedule a tick while holding
	// steerMu.
	steerTickMu sync.Mutex
	steerTick   *time.Timer
	// eventMu guards eventQueue.
	eventMu sync.Mutex
	// eventQueue holds machine-generated events (ai send --event). Lower
	// priority than steer/followUp: runMessage drains it only after every
	// human message is exhausted, so machine traffic cannot starve a person.
	eventQueue    []QueuedMessage
	runLoopFn     func(ctx context.Context, prompts []agentctx.AgentMessage, agentCtx *agentctx.AgentContext, config *LoopConfig) *llm.EventStream[AgentEvent, []agentctx.AgentMessage]
	traceBuf      *traceevent.TraceBuf
	traceStop     chan struct{}
	traceDone     chan struct{}
	shutdownOnce  sync.Once
	traceSeq      atomic.Uint64
	manualCompact atomic.Bool

	// LoopConfig embedded for unified configuration management
	LoopConfig
}

// NewAgent creates a new agent with default configuration.
func NewAgent(model llm.Model, apiKey, systemPrompt string) *Agent {
	return NewAgentWithContext(model, apiKey, agentctx.NewAgentContext(systemPrompt))
}

// NewAgentFromConfig creates a new agent from a LoopConfig.
// This is the preferred constructor for production use.
func NewAgentFromConfig(model llm.Model, apiKey, systemPrompt string, cfg *LoopConfig) *Agent {
	return NewAgentFromConfigWithContext(model, apiKey, agentctx.NewAgentContext(systemPrompt), cfg)
}

// NewAgentWithContext creates a new agent with a custom context.
func NewAgentWithContext(model llm.Model, apiKey string, agentCtx *agentctx.AgentContext) *Agent {
	return NewAgentFromConfigWithContext(model, apiKey, agentCtx, DefaultLoopConfig())
}

// NewAgentFromConfigWithContext creates a new agent with a custom context from LoopConfig.
func NewAgentFromConfigWithContext(model llm.Model, apiKey string, agentCtx *agentctx.AgentContext, cfg *LoopConfig) *Agent {
	traceBuf := traceevent.NewTraceBuf()
	traceBuf.SetTraceID(traceevent.GenerateTraceID("session", 0))
	traceBuf.SetFlushEvery(traceFlushEvery)
	traceBuf.SetFlushInterval(traceFlushWindow)
	traceevent.SetActiveTraceBuf(traceBuf)

	// Ensure Executor is set in config
	if cfg.Executor == nil {
		cfg.Executor = NewToolExecutor(10, 60)
	}

	// Ensure ToolOutput is set in config
	if cfg.ToolOutput.MaxChars == 0 {
		cfg.ToolOutput = DefaultToolOutputLimits()
	}

	a := &Agent{
		mu:            make(chan struct{}, 1),
		model:         model,
		apiKey:        apiKey,
		systemPrompt:  agentCtx.SystemPrompt,
		context:       agentCtx,
		eventChan:     make(chan AgentEvent, 100),
		followUpQueue: make(chan string, 100),
		runLoopFn:     RunLoop,
		traceBuf:      traceBuf,
		traceStop:     make(chan struct{}),
		traceDone:     make(chan struct{}),
		LoopConfig:    *cfg, // Embedded LoopConfig with applied options
	}

	// Set GetModel callback to enable dynamic model switching during loop execution.
	// This allows the loop to get the current model value after SetModel() is called.
	a.LoopConfig.GetModel = func() llm.Model {
		return a.model
	}

	// Set GetAPIKey callback to enable dynamic API key switching during loop execution.
	// This allows the loop to get the current API key value after SetAPIKey() is called.
	a.LoopConfig.GetAPIKey = func() string {
		return a.apiKey
	}
	a.LoopConfig.ConsumeManualCompaction = func() bool {
		return a.manualCompact.Swap(false)
	}

	// Steering queue hooks: the run loop drains queued steer messages at
	// each model-call boundary and stays alive while any remain.
	a.LoopConfig.DrainSteer = a.drainSteers
	a.LoopConfig.DrainEvent = a.drainEvents
	a.LoopConfig.HasPendingSteer = func() bool { return a.GetPendingSteer() > 0 }

	go a.runTraceFlusher()
	return a
}

// RequestCompaction queues a manual compaction for the running agent loop.
// The request is consumed at the next safe step boundary.
func (a *Agent) RequestCompaction() {
	a.manualCompact.Store(true)
}

// HasPendingCompaction reports whether a manual compaction is queued.
func (a *Agent) HasPendingCompaction() bool {
	return a.manualCompact.Load()
}

// Prompt sends a user message to the agent and waits for completion.
// Waits up to agentBusyTimeout for the agent to become available.
func (a *Agent) Prompt(message string) error {
	timer := time.NewTimer(agentBusyTimeout)
	defer timer.Stop()

	select {
	case a.mu <- struct{}{}:
		a.startRunLocked(message)
		return nil
	case <-timer.C:
		return fmt.Errorf("agent busy timeout after %v", agentBusyTimeout)
	}
}

// tryStartRun starts a run immediately if the agent is idle, without
// waiting. Returns ErrAgentBusy when a run is in progress.
func (a *Agent) tryStartRun(message string) error {
	select {
	case a.mu <- struct{}{}:
		a.startRunLocked(message)
		return nil
	default:
		return ErrAgentBusy
	}
}

// startRunLocked spawns the goroutine that owns the run. The caller must
// hold a.mu; runMessage releases it when the run — and any steering or
// follow-up messages queued behind it — finish.
func (a *Agent) startRunLocked(message string) {
	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := traceevent.WithTraceBuf(baseCtx, a.traceBuf)
	a.cancel = cancel

	a.wg.Add(1)
	go func() {
		defer func() { <-a.mu }()
		defer a.wg.Done()
		a.runMessage(ctx, message)
	}()
}

// runMessage runs one prompt to completion, then processes messages that
// were queued while it ran: steering messages first (they target the run
// that just ended), then follow-ups. Because a.mu stays held for the whole
// chain, Steer and FollowUp enqueue instead of starting a competing run.
func (a *Agent) runMessage(ctx context.Context, message string) {
	slog.Info("[Agent] Starting prompt", "message", message)
	a.processPrompt(ctx, message)

	for {
		if steerMsg := a.popSteer(); steerMsg.Text != "" {
			if ctx.Err() != nil {
				// Aborted before this steer was consumed: keep it queued for
				// the next run instead of silently dropping it.
				a.pushSteerFront(steerMsg)
				return
			}
			slog.Info("[Agent] Processing queued steer", "message", steerMsg.Text)
			a.processPrompt(ctx, steerMsg.Text)
			continue
		}
		select {
		case followUpMsg := <-a.followUpQueue:
			if ctx.Err() != nil {
				// Aborted while a follow-up was queued. Put it back so the
				// next run processes it with a fresh context instead of
				// running with a dead one.
				select {
				case a.followUpQueue <- followUpMsg:
				default: // queue full — drop rather than block forever
				}
				return
			}
			slog.Info("[Agent] Processing follow-up", "message", followUpMsg)
			a.processPrompt(ctx, followUpMsg)
			continue
		default:
		}
		// Humans first: only when no steer and no follow-up is pending do we
		// spend a turn on a machine event, so event traffic cannot preempt or
		// starve a person.
		if evtMsg := a.popEvent(); evtMsg.Text != "" {
			if ctx.Err() != nil {
				a.pushEventFront(evtMsg)
				return
			}
			slog.Info("[Agent] Processing queued event", "message", evtMsg.Text)
			a.processPrompt(ctx, evtMsg.Text)
			continue
		}
		return
	}
}

// processPrompt handles a single prompt (shared by Prompt and follow-up).
func (a *Agent) processPrompt(ctx context.Context, message string) {
	hadError := false
	promptErrorMessage := ""
	promptErrorStack := ""

	a.rotateTraceForPrompt(ctx)
	defer a.finalizeTraceForPrompt(ctx)

	// Create span for prompt processing - auto-records begin/end
	span := traceevent.StartSpan(ctx, "prompt", traceevent.CategoryEvent,
		traceevent.Field{Key: "message", Value: message})
	defer span.End()

	// Create child span for event iteration
	eventLoopSpan := span.StartChild("event_loop",
		traceevent.Field{Key: "turn_count", Value: len(a.context.RecentMessages)})
	defer eventLoopSpan.End()

	prompts := []agentctx.AgentMessage{agentctx.NewUserMessage(message)}

	slog.Info("[Agent] Starting RunLoop")
	stream := a.runLoopFn(ctx, prompts, a.context, &a.LoopConfig)
	a.setCurrentStream(stream)
	defer a.setCurrentStream(nil)

	// Emit events to channel
	slog.Info("[Agent] Starting event iteration")
	eventCount := 0
	handleEvent := func(event llm.IterResult[AgentEvent]) {
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
		if event.Value.Type == EventError {
			if event.Value.Error != "" {
				traceFields = append(traceFields, traceevent.Field{Key: "error_message", Value: event.Value.Error})
			}
			if event.Value.ErrorStack != "" {
				traceFields = append(traceFields, traceevent.Field{Key: "error_stack", Value: event.Value.ErrorStack})
			}
		}

		var eventAt time.Time
		if event.Value.EventAt > 0 {
			eventAt = time.Unix(0, event.Value.EventAt)
		}
		traceevent.LogAt(ctx, traceevent.CategoryEvent, event.Value.Type, eventAt, traceFields...)

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
			slog.Info("[Agent] Got event", "type", event.Value.Type)
		}

		switch event.Value.Type {
		case EventToolExecutionEnd:
			if event.Value.IsError {
				hadError = true
			}
		case EventError:
			hadError = true
			if event.Value.Error != "" {
				promptErrorMessage = event.Value.Error
			}
			if event.Value.ErrorStack != "" {
				promptErrorStack = event.Value.ErrorStack
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
				a.ctxMu.Lock()
				a.context.AddMessage(*msg)
				a.ctxMu.Unlock()
			}
		}
		if event.Value.Type == EventTurnEnd {
			msg := event.Value.Message
			if msg != nil {
				a.ctxMu.Lock()
				a.context.AddMessage(*msg)
				a.ctxMu.Unlock()
			}
			for _, tr := range event.Value.ToolResults {
				a.ctxMu.Lock()
				a.context.AddMessage(tr)
				a.ctxMu.Unlock()
			}
			// Auto-compaction is owned by runInnerLoop pre-LLM checks.
			// Keeping a second trigger here causes duplicate context management runs.
		}
		if event.Value.Type == EventCompactionEnd {
			// Sync compaction result to the outer context immediately.
			// Without this, a.context.RecentMessages keeps growing via
			// AddMessage above while the loop's currentCtx gets compacted.
			// If EventAgentEnd is never consumed (e.g. Abort cancels ctx),
			// the next prompt starts with the stale un-compacted messages,
			// triggering a spurious compaction.
			if len(event.Value.Messages) > 0 {
				a.ctxMu.Lock()
				a.context.RecentMessages = append([]agentctx.AgentMessage(nil), event.Value.Messages...)
				a.ctxMu.Unlock()
			}
		}
		if event.Value.Type == EventAgentEnd {
			// Keep in-memory context consistent with loop-side mutations
			// (e.g. auto-compaction editing older messages).
			a.ctxMu.Lock()
			a.context.RecentMessages = append([]agentctx.AgentMessage(nil), event.Value.Messages...)
			a.ctxMu.Unlock()
		}

		// Send to event channel
		a.emitEvent(event.Value)
	}

	for event := range stream.Iterator(ctx) {
		if event.Done {
			slog.Info("[Agent] Event stream done", "totalEvents", eventCount)
			break
		}
		handleEvent(event)
	}

	// When ctx is canceled mid-run (steer/abort), Iterator returns on
	// ctx.Done() without draining events the loop pushed afterwards — the
	// salvaged "aborted" assistant message plus its turn_end among them.
	// Wait briefly for the producer to finish, then process whatever is
	// queued so the aborted message reaches a.context (and session
	// persistence) instead of being silently dropped.
	//
	// Drained agent_end events are skipped: abortCurrentStream already emits
	// its synthetic copy directly (re-emitting here would duplicate the
	// subscriber signal), and its snapshot may predate the salvage in this
	// same batch — reprocessing the overwrite would wipe the turn_end
	// writeback above. Baseline parity also gives steer's old run no
	// agent_end; loop-side compaction still syncs via EventCompactionEnd.
	stream.WaitDone(eventDrainTimeout)
	for _, tail := range stream.DrainQueued() {
		if tail.Type == EventAgentEnd {
			continue
		}
		handleEvent(llm.IterResult[AgentEvent]{Value: tail})
	}
	span.AddField("error", hadError)
	if hadError {
		if promptErrorMessage == "" {
			promptErrorMessage = "prompt failed"
		}
		span.AddField("error_message", promptErrorMessage)
		if promptErrorStack != "" {
			span.AddField("error_stack", promptErrorStack)
		}
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

	// Increment prompt count for unique trace file naming
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			fh.IncrementPromptCount()
		}
	}

	seq := int(a.traceSeq.Add(1))
	a.traceBuf.SetTraceID(traceevent.GenerateTraceID("session", seq))
}

func (a *Agent) finalizeTraceForPrompt(ctx context.Context) {
	if a.traceBuf == nil {
		return
	}
	if err := a.traceBuf.Flush(ctx); err != nil {
		slog.Error("[Agent] Failed to flush prompt trace", "error", err)
	}
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

// QueuedMessage is a message waiting for a model-call boundary. EnqueuedAt
// is when it was queued, so the loop can report how long it waited before
// injection instead of leaving that to be inferred from separate markers.
type QueuedMessage struct {
	Text       string
	EnqueuedAt time.Time
}

// Steer interrupts the current execution and sends a new message.
// Steer injects a message into the conversation at the next model-call
// boundary. It never cancels the in-flight prompt: while the agent is
// busy the message is queued (bounded by steerDepth, 0=default) and
// claimed by the run loop before its next LLM call; when the agent is
// idle, Steer starts a new prompt directly.
func (a *Agent) Steer(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		slog.Warn("[Agent] Steer called with empty message")
		return nil
	}

	if err := a.tryStartRun(message); !errors.Is(err, ErrAgentBusy) {
		return err // nil (a run started) or an unexpected error
	}

	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	depth := a.steerDepth
	if depth == 0 {
		depth = defaultSteerQueueDepth
	}
	if len(a.steerQueue) >= depth {
		return fmt.Errorf("steer queue full (depth %d)", depth)
	}
	a.steerQueue = append(a.steerQueue, QueuedMessage{Text: message, EnqueuedAt: time.Now()})
	slog.Info("[Agent] Steer queued", "queue", len(a.steerQueue), "depth", depth)
	traceevent.Log(context.Background(), traceevent.CategoryEvent, "steer_enqueued",
		traceevent.Field{Key: "queue_depth", Value: len(a.steerQueue)},
		traceevent.Field{Key: "message", Value: message},
	)
	// The busy run may exit without draining this message (it could have
	// passed its final popSteer before the append above). Arm a tick to
	// pick it up if that happens.
	a.scheduleSteerTick()
	return nil
}

// drainSteers claims all queued steering messages for the run loop. The
// claim is the first phase of the steer ack: once drained, the messages
// belong to the loop's history (injected as user messages accompanied by
// MessageStart/End events, which write the outer context back), so they
// survive even if the run ends before that writeback lands.
func (a *Agent) drainSteers() []QueuedMessage {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerQueue) == 0 {
		return nil
	}
	msgs := a.steerQueue
	a.steerQueue = nil
	return msgs
}

// drainEvents claims every queued machine event for the run loop, mirroring
// drainSteers.
func (a *Agent) drainEvents() []QueuedMessage {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	if len(a.eventQueue) == 0 {
		return nil
	}
	msgs := a.eventQueue
	a.eventQueue = nil
	return msgs
}

// popSteer removes the oldest queued steering message (empty when none).
func (a *Agent) popSteer() QueuedMessage {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerQueue) == 0 {
		return QueuedMessage{}
	}
	msg := a.steerQueue[0]
	a.steerQueue = a.steerQueue[1:]
	return msg
}

// pushSteerFront puts a claimed steering message back at the front of the
// queue (order-preserving put-back when a run is aborted mid-chain).
func (a *Agent) pushSteerFront(msg QueuedMessage) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	a.steerQueue = append([]QueuedMessage{msg}, a.steerQueue...)
}

// GetPendingSteer returns the number of steering messages waiting for the
// next model-call boundary.
func (a *Agent) GetPendingSteer() int {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return len(a.steerQueue)
}

// EnqueueEvent queues a machine-generated event (ai send --event). Like
// Steer it starts a run when idle and queues when busy. A queued event is
// injected at the next model-call boundary, right after any steering message
// (see DrainEvent), so a person still outranks a machine without letting
// events starve for the rest of the run.
func (a *Agent) EnqueueEvent(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if err := a.tryStartRun(message); !errors.Is(err, ErrAgentBusy) {
		return err
	}
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	if len(a.eventQueue) >= defaultEventQueueDepth {
		// Dropping a background result loses information, so reject instead
		// of silently overwriting: 64 undelivered events means the pipeline
		// is broken, not that one more event is fine to drop.
		return fmt.Errorf("event queue full (depth %d)", defaultEventQueueDepth)
	}
	a.eventQueue = append(a.eventQueue, QueuedMessage{Text: message, EnqueuedAt: time.Now()})
	traceevent.Log(context.Background(), traceevent.CategoryEvent, "event_enqueued",
		traceevent.Field{Key: "queue_depth", Value: len(a.eventQueue)},
		traceevent.Field{Key: "message", Value: message},
	)
	// Same enqueue-vs-run-exit window as steer: arm the shared tick so an
	// orphaned event still gets picked up.
	a.scheduleSteerTick()
	return nil
}

// popEvent dequeues the oldest event (empty when none).
func (a *Agent) popEvent() QueuedMessage {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	if len(a.eventQueue) == 0 {
		return QueuedMessage{}
	}
	msg := a.eventQueue[0]
	a.eventQueue = a.eventQueue[1:]
	return msg
}

// pushEventFront returns an unconsumed event to the head of the queue (abort
// path), mirroring pushSteerFront.
func (a *Agent) pushEventFront(msg QueuedMessage) {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	a.eventQueue = append([]QueuedMessage{msg}, a.eventQueue...)
}

// GetPendingEvent reports how many machine events are queued.
func (a *Agent) GetPendingEvent() int {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	return len(a.eventQueue)
}

// SetSteerQueueDepth caps the number of queued steering messages
// (0 = defaultSteerQueueDepth). Steering mode "one-at-a-time" maps to 1.
func (a *Agent) SetSteerQueueDepth(depth int) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	a.steerDepth = depth
}

// scheduleSteerTick arms a one-shot timer that rechecks the steer queue. It
// closes the enqueue-vs-run-exit race: a steer enqueued right after the
// busy run's final popSteer would otherwise sit in the queue with no run to
// drain it. No resident goroutine — time.AfterFunc fires once. Any pending
// timer is stopped and replaced so exactly one tick is armed.
func (a *Agent) scheduleSteerTick() {
	a.steerTickMu.Lock()
	defer a.steerTickMu.Unlock()
	if a.steerTick != nil {
		a.steerTick.Stop()
	}
	a.steerTick = time.AfterFunc(steerTickInterval, a.steerTickFn)
}

// disarmSteerTick cancels a pending drain-retry tick. Abort calls this so an
// abort is not undone by the tick auto-starting a run for a put-back steer;
// the message stays queued for the next Prompt instead.
func (a *Agent) disarmSteerTick() {
	a.steerTickMu.Lock()
	defer a.steerTickMu.Unlock()
	if a.steerTick != nil {
		a.steerTick.Stop()
		a.steerTick = nil
	}
}

// steerTickFn drains the steer queue if it is still non-empty and the agent
// has gone idle. When a run is still active it re-arms instead: that run may
// yet drain the queue at its next boundary, or may already be past its final
// check — firing again after it exits covers the latter.
func (a *Agent) steerTickFn() {
	if a.GetPendingSteer() == 0 && a.GetPendingEvent() == 0 {
		return // drained by the normal run
	}
	select {
	case a.mu <- struct{}{}:
	default: // busy
		a.scheduleSteerTick()
		return
	}
	// Prefer a human steer; a machine event is claimed only when no steer is
	// waiting. Whichever is claimed becomes the run's first prompt, and
	// runMessage drains the rest with the same human-first priority.
	msg := a.popSteer()
	if msg.Text == "" {
		msg = a.popEvent()
	}
	if msg.Text == "" {
		<-a.mu // queue emptied between the check and acquiring mu
		return
	}
	a.startRunLocked(msg.Text)
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
	// Do not let a pending drain-retry tick restart a run for a put-back
	// steer: abort means stop, and the message stays queued for the next
	// Prompt.
	a.disarmSteerTick()
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
		slog.Info("[Agent] Follow-up queued", "message", message)
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
		"messageCount": len(a.context.RecentMessages),
		"toolCount":    len(a.context.Tools),
	}
}

// SetModel updates the active model configuration.
func (a *Agent) SetModel(model llm.Model) {
	a.model = model
	a.LoopConfig.Model = model // Keep LoopConfig in sync for loop that reads from config
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
func (a *Agent) GetMessages() []agentctx.AgentMessage {
	a.ctxMu.RLock()
	defer a.ctxMu.RUnlock()
	return a.context.RecentMessages
}

// AddTool adds a tool to the agent.
func (a *Agent) AddTool(tool agentctx.Tool) {
	a.context.AddTool(tool)
}

// SetContext sets the agent context.
func (a *Agent) SetContext(ctx *agentctx.AgentContext) {
	if ctx != nil && len(ctx.Tools) == 0 && a.context != nil && len(a.context.Tools) > 0 {
		ctx.Tools = append(ctx.Tools, a.context.Tools...)
	}
	a.context = ctx
}

// GetContext returns the agent context.
func (a *Agent) GetContext() *agentctx.AgentContext {
	return a.context
}

// SetCompactor sets the compactor for automatic context compression.
func (a *Agent) SetCompactor(compactor Compactor) {
	a.LoopConfig.Compactor = compactor
}

// SetExecutor sets the tool executor for concurrency control.
func (a *Agent) SetExecutor(executor ToolExecutor) {
	a.LoopConfig.Executor = executor
}

// SetToolOutputLimits sets truncation limits for tool output.
func (a *Agent) SetToolOutputLimits(limits ToolOutputLimits) {
	a.LoopConfig.ToolOutput = normalizeToolOutputLimits(limits)
}

// SetToolCallCutoff sets threshold for automatic tool output summarization.
func (a *Agent) SetToolCallCutoff(cutoff int) {
	if cutoff < 0 {
		cutoff = 0
	}
	a.LoopConfig.ToolCallCutoff = cutoff
}

// SetThinkingLevel controls reasoning depth instructions sent to the model.
func (a *Agent) SetThinkingLevel(level string) {
	a.LoopConfig.ThinkingLevel = prompt.NormalizeThinkingLevel(level)
}

// SetAutoRetry enables/disables LLM automatic retry behavior.
func (a *Agent) SetAutoRetry(enabled bool) {
	if enabled {
		if a.LoopConfig.MaxLLMRetries <= 0 {
			a.LoopConfig.MaxLLMRetries = defaultLLMMaxRetries
		}
		if a.LoopConfig.RetryBaseDelay <= 0 {
			a.LoopConfig.RetryBaseDelay = defaultRetryBaseDelay
		}
		return
	}
	a.LoopConfig.MaxLLMRetries = 0
}

// SetContextWindow sets the context window for the model.
func (a *Agent) SetContextWindow(contextWindow int) {
	if contextWindow < 0 {
		contextWindow = 0
	}
	a.LoopConfig.ContextWindow = contextWindow
}

// GetPendingFollowUps returns the number of queued follow-up messages.
func (a *Agent) GetPendingFollowUps() int {
	return len(a.followUpQueue)
}

func (a *Agent) emitEvent(event AgentEvent) {
	select {
	case a.eventChan <- event:
	default:
		slog.Warn("[Agent] Event channel full, dropping event")
	}
}

func (a *Agent) setCurrentStream(stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage]) {
	a.streamMu.Lock()
	a.currentStream = stream
	a.streamMu.Unlock()
}

func (a *Agent) getCurrentStream() *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
	a.streamMu.RLock()
	stream := a.currentStream
	a.streamMu.RUnlock()
	return stream
}

func (a *Agent) abortCurrentStream() bool {
	stream := a.getCurrentStream()
	a.ctxMu.RLock()
	recent := append([]agentctx.AgentMessage(nil), a.context.RecentMessages...)
	a.ctxMu.RUnlock()

	agentEnd := NewAgentEndEvent(recent)
	if stream != nil && !stream.IsDone() {
		stream.Push(agentEnd)
	}
	// Always emit directly to the event channel. When ctx is cancelled the
	// iterator exits on ctx.Done() and processPrompt clears currentStream,
	// so by the time we get here the stream may be nil or done. Subscribers
	// (UI, event collectors) still need agent_end to reset their state.
	a.emitEvent(agentEnd)
	return stream != nil && !stream.IsDone()
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
