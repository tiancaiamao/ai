package rpc

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"

	"github.com/tiancaiamao/ai/pkg/agentconfig"
)

// rpcAppSetupParams holds the parameters needed to construct an rpcApp.
type rpcAppSetupParams struct {
	customSystemPrompt string
	maxTurns           int
	debugAddr          string
	modelOverride      string
	runID              string
	role               string
}

// newRPCApp constructs a fully initialized rpcApp by performing all setup:
// config loading, model resolution, session loading/creation, tool registration,
// compactor creation, and skill loading.
func newRPCApp(sessionPath string, params rpcAppSetupParams) (*rpcApp, error) {
	// --- Home directory (used for role + skills paths) ---
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	agentDir := filepath.Join(homeDir, ".ai")

	// --- Config + Logger ---
	cfg, configPath, err := loadConfigWithLogger()
	if err != nil {
		return nil, err
	}

	// --- Working directory ---
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// --- Session ---
	sessionPath, err = normalizeSessionPath(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize session path: %w", err)
	}

	sess, sessionID, sessionName, sessionMgr, err := loadOrCreateSession(sessionPath, cwd)
	if err != nil {
		return nil, err
	}

	// --- Resume role recovery ---
	// If no --role specified but session has one recorded, recover it.
	if params.role == "" && sessionID != "" {
		meta, err := sessionMgr.GetMeta(sessionID)
		if err == nil && meta.Role != "" {
			params.role = meta.Role
			slog.Info("Recovered role from session", "role", params.role)
		}
	}

	// --- Role mismatch warning ---
	if params.role != "" && sessionID != "" {
		meta, err := sessionMgr.GetMeta(sessionID)
		if err == nil && meta.Role != "" && meta.Role != params.role {
			slog.Warn("Role mismatch between session and current --role",
				"session_role", meta.Role,
				"current_role", params.role)
		}
	}

	// --- Role-based agent config ---
	var agentCfg *agentconfig.AgentConfig
	if params.role != "" {
		roleDir := filepath.Join(agentDir, "roles", params.role)
		roleConfigPath := filepath.Join(roleDir, "agent.yaml")

		roleCfg, err := agentconfig.Load(roleConfigPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("role %q not found: no config at %s", params.role, roleConfigPath)
			}
			return nil, fmt.Errorf("failed to load role config for %q: %w", params.role, err)
		}
		agentCfg = roleCfg
		slog.Info("Loaded role config", "role", params.role, "path", roleConfigPath)

		// Apply role's default model if no --model CLI override.
		if params.modelOverride == "" && agentCfg.Model != "" {
			slog.Info("Applying role default model", "role", params.role, "model", agentCfg.Model)
			applyModelOverride(cfg, agentCfg.Model)
		}
	}

	// --- Model override from CLI (highest priority) ---
	if params.modelOverride != "" {
		applyModelOverride(cfg, params.modelOverride)
	}

	// --- Model + API Key ---
	model, apiKey, activeSpec, err := resolveModelAndKey(cfg)
	if err != nil {
		return nil, err
	}

	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentModelInfo.MaxTokens = model.MaxTokens
	currentModelInfo.ContextWindow = model.ContextWindow
	currentContextWindow := activeSpec.ContextWindow
	if currentContextWindow <= 0 {
		currentContextWindow = model.ContextWindow
	}

	// Record role in session meta for future resume.
	if params.role != "" && sessionID != "" {
		if err := sessionMgr.SetSessionRole(sessionID, params.role); err != nil {
			slog.Warn("Failed to record role in session meta", "role", params.role, "error", err)
		}
	}

	// --- Workspace & Tools ---
	ws, registry, err := createWorkspaceAndRegistry(cwd, cfg)
	if err != nil {
		return nil, err
	}

	// --- Compactor ---
	compactor, compactorConfig := createCompactor(cfg, model, apiKey, currentContextWindow, sess.GetDir())

	slog.Info("Registered tools: read, bash, write, grep, edit", "count", len(registry.All()))

	// --- Trace + Skills ---
	traceHandler, traceOutputPath, err := initTraceFileHandler(sessionID)
	_ = traceHandler
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
	} else {
		slog.Info("Trace handler initialized", "outputDir", traceOutputPath)
	}

	// Skill-stats path: per-role if role specified, otherwise global.
	var skillStatsPath string
	if params.role != "" {
		skillStatsPath = filepath.Join(agentDir, "roles", params.role, "skill-stats.json")
		// Auto-create empty skill-stats.json for roles that don't have one yet.
		if _, err := os.Stat(skillStatsPath); os.IsNotExist(err) {
			emptyStats := skill.LoadStats(skillStatsPath)
			if saveErr := emptyStats.Save(); saveErr != nil {
				slog.Warn("[SkillStats] failed to create stats file for role",
					"role", params.role, "path", skillStatsPath, "error", saveErr)
			}
		}
	} else {
		skillStatsPath = filepath.Join(agentDir, "skill-stats.json")
	}

	skillResult, skillStats := loadSkills(agentDir, cwd, registry, skillStatsPath)

	// --- Build rpcApp ---
	app := &rpcApp{
		customSystemPrompt:    params.customSystemPrompt,
		maxTurns:              params.maxTurns,
		debugAddr:             params.debugAddr,
		cfg:                   cfg,
		configPath:            configPath,
		model:                 model,
		apiKey:                apiKey,
		activeSpec:            activeSpec,
		currentModelInfo:      currentModelInfo,
		currentContextWindow:  currentContextWindow,
		cwd:                   cwd,
		agentDir:              agentDir,
		role:                  params.role,
		sessionPath:           sessionPath,
		sessionMgr:            sessionMgr,
		sess:                  sess,
		sessionID:             sessionID,
		sessionName:           sessionName,
		ws:                    ws,
		registry:              registry,
		compactor:             compactor,
		compactorConfig:       compactorConfig,
		traceOutputPath:       traceOutputPath,
		skillResult:           skillResult,
		skillStats:            skillStats,
		autoCompactionEnabled: compactorConfig.AutoCompact,
		agentConfig:           agentCfg,
		steeringMode:          "all",
		followUpMode:          "one-at-a-time",
		currentThinkingLevel:  cfg.ThinkingLevel,
		showThinking:          true,
		showTools:             true,
		showPrefix:            true,
		busyMode:              "steer",
		runID:                 params.runID,
	}

	// Always use LLM-decides compaction (unified context management).
	decideCfg := compact.DefaultLLMDecideConfig(currentContextWindow)
	compactorConfig.LLMDecide = &decideCfg
	slog.Info("Using LLMDecide compaction",
		"contextWindow", currentContextWindow,
		"softThreshold", decideCfg.SoftThreshold,
		"hardLimit", decideCfg.HardLimit,
	)

	return app, nil
}

// loadConfigWithLogger loads the config file and initializes the slog logger.
func loadConfigWithLogger() (*config.Config, string, error) {
	configPath, err := config.ResolveConfigPath()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		cfg, _ = config.LoadConfig(configPath)
	}

	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	return cfg, configPath, nil
}

// resolveModelAndKey resolves the LLM model and API key from config.
func resolveModelAndKey(cfg *config.Config) (llm.Model, string, config.ModelSpec, error) {
	model := cfg.GetLLMModel()

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return llm.Model{}, "", config.ModelSpec{}, fmt.Errorf("missing API key: %w", err)
	}

	slog.Info("Model", "id", model.ID, "provider", model.Provider, "baseURL", model.BaseURL)
	if cfg.Compactor != nil {
		slog.Info("Compactor", "maxMessages", cfg.Compactor.MaxMessages, "maxTokens", cfg.Compactor.MaxTokens,
			"keepRecent", cfg.Compactor.KeepRecent, "keepRecentTokens", cfg.Compactor.KeepRecentTokens,
			"reserveTokens", cfg.Compactor.ReserveTokens,
			"toolCallCutoff", cfg.Compactor.ToolCallCutoff,
			"toolSummaryAutomation", cfg.Compactor.ToolSummaryAutomation)
	}

	activeSpec, err := resolveActiveModelSpec(cfg)
	if err != nil {
		slog.Info("Model spec fallback", "error", err)
	}
	model = applyModelLimitsFromSpec(model, activeSpec)

	return model, apiKey, activeSpec, nil
}

// loadOrCreateSession loads an existing session or creates a new one.
func loadOrCreateSession(sessionPath string, cwd string) (*session.Session, string, string, *session.SessionManager, error) {
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("failed to get sessions path: %w", err)
	}

	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	sessionMgr := session.NewSessionManager(sessionsDir)

	var sess *session.Session
	var sessionID string
	var sessionName string

	if sessionPath != "" {
		sess, err = session.LoadSession(sessionPath)
		if err != nil {
			return nil, "", "", nil, fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		sessionName = resolveSessionName(sessionMgr, sessionID)
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			name := time.Now().Format("20060102-150405")
			sess, err = sessionMgr.CreateSession(name, name)
			if err != nil {
				return nil, "", "", nil, fmt.Errorf("failed to create new session: %w", err)
			}
			sessionID = sess.GetID()
			sessionName = name
			if err := sessionMgr.SetCurrent(sessionID); err != nil {
				slog.Info("Failed to set current session:", "value", err)
			}
			if err := sessionMgr.SaveCurrent(); err != nil {
				slog.Info("Failed to update session metadata:", "value", err)
			}
			slog.Info("Created new session", "id", sessionID, "count", len(sess.GetMessages()))
		} else {
			sessionName = resolveSessionName(sessionMgr, sessionID)
			slog.Info("Restored previous session", "id", sessionID, "name", sessionName, "count", len(sess.GetMessages()))
		}
	}

	return sess, sessionID, sessionName, sessionMgr, nil
}

// createWorkspaceAndRegistry creates the workspace and tool registry.
func createWorkspaceAndRegistry(cwd string, cfg *config.Config) (*tools.Workspace, *tools.Registry, error) {
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	registry := tools.NewRegistry()
	readTool := tools.NewReadTool(ws)
	editTool := tools.NewEditTool(ws)

	registry.Register(readTool)
	registry.Register(tools.NewBashTool(ws))
	registry.Register(tools.NewWriteTool(ws))
	registry.Register(tools.NewGrepTool(ws))
	registry.Register(editTool)
	registry.Register(tools.NewChangeWorkspaceTool(ws))

	return ws, registry, nil
}

// createCompactor creates the main compactor.
func createCompactor(cfg *config.Config, model llm.Model, apiKey string, contextWindow int, sessionDir string) (*compact.Compactor, *compact.Config) {
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}

	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		prompt.CompactorBasePrompt(),
		contextWindow,
		sessionDir,
	)

	return compactor, compactorConfig
}

// loadSkills loads skills from the agent directory and registers find_skill tool.
func loadSkills(agentDir string, cwd string, registry *tools.Registry, statsPath string) (*skill.LoadResult, *skill.SkillStatsFile) {
	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	if len(skillResult.Diagnostics) > 0 {
		slog.Info("Skill loading:  diagnostics", "count", len(skillResult.Diagnostics))
		for _, diag := range skillResult.Diagnostics {
			if diag.Type == "error" {
				slog.Error("Skill error", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			} else {
				slog.Warn("Skill warning", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			}
		}
	}

	slog.Info("Loaded  skills", "count", len(skillResult.Skills))
	for _, s := range skillResult.Skills {
		slog.Info("Skill", "name", s.Name, "description", s.Description)
	}

	skillStats := skill.LoadStats(statsPath)
	registry.Register(tools.NewFindSkillTool(skillResult.Skills, skillStats))

	return skillResult, skillStats
}
