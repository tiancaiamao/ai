package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

// runJSON implements --mode json: single-shot execution with JSON Lines output.
// Based on pi-mono's print-mode.ts behavior.
func runJSON(sessionPath string, debugAddr string, prompts []string, output io.Writer, debug bool) error {
	if len(prompts) == 0 {
		return fmt.Errorf("at least one prompt argument is required for --mode json")
	}

	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		cfg, _ = config.LoadConfig(configPath)
	}
	if debug {
		cfg.Log.Level = "debug"
	}

	// Initialize logger from config
	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	aiLogPath := config.ResolveLogPath(cfg.Log)
	if aiLogPath != "" {
		slog.Info("Log file", "path", aiLogPath)
	}

	// Convert config to llm.Model
	model := cfg.GetLLMModel()
	var _ llm.Model = model

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return fmt.Errorf("missing API key: %w", err)
	}

	slog.Info("Model", "id", model.ID, "provider", model.Provider, "baseURL", model.BaseURL)

	activeSpec, err := resolveActiveModelSpec(cfg)
	if err != nil {
		slog.Info("Model spec fallback", "error", err)
	}
	currentContextWindow := activeSpec.ContextWindow

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	sessionPath, err = normalizeSessionPath(sessionPath)
	if err != nil {
		return fmt.Errorf("failed to normalize session path: %w", err)
	}

	// Initialize session manager
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return fmt.Errorf("failed to get sessions path: %w", err)
	}
	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	sessionMgr := session.NewSessionManager(sessionsDir)

	// Load or create session
	var sess *session.Session
	sessionID := ""
	if sessionPath != "" {
		sess, err = session.LoadSession(sessionPath)
		if err != nil {
			return fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to save session pointer:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			sess, sessionID, err = sessionMgr.LoadCurrent()
			if err != nil {
				return fmt.Errorf("failed to create default session: %w", err)
			}
		}
		slog.Info("Loaded session", "id", sessionID, "count", len(sess.GetMessages()))
	}

	// Output session header (JSON Lines format)
	header := sess.GetHeader()
	if header.ID == "" {
		header.ID = sessionID
	}
	if header.Cwd == "" {
		header.Cwd = cwd
	}
	if header.Type == "" {
		header.Type = session.EntryTypeSession
	}
	if header.Version == 0 {
		header.Version = session.CurrentSessionVersion
	}
	if err := writeJSONLine(output, header); err != nil {
		return err
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
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	agentDir := filepath.Join(homeDir, ".ai")
	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	// Create agent with skills
	systemPrompt := `You are a helpful coding assistant.
You have access to tools: read, write, grep, bash, edit.
When you need to inspect files or run commands, call the tools. Do not write tool markup like <read_file> in plain text.
Do not include chain-of-thought or <think> tags in your output.`
	createBaseContext := func() *agent.AgentContext {
		if len(skillResult.Skills) > 0 {
			return agent.NewAgentContextWithSkills(systemPrompt, skillResult.Skills)
		}
		return agent.NewAgentContext(systemPrompt)
	}
	agentCtx := createBaseContext()

	ag := agent.NewAgentWithContext(model, apiKey, agentCtx)
	for _, tool := range registry.All() {
		ag.AddTool(tool)
	}

	// Create compactor
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
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

	// Subscribe to all events and write them as JSON Lines
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-ag.Events():
				if event.Type == "message_end" && event.Message != nil {
					sessionWriter.Append(sess, *event.Message)
				}
				if event.Type == "tool_execution_end" && event.Result != nil {
					sessionWriter.Append(sess, *event.Result)
				}

				if event.EventAt == 0 {
					event.EventAt = time.Now().UnixNano()
				}
				if err := writeJSONLine(output, event); err != nil {
					slog.Error("Failed to write JSON event", "error", err)
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

						if event.EventAt == 0 {
							event.EventAt = time.Now().UnixNano()
						}
						if err := writeJSONLine(output, event); err != nil {
							slog.Error("Failed to write JSON event during shutdown", "error", err)
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
	for i, prompt := range prompts {
		slog.Info("Processing prompt", "index", i+1, "total", len(prompts), "prompt", prompt[:min(len(prompt), 100)])
		if err := ag.Prompt(prompt); err != nil {
			slog.Error("Prompt failed", "error", err)
			return err
		}
	}

	// Wait for agent to complete
	ag.Wait()
	close(shutdownEmitter)
	<-eventEmitterDone

	sessionWriter.Close()
	if err := sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	// Flush stdout before exit
	if f, ok := output.(*os.File); ok && f == os.Stdout {
		_ = f.Sync()
	}

	slog.Info("JSON mode completed")
	return nil
}

// writeJSONLine writes a JSON object followed by a newline.
func writeJSONLine(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
