package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// runHeadless executes prompts in headless mode, outputting only the final result.
// No intermediate events are streamed - just a single JSON output at the end.
func runHeadless(sessionPath string, noSession bool, maxTurns int, allowedTools []string, isSubagent bool, prompts []string, output io.Writer) error {
	startTime := time.Now()
	slog.Info("Starting headless mode", "prompts", len(prompts), "no_session", noSession, "max_turns", maxTurns, "tools", allowedTools, "is_subagent", isSubagent)

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

	if noSession {
		// Create a new temporary session without persistence
		sess = session.NewSession("") // Empty path = no persistence
		sessionID = sess.GetID()
		slog.Info("Created temporary session (no persistence)", "id", sessionID)
	} else {
		// Normal session handling with persistence
		sessionsDir, err := session.GetDefaultSessionsDir(cwd)
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
		} else {
			sess, sessionID, err = sessionMgr.LoadCurrent()
			if err != nil {
				sess, sessionID, err = sessionMgr.LoadCurrent()
				if err != nil {
					return writeHeadlessError(output, fmt.Sprintf("failed to create default session: %v", err))
				}
			}
		}
		slog.Info("Loaded session", "id", sessionID, "count", len(sess.GetMessages()))
	}

	// Create tool registry and register tools
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool(cwd))
	registry.Register(tools.NewBashTool(cwd))
	registry.Register(tools.NewWriteTool(cwd))
	registry.Register(tools.NewGrepTool(cwd))
	registry.Register(tools.NewEditTool(cwd))

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

	// Create agent with skills using structured prompt builder
	basePrompt := `You are a helpful coding assistant.
When you need to inspect files or run commands, call the tools. Do not write tool markup like <read_file> in plain text.
Do not include chain-of-thought or <thinking> tags in your output.`

	// Use focused subagent prompt if running as subagent
	if isSubagent {
		basePrompt = `You are a focused subagent executing a specific task.
Complete the task efficiently and report your findings.
Do not include chain-of-thought or <thinking> tags in your output.
Be concise and focused on the task at hand.`
	}

	// Build the full system prompt
	promptBuilder := prompt.NewBuilder(basePrompt, cwd)
	promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)
	systemPrompt := promptBuilder.Build()

	// Create agent context
	agentCtx := agent.NewAgentContext(systemPrompt)

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

	// Create compactor
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	currentContextWindow := 128000 // default context window
	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		"You are a helpful coding assistant.",
		currentContextWindow,
	)
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}
	ag.SetCompactor(sessionComp)
	ag.SetToolCallCutoff(compactorConfig.ToolCallCutoff)
	ag.SetToolSummaryStrategy(compactorConfig.ToolSummaryStrategy)

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
		MaxLines:             toolOutputConfig.MaxLines,
		MaxBytes:             toolOutputConfig.MaxBytes,
		MaxChars:             toolOutputConfig.MaxChars,
		LargeOutputThreshold: toolOutputConfig.LargeOutputThreshold,
		TruncateMode:         toolOutputConfig.TruncateMode,
	})

	// Subscribe to events silently (write to session, not stdout)
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-ag.Events():
				// Write to session but don't output to stdout
				if event.Type == "message_end" && event.Message != nil {
					sessionWriter.Append(sess, *event.Message)
				}
				if event.Type == "tool_execution_end" && event.Result != nil {
					sessionWriter.Append(sess, *event.Result)
				}
				if event.Type == "agent_end" && !noSession {
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
						if event.Type == "agent_end" && !noSession {
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

	// Wait for agent to complete
	ag.Wait()
	close(shutdownEmitter)
	<-eventEmitterDone

	sessionWriter.Close()
	if !noSession {
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}

	// Get all messages from the session
	messages := sess.GetMessages()

	// Extract final result
	finalText := agent.GetFinalAssistantText(messages)
	usage := agent.GetTotalUsage(messages)

	// Build result
	result := agent.HeadlessResult{
		Text:     finalText,
		Usage:    usage,
		ExitCode: 0,
	}

	// Output single JSON line
	data, err := json.Marshal(result)
	if err != nil {
		return writeHeadlessError(output, fmt.Sprintf("failed to marshal result: %v", err))
	}

	// Write result
	if _, err := output.Write(append(data, '\n')); err != nil {
		return err
	}

	elapsed := time.Since(startTime)
	slog.Info("Headless mode completed", "duration", elapsed, "output_length", len(finalText))

	return nil
}

// writeHeadlessError writes an error result as JSON.
func writeHeadlessError(w io.Writer, errMsg string) error {
	result := agent.HeadlessResult{
		Text:     "",
		Usage:    agent.UsageStats{},
		Error:    errMsg,
		ExitCode: 1,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
