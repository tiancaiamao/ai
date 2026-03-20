// aiclaw - AI Claw Bot with picoclaw channels and ai agent core.
// Configuration is unified in ~/.aiclaw/config.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	_ "github.com/sipeed/picoclaw/pkg/channels/feishu" // 注册飞书通道工厂
	picoclawconfig "github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/tiancaiamao/ai/claw/pkg/adapter"
	"github.com/tiancaiamao/ai/claw/pkg/cron"
	"github.com/tiancaiamao/ai/claw/pkg/voice"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

var (
	logLevel = flag.String("log-level", "info", "Log level: debug, info, warn, error")
	trace    = flag.Bool("trace", false, "Enable trace output to ~/.aiclaw/traces/")
)

// ModelConfig 模型配置
type ModelConfig struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	API      string `json:"api,omitempty"`
}

// VoiceConfig 语音配置
type VoiceConfig struct {
	Enabled  bool   `json:"enabled"`           // 是否启用语音支持
	Provider string `json:"provider"`          // 语音服务提供商: "groq" 或 "zhipu" (默认 "zhipu")
	APIKey   string `json:"apiKey,omitempty"`  // API Key (可从 auth.json 读取)
	APIBase  string `json:"apiBase,omitempty"` // API Base URL (可选)
	Model    string `json:"model,omitempty"`   // 模型名称 (可选)
}

// Config 是 aiclaw 的统一配置
type Config struct {
	Model    ModelConfig                   `json:"model"`
	Voice    VoiceConfig                   `json:"voice,omitempty"`
	Channels picoclawconfig.ChannelsConfig `json:"channels,omitempty"`
}

func main() {
	flag.Parse()

	// Setup logging
	setupLogging(*logLevel)

	// 加载统一配置 (~/.aiclaw/config.json)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Failed to get home directory", "error", err)
		os.Exit(1)
	}

	clawDir := filepath.Join(homeDir, ".aiclaw")

	lockFile, err := acquireInstanceLock(clawDir)
	if err != nil {
		slog.Error("Failed to acquire instance lock", "error", err)
		os.Exit(1)
	}
	defer releaseInstanceLock(lockFile)

	// 设置 trace 输出到 ~/.aiclaw/traces/（可选，调试时启用）
	if *trace {
		if err := setupTracing(clawDir); err != nil {
			slog.Warn("Failed to setup tracing", "error", err)
		}
	}

	configPath := filepath.Join(clawDir, "config.json")
	cfg, err := loadConfig(configPath)
	if err != nil {
		slog.Error("Failed to load config", "error", err, "path", configPath)
		os.Exit(1)
	}

	slog.Info("Loaded config", "path", configPath,
		"model", cfg.Model.ID,
		"provider", cfg.Model.Provider,
		"baseUrl", cfg.Model.BaseURL)

	// 创建 picoclaw 兼容的配置（用于 channels）
	picoCfg := &picoclawconfig.Config{
		Channels: cfg.Channels,
	}

	// 创建消息总线
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	// 创建媒体存储
	mediaStore := media.NewFileMediaStore()

	// 创建通道管理器
	cm, err := channels.NewManager(picoCfg, msgBus, mediaStore)
	if err != nil {
		slog.Error("Failed to create channel manager", "error", err)
		os.Exit(1)
	}

	// 创建 AgentLoop
	// 从 ~/.aiclaw/auth.json 读取 API Key
	apiKey, err := resolveAPIKey(clawDir, cfg.Model.Provider)
	if err != nil {
		slog.Warn("Failed to resolve API key", "error", err)
		// 继续执行，可能从环境变量读取
	}

	// 创建语音转录器（如果启用）
	var transcriber voice.Transcriber
	if cfg.Voice.Enabled {
		// 确定语音服务提供商
		provider := cfg.Voice.Provider
		if provider == "" {
			provider = "zhipu" // 默认使用智谱
		}

		// 获取 API Key
		apiKey := cfg.Voice.APIKey
		if apiKey == "" {
			// 尝试从 auth.json 读取
			apiKey, _ = resolveAPIKey(clawDir, provider)
		}

		if apiKey != "" {
			switch provider {
			case "groq":
				transcriber = voice.NewGroqTranscriber(voice.GroqConfig{
					APIKey:  apiKey,
					APIBase: cfg.Voice.APIBase,
					Model:   cfg.Voice.Model,
				})
				slog.Info("Voice transcription enabled", "provider", "groq", "model", cfg.Voice.Model)
			case "zhipu":
				transcriber = voice.NewZhipuTranscriber(voice.ZhipuConfig{
					APIKey:  apiKey,
					APIBase: cfg.Voice.APIBase,
				})
				slog.Info("Voice transcription enabled", "provider", "zhipu", "model", "glm-asr-2512")
				if !isFFmpegAvailable() {
					slog.Warn("ffmpeg not found; some audio formats may fail on zhipu ASR. Install ffmpeg for automatic transcoding fallback.")
				}
			default:
				slog.Warn("Unknown voice provider", "provider", provider)
			}
		} else {
			slog.Warn("Voice enabled but no API key found", "provider", provider)
		}
	}

	// Load skills
	skillLoader := skill.NewLoader(clawDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             "", // no workspace
		AgentDir:        clawDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})
	if len(skillResult.Diagnostics) > 0 {
		for _, diag := range skillResult.Diagnostics {
			if diag.Type == "error" {
				slog.Error("Skill error", "path", diag.Path, "message", diag.Message)
			}
		}
	}
	if len(skillResult.Skills) > 0 {
		slog.Info("Loaded skills", "count", len(skillResult.Skills))
	}

	// Create tool registry and register tools
	// For claw (chat bot), we create a workspace with clawDir as working directory since it's not tied to a specific project
	workspace := tools.MustNewWorkspace(clawDir)
	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.NewReadTool(workspace))
	toolRegistry.Register(tools.NewBashTool(workspace))
	toolRegistry.Register(tools.NewWriteTool(workspace))
	toolRegistry.Register(tools.NewGrepTool(workspace))
	toolRegistry.Register(tools.NewEditTool(workspace))

	agentConfig := &adapter.Config{
		Model:           cfg.Model.ID,
		Provider:        cfg.Model.Provider,
		APIURL:          cfg.Model.BaseURL,
		API:             cfg.Model.API,
		APIKey:          apiKey,
		SystemPrompt:    buildSystemPrompt(clawDir, skillResult.Skills),
		Tools:           toolRegistry.All(),
		ClawDir:         clawDir, // 传递 claw 配置目录
		Transcriber:     transcriber,
		FeishuAppID:     cfg.Channels.Feishu.AppID,
		FeishuAppSecret: cfg.Channels.Feishu.AppSecret,
		Skills:          skillResult.Skills,
	}

	slog.Info("Registered tools", "count", len(agentConfig.Tools))

	// 创建 CronService（需要在 AgentLoop 之前创建）
	cronStorePath := filepath.Join(clawDir, "cron", "jobs.json")
	cronService := cron.NewCronService(cronStorePath, func(job *cron.CronJob) (string, error) {
		slog.Info("[cron] Executing job", "name", job.Name, "id", job.ID)
		// ProcessDirect 将在 AgentLoop 创建后调用
		return "", nil
	})
	agentConfig.CronService = cronService

	agentLoop := adapter.NewAgentLoop(agentConfig, msgBus)

	// 设置上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 处理信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("Shutdown signal received", "signal", sig)
		cancel()
	}()

	// 启动通道
	slog.Info("Starting channels")
	if err := cm.StartAll(ctx); err != nil {
		slog.Error("Failed to start channels", "error", err)
		os.Exit(1)
	}

	// 注册 cron 命令
	registerCronCommands(agentLoop, cronService)

	// 更新 CronService 的 job handler
	cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		slog.Info("[cron] Executing job", "name", job.Name, "id", job.ID)
		return agentLoop.ProcessDirect(ctx, job.Payload.Message, "cron:"+job.ID)
	})

	// 启动 cron 服务
	if err := cronService.Start(); err != nil {
		slog.Error("Failed to start cron service", "error", err)
	} else {
		slog.Info("Cron service started", "jobs", len(cronService.ListJobs(false)))
	}
	defer cronService.Stop()

	slog.Info("Starting aiclaw",
		"model", cfg.Model.ID,
		"provider", cfg.Model.Provider)

	// 运行 AgentLoop
	if err := agentLoop.Run(ctx); err != nil {
		slog.Error("AgentLoop error", "error", err)
	}

	// 清理
	agentLoop.Close()
	slog.Info("aiclaw stopped")
}

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	logger := slog.New(newSimpleHandler(os.Stdout, opts))
	slog.SetDefault(logger)
}

// simpleHandler is a custom slog handler with cleaner output format
type simpleHandler struct {
	writer io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
}

func newSimpleHandler(w io.Writer, opts *slog.HandlerOptions) *simpleHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &simpleHandler{
		writer: w,
		level:  opts.Level,
	}
}

// Enabled implements slog.Handler
func (h *simpleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle implements slog.Handler
func (h *simpleHandler) Handle(ctx context.Context, r slog.Record) error {
	// Build a simple format: TIME LEVEL FILE:LINE MSG key1=value1 key2=value2
	var sb strings.Builder

	// Time and level
	sb.WriteString(r.Time.Format("15:04:05.000"))
	sb.WriteString(" ")

	// Shorten level names
	levelStr := strings.ToUpper(r.Level.String())
	if len(levelStr) > 4 {
		levelStr = levelStr[:4]
	}
	sb.WriteString(levelStr)
	sb.WriteString(" ")

	// Source (file:line)
	if r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := frames.Next()
		if frame.File != "" {
			file := frame.File
			if idx := strings.LastIndex(file, "/"); idx >= 0 {
				file = file[idx+1:]
			}
			sb.WriteString(file)
			sb.WriteString(":")
			sb.WriteString(strconv.Itoa(frame.Line))
			sb.WriteString(" ")
		}
	}

	// Message
	sb.WriteString(r.Message)

	// Add handler attrs
	for _, a := range h.attrs {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
	}

	// Attrs from record
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		// Handle different value types
		switch v := a.Value.Any().(type) {
		case string:
			sb.WriteString(v)
		case int64, int, uint64, uint:
			sb.WriteString(fmt.Sprintf("%d", v))
		case float64:
			sb.WriteString(fmt.Sprintf("%f", v))
		case bool:
			sb.WriteString(strconv.FormatBool(v))
		case time.Duration:
			sb.WriteString(v.String())
		default:
			sb.WriteString(a.Value.String())
		}
		return true
	})

	sb.WriteString("\n")
	_, err := h.writer.Write([]byte(sb.String()))
	return err
}

// WithAttrs implements slog.Handler
func (h *simpleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &simpleHandler{
		writer: h.writer,
		level:  h.level,
		attrs:  newAttrs,
	}
}

// WithGroup implements slog.Handler
func (h *simpleHandler) WithGroup(name string) slog.Handler {
	// For simplicity, just return self without group support
	return h
}

// setupTracing 配置 trace 输出到 ~/.aiclaw/traces/
// claw 模式：slog 日志到控制台，trace 事件到文件（分离模式）
func setupTracing(clawDir string) error {
	tracesDir := filepath.Join(clawDir, "traces")
	if err := os.MkdirAll(tracesDir, 0755); err != nil {
		return fmt.Errorf("failed to create traces dir: %w", err)
	}
	handler, err := traceevent.NewFileHandler(tracesDir)
	if err != nil {
		return fmt.Errorf("failed to create trace handler: %w", err)
	}
	traceevent.SetHandler(handler)
	slog.Info("Tracing enabled", "dir", tracesDir)
	return nil
}

func acquireInstanceLock(clawDir string) (*os.File, error) {
	if err := os.MkdirAll(clawDir, 0755); err != nil {
		return nil, fmt.Errorf("create claw dir: %w", err)
	}

	lockPath := filepath.Join(clawDir, "aiclaw.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another aiclaw instance is already running (lock: %s)", lockPath)
		}
		return nil, fmt.Errorf("acquire lock %s: %w", lockPath, err)
	}

	_ = lockFile.Truncate(0)
	if _, err := lockFile.Seek(0, 0); err == nil {
		_, _ = fmt.Fprintf(lockFile, "%d\n", os.Getpid())
	}

	return lockFile, nil
}

func releaseInstanceLock(lockFile *os.File) {
	if lockFile == nil {
		return
	}

	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
}

func isFFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// loadConfig 加载统一配置文件
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate required model configuration is present from config.json
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate ensures required configuration fields are present
// 
// Why load from config instead of hardcoding defaults?
// 1. User flexibility: Different users may prefer different models (e.g., glm-4-flash, gpt-4, claude)
// 2. Environment-specific: Development vs production may use different providers
// 3. Cost control: Users can choose cheaper/faster models based on their needs
// 4. No surprises: Explicit config prevents unexpected behavior from silent defaults
// 5. Multi-tenant: Different deployments can use different models without code changes
func (c *Config) validate() error {
	// Model ID is required - should be configured in ~/.aiclaw/config.json
	if c.Model.ID == "" {
		return fmt.Errorf("model.id is required in config.json")
	}
	// Provider is required - should be configured in ~/.aiclaw/config.json
	if c.Model.Provider == "" {
		return fmt.Errorf("model.provider is required in config.json")
	}
	// BaseURL is required - should be configured in ~/.aiclaw/config.json
	if c.Model.BaseURL == "" {
		return fmt.Errorf("model.baseUrl is required in config.json")
	}
	// API type is optional, default to openai-completions if not specified
	if c.Model.API == "" {
		c.Model.API = "openai-completions"
	}
	return nil
}

// resolveAPIKey 从 auth.json 或环境变量解析 API Key
func resolveAPIKey(clawDir, provider string) (string, error) {
	// 先尝试环境变量
	envVar := strings.ToUpper(provider) + "_API_KEY"
	if key := os.Getenv(envVar); key != "" {
		return key, nil
	}

	// 再尝试 auth.json
	authPath := filepath.Join(clawDir, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return "", fmt.Errorf("failed to read auth.json: %w (set %s env var)", err, envVar)
	}

	var auth map[string]map[string]string
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", fmt.Errorf("failed to parse auth.json: %w", err)
	}

	if providerAuth, ok := auth[provider]; ok {
		// 尝试不同的 key 字段名
		for _, keyField := range []string{"apiKey", "api_key", "key", "token"} {
			if key, ok := providerAuth[keyField]; ok && key != "" {
				return key, nil
			}
		}
	}

	return "", fmt.Errorf("API key not found for provider %s in auth.json", provider)
}

// buildSystemPrompt 构建 system prompt
// 使用 prompt.Builder，支持从 ~/.aiclaw/AGENTS.md 加载身份
func buildSystemPrompt(clawDir string, skills []skill.Skill) string {
	// 默认身份
	defaultIdentity := `# Claw Assistant

You are a helpful AI assistant accessible via chat platforms (Feishu, Telegram, etc).

## Important Rules

1. **Be helpful and accurate** - Provide clear, useful responses.

2. **Be concise** - Chat messages should be brief and readable. Avoid overly long explanations.

3. **Be friendly** - Maintain a warm, approachable tone.

4. **Use tools when needed** - When you need to perform actions, call the appropriate tool.

## Capabilities

- Answer questions and provide information
- Help with various tasks
- Remember conversation context within a session

## Limitations

- You cannot access the internet directly unless tools are available
- You cannot execute code unless tools are available
- Each chat session is independent - you don't share memory across different groups/private chats
`
	// 尝试从 ~/.aiclaw/AGENTS.md 加载自定义身份
	basePrompt := defaultIdentity
	agentsPath := filepath.Join(clawDir, "AGENTS.md")
	if content, err := os.ReadFile(agentsPath); err == nil && len(content) > 0 {
		basePrompt = string(content)
		slog.Info("Loaded custom identity from AGENTS.md", "path", agentsPath)
	}

	// 使用 prompt.Builder 构建
	builder := prompt.NewBuilder(basePrompt, "").
		SetNoWorkspace(true).
		SetSkills(skills).
		SetTaskTrackingEnabled(true).
		SetContextManagementEnabled(true)

	return builder.Build()
}

// registerCronCommands registers cron control commands.
func registerCronCommands(agentLoop *adapter.AgentLoop, cronService *cron.CronService) {
	// /cron - shows cron usage
	agentLoop.RegisterCommand("cron", func(args string, sess *adapter.Session) (string, error) {
		if cronService == nil {
			return "Cron service is not configured", nil
		}
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return `Usage:
  /cron list          List all cron jobs
  /cron add <expr> <message>  Add a cron job
  /cron remove <id>   Remove a cron job
  /cron enable <id>   Enable a cron job
  /cron disable <id>  Disable a cron job
  /cron status        Show cron service status`, nil
		}

		cmd := fields[0]
		switch cmd {
		case "list":
			return cmdCronList(cronService), nil
		case "add":
			if len(fields) < 3 {
				return "Usage: /cron add <expr> <message>\nExample: /cron add '0 9 * * *' Good morning", nil
			}
			expr := fields[1]
			message := strings.Join(fields[2:], " ")
			return cmdCronAdd(cronService, expr, message), nil
		case "remove", "rm", "del":
			if len(fields) < 2 {
				return "Usage: /cron remove <id>", nil
			}
			return cmdCronRemove(cronService, fields[1]), nil
		case "enable":
			if len(fields) < 2 {
				return "Usage: /cron enable <id>", nil
			}
			return cmdCronEnable(cronService, fields[1], true), nil
		case "disable":
			if len(fields) < 2 {
				return "Usage: /cron disable <id>", nil
			}
			return cmdCronEnable(cronService, fields[1], false), nil
		case "status":
			return cmdCronStatus(cronService), nil
		default:
			return fmt.Sprintf("Unknown cron command: %s\nUse /cron to see usage", cmd), nil
		}
	})
}

// cmdCronList 列出所有 cron 任务
func cmdCronList(cronService *cron.CronService) string {
	jobs := cronService.ListJobs(true)
	if len(jobs) == 0 {
		return "No cron jobs"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Cron Jobs (%d):\n\n", len(jobs)))
	for i, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}
		b.WriteString(fmt.Sprintf("[%d] %s\n", i, job.ID[:8]))
		b.WriteString(fmt.Sprintf("    Name: %s\n", job.Name))
		b.WriteString(fmt.Sprintf("    Status: %s\n", status))
		b.WriteString(fmt.Sprintf("    Schedule: %s\n", formatSchedule(&job.Schedule)))
		b.WriteString(fmt.Sprintf("    Message: %s\n", truncate(job.Payload.Message, 60)))
		if job.State.LastRunAtMS != nil {
			lastRun := time.UnixMilli(*job.State.LastRunAtMS).Format("2006-01-02 15:04:05")
			b.WriteString(fmt.Sprintf("    Last Run: %s\n", lastRun))
			b.WriteString(fmt.Sprintf("    Last Status: %s\n", job.State.LastStatus))
		}
		if job.State.NextRunAtMS != nil {
			nextRun := time.UnixMilli(*job.State.NextRunAtMS).Format("2006-01-02 15:04:05")
			b.WriteString(fmt.Sprintf("    Next Run: %s\n", nextRun))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// cmdCronAdd 添加 cron 任务
func cmdCronAdd(cronService *cron.CronService, expr, message string) string {
	schedule := cron.CronSchedule{
		Kind: "cron",
		Expr: expr,
	}

	job, err := cronService.AddJob(
		fmt.Sprintf("job_%d", time.Now().Unix()),
		schedule,
		message,
		false, // deliver
		"",    // channel
		"",    // to
	)
	if err != nil {
		return fmt.Sprintf("Failed to add cron job: %v", err)
	}

	return fmt.Sprintf("Cron job added:\n  ID: %s\n  Schedule: %s\n  Message: %s",
		job.ID[:8], expr, truncate(message, 60))
}

// cmdCronRemove 删除 cron 任务
func cmdCronRemove(cronService *cron.CronService, id string) string {
	// Try to find job by partial ID
	matched := false
	for _, job := range cronService.ListJobs(true) {
		if strings.HasPrefix(job.ID, id) || job.ID == id {
			id = job.ID
			matched = true
			break
		}
	}

	if !matched {
		return fmt.Sprintf("Job not found: %s", id)
	}

	if cronService.RemoveJob(id) {
		return fmt.Sprintf("Cron job removed: %s", id[:8])
	}
	return fmt.Sprintf("Failed to remove job: %s", id[:8])
}

// cmdCronEnable 启用/禁用 cron 任务
func cmdCronEnable(cronService *cron.CronService, id string, enable bool) string {
	// Try to find job by partial ID
	matched := false
	for _, job := range cronService.ListJobs(true) {
		if strings.HasPrefix(job.ID, id) || job.ID == id {
			id = job.ID
			matched = true
			break
		}
	}

	if !matched {
		return fmt.Sprintf("Job not found: %s", id)
	}

	job := cronService.EnableJob(id, enable)
	if job == nil {
		return fmt.Sprintf("Failed to %s job: %s", map[bool]string{true: "enable", false: "disable"}[enable], id[:8])
	}

	action := "disabled"
	if enable {
		action = "enabled"
	}
	return fmt.Sprintf("Cron job %s: %s", action, job.ID[:8])
}

// cmdCronStatus 显示 cron 服务状态
func cmdCronStatus(cronService *cron.CronService) string {
	status := cronService.Status()
	running := "stopped"
	if status["running"].(bool) {
		running = "running"
	}
	return fmt.Sprintf("Cron Service:\n  Status: %s\n  Total Jobs: %d\n  Enabled Jobs: %d",
		running, status["jobs"], status["enabled"])
}

// formatSchedule 格式化调度信息
func formatSchedule(s *cron.CronSchedule) string {
	switch s.Kind {
	case "cron":
		return s.Expr
	case "every":
		if s.EveryMS != nil {
			return fmt.Sprintf("every %d ms", *s.EveryMS)
		}
		return "every <unknown>"
	case "at":
		if s.AtMS != nil {
			t := time.UnixMilli(*s.AtMS)
			return fmt.Sprintf("at %s", t.Format("2006-01-02 15:04:05"))
		}
		return "at <unknown>"
	default:
		return s.Kind
	}
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s[:maxLen]
}

// EchoTool 是一个简单的示例工具
type EchoTool struct{}

func (t *EchoTool) Name() string        { return "echo" }
func (t *EchoTool) Description() string { return "Echo back the input message" }
func (t *EchoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "Message to echo back",
			},
		},
		"required": []string{"message"},
	}
}
func (t *EchoTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	msg, ok := args["message"].(string)
	if !ok {
		return nil, fmt.Errorf("message argument must be a string")
	}
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Echo: %s", msg)},
	}, nil
}
