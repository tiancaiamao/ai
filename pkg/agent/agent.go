package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/llm"
)

const (
	agentBusyTimeout = 60 * time.Second // Wait timeout for agent lock
)

var logStreamEvents = os.Getenv("AI_LOG_STREAM_EVENTS") == "1"

func shouldLogAgentEvent(eventType string) bool {
	switch eventType {
	case EventMessageUpdate, EventTextDelta, EventThinkingDelta, EventToolCallDelta:
		return logStreamEvents
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
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	compactor     Compactor     // Optional compactor for automatic context compression
	followUpQueue chan string   // Queue for follow-up messages
	executor      *ExecutorPool // Tool executor with concurrency control
	metrics       *Metrics      // Performance and usage metrics
	toolOutput    ToolOutputLimits
}

// NewAgent creates a new agent.
func NewAgent(model llm.Model, apiKey, systemPrompt string) *Agent {
	return NewAgentWithContext(model, apiKey, NewAgentContext(systemPrompt))
}

// NewAgentWithContext creates a new agent with a custom context.
func NewAgentWithContext(model llm.Model, apiKey string, agentCtx *AgentContext) *Agent {
	ctx, cancel := context.WithCancel(context.Background())

	return &Agent{
		mu:            make(chan struct{}, 1),
		model:         model,
		apiKey:        apiKey,
		systemPrompt:  agentCtx.SystemPrompt,
		context:       agentCtx,
		eventChan:     make(chan AgentEvent, 100),
		ctx:           ctx,
		cancel:        cancel,
		followUpQueue: make(chan string, 100), // Buffer up to 100 follow-up messages (increased from 10)
		executor:      NewExecutorPool(map[string]int{"maxConcurrentTools": 3, "toolTimeout": 30, "queueTimeout": 60}),
		metrics:       NewMetrics(),
		toolOutput:    DefaultToolOutputLimits(),
	}
}

// Prompt sends a user message to the agent and waits for completion.
// Waits up to agentBusyTimeout for the agent to become available.
func (a *Agent) Prompt(message string) error {
	timer := time.NewTimer(agentBusyTimeout)
	defer timer.Stop()

	select {
	case a.mu <- struct{}{}:
		a.wg.Add(1)
		go func() {
			defer func() { <-a.mu }()
			defer a.wg.Done()
			slog.Info("[Agent] Starting prompt", "message", message)

			a.processPrompt(message)

			// Check for follow-up messages
			for {
				select {
				case followUpMsg := <-a.followUpQueue:
					slog.Info("[Agent] Processing follow-up", "message", followUpMsg)
					a.processPrompt(followUpMsg)
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
func (a *Agent) processPrompt(message string) {
	promptStart := time.Now()
	hadError := false
	if a.metrics != nil {
		a.metrics.RecordPromptStart()
	}

	prompts := []AgentMessage{NewUserMessage(message)}

	config := &LoopConfig{
		Model:      a.model,
		APIKey:     a.apiKey,
		Executor:   a.executor,
		Metrics:    a.metrics,
		ToolOutput: a.toolOutput,
		Compactor:  a.compactor,
	}

	slog.Info("[Agent] Starting RunLoop")
	stream := RunLoop(a.ctx, prompts, a.context, config)
	a.setCurrentStream(stream)
	defer a.setCurrentStream(nil)

	// Emit events to channel
	slog.Info("[Agent] Starting event iteration")
	eventCount := 0
	for event := range stream.Iterator(a.ctx) {
		if event.Done {
			slog.Info("[Agent] Event stream done", "totalEvents", eventCount)
			break
		}

		eventCount++
		if shouldLogAgentEvent(event.Value.Type) {
			slog.Debug("[Agent] Got event", "type", event.Value.Type)
		}

		if a.metrics != nil {
			switch event.Value.Type {
			case EventMessageEnd:
				if event.Value.Message != nil {
					a.metrics.RecordMessage(event.Value.Message.Role)
				}
			case EventToolExecutionStart:
				a.metrics.RecordToolCall()
			case EventToolExecutionEnd:
				a.metrics.RecordToolResult()
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
			a.tryAutoCompact()
		}

		// Send to event channel
		a.emitEvent(event.Value)
	}
	if a.metrics != nil {
		var err error
		if hadError {
			err = errors.New("prompt failed")
		}
		a.metrics.RecordPrompt(time.Since(promptStart), err)
	}
	slog.Info("[Agent] Prompt completed")
}

// Wait waits for all agent operations to complete.
func (a *Agent) Wait() {
	a.wg.Wait()
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
	a.ctx = ctx
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
	}
	aborted := a.abortCurrentStream()
	if aborted {
		a.clearFollowUps()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel = cancel
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
func (a *Agent) tryAutoCompact() {
	if a.compactor == nil {
		return
	}

	messages := a.context.Messages
	if a.compactor.ShouldCompact(messages) {
		before := len(messages)
		slog.Info("[Agent] Auto-compacting", "beforeCount", before)
		a.emitEvent(NewCompactionStartEvent(CompactionInfo{
			Auto:   true,
			Before: before,
		}))
		if err := a.Compact(a.compactor); err != nil {
			slog.Error("[Agent] Auto-compact failed", "error", err)
			a.emitEvent(NewCompactionEndEvent(CompactionInfo{
				Auto:   true,
				Before: before,
				Error:  err.Error(),
			}))
		} else {
			after := len(a.context.Messages)
			slog.Info("[Agent] Auto-compact successful", "before", before, "after", after)
			a.emitEvent(NewCompactionEndEvent(CompactionInfo{
				Auto:   true,
				Before: before,
				After:  after,
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
