package main

import (
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

// runJSON implements --mode json: single-shot execution with JSON Lines output.
// Based on pi-mono's print-mode.ts behavior.
func runJSON(sessionPath string, debugAddr string, prompts []string, output io.Writer) error {
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
	// Initialize logger from config
	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	// Get current working directory
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
		sess, err = session.LoadSessionLazy(sessionPath, session.DefaultLoadOptions())
		if err != nil {
			return fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		name := time.Now().Format("20060102-150405")
		sess, err = sessionMgr.CreateSession(name, name)
		if err != nil {
			return fmt.Errorf("failed to create new session: %w", err)
		}
		sessionID = sess.GetID()
		if err := sessionMgr.SetCurrent(sessionID); err != nil {
			slog.Info("Failed to set current session:", "value", err)
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Created new session", "id", sessionID, "count", len(sess.GetMessages()))
	}

	// Initialize trace handler with sessionID
	_, traceOutputPath, err := initTraceFileHandler(sessionID)
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
	} else {
		slog.Info("Trace handler initialized", "outputDir", traceOutputPath)
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
	// Create a shared workspace object for all tools to track directory changes
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	registry := tools.NewRegistry()
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

	// Create compactor for automatic context compression
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

	// Create agent with skills using structured prompt builder.
	basePrompt := prompt.JSONModeBasePrompt()

	// Build the full system prompt
	// Use workspace to get dynamic cwd for each prompt build
	promptBuilder := prompt.NewBuilderWithWorkspace(basePrompt, ws)
	promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)

	// Set task tracking and context management based on config
	promptBuilder.SetTaskTrackingEnabled(cfg.TaskTracking)
	promptBuilder.SetContextManagementEnabled(cfg.ContextManagement)

	systemPrompt := promptBuilder.Build()

	// Helper function to create a new agent context
	createBaseContext := func() *agentctx.AgentContext {
		ctx := agentctx.NewAgentContext(systemPrompt)
		for _, tool := range registry.All() {
			ctx.AddTool(tool)
		}
		return ctx
	}
	agentCtx := createBaseContext()

	// Build LoopConfig from application config
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}

	concurrencyConfig := cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewExecutorPool(map[string]int{
		"maxConcurrentTools": concurrencyConfig.MaxConcurrentTools,
		"toolTimeout":        concurrencyConfig.ToolTimeout,
		"queueTimeout":       concurrencyConfig.QueueTimeout,
	})

	toolOutputConfig := cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}

	// Build LoopConfig with all settings
	loopCfg := cfg.ToLoopConfig(
		config.WithCompactor(sessionComp),
		config.WithContextWindow(currentContextWindow),
		config.WithToolCallCutoff(compactorConfig.ToolCallCutoff),
		config.WithExecutor(executor),
		config.WithToolOutputLimits(agent.ToolOutputLimits{
			MaxChars: toolOutputConfig.MaxChars,
		}),
	)

	// Set model and apiKey (not handled by ToLoopConfig)
	loopCfg.Model = model
	loopCfg.APIKey = apiKey
	loopCfg.GetWorkingDir = ws.GetCWD
	loopCfg.GetStartupPath = ws.GetGitRoot

	// Create agent with LoopConfig
	ag := agent.NewAgentFromConfigWithContext(model, apiKey, agentCtx, loopCfg)
	defer ag.Shutdown()

	// Load previous messages into agent context
	for _, msg := range sess.GetMessages() {
		ag.GetContext().AddMessage(msg)
	}

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
				if event.Type == "agent_end" {
					if err := sessionWriter.Replace(sess, event.Messages); err != nil {
						slog.Info("Failed to replace session messages on agent_end:", "value", err)
					}
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
						if event.Type == "agent_end" {
							if err := sessionWriter.Replace(sess, event.Messages); err != nil {
								slog.Info("Failed to replace session messages on agent_end:", "value", err)
							}
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
