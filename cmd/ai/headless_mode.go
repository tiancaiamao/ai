package main

import (
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"

	"log/slog"
)

// HeadlessStatus represents the status of a headless agent run.
// Written to status.json for external monitoring (e.g., aiclaw).
type HeadlessStatus struct {
	SessionID    string    `json:"session_id"`
	PID          int       `json:"pid"`
	Status       string    `json:"status"` // "running", "completed", "timeout", "error"
	CurrentTurn  int       `json:"current_turn"`
	LastTool     string    `json:"last_tool,omitempty"`
	LastActivity time.Time `json:"last_activity"`
	StartedAt    time.Time `json:"started_at"`
	Error        string    `json:"error,omitempty"`
	mu           sync.Mutex
}

// Write writes the status to a JSON file.
func (s *HeadlessStatus) Write(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Update safely updates the status and writes to file.
func (s *HeadlessStatus) Update(path string, fn func(*HeadlessStatus)) error {
	s.mu.Lock()
	fn(s)
	s.LastActivity = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Helper functions for headless output formatting

// formatArgsBrief formats tool arguments briefly.
func formatArgsBrief(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	// Show first few args
	parts := []string{}
	for k, v := range args {
		if len(parts) >= 2 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	result := fmt.Sprint(parts)
	if len(args) > len(parts) {
		result += "..."
	}
	return result
}

// truncateLines truncates text to max lines and adds "..." if truncated.
func truncateLines(text string, maxLines int) string {
	lines := splitLines(text)
	if len(lines) <= maxLines {
		return text
	}
	result := joinLines(lines[:maxLines])
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	result += "..."
	return result
}

// truncateString truncates string to max length and adds "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// splitLines splits text into lines.
func splitLines(text string) []string {
	lines := []string{}
	current := ""
	for _, ch := range text {
		if ch == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// joinLines joins lines with newlines.
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func registerHeadlessTools(registry *tools.Registry, ws *tools.Workspace, compactor *compact.Compactor, cfg *config.Config) {
	readTool := tools.NewReadTool(ws)
	editTool := tools.NewEditTool(ws)

	// Apply hashline configuration if enabled
	if cfg.ToolOutput != nil && cfg.ToolOutput.HashLines {
		readTool.SetHashLines(true)
	}
	if cfg.Edit != nil && cfg.Edit.Mode == "hashline" {
		editTool.SetEditMode(tools.EditModeHashline)
	}

	registry.Register(readTool)
	registry.Register(tools.NewBashTool(ws))
	registry.Register(tools.NewWriteTool(ws))
	registry.Register(tools.NewGrepTool(ws))
	registry.Register(editTool)
	registry.Register(tools.NewChangeWorkspaceTool(ws))
	if compactor != nil {
		registry.Register(tools.NewLLMContextUpdateTool())
		registry.Register(tools.NewLLMContextDecisionTool(compactor.ToContextCompactor()))
	}
}

// runHeadless executes prompts in headless mode, outputting turn-by-turn human-readable format.
// Each turn shows: thinking, tool calls (simplified), and assistant output.
func runHeadless(sessionPath string, maxTurns int, allowedTools []string, timeout time.Duration, customSystemPrompt string, prompts []string, output io.Writer) error {
	startTime := time.Now()
	slog.Info("Starting headless mode", "prompts", len(prompts), "max_turns", maxTurns, "tools", allowedTools, "timeout", timeout, "has_custom_prompt", customSystemPrompt != "")

	if len(prompts) == 0 {
		return writeHeadlessError(output, "at least one prompt argument is required for --mode headless")
	}

	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to get config path: %v", err))
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
	}

	// Convert config to llm.Model
	model := cfg.GetLLMModel()

	// Resolve API key
	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to resolve API key: %v", err))
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to get working directory: %v", err))
	}

	// Normalize session path
	if sessionPath != "" {
		sessionPath, err = filepath.Abs(sessionPath)
		if err != nil {
			return writeHeadlessError(output, fmt.Sprintf("failed to normalize session path: %v", err))
		}
	}

	// Initialize session manager
	var sess *session.Session
	var sessionID string
	var sessionMgr *session.SessionManager
	var sessionsDir string // For tools

	// Session handling with persistence
	sessionsDir, err = session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to get sessions path: %v", err))
	}
	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}

	sessionMgr = session.NewSessionManager(sessionsDir)

	if sessionPath != "" {
		sess, err = session.LoadSessionLazy(sessionPath, session.DefaultLoadOptions())
		if err != nil {
			return writeHeadlessError(output, fmt.Sprintf("failed to load session from %s: %v", sessionPath, err))
		}
		sessionID = sess.GetID()
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	} else {
		name := time.Now().Format("20060102-150405")
		sess, err = sessionMgr.CreateSession(name, name)
		if err != nil {
			return writeHeadlessError(output, fmt.Sprintf("failed to create new session: %v", err))
		}
		sessionID = sess.GetID()
		if err := sessionMgr.SetCurrent(sessionID); err != nil {
			slog.Info("Failed to set current session:", "value", err)
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	slog.Info("Loaded session", "id", sessionID, "count", len(sess.GetMessages()))

	// Initialize trace handler with sessionID
	_, traceFile, err := initTraceFileHandler(sessionID)
	if err != nil {
		slog.Warn("Failed to create trace handler", "file", traceFile, "error", err)
	} else {
		slog.Info("Trace handler initialized", "file", traceFile)
	}

	// Initialize status file for observability
	status := &HeadlessStatus{
		SessionID:    sessionID,
		PID:          os.Getpid(),
		Status:       "running",
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	}
	sessionFile := filepath.Join(sessionsDir, sessionID, "messages.jsonl")
	statusFile := filepath.Join(sessionsDir, sessionID, "status.json")
	if err := status.Write(statusFile); err != nil {
		slog.Warn("Failed to write status file", "error", err)
	}

	// Output session info for observability
	fmt.Fprintf(output, "=== Session Info ===\n")
	fmt.Fprintf(output, "Session ID: %s\n", sessionID)
	fmt.Fprintf(output, "PID: %d\n", os.Getpid())
	fmt.Fprintf(output, "Session file: %s\n", sessionFile)
	fmt.Fprintf(output, "Status file: %s\n", statusFile)
	if traceFile != "" {
		fmt.Fprintf(output, "Trace file: %s\n", traceFile)
	}
	fmt.Fprintln(output)


	// Create tool registry and register tools
	// Create a shared workspace object for all tools to track directory changes
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to create workspace: %v", err))
	}

	registry := tools.NewRegistry()

	// Resolve context window and create compactor for automatic context compression
	activeSpec, err := resolveActiveModelSpec(cfg)
	if err != nil {
		slog.Warn("Failed to resolve model spec, using default context window", "error", err)
	}
	currentContextWindow := activeSpec.ContextWindow
	if currentContextWindow <= 0 {
		currentContextWindow = 128000 // default context window
	}
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		prompt.CompactorBasePrompt(),
		currentContextWindow,
	)
	registerHeadlessTools(registry, ws, compactor, cfg)

	// Load skills
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to get home directory: %v", err))
	}

	agentDir := filepath.Join(homeDir, ".ai")
	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	// Create agent with skills using structured prompt builder.
	basePrompt := prompt.HeadlessBasePrompt(false)

	// Build the full system prompt
	// Use workspace to get dynamic cwd for each prompt build
	promptBuilder := prompt.NewBuilderWithWorkspace(basePrompt, ws)
	promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)

	// Set llm context for system prompt explanation (tells LLM about the mechanism)
	// The actual content is injected dynamically in the agent loop
	if sess != nil {
		sessionDir := sess.GetDir()
		if sessionDir != "" {
			wm := agentctx.NewLLMContext(sessionDir)
			promptBuilder.SetLLMContext(wm)
		}
	}
	
	// Use custom system prompt if provided, otherwise use default
	var systemPrompt string
	if customSystemPrompt != "" {
		systemPrompt = customSystemPrompt
		slog.Info("Using custom system prompt", "length", len(customSystemPrompt))
	} else {
		systemPrompt = promptBuilder.Build()
	}

	// Create agent context
	agentCtx := agentctx.NewAgentContext(systemPrompt)

	// Initialize llm context from session directory (for dynamic injection)
	if sess != nil {
		sessionDir := sess.GetDir()
		if sessionDir != "" {
			wm := agentctx.NewLLMContext(sessionDir)
			agentCtx.LLMContext = wm
		}
	}

	// Add tools, optionally filtering by whitelist
	allTools := registry.All()
	if len(allowedTools) > 0 {
		// Filter tools by whitelist
		whitelist := make(map[string]bool)
		for _, name := range allowedTools {
			whitelist[name] = true
		}
		for _, tool := range allTools {
			if whitelist[tool.Name()] {
				agentCtx.AddTool(tool)
			}
		}
		slog.Info("Tool whitelist applied", "allowed", allowedTools)
	} else {
		// Add all tools
		for _, tool := range allTools {
			agentCtx.AddTool(tool)
		}
	}

	// Create agent
	ag := agent.NewAgentWithContext(model, apiKey, agentCtx)
	defer ag.Shutdown()

	// Set max turns if specified
	if maxTurns > 0 {
		ag.SetMaxTurns(maxTurns)
		slog.Info("Max turns limit set", "max_turns", maxTurns)
	}

	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}
	ag.SetCompactor(sessionComp)
	ag.SetContextWindow(currentContextWindow)
	ag.SetToolCallCutoff(compactorConfig.ToolCallCutoff)

	// Load previous messages into agent context
	for _, msg := range sess.GetMessages() {
		ag.GetContext().AddMessage(msg)
	}

	// Set up executor and tool output limits
	concurrencyConfig := cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewExecutorPool(map[string]int{
		"maxConcurrentTools": concurrencyConfig.MaxConcurrentTools,
		"toolTimeout":        concurrencyConfig.ToolTimeout,
		"queueTimeout":       concurrencyConfig.QueueTimeout,
	})
	ag.SetExecutor(executor)

	toolOutputConfig := cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}
	ag.SetToolOutputLimits(agent.ToolOutputLimits{
		MaxChars: toolOutputConfig.MaxChars,
	})

	// Track current turn state
	turnCounter := 0

	type toolCallInfo struct {
		name string
		args string
	}

	fmt.Fprintln(output)

	// Subscribe to events and output turn-by-turn
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-ag.Events():
				// Write to session
				if event.Type == "message_end" && event.Message != nil {
					sessionWriter.Append(sess, *event.Message)
				}
				if event.Type == "tool_execution_end" && event.Result != nil {
					sessionWriter.Append(sess, *event.Result)
				}
				if event.Type == "agent_end" {
					if err := sessionWriter.Replace(sess, event.Messages); err != nil {
						slog.Info("Failed to replace session messages on agent_end:", "value", err)
					}
				}

				// Output turn information
				if event.Type == "message_end" && event.Message != nil {
					msg := event.Message
					if msg.Role == "assistant" {
						turnCounter++

						// Extract thinking, tool calls, and output from message
						thinking := ""
						toolCalls := []toolCallInfo{}
						textOutput := ""

						for _, block := range msg.Content {
							switch b := block.(type) {
							case agentctx.ThinkingContent:
								if b.Thinking != "" {
									thinking = b.Thinking
								}
							case agentctx.ToolCallContent:
								// Capture tool call info
								argsStr := ""
								if len(b.Arguments) > 0 {
									argsStr = formatArgsBrief(b.Arguments)
								}
								toolCalls = append(toolCalls, toolCallInfo{
									name: b.Name,
									args: argsStr,
								})
							case agentctx.TextContent:
								if b.Text != "" {
									textOutput = b.Text
								}
							}
						}

						// Update status file
						if len(toolCalls) > 0 {
							status.Update(statusFile, func(s *HeadlessStatus) {
								s.CurrentTurn = turnCounter
								s.LastTool = toolCalls[0].name
							})
						} else {
							status.Update(statusFile, func(s *HeadlessStatus) {
								s.CurrentTurn = turnCounter
							})
						}

						// Output turn summary
						fmt.Fprintf(output, "\n=== Turn %d ===\n", turnCounter)
						if thinking != "" {
							fmt.Fprintf(output, "Thinking: %s\n\n", truncateLines(thinking, 3))
						}
						if len(toolCalls) > 0 {
							fmt.Fprintln(output, "Tool calls:")
							for _, tc := range toolCalls {
								if tc.args != "" {
									fmt.Fprintf(output, "  • %s: %s\n", tc.name, truncateString(tc.args, 60))
								} else {
									fmt.Fprintf(output, "  • %s\n", tc.name)
								}
							}
							fmt.Fprintln(output)
						}
						if textOutput != "" {
							fmt.Fprintf(output, "Output: %s\n", truncateLines(textOutput, 5))
						}
					}
				}

				if event.Type == "agent_end" {
					if err := sessionMgr.SaveCurrent(); err != nil {
						slog.Info("Failed to update session metadata:", "value", err)
					}
				}
			case <-shutdownEmitter:
				// Drain remaining events
				for {
					select {
					case event := <-ag.Events():
						if event.Type == "message_end" && event.Message != nil {
							sessionWriter.Append(sess, *event.Message)
						}
						if event.Type == "tool_execution_end" && event.Result != nil {
							sessionWriter.Append(sess, *event.Result)
						}
						if event.Type == "agent_end" {
							if err := sessionWriter.Replace(sess, event.Messages); err != nil {
								slog.Info("Failed to replace session messages on agent_end:", "value", err)
							}
						}
						if event.Type == "agent_end" {
							if err := sessionMgr.SaveCurrent(); err != nil {
								slog.Info("Failed to update session metadata:", "value", err)
							}
						}
					default:
						return
					}
				}
			}
		}
	}()

	// Send all prompts sequentially
	for i, promptText := range prompts {
		slog.Info("Processing prompt", "index", i+1, "total", len(prompts), "prompt", promptText[:min(len(promptText), 100)])
		if err := ag.Prompt(promptText); err != nil {
			slog.Error("Prompt failed", "error", err)
			close(shutdownEmitter)
			<-eventEmitterDone
			return writeHeadlessError(output, fmt.Sprintf("failed to process prompt: %v", err))
		}
	}

	// Wait for agent to complete with optional timeout
	var waitErr error
	if timeout > 0 {
		// Wait with timeout
		done := make(chan struct{})
		go func() {
			ag.Wait()
			close(done)
		}()
		select {
		case <-done:
			// Completed normally
		case <-time.After(timeout):
			slog.Error("Timeout exceeded", "timeout", timeout)
			ag.Shutdown()
			waitErr = fmt.Errorf("timeout after %s", timeout)
			fmt.Fprintf(output, "\n=== Timeout ===\nExecution exceeded %s timeout and was terminated.\n", timeout)
		}
	} else {
		// Wait without timeout
		ag.Wait()
	}
	close(shutdownEmitter)
	<-eventEmitterDone

	if waitErr != nil {
		status.Update(statusFile, func(s *HeadlessStatus) {
			s.Status = "error"
			s.Error = waitErr.Error()
		})
		return writeHeadlessError(output, waitErr.Error())
	}

	// Mark status as completed
	status.Update(statusFile, func(s *HeadlessStatus) {
		s.Status = "completed"
	})

	sessionWriter.Close()
	if err := sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	// Get all messages from the session
	messages := sess.GetMessages()
	usage := agent.GetTotalUsage(messages)

	// Output final summary
	fmt.Fprintf(output, "\n=== Summary ===\n")
	fmt.Fprintf(output, "Total turns: %d\n", turnCounter)
	if usage.TotalTokens > 0 {
		fmt.Fprintf(output, "Tokens: %d input, %d output, %d total\n",
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	}
	elapsed := time.Since(startTime)
	fmt.Fprintf(output, "Duration: %s\n", elapsed.Round(time.Millisecond))

	slog.Info("Headless mode completed", "duration", elapsed, "turns", turnCounter)
	return nil
}

// writeHeadlessError writes an error message.
func writeHeadlessError(w io.Writer, errMsg string) error {
	fmt.Fprintf(w, "Error: %s\n", errMsg)
	return nil
}
