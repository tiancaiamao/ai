package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"

	"github.com/tiancaiamao/ai/pkg/agentconfig"
)

// ModelCatalog returns model registry data used by protocol adapters.
func (app *App) ModelCatalog() ([]config.ModelSpec, config.ModelInfo, error) {
	specs, path, err := loadModelSpecs(app.cfg)
	if err != nil {
		return nil, config.ModelInfo{}, fmt.Errorf("load models from %s: %w", path, err)
	}
	return filterModelSpecsWithKeys(specs), app.currentModelInfo, nil
}

// LoadSession loads and activates a persisted session.
func (app *App) LoadSession(id string) (*session.Session, string, error) {
	if _, err := app.sessionMgr.GetMeta(id); err != nil {
		return nil, "", err
	}
	sess, err := app.sessionMgr.GetSession(id)
	if err != nil {
		return nil, "", err
	}
	if err := app.sessionMgr.SetCurrent(id); err != nil {
		return nil, "", err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		return nil, "", err
	}
	name := resolveSessionName(app.sessionMgr, id)
	app.setSession(sess, id, name)
	return sess, name, nil
}

// AppSetupParams holds the parameters needed to construct an App.
type AppSetupParams struct {
	CustomSystemPrompt string
	MaxTurns           int
	DebugAddr          string
	ModelOverride      string
	RunID              string
	Role               string
	AgentConfigPath    string
}

// NewApp constructs a fully initialized App by performing all setup:
// config loading, model resolution, session loading/creation, tool registration,
// compactor creation, and skill loading.
func NewApp(sessionPath string, params AppSetupParams) (*App, error) {
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
	if params.Role == "" && sessionID != "" {
		meta, err := sessionMgr.GetMeta(sessionID)
		if err == nil && meta.Role != "" {
			params.Role = meta.Role
			slog.Info("Recovered role from session", "role", params.Role)
		}
	}

	// --- Role mismatch warning ---
	if params.Role != "" && sessionID != "" {
		meta, err := sessionMgr.GetMeta(sessionID)
		if err == nil && meta.Role != "" && meta.Role != params.Role {
			slog.Warn("Role mismatch between session and current --role",
				"session_role", meta.Role,
				"current_role", params.Role)
		}
	}

	var agentCfg *agentconfig.AgentConfig
	if params.AgentConfigPath != "" {
		agentCfg, err = agentconfig.Load(params.AgentConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load agent config: %w", err)
		}
		slog.Info("Loaded agent config", "path", params.AgentConfigPath)
		if params.ModelOverride == "" && agentCfg.Model != "" {
			applyModelOverride(cfg, agentCfg.Model)
		}
	} else if params.Role != "" {
		roleDir := filepath.Join(agentDir, "roles", params.Role)
		roleConfigPath := filepath.Join(roleDir, "agent.yaml")

		roleCfg, err := agentconfig.Load(roleConfigPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("role %q not found: no config at %s", params.Role, roleConfigPath)
			}
			return nil, fmt.Errorf("failed to load role config for %q: %w", params.Role, err)
		}
		agentCfg = roleCfg
		slog.Info("Loaded role config", "role", params.Role, "path", roleConfigPath)

		// Apply role's default model if no --model CLI override.
		if params.ModelOverride == "" && agentCfg.Model != "" {
			slog.Info("Applying role default model", "role", params.Role, "model", agentCfg.Model)
			applyModelOverride(cfg, agentCfg.Model)
		}
	}

	// --- Model override from CLI (highest priority) ---
	if params.ModelOverride != "" {
		applyModelOverride(cfg, params.ModelOverride)
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
	if params.Role != "" && sessionID != "" {
		if err := sessionMgr.SetSessionRole(sessionID, params.Role); err != nil {
			slog.Warn("Failed to record role in session meta", "role", params.Role, "error", err)
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
	if params.Role != "" {
		skillStatsPath = filepath.Join(agentDir, "roles", params.Role, "skill-stats.json")
		// Auto-create empty skill-stats.json for roles that don't have one yet.
		if _, err := os.Stat(skillStatsPath); os.IsNotExist(err) {
			emptyStats := skill.LoadStats(skillStatsPath)
			if saveErr := emptyStats.Save(); saveErr != nil {
				slog.Warn("[SkillStats] failed to create stats file for role",
					"role", params.Role, "path", skillStatsPath, "error", saveErr)
			}
		}
	} else {
		skillStatsPath = filepath.Join(agentDir, "skill-stats.json")
	}

	skillResult, skillStats := loadSkills(agentDir, cwd, registry, skillStatsPath)

	// --- Build App ---
	app := &App{
		customSystemPrompt:   params.CustomSystemPrompt,
		maxTurns:             params.MaxTurns,
		debugAddr:            params.DebugAddr,
		cfg:                  cfg,
		configPath:           configPath,
		model:                model,
		apiKey:               apiKey,
		activeSpec:           activeSpec,
		currentModelInfo:     currentModelInfo,
		currentContextWindow: currentContextWindow,
		cwd:                  cwd,
		agentDir:             agentDir,
		role:                 params.Role,
		sessionPath:          sessionPath,
		sessionMgr:           sessionMgr,
		sess:                 sess,
		sessionID:            sessionID,
		sessionName:          sessionName,
		ws:                   ws,
		registry:             registry,
		commands:             command.New(),
		emitEvent:            func(event agent.AgentEvent) {},

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
		runID:                 params.RunID,
	}

	// Record the initial session in run.json (serve/run init) so run-id
	// addressing works from the very first prompt.
	app.updateRunSession(sessionID)

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
	apiKey, err := config.ResolveAPIKeyWithProxy(model.Provider, model.Proxy)
	if err != nil {
		return llm.Model{}, "", config.ModelSpec{}, fmt.Errorf("missing API key: %w", err)
	}

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
