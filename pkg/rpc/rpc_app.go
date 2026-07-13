package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"

	"github.com/tiancaiamao/ai/pkg/agentconfig"
)

// rpcApp holds all state that was previously captured by closure variables in runRPC.
// Each field corresponds to a local variable or parameter that was captured by
// one or more server.Register / server.RegisterSlash closures.
type rpcApp struct {
	// --- Parameters from runRPC ---
	customSystemPrompt string
	maxTurns           int
	output             io.Writer
	debugAddr          string
	runID              string // run ID from parent ai serve (empty for standalone rpc)

	// --- Config ---
	cfg        *config.Config
	configPath string

	// --- Model ---
	model                llm.Model
	apiKey               string
	activeSpec           config.ModelSpec
	currentModelInfo     config.ModelInfo
	currentContextWindow int

	// --- Paths ---
	cwd      string
	agentDir string
	role     string // agent role name; empty = embedded default

	// --- Session ---
	sessionPath string
	sessionMgr  *session.SessionManager
	sess        *session.Session
	sessionID   string
	sessionName string

	// --- Workspace & Tools ---
	ws       *tools.Workspace
	registry *tools.Registry

	// --- Compaction ---
	compactor       *compact.Compactor
	compactorConfig *compact.Config

	// --- Tracing ---
	traceOutputPath string

	// --- Skills ---
	skillResult   *skill.LoadResult
	skillStats    *skill.SkillStatsFile
	skillCommands []SlashCommand

	// --- Agent ---
	ag               *agent.Agent
	agentCtx         *agentctx.AgentContext
	agentConfig      *agentconfig.AgentConfig
	loopCfg          *agent.LoopConfig
	executor         agent.ToolExecutor
	toolOutputConfig *config.ToolOutputConfig

	// --- Session I/O ---
	sessionWriter *sessionWriter
	sessionComp   *sessionCompactor

	// --- System prompt ---
	systemPrompt string

	// Agent context prefix combines skills + AGENTS.md into a single user message
	// injected before the first user message on each LLM call. Empty when neither
	// is available. Merged into one message for maximum prefix cache stability.
	agentContextPrefix string

	// --- RPC Server ---
	server *Server

	// --- Mutable state protected by stateMu ---
	stateMu                       sync.Mutex
	isStreaming                   bool
	isCompacting                  bool
	currentThinkingLevel          string
	autoCompactionEnabled         bool
	steeringMode                  string
	followUpMode                  string
	pendingSteer                  bool
	showThinking                  bool
	showTools                     bool
	showPrefix                    bool
	busyMode                      string
	consecutiveCompactionFailures int
}

// parseJSONArgs attempts to unmarshal args as JSON into target.
// Returns true if args looks like JSON and was successfully parsed.
func (app *rpcApp) parseJSONArgs(args string, target any) bool {
	args = strings.TrimSpace(args)
	if len(args) > 0 && args[0] == '{' {
		return json.Unmarshal([]byte(args), target) == nil
	}
	return false
}

// nuclearTruncate force-truncates oldest messages without LLM summary.
//
// This is the last-resort fallback when compaction repeatedly fails (e.g.,
// the summarization model cannot handle the conversation size).
//
// NOTE: This path is unreachable in normal operation. It is only triggered
// when consecutiveCompactionFailures reaches 3, which occurs only when
// compaction fails repeatedly. Coverage: 0% because actual compaction
// does not fail in typical usage.
func (app *rpcApp) nuclearTruncate() {
	if app.ag == nil {
		return
	}
	agentCtx := app.ag.GetContext()
	if agentCtx == nil {
		return
	}
	messages := agentCtx.RecentMessages
	if len(messages) <= 10 {
		return
	}

	// Keep only the last 20% of messages (minimum 10)
	keepCount := max(10, int(float64(len(messages))*0.2))
	truncatedCount := len(messages) - keepCount
	agentCtx.RecentMessages = messages[len(messages)-keepCount:]

	// Also sync to session
	if app.sess != nil {
		if err := app.sess.SaveMessages(agentCtx.RecentMessages); err != nil {
			slog.Error("[Compact] Nuclear truncation failed to save session", "error", err)
		}
	}

	// Reset failure counter after nuclear truncation
	app.stateMu.Lock()
	app.consecutiveCompactionFailures = 0
	app.stateMu.Unlock()

	slog.Warn("[Compact] Nuclear truncation completed",
		"messages_before", len(messages),
		"messages_after", keepCount,
		"truncated_count", truncatedCount)
}

// initEventEmitter starts the goroutine that reads agent events and forwards

func (app *rpcApp) initEventEmitter() (chan struct{}, chan struct{}) {
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})

	processEvent := func(event agent.AgentEvent) {
		if event.Type == "agent_start" {
			app.stateMu.Lock()
			app.isStreaming = true
			app.stateMu.Unlock()
		}
		if event.Type == "agent_end" {
			app.stateMu.Lock()
			app.isStreaming = false
			app.isCompacting = false
			app.pendingSteer = false
			app.stateMu.Unlock()

			// Session persistence is handled incrementally:
			// - message_end/tool_execution_end → sessionWriter.Append (per message)
			// - compaction_end → sess.AppendCompaction (snapshot + entry)
			// No Replace needed — messages.jsonl is append-only.
		}
		if event.Type == "compaction_start" {
			app.stateMu.Lock()
			app.isCompacting = true
			app.stateMu.Unlock()
		}
		if event.Type == "compaction_end" {
			app.stateMu.Lock()
			app.isCompacting = false
			app.stateMu.Unlock()

			// Persist compaction: save snapshot file + append compaction entry
			// to messages.jsonl. This is the loop-triggered compaction path.
			if event.Compaction != nil && len(event.Messages) > 0 && app.sess != nil {
				if _, err := app.sess.AppendCompaction(
					event.Compaction.Summary, event.Messages,
				); err != nil {
					slog.Error("Failed to persist compaction entry", "error", err)
				}
			}
		}

		if event.Type == "message_end" && event.Message != nil {
			if app.sessionWriter != nil {
				app.sessionWriter.Append(app.sess, *event.Message)
			}
		}
		if event.Type == "tool_execution_end" && event.Result != nil {
			if app.sessionWriter != nil {
				app.sessionWriter.Append(app.sess, *event.Result)
			}
		}

		emitAt := time.Now()
		if event.EventAt == 0 {
			event.EventAt = emitAt.UnixNano()
		}
		app.server.EmitEvent(event)

		if event.Type == "agent_end" {
			go func() {
				if err := app.sessionMgr.SaveCurrent(); err != nil {
					slog.Info("Failed to update session metadata:", "value", err)
				}
			}()
		}
	}

	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-app.ag.Events():
				processEvent(event)
			case <-shutdownEmitter:
				// Drain remaining events
				for {
					select {
					case event := <-app.ag.Events():
						processEvent(event)
					default:
						return
					}
				}
			}
		}
	}()

	return shutdownEmitter, eventEmitterDone
}

func (app *rpcApp) startDebugServer() {
	if app.debugAddr == "" {
		return
	}
	go func() {
		slog.Info("Debug server listening on", "value", app.debugAddr)
		slog.Info("Debug endpoints available at:")
		slog.Info("- http:///debug/pprof/          (profiling index)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/profile   (CPU profile)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/heap       (memory profile)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/goroutine  (goroutine dump)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/trace      (execution trace)", "value", app.debugAddr)

		if err := http.ListenAndServe(app.debugAddr, nil); err != nil {
			slog.Error("Debug server error:", "error", err)
		}
	}()
}

// emitStartEvent sends the initial server_start event with model and tool info.

func (app *rpcApp) emitStartEvent() {
	allTools := app.registry.All()
	toolNames := make([]string, len(allTools))
	for i, t := range allTools {
		toolNames[i] = t.Name()
	}
	app.server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": app.model.ID,
		"tools": toolNames,
	})
}

// --- Slash command aliases (registration helpers) ---

func (app *rpcApp) registerHiddenAlias(alias, desc, canonical string) {
	app.server.RegisterHiddenSlash(alias, desc, func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler(canonical)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", canonical)
		}
		return h(args)
	})
}

func (app *rpcApp) forwardToSet(subcmd string) func(string) (any, error) {
	return func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h(subcmd + " " + args)
	}
}

// collectSessionUsageFromAgent is a convenience wrapper around collectSessionUsage

func (app *rpcApp) handlePrompt(cmd RPCCommand) (any, error) {
	var data struct {
		Message           string            `json:"message"`
		StreamingBehavior string            `json:"streamingBehavior"`
		Images            []json.RawMessage `json:"images"`
	}
	if len(cmd.Data) > 0 {
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
	}
	message := cmd.Message
	if message == "" {
		message = data.Message
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty prompt message")
	}
	if len(data.Images) > 0 {
		return nil, fmt.Errorf("images are not supported in this RPC implementation")
	}

	// Expand /skill:name commands BEFORE generic slash dispatch.
	if skill.IsSkillCommand(message) {
		expandedMessage := app.expandSkillCommands(message)
		slog.Info("Expanded skill command", "original", message, "skill", skill.ExtractSkillName(message))

		app.stateMu.Lock()
		streaming := app.isStreaming
		app.stateMu.Unlock()

		if streaming {
			app.stateMu.Lock()
			app.pendingSteer = true
			app.stateMu.Unlock()
			app.ag.Steer(expandedMessage)
			return nil, nil
		}

		app.compactBeforeRequest("pre_request_prompt")
		return nil, app.ag.Prompt(expandedMessage)
	}

	// Intercept slash commands
	if message[0] == '/' {
		cmdName, args, err := command.ParseSlashCommand(message)
		if err != nil {
			return nil, fmt.Errorf("invalid slash command: %w", err)
		}
		handler, ok := app.server.GetSlashHandler(cmdName)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", cmdName)
		}
		return handler(args)
	}

	app.stateMu.Lock()
	streaming := app.isStreaming
	mode := app.steeringMode
	followMode := app.followUpMode
	pending := app.pendingSteer
	app.stateMu.Unlock()

	if streaming {
		behavior := strings.TrimSpace(data.StreamingBehavior)
		if behavior == "" {
			behavior = "steer"
		}
		switch behavior {
		case "steer":
			if mode == "one-at-a-time" && pending {
				return nil, fmt.Errorf("steer already pending")
			}
			app.stateMu.Lock()
			app.pendingSteer = true
			app.stateMu.Unlock()
			app.ag.Steer(message)
			return nil, nil
		case "followUp", "follow_up":
			if followMode == "one-at-a-time" && app.ag.GetPendingFollowUps() > 0 {
				return nil, fmt.Errorf("follow-up queue already has a pending message")
			}
			return nil, app.ag.FollowUp(message)
		case "reject":
			return nil, fmt.Errorf("agent is streaming; rejected by busy-mode policy")
		default:
			return nil, fmt.Errorf("invalid streamingBehavior: %s", behavior)
		}
	}

	app.compactBeforeRequest("pre_request_prompt")
	return nil, app.ag.Prompt(message)
}

func (app *rpcApp) handleSteer(cmd RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received steer:", "value", message)

	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty steer message")
	}

	expandedMessage := app.expandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in steer", "original", message, "skill", skill.ExtractSkillName(message))
	}

	app.stateMu.Lock()
	mode := app.steeringMode
	pending := app.pendingSteer
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if mode == "one-at-a-time" && pending {
		return nil, fmt.Errorf("steer already pending")
	}
	if !streaming {
		// Steer is a fresh start: reset compaction tracking so the old
		// loop's tool-call count doesn't trigger premature compaction
		// of accumulated pre-steer messages.
		app.ag.GetContext().AgentState.ToolCallsSinceLastTrigger = 0
		if app.compactor != nil {
			app.compactor.ResetDecideState()
		}
		app.compactBeforeRequest("pre_request_steer")
	}
	app.stateMu.Lock()
	app.pendingSteer = true
	app.stateMu.Unlock()
	app.ag.Steer(expandedMessage)
	return nil, nil
}

func (app *rpcApp) handleFollowUp(cmd RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received follow_up:", "value", message)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty follow-up message")
	}

	expandedMessage := app.expandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in follow_up", "original", message, "skill", skill.ExtractSkillName(message))
	}

	app.stateMu.Lock()
	mode := app.followUpMode
	app.stateMu.Unlock()
	if mode == "one-at-a-time" && app.ag.GetPendingFollowUps() > 0 {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}
	return nil, app.ag.FollowUp(expandedMessage)
}

func (app *rpcApp) handleAbort(cmd RPCCommand) (any, error) {
	_ = cmd
	slog.Info("Received abort")
	app.ag.Abort()
	return nil, nil
}

// registerHandlers registers all RPC command handlers and slash commands.
// Handler methods are distributed across topic-specific files; this method
// wires up protocol commands and delegates slash command registration.
func (app *rpcApp) registerHandlers(
	validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels map[string]bool,
) {
	// === Protocol command handlers ===
	app.server.Register(CommandPrompt, app.handlePrompt)
	app.server.Register(CommandSteer, app.handleSteer)
	app.server.Register(CommandFollowUp, app.handleFollowUp)
	app.server.Register(CommandAbort, app.handleAbort)

	// === Slash command handlers (topic-specific registration) ===
	app.registerSessionHandlers()
	app.registerMessageHandlers()
	app.registerConfigHandlers(validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels)
	app.registerHelpHandlers()
}
