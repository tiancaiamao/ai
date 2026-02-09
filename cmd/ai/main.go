package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/logger"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

var log *logger.Logger

func normalizeSessionPath(sessionPath string) (string, error) {
	if sessionPath == "" {
		return "", nil
	}
	if sessionPath == "~" || strings.HasPrefix(sessionPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if sessionPath == "~" {
			sessionPath = homeDir
		} else {
			sessionPath = filepath.Join(homeDir, sessionPath[2:])
		}
	}
	absPath, err := filepath.Abs(sessionPath)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

func sessionIDFromPath(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".jsonl") {
		return strings.TrimSuffix(base, ".jsonl")
	}
	ext := filepath.Ext(base)
	if ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

func resolveSessionName(sessionMgr *session.SessionManager, sessionID string) string {
	if sessionMgr == nil || sessionID == "" {
		return sessionID
	}
	meta, err := sessionMgr.GetMeta(sessionID)
	if err != nil || meta == nil || meta.Name == "" {
		return sessionID
	}
	return meta.Name
}

func modelInfoFromModel(model llm.Model) rpc.ModelInfo {
	return rpc.ModelInfo{
		ID:       model.ID,
		Name:     model.ID,
		Provider: model.Provider,
		API:      model.API,
		Input:    []string{"text"},
	}
}

func modelInfoFromSpec(spec config.ModelSpec) rpc.ModelInfo {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	input := spec.Input
	if len(input) == 0 {
		input = []string{"text"}
	}
	return rpc.ModelInfo{
		ID:            spec.ID,
		Name:          name,
		Provider:      spec.Provider,
		API:           spec.API,
		Reasoning:     spec.Reasoning,
		Input:         input,
		ContextWindow: spec.ContextWindow,
		MaxTokens:     spec.MaxTokens,
	}
}

func modelSpecFromConfig(cfg *config.Config) config.ModelSpec {
	return config.ModelSpec{
		ID:       cfg.Model.ID,
		Name:     cfg.Model.ID,
		Provider: cfg.Model.Provider,
		BaseURL:  cfg.Model.BaseURL,
		API:      cfg.Model.API,
		Input:    []string{"text"},
	}
}

func resolveActiveModelSpec(cfg *config.Config) (config.ModelSpec, error) {
	specs, modelsPath, err := loadModelSpecs(cfg)
	if err != nil {
		return modelSpecFromConfig(cfg), fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	if spec, ok := findModelSpec(specs, cfg.Model.Provider, cfg.Model.ID); ok {
		return spec, nil
	}
	return modelSpecFromConfig(cfg), nil
}

func buildCompactionState(cfg *compact.Config, compactor *compact.Compactor) *rpc.CompactionState {
	if cfg == nil || compactor == nil {
		return nil
	}
	limit, source := compactor.EffectiveTokenLimit()
	return &rpc.CompactionState{
		MaxMessages:      cfg.MaxMessages,
		MaxTokens:        cfg.MaxTokens,
		KeepRecent:       cfg.KeepRecent,
		ReserveTokens:    compactor.ReserveTokens(),
		ContextWindow:    compactor.ContextWindow(),
		TokenLimit:       limit,
		TokenLimitSource: source,
	}
}

func loadModelSpecs(cfg *config.Config) ([]config.ModelSpec, string, error) {
	modelsPath, err := config.ResolveModelsPath()
	if err != nil {
		return nil, "", err
	}

	specs, err := config.LoadModelSpecs(modelsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []config.ModelSpec{modelSpecFromConfig(cfg)}, modelsPath, nil
		}
		return nil, modelsPath, err
	}

	if len(specs) == 0 {
		return nil, modelsPath, fmt.Errorf("no models defined in %s", modelsPath)
	}

	return specs, modelsPath, nil
}

func filterModelSpecsWithKeys(specs []config.ModelSpec) []config.ModelSpec {
	available := make(map[string]bool)
	filtered := make([]config.ModelSpec, 0, len(specs))
	for _, spec := range specs {
		provider := strings.TrimSpace(spec.Provider)
		if provider == "" || strings.TrimSpace(spec.ID) == "" {
			continue
		}
		ok, seen := available[provider]
		if !seen {
			if _, err := config.ResolveAPIKey(provider); err == nil {
				ok = true
			} else {
				ok = false
			}
			available[provider] = ok
		}
		if ok {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

func findModelSpec(specs []config.ModelSpec, provider, modelID string) (config.ModelSpec, bool) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	for _, spec := range specs {
		if strings.EqualFold(spec.Provider, provider) && spec.ID == modelID {
			return spec, true
		}
	}
	return config.ModelSpec{}, false
}

func buildSkillCommands(skills []skill.Skill) []rpc.SlashCommand {
	commands := make([]rpc.SlashCommand, 0, len(skills))
	for _, s := range skills {
		name := s.Name
		if name == "" {
			continue
		}
		commands = append(commands, rpc.SlashCommand{
			Name:        "skill:" + name,
			Description: s.Description,
			Source:      "skill",
			Location:    s.Source,
			Path:        s.FilePath,
		})
	}
	return commands
}

func forkEntryID(msg agent.AgentMessage, index int) string {
	if msg.Timestamp != 0 {
		return fmt.Sprintf("msg-%d", msg.Timestamp)
	}
	return fmt.Sprintf("idx-%d", index)
}

func buildForkMessages(messages []agent.AgentMessage) []rpc.ForkMessage {
	results := make([]rpc.ForkMessage, 0)
	for i, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		results = append(results, rpc.ForkMessage{
			EntryID: forkEntryID(msg, i),
			Text:    msg.ExtractText(),
		})
	}
	return results
}

func resolveForkEntry(entryID string, messages []agent.AgentMessage) (int, string, error) {
	if entryID == "" {
		return -1, "", fmt.Errorf("entryId is required")
	}
	if strings.HasPrefix(entryID, "msg-") {
		raw := strings.TrimPrefix(entryID, "msg-")
		ts, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return -1, "", fmt.Errorf("invalid entryId: %s", entryID)
		}
		for i, msg := range messages {
			if msg.Timestamp == ts {
				return i, msg.ExtractText(), nil
			}
		}
		return -1, "", fmt.Errorf("entryId not found: %s", entryID)
	}
	if strings.HasPrefix(entryID, "idx-") {
		raw := strings.TrimPrefix(entryID, "idx-")
		index, err := strconv.Atoi(raw)
		if err != nil {
			return -1, "", fmt.Errorf("invalid entryId: %s", entryID)
		}
		if index < 0 || index >= len(messages) {
			return -1, "", fmt.Errorf("entryId out of range: %s", entryID)
		}
		return index, messages[index].ExtractText(), nil
	}
	return -1, "", fmt.Errorf("unknown entryId format: %s", entryID)
}

func collectSessionUsage(messages []agent.AgentMessage) (int, int, int, int, rpc.SessionTokenStats, float64) {
	var userCount int
	var assistantCount int
	var toolCalls int
	var toolResults int
	var tokens rpc.SessionTokenStats
	var cost float64

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
			toolCalls += len(msg.ExtractToolCalls())
			if msg.Usage != nil {
				tokens.Input += msg.Usage.InputTokens
				tokens.Output += msg.Usage.OutputTokens
				tokens.CacheRead += msg.Usage.CacheRead
				tokens.CacheWrite += msg.Usage.CacheWrite
				tokens.Total += msg.Usage.TotalTokens
				cost += msg.Usage.Cost.Total
			}
		case "toolResult":
			toolResults++
		}
	}

	if tokens.Total == 0 {
		tokens.Total = tokens.Input + tokens.Output + tokens.CacheRead + tokens.CacheWrite
	}

	return userCount, assistantCount, toolCalls, toolResults, tokens, cost
}

func main() {
	mode := flag.String("mode", "rpc", "Run mode (rpc only)")
	sessionPathFlag := flag.String("session", "", "Session file path")
	debugAddr := flag.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	flag.Parse()

	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		logger.NewDefaultLogger().Fatalf("Failed to get config path: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.NewDefaultLogger().Warnf("Failed to load config from %s: %v", configPath, err)
		// Use defaults - LoadConfig already provides defaults
		cfg, _ = config.LoadConfig(configPath)
	}

	// Initialize logger from config
	log, err = cfg.Log.CreateLogger()
	if err != nil {
		logger.NewDefaultLogger().Fatalf("Failed to create logger: %v", err)
	}
	defer log.Close()
	aiLogPath := config.ResolveLogPath(cfg.Log)
	if aiLogPath != "" {
		log.Infof("Log file: %s", aiLogPath)
	}

	if *mode != "rpc" {
		log.Fatalf("Unsupported mode: %s (only --mode rpc is supported)", *mode)
	}

	// Convert config to llm.Model
	model := cfg.GetLLMModel()

	// Verify model type (ensures llm package is used)
	var _ llm.Model = model

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		log.Fatalf("Missing API key: %v", err)
	}

	// Log model info
	log.Info("Model: %s, Provider: %s, BaseURL: %s", model.ID, model.Provider, model.BaseURL)
	if cfg.Compactor != nil {
		log.Info("Compactor: MaxMessages=%d, MaxTokens=%d, ReserveTokens=%d",
			cfg.Compactor.MaxMessages, cfg.Compactor.MaxTokens, cfg.Compactor.ReserveTokens)
	}

	activeSpec, err := resolveActiveModelSpec(cfg)
	if err != nil {
		log.Infof("Model spec fallback: %v", err)
	}
	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentContextWindow := activeSpec.ContextWindow

	sessionPath, err := normalizeSessionPath(*sessionPathFlag)
	if err != nil {
		log.Fatalf("Failed to normalize session path: %v", err)
	}

	// Initialize session manager
	sessionsDir, err := session.GetDefaultSessionPath()
	if err != nil {
		log.Fatalf("Failed to get sessions path: %v", err)
	}

	// Extract directory from session path
	sessionsDir = filepath.Dir(sessionsDir)
	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	sessionMgr := session.NewSessionManager(sessionsDir)

	// Load current session
	var sess *session.Session
	sessionID := ""
	sessionName := ""
	if sessionPath != "" {
		sess, err = session.LoadSession(sessionPath)
		if err != nil {
			log.Fatalf("Failed to load session from %s: %v", sessionPath, err)
		}
		sessionID = sessionIDFromPath(sessionPath)
		sessionName = resolveSessionName(sessionMgr, sessionID)
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			log.Infof("Failed to save session pointer: %v", err)
		}
		log.Infof("Loaded session from '%s' with %d messages", sessionPath, len(sess.GetMessages()))
	} else {
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			log.Infof("Warning: Failed to load current session: %v", err)
			sess, sessionID, err = sessionMgr.LoadCurrent()
			if err != nil {
				log.Fatalf("Failed to create default session: %v", err)
			}
		}
		sessionName = resolveSessionName(sessionMgr, sessionID)
		log.Infof("Loaded session '%s' with %d messages", sessionID, len(sess.GetMessages()))
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}

	// Create tool registry and register tools
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool(cwd))
	registry.Register(tools.NewBashTool(cwd))
	registry.Register(tools.NewWriteTool(cwd))
	registry.Register(tools.NewGrepTool(cwd))
	registry.Register(tools.NewEditTool(cwd))

	log.Infof("Registered %d tools: read, bash, write, grep, edit", len(registry.All()))

	// Load skills
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}

	agentDir := filepath.Join(homeDir, ".ai")
	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	// Log skill diagnostics
	if len(skillResult.Diagnostics) > 0 {
		log.Infof("Skill loading: %d diagnostics", len(skillResult.Diagnostics))
		for _, diag := range skillResult.Diagnostics {
			if diag.Type == "error" {
				log.Errorf("  [%s] %s: %s", diag.Type, diag.Path, diag.Message)
			} else {
				log.Warnf("  [%s] %s: %s", diag.Type, diag.Path, diag.Message)
			}
		}
	}

	log.Infof("Loaded %d skills", len(skillResult.Skills))
	for _, s := range skillResult.Skills {
		log.Debugf("  - %s: %s", s.Name, s.Description)
	}

	// Create agent with skills
	systemPrompt := "You are a helpful coding assistant."
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

	// Create compactor for context compression
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

	// Enable automatic compression
	ag.SetCompactor(compactor)
	log.Infof("Auto-compact enabled: MaxMessages=%d, MaxTokens=%d",
		compactorConfig.MaxMessages,
		compactorConfig.MaxTokens)

	// Set up executor with concurrency control
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
	log.Infof("Concurrency control enabled: MaxConcurrentTools=%d, ToolTimeout=%ds",
		concurrencyConfig.MaxConcurrentTools,
		concurrencyConfig.ToolTimeout)

	// Load previous messages into agent context
	for _, msg := range sess.GetMessages() {
		ag.GetContext().AddMessage(msg)
	}

	// Create RPC server
	server := rpc.NewServer()
	stateMu := sync.Mutex{}
	isStreaming := false
	isCompacting := false
	currentThinkingLevel := "medium"
	autoCompactionEnabled := compactorConfig.AutoCompact

	// Set up handlers
	server.SetPromptHandler(func(message string) error {
		log.Infof("Received prompt: %s", message)
		return ag.Prompt(message)
	})

	server.SetSteerHandler(func(message string) error {
		log.Infof("Received steer: %s", message)
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("empty steer message")
		}
		ag.Steer(message)
		return nil
	})

	server.SetFollowUpHandler(func(message string) error {
		log.Infof("Received follow_up: %s", message)
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("empty follow-up message")
		}
		return ag.FollowUp(message)
	})

	server.SetAbortHandler(func() error {
		log.Info("Received abort")
		ag.Abort()
		return nil
	})

	server.SetClearSessionHandler(func() error {
		log.Info("Received clear_session")
		if err := sess.Clear(); err != nil {
			return err
		}
		// Clear agent context
		ag.SetContext(createBaseContext())
		log.Info("Session cleared")
		return nil
	})

	server.SetNewSessionHandler(func(name, title string) (string, error) {
		log.Infof("Received new_session: name=%s, title=%s", name, title)
		if strings.TrimSpace(name) == "" {
			name = time.Now().Format("20060102-150405")
		}
		if strings.TrimSpace(title) == "" {
			title = name
		}
		newSess, err := sessionMgr.CreateSession(name, title)
		if err != nil {
			return "", err
		}

		// Get the session ID from the file path
		sessPath := newSess.GetPath()
		newSessionID := sessionIDFromPath(sessPath)

		// Update session manager's current ID
		if err := sessionMgr.SetCurrent(newSessionID); err != nil {
			return "", err
		}

		// Save the current session pointer
		if err := sessionMgr.SaveCurrent(); err != nil {
			log.Infof("Failed to save session pointer: %v", err)
		}

		sess = newSess
		ag.SetContext(createBaseContext())

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = name
		stateMu.Unlock()

		log.Infof("Created new session '%s' (id: %s)", name, newSessionID)
		return newSessionID, nil
	})

	server.SetListSessionsHandler(func() ([]any, error) {
		log.Info("Received list_sessions")
		sessions, err := sessionMgr.ListSessions()
		if err != nil {
			return nil, err
		}

		result := make([]any, len(sessions))
		for i, sess := range sessions {
			result[i] = sess
		}
		return result, nil
	})

	server.SetSwitchSessionHandler(func(id string) error {
		log.Infof("Received switch_session: id=%s", id)
		if id == "" {
			return fmt.Errorf("session id is required")
		}

		// Treat absolute or relative path as session file
		if strings.Contains(id, string(os.PathSeparator)) || strings.HasSuffix(id, ".jsonl") {
			sessionPath, err := normalizeSessionPath(id)
			if err != nil {
				return err
			}
			newSess, err := session.LoadSession(sessionPath)
			if err != nil {
				return err
			}
			newSessionID := sessionIDFromPath(sessionPath)
			sessionsDir = filepath.Dir(sessionPath)
			sessionMgr = session.NewSessionManager(sessionsDir)
			_ = sessionMgr.SetCurrent(newSessionID)
			if err := sessionMgr.SaveCurrent(); err != nil {
				log.Infof("Failed to save session pointer: %v", err)
			}

			// Clear agent context and load new messages
			sess = newSess
			ag.SetContext(createBaseContext())
			for _, msg := range newSess.GetMessages() {
				ag.GetContext().AddMessage(msg)
			}

			stateMu.Lock()
			sessionID = newSessionID
			sessionName = resolveSessionName(sessionMgr, newSessionID)
			stateMu.Unlock()

			log.Infof("Switched to session '%s' with %d messages", newSessionID, len(newSess.GetMessages()))
			return nil
		}

		if err := sessionMgr.SetCurrent(id); err != nil {
			return err
		}

		// Load the new session
		newSess, newSessionID, err := sessionMgr.LoadCurrent()
		if err != nil {
			return err
		}

		// Clear agent context and load new messages
		sess = newSess
		ag.SetContext(createBaseContext())
		for _, msg := range newSess.GetMessages() {
			ag.GetContext().AddMessage(msg)
		}

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = resolveSessionName(sessionMgr, newSessionID)
		stateMu.Unlock()

		log.Infof("Switched to session '%s' with %d messages", newSessionID, len(newSess.GetMessages()))
		return nil
	})

	server.SetDeleteSessionHandler(func(id string) error {
		log.Infof("Received delete_session: id=%s", id)
		return sessionMgr.DeleteSession(id)
	})

	server.SetGetStateHandler(func() (*rpc.SessionState, error) {
		log.Info("Received get_state")
		compactionState := buildCompactionState(compactorConfig, compactor)
		stateMu.Lock()
		currentSessionID := sessionID
		currentSessionName := sessionName
		streaming := isStreaming
		compacting := isCompacting
		thinkingLevel := currentThinkingLevel
		autoCompact := autoCompactionEnabled
		modelInfo := currentModelInfo
		stateMu.Unlock()

		return &rpc.SessionState{
			Model:                 &modelInfo,
			ThinkingLevel:         thinkingLevel,
			IsStreaming:           streaming,
			IsCompacting:          compacting,
			SteeringMode:          "off",
			FollowUpMode:          "queue",
			SessionFile:           sess.GetPath(),
			SessionID:             currentSessionID,
			SessionName:           currentSessionName,
			AIPid:                 os.Getpid(),
			AILogPath:             aiLogPath,
			AIWorkingDir:          cwd,
			AutoCompactionEnabled: autoCompact,
			MessageCount:          len(ag.GetMessages()),
			PendingMessageCount:   ag.GetPendingFollowUps(),
			Compaction:            compactionState,
		}, nil
	})

	server.SetGetMessagesHandler(func() ([]any, error) {
		log.Info("Received get_messages")
		messages := ag.GetMessages()
		result := make([]any, len(messages))
		for i, msg := range messages {
			result[i] = msg
		}
		return result, nil
	})

	server.SetCompactHandler(func() (*rpc.CompactResult, error) {
		log.Info("Received compact")
		beforeMessages := ag.GetMessages()
		beforeCount := len(beforeMessages)
		tokensBefore := compactor.EstimateTokens(beforeMessages)
		compacted, err := compactor.Compact(beforeMessages)
		if err != nil {
			log.Infof("Compact failed: %v", err)
			return nil, err
		}

		// Replace messages with compacted version
		ag.GetContext().Messages = compacted

		// Save compacted session
		if err := sess.SaveMessages(compacted); err != nil {
			log.Infof("Failed to save compacted session: %v", err)
		}

		afterCount := len(compacted)
		tokensAfter := compactor.EstimateTokens(compacted)
		summary := ""
		firstKept := ""
		if len(compacted) > 0 {
			summaryText := compacted[0].ExtractText()
			if strings.HasPrefix(summaryText, "[Previous conversation summary]\n\n") {
				summary = strings.TrimPrefix(summaryText, "[Previous conversation summary]\n\n")
			}
		}
		if summary != "" && len(compacted) > 1 {
			firstKept = forkEntryID(compacted[1], 1)
		}

		log.Infof("Compact successful: %d -> %d messages", beforeCount, afterCount)
		return &rpc.CompactResult{
			Summary:          summary,
			FirstKeptEntryID: firstKept,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensAfter,
		}, nil
	})

	server.SetGetAvailableModelsHandler(func() ([]rpc.ModelInfo, error) {
		log.Info("Received get_available_models")
		specs, modelsPath, err := loadModelSpecs(cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}

		specs = filterModelSpecsWithKeys(specs)
		if len(specs) == 0 {
			authPath, _ := config.GetDefaultAuthPath()
			return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
		}

		models := make([]rpc.ModelInfo, 0, len(specs))
		for _, spec := range specs {
			models = append(models, modelInfoFromSpec(spec))
		}
		return models, nil
	})

	server.SetSetModelHandler(func(provider, modelID string) (*rpc.ModelInfo, error) {
		log.Infof("Received set_model: provider=%s, modelId=%s", provider, modelID)
		if strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
			return nil, fmt.Errorf("provider and modelId are required")
		}

		specs, modelsPath, err := loadModelSpecs(cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}
		filtered := filterModelSpecsWithKeys(specs)
		spec, ok := findModelSpec(filtered, provider, modelID)
		if !ok {
			if _, exists := findModelSpec(specs, provider, modelID); exists {
				authPath, _ := config.GetDefaultAuthPath()
				envVar := strings.ToUpper(strings.TrimSpace(provider)) + "_API_KEY"
				return nil, fmt.Errorf("no API key for %q (set %s or update %s)", provider, envVar, authPath)
			}
			return nil, fmt.Errorf("model not found: %s/%s (edit %s)", provider, modelID, modelsPath)
		}
		if strings.TrimSpace(spec.BaseURL) == "" || strings.TrimSpace(spec.API) == "" {
			return nil, fmt.Errorf("model %s/%s missing baseUrl or api in %s", spec.Provider, spec.ID, modelsPath)
		}

		newAPIKey, err := config.ResolveAPIKey(spec.Provider)
		if err != nil {
			return nil, err
		}

		model = llm.Model{
			ID:       spec.ID,
			Provider: spec.Provider,
			BaseURL:  spec.BaseURL,
			API:      spec.API,
		}
		apiKey = newAPIKey

		cfg.Model.ID = spec.ID
		cfg.Model.Provider = spec.Provider
		cfg.Model.BaseURL = spec.BaseURL
		cfg.Model.API = spec.API

		ag.SetModel(model)
		ag.SetAPIKey(apiKey)

		// Recreate compactor with new model
		compactor = compact.NewCompactor(compactorConfig, model, apiKey, systemPrompt, spec.ContextWindow)
		ag.SetCompactor(compactor)

		if err := config.SaveConfig(cfg, configPath); err != nil {
			log.Infof("Failed to save config: %v", err)
		}

		info := modelInfoFromSpec(spec)
		stateMu.Lock()
		currentModelInfo = info
		currentContextWindow = spec.ContextWindow
		stateMu.Unlock()
		return &info, nil
	})

	skillCommands := buildSkillCommands(skillResult.Skills)
	server.SetGetCommandsHandler(func() ([]rpc.SlashCommand, error) {
		log.Info("Received get_commands")
		return skillCommands, nil
	})

	server.SetGetSessionStatsHandler(func() (*rpc.SessionStats, error) {
		log.Info("Received get_session_stats")
		messages := ag.GetMessages()
		userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(messages)
		stateMu.Lock()
		currentSessionID := sessionID
		stateMu.Unlock()
		return &rpc.SessionStats{
			SessionFile:       sess.GetPath(),
			SessionID:         currentSessionID,
			UserMessages:      userCount,
			AssistantMessages: assistantCount,
			ToolCalls:         toolCalls,
			ToolResults:       toolResults,
			TotalMessages:     len(messages),
			Tokens:            tokens,
			Cost:              cost,
		}, nil
	})

	server.SetSetAutoCompactionHandler(func(enabled bool) error {
		log.Infof("Received set_auto_compaction: enabled=%v", enabled)
		compactorConfig.AutoCompact = enabled
		stateMu.Lock()
		autoCompactionEnabled = enabled
		stateMu.Unlock()
		return nil
	})

	validThinkingLevels := map[string]bool{
		"off":     true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	}
	thinkingCycle := []string{"off", "minimal", "low", "medium", "high", "xhigh"}

	server.SetSetThinkingLevelHandler(func(level string) (string, error) {
		level = strings.ToLower(strings.TrimSpace(level))
		if !validThinkingLevels[level] {
			return "", fmt.Errorf("invalid thinking level")
		}
		stateMu.Lock()
		currentThinkingLevel = level
		stateMu.Unlock()
		return level, nil
	})

	server.SetCycleThinkingLevelHandler(func() (string, error) {
		stateMu.Lock()
		current := currentThinkingLevel
		stateMu.Unlock()

		next := "medium"
		for i, level := range thinkingCycle {
			if level == current {
				next = thinkingCycle[(i+1)%len(thinkingCycle)]
				break
			}
		}

		stateMu.Lock()
		currentThinkingLevel = next
		stateMu.Unlock()
		return next, nil
	})

	server.SetGetLastAssistantTextHandler(func() (string, error) {
		log.Info("Received get_last_assistant_text")
		messages := ag.GetMessages()
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				return messages[i].ExtractText(), nil
			}
		}
		return "", nil
	})

	server.SetGetForkMessagesHandler(func() ([]rpc.ForkMessage, error) {
		log.Info("Received get_fork_messages")
		return buildForkMessages(ag.GetMessages()), nil
	})

	server.SetForkHandler(func(entryID string) (*rpc.ForkResult, error) {
		log.Infof("Received fork: entryId=%s", entryID)
		messages := ag.GetMessages()
		targetIndex, text, err := resolveForkEntry(entryID, messages)
		if err != nil {
			return nil, err
		}

		forkMessages := make([]agent.AgentMessage, targetIndex+1)
		copy(forkMessages, messages[:targetIndex+1])

		name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
		title := "Forked Session"
		newSess, err := sessionMgr.CreateSession(name, title)
		if err != nil {
			return nil, err
		}

		if err := newSess.SaveMessages(forkMessages); err != nil {
			return nil, err
		}

		newSessionID := sessionIDFromPath(newSess.GetPath())
		if err := sessionMgr.SetCurrent(newSessionID); err != nil {
			return nil, err
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			log.Infof("Failed to save session pointer: %v", err)
		}

		sess = newSess
		ag.SetContext(createBaseContext())
		for _, msg := range forkMessages {
			ag.GetContext().AddMessage(msg)
		}

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = name
		stateMu.Unlock()

		log.Infof("Forked to new session '%s' (id: %s)", name, newSessionID)
		return &rpc.ForkResult{Cancelled: false, Text: text}, nil
	})

	// Start event emitter
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	metrics := ag.GetMetrics()
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-ag.Events():
				if event.Type == "agent_start" {
					stateMu.Lock()
					isStreaming = true
					stateMu.Unlock()
				}
				if event.Type == "agent_end" {
					stateMu.Lock()
					isStreaming = false
					isCompacting = false
					stateMu.Unlock()
				}
				if event.Type == "compaction_start" {
					stateMu.Lock()
					isCompacting = true
					stateMu.Unlock()
				}
				if event.Type == "compaction_end" {
					stateMu.Lock()
					isCompacting = false
					stateMu.Unlock()
				}

				emitAt := time.Now()
				if event.EventAt == 0 {
					event.EventAt = emitAt.UnixNano()
				}
				if metrics != nil {
					metrics.RecordEventEmit(event.Type, time.Unix(0, event.EventAt), emitAt)
				}
				server.EmitEvent(event)

				// Auto-save on agent_end
				if event.Type == "agent_end" {
					// Save in background to avoid blocking
					go func() {
						messages := ag.GetMessages()
						if err := sess.SaveMessages(messages); err != nil {
							log.Infof("Failed to save session: %v", err)
						} else {
							log.Infof("Session saved: %d messages", len(messages))
						}
					}()
				}

			case <-shutdownEmitter:
				for {
					select {
					case event := <-ag.Events():
						if event.Type == "agent_start" {
							stateMu.Lock()
							isStreaming = true
							stateMu.Unlock()
						}
						if event.Type == "agent_end" {
							stateMu.Lock()
							isStreaming = false
							isCompacting = false
							stateMu.Unlock()
						}
						if event.Type == "compaction_start" {
							stateMu.Lock()
							isCompacting = true
							stateMu.Unlock()
						}
						if event.Type == "compaction_end" {
							stateMu.Lock()
							isCompacting = false
							stateMu.Unlock()
						}

						emitAt := time.Now()
						if event.EventAt == 0 {
							event.EventAt = emitAt.UnixNano()
						}
						if metrics != nil {
							metrics.RecordEventEmit(event.Type, time.Unix(0, event.EventAt), emitAt)
						}
						server.EmitEvent(event)
						if event.Type == "agent_end" {
							go func() {
								messages := ag.GetMessages()
								if err := sess.SaveMessages(messages); err != nil {
									log.Infof("Failed to save session: %v", err)
								} else {
									log.Infof("Session saved: %d messages", len(messages))
								}
							}()
						}
					default:
						return
					}
				}
			}
		}
	}()

	// Emit start event
	server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": model.ID,
		"tools": []string{"read", "bash", "write", "grep", "edit"},
	})

	// Start debug server if enabled
	if *debugAddr != "" {
		go func() {
			// Register metrics endpoint on DefaultServeMux
			http.HandleFunc("/debug/metrics", func(w http.ResponseWriter, r *http.Request) {
				metrics := ag.GetMetrics()
				if metrics == nil {
					http.Error(w, "Metrics not available", http.StatusServiceUnavailable)
					return
				}

				fullMetrics := metrics.GetFullMetrics()
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(fullMetrics); err != nil {
					log.Errorf("Failed to encode metrics: %v", err)
					http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
				}
			})

			log.Infof("Debug server listening on %s", *debugAddr)
			log.Infof("Debug endpoints available at:")
			log.Infof("  - http://%s/debug/pprof/          (profiling index)", *debugAddr)
			log.Infof("  - http://%s/debug/pprof/profile   (CPU profile)", *debugAddr)
			log.Infof("  - http://%s/debug/pprof/heap       (memory profile)", *debugAddr)
			log.Infof("  - http://%s/debug/pprof/goroutine  (goroutine dump)", *debugAddr)
			log.Infof("  - http://%s/debug/pprof/trace      (execution trace)", *debugAddr)
			log.Infof("  - http://%s/debug/metrics         (agent metrics)", *debugAddr)

			if err := http.ListenAndServe(*debugAddr, nil); err != nil {
				log.Errorf("Debug server error: %v", err)
			}
		}()
	}

	// Run RPC server
	log.Infof("RPC server started (model: %s, cwd: %s)", model.ID, cwd)
	log.Info("Waiting for commands...")
	if err := server.Run(); err != nil {
		log.Fatalf("RPC server error: %v", err)
	}

	// Server stopped, event emitter will exit automatically
	log.Info("RPC server stopped, waiting for cleanup...")

	// Wait for agent to complete
	log.Info("Waiting for agent to complete...")
	ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	log.Info("Agent completed, exiting...")
}
