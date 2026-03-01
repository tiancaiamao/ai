// aiclaw - AI Claw Bot with picoclaw channels and ai agent core.
// Configuration is unified in ~/.aiclaw/config.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"log/slog"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	_ "github.com/sipeed/picoclaw/pkg/channels/feishu" // 注册飞书通道工厂
	picoclawconfig "github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/claw/pkg/adapter"
)

var (
	logLevel = flag.String("log-level", "info", "Log level: debug, info, warn, error")
)

// ModelConfig 模型配置
type ModelConfig struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
}

// Config 是 aiclaw 的统一配置
type Config struct {
	Model    ModelConfig                   `json:"model"`
	Channels picoclawconfig.ChannelsConfig `json:"channels,omitempty"`
}

func main() {
	flag.Parse()

	// 设置日志
	setupLogging(*logLevel)

	// 加载统一配置 (~/.aiclaw/config.json)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Failed to get home directory", "error", err)
		os.Exit(1)
	}

	clawDir := filepath.Join(homeDir, ".aiclaw")

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

	agentConfig := &adapter.Config{
		Model:        cfg.Model.ID,
		Provider:     cfg.Model.Provider,
		APIURL:       cfg.Model.BaseURL,
		APIKey:       apiKey,
		SystemPrompt: buildSystemPrompt(clawDir),
		Tools:        []agentctx.Tool{}, // 暂时不注册工具
		ClawDir:      clawDir,           // 传递 claw 配置目录
	}
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))
	slog.SetDefault(logger)
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

	return &cfg, nil
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
func buildSystemPrompt(clawDir string) string {
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

	// 加载 skills
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

	// 使用 prompt.Builder 构建
	builder := prompt.NewBuilder(basePrompt, "").
		SetNoWorkspace(true).
		SetSkills(skillResult.Skills)

	return builder.Build()
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
