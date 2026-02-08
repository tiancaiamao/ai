package agent

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/tiancaiamao/ai/pkg/llm"
)

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
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	compactor     Compactor     // Optional compactor for automatic context compression
	followUpQueue chan string   // Queue for follow-up messages
	executor      *ExecutorPool // Tool executor with concurrency control
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
		followUpQueue: make(chan string, 10), // Buffer up to 10 follow-up messages
		executor:      NewExecutorPool(map[string]int{"maxConcurrentTools": 3, "toolTimeout": 30, "queueTimeout": 60}),
	}
}

// Prompt sends a user message to the agent and waits for completion.
func (a *Agent) Prompt(message string) error {
	select {
	case a.mu <- struct{}{}:
		a.wg.Add(1)
		go func() {
			defer func() { <-a.mu }()
			defer a.wg.Done()
			log.Printf("[Agent] Starting prompt: %s", message)

			a.processPrompt(message)

			// Check for follow-up messages
			for {
				select {
				case followUpMsg := <-a.followUpQueue:
					log.Printf("[Agent] Processing follow-up: %s", followUpMsg)
					a.processPrompt(followUpMsg)
				default:
					// No more follow-up messages
					return
				}
			}
		}()
		return nil
	default:
		return fmt.Errorf("agent is busy")
	}
}

// processPrompt handles a single prompt (shared by Prompt and follow-up).
func (a *Agent) processPrompt(message string) {
	prompts := []AgentMessage{NewUserMessage(message)}

	config := &LoopConfig{
		Model:    a.model,
		APIKey:   a.apiKey,
		Executor: a.executor,
	}

	log.Printf("[Agent] Starting RunLoop...")
	a.currentStream = RunLoop(a.ctx, prompts, a.context, config)

	// Emit events to channel
	log.Printf("[Agent] Starting event iteration...")
	eventCount := 0
	for event := range a.currentStream.Iterator(a.ctx) {
		if event.Done {
			log.Printf("[Agent] Event stream done, total events: %d", eventCount)
			break
		}

		eventCount++
		log.Printf("[Agent] Got event: %s", event.Value.Type)

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
		select {
		case a.eventChan <- event.Value:
		default:
			log.Println("Event channel full, dropping event")
		}
	}
	log.Printf("[Agent] Prompt completed")
}

// Wait waits for all agent operations to complete.
func (a *Agent) Wait() {
	a.wg.Wait()
}

// Steer interrupts the current execution and sends a new message.
func (a *Agent) Steer(message string) {
	// Cancel current execution
	if a.cancel != nil {
		a.cancel()
	}

	// Create new context
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel = cancel

	// Send prompt with steering message
	a.Prompt(message)
}

// Abort stops the current execution.
func (a *Agent) Abort() {
	if a.cancel != nil {
		a.cancel()
	}
}

// FollowUp adds a message to be processed after the current prompt completes.
func (a *Agent) FollowUp(message string) error {
	select {
	case a.followUpQueue <- message:
		log.Printf("[Agent] Follow-up queued: %s", message)
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
		log.Printf("[Agent] Auto-compacting %d messages...", len(messages))
		if err := a.Compact(a.compactor); err != nil {
			log.Printf("[Agent] Auto-compact failed: %v", err)
		} else {
			log.Printf("[Agent] Auto-compact successful: %d -> %d messages",
				len(messages), len(a.context.Messages))
		}
	}
}
