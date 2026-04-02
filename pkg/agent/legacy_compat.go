package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// ErrAgentBusy indicates the legacy-compatible agent is currently processing.
var ErrAgentBusy = errors.New("agent is busy")

// Compactor is a legacy alias for context compactor.
type Compactor = agentctx.Compactor

// CompactionResult is a legacy alias for compaction result.
type CompactionResult = agentctx.CompactionResult

// LoopConfig is a legacy compatibility config.
type LoopConfig struct {
	Compactor                Compactor
	ContextWindow            int
	ThinkingLevel            string
	Executor                 *ExecutorPool
	ToolOutput               ToolOutputLimits
	TaskTrackingEnabled      bool
	ContextManagementEnabled bool
	Model                    llm.Model
	APIKey                   string
	ToolCallCutoff           int
}

// DefaultLoopConfig returns default loop config for legacy compatibility.
func DefaultLoopConfig() *LoopConfig {
	return &LoopConfig{
		ContextWindow:            200000,
		ThinkingLevel:            "off",
		Executor:                 NewExecutorPool(map[string]int{"maxConcurrentTools": 5, "queueTimeout": 60}),
		ToolOutput:               DefaultToolOutputLimits(),
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}
}

// Agent is a legacy-compatible wrapper around AgentNew.
type Agent struct {
	mu sync.Mutex

	model  llm.Model
	apiKey string

	compatCtx *agentctx.AgentContext
	compactor Compactor

	ag         *AgentNew
	sessionDir string
	sessionID  string

	events chan AgentEvent

	processing bool
	pending    []string
	closed     bool
}

// NewAgentWithContext creates a legacy-compatible agent.
func NewAgentWithContext(model llm.Model, apiKey string, ctx *agentctx.AgentContext) *Agent {
	return NewAgentFromConfigWithContext(model, apiKey, ctx, DefaultLoopConfig())
}

// NewAgentFromConfigWithContext creates a legacy-compatible agent with loop config.
func NewAgentFromConfigWithContext(model llm.Model, apiKey string, ctx *agentctx.AgentContext, loopCfg *LoopConfig) *Agent {
	if ctx == nil {
		ctx = agentctx.NewAgentContext("")
	}
	if loopCfg == nil {
		loopCfg = DefaultLoopConfig()
	}

	sessionDir, _ := os.MkdirTemp("", "ai-legacy-agent-*")
	sessionID := "legacy-" + uuid.NewString()

	// Keep model fields from LoopConfig if explicitly set.
	if loopCfg.Model.ID != "" {
		model = loopCfg.Model
	}
	if loopCfg.APIKey != "" {
		apiKey = loopCfg.APIKey
	}
	if loopCfg.ContextWindow > 0 && model.ContextWindow <= 0 {
		model.ContextWindow = loopCfg.ContextWindow
	}

	ag, err := NewAgentNew(sessionDir, sessionID, &model, apiKey, nil)
	if err != nil {
		// Keep legacy constructor behavior non-panicking in runtime usage.
		// If construction fails, use nil AgentNew and surface errors on Prompt.
		ag = nil
	}

	wrapped := &Agent{
		model:      model,
		apiKey:     apiKey,
		compatCtx:  ctx,
		compactor:  loopCfg.Compactor,
		ag:         ag,
		sessionDir: sessionDir,
		sessionID:  sessionID,
		events:     make(chan AgentEvent, 256),
	}

	if ag != nil {
		snap := ag.GetSnapshot()
		snap.RecentMessages = append(snap.RecentMessages, ctx.Messages...)
		if model.ContextWindow > 0 {
			snap.AgentState.TokensLimit = model.ContextWindow
		}
	}

	return wrapped
}

// AddTool keeps API compatibility. AgentNew already loads full tool set.
func (a *Agent) AddTool(tool agentctx.Tool) {
	if a.compatCtx != nil {
		a.compatCtx.AddTool(tool)
	}
}

// SetCompactor stores legacy compactor for compatibility.
func (a *Agent) SetCompactor(compactor Compactor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.compactor = compactor
}

// SetContextWindow updates model context window for subsequent turns.
func (a *Agent) SetContextWindow(window int) {
	if window <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model.ContextWindow = window
	if a.ag != nil {
		a.ag.GetSnapshot().AgentState.TokensLimit = window
	}
}

// SetThinkingLevel keeps API compatibility.
func (a *Agent) SetThinkingLevel(_ string) {}

// SetModel updates model for subsequent turns.
func (a *Agent) SetModel(model llm.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	if model.ContextWindow > 0 && a.ag != nil {
		a.ag.GetSnapshot().AgentState.TokensLimit = model.ContextWindow
	}
}

// SetAPIKey updates API key for subsequent turns.
func (a *Agent) SetAPIKey(apiKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiKey = apiKey
}

// Events returns legacy event channel.
func (a *Agent) Events() <-chan AgentEvent {
	return a.events
}

// Prompt starts one prompt turn. Returns ErrAgentBusy when another turn is in progress.
func (a *Agent) Prompt(message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("agent is closed")
	}
	if a.processing {
		return ErrAgentBusy
	}
	a.processing = true
	go a.runQueued([]string{message})
	return nil
}

// FollowUp queues follow-up message while current turn is running.
func (a *Agent) FollowUp(message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return fmt.Errorf("agent is closed")
	}
	if !a.processing {
		a.processing = true
		go a.runQueued([]string{message})
		return nil
	}
	a.pending = append(a.pending, message)
	return nil
}

// Shutdown stops future processing and releases resources.
func (a *Agent) Shutdown() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	ag := a.ag
	sessionDir := a.sessionDir
	a.mu.Unlock()

	if ag != nil {
		_ = ag.Close()
	}
	if sessionDir != "" {
		_ = os.RemoveAll(sessionDir)
	}
}

func (a *Agent) runQueued(initial []string) {
	queue := append([]string(nil), initial...)

	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		a.runSinglePrompt(msg)

		a.mu.Lock()
		if a.closed {
			a.processing = false
			a.mu.Unlock()
			return
		}
		if len(a.pending) > 0 {
			queue = append(queue, a.pending...)
			a.pending = nil
		}
		a.mu.Unlock()
	}

	a.mu.Lock()
	a.processing = false
	a.mu.Unlock()
}

func (a *Agent) runSinglePrompt(message string) {
	a.emitEvent(NewAgentStartEvent())
	a.emitEvent(NewTurnStartEvent())

	a.mu.Lock()
	ag := a.ag
	model := a.model
	apiKey := a.apiKey
	a.mu.Unlock()

	if ag == nil {
		a.emitEvent(NewErrorEvent(fmt.Errorf("agent not initialized")))
		a.emitEvent(NewAgentEndEvent(nil))
		return
	}

	ag.model = &model
	ag.apiKey = apiKey

	beforeCount := 0
	if snap := ag.GetSnapshot(); snap != nil {
		beforeCount = len(snap.RecentMessages)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := ag.ExecuteTurn(ctx, message)
	cancel()
	if err != nil {
		a.emitEvent(NewErrorEvent(err))
		a.emitEvent(NewAgentEndEvent(nil))
		return
	}

	snap := ag.GetSnapshot()
	if snap == nil {
		a.emitEvent(NewTurnEndEvent(nil, nil))
		a.emitEvent(NewAgentEndEvent(nil))
		return
	}

	if a.compatCtx != nil {
		a.compatCtx.Messages = append([]agentctx.AgentMessage(nil), snap.RecentMessages...)
	}

	if beforeCount < 0 || beforeCount > len(snap.RecentMessages) {
		beforeCount = len(snap.RecentMessages)
	}
	newMessages := snap.RecentMessages[beforeCount:]
	var assistantMessage *agentctx.AgentMessage
	toolResults := make([]agentctx.AgentMessage, 0)
	for _, msg := range newMessages {
		switch msg.Role {
		case "assistant":
			a.emitEvent(NewMessageEndEvent(msg))
			copyMsg := msg
			assistantMessage = &copyMsg
		case "toolResult":
			copyMsg := msg
			toolResults = append(toolResults, copyMsg)
			a.emitEvent(NewToolExecutionEndEvent(copyMsg.ToolCallID, copyMsg.ToolName, &copyMsg, copyMsg.IsError))
		}
	}

	a.emitEvent(NewTurnEndEvent(assistantMessage, toolResults))
	a.emitEvent(NewAgentEndEvent(nil))
}

func (a *Agent) emitEvent(event AgentEvent) {
	select {
	case a.events <- event:
	default:
		// Drop only when channel is full to avoid deadlocks in compatibility mode.
	}
}

// Wait is kept for compatibility.
func (a *Agent) Wait() {
	for {
		a.mu.Lock()
		processing := a.processing
		a.mu.Unlock()
		if !processing {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// GetMessages returns latest conversation messages tracked by compatibility context.
func (a *Agent) GetMessages() []agentctx.AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.compatCtx == nil {
		return nil
	}
	return append([]agentctx.AgentMessage(nil), a.compatCtx.Messages...)
}

// SessionDir returns compatibility agent temporary session directory.
func (a *Agent) SessionDir() string {
	return filepath.Clean(a.sessionDir)
}
