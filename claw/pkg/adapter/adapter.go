// Package adapter provides an AgentLoop implementation that uses ai agent core.
// It consumes messages from picoclaw's MessageBus and processes them with ai's agent.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/larksuite/oapi-sdk-go/v3"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/tiancaiamao/ai/claw/pkg/cron"
	"github.com/tiancaiamao/ai/claw/pkg/voice"
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	aiconfig "github.com/tiancaiamao/ai/pkg/config"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/modelselect"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// CommandHandler is the function signature for handling control commands.
// args: the remaining arguments after the command name
// sess: the current session (may be nil for commands that don't need session)
// Returns the response text and an optional error.
type CommandHandler func(args string, sess *Session) (string, error)

// CommandRegistry stores registered control commands.
type CommandRegistry struct {
	commands map[string]CommandHandler
	mu       sync.RWMutex
}

// NewCommandRegistry creates a new command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]CommandHandler),
	}
}

// Register registers a command handler.
func (r *CommandRegistry) Register(name string, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = handler
}

// Get retrieves a command handler by name.
func (r *CommandRegistry) Get(name string) (CommandHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.commands[name]
	return handler, ok
}

// List returns all registered command names.
func (r *CommandRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	return names
}

// AgentLoop implements the message processing loop using ai agent core.
// It is compatible with picoclaw's MessageBus interface.
type AgentLoop struct {
	bus        *bus.MessageBus
	sessions   map[string]*Session // 按 sessionKey 隔离的会话
	sessionsMu sync.RWMutex
	running    atomic.Bool

	// 配置
	appConfig  *aiconfig.Config // Application config for LoopConfig
	model      llm.Model
	apiKey     string
	systemPrompt string
	tools      []agentctx.Tool
	sessionsDir string // session storage directory
	compactor   *compact.Compactor

	// Voice transcription support
	transcriber voice.Transcriber

	// Feishu configuration for voice file download
	feishuClient    *lark.Client
	feishuAppID     string
	feishuAppSecret string

	// Cron service
	cronService *cron.CronService

	// Skills
	skills []skill.Skill

	// Thinking level (off, minimal, low, medium, high, xhigh)
	thinkingLevel   string
	thinkingLevelMu sync.RWMutex

	// Command registry
	commands *CommandRegistry

	// Statistics
	messageCount atomic.Int64
	totalTokens  atomic.Int64
	startTime    time.Time
}

// Session represents an isolated conversation session
type Session struct {
	Key       string
	Agent     *agent.Agent
	Session   *session.Session
	Compactor *clawCompactor
}

// clawCompactor 实现 agent.Compactor 接口
type clawCompactor struct {
	mu        sync.Mutex
	sess      *session.Session
	compactor *compact.Compactor
}

func (c *clawCompactor) Update(sess *session.Session, comp *compact.Compactor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess = sess
	c.compactor = comp
}

func (c *clawCompactor) ShouldCompact(messages []agentctx.AgentMessage) bool {
	c.mu.Lock()
	sess := c.sess
	comp := c.compactor
	c.mu.Unlock()

	if comp == nil || sess == nil {
		return false
	}
	if !comp.ShouldCompact(messages) {
		return false
	}
	return sess.CanCompact(comp)
}

func (c *clawCompactor) Compact(messages []agentctx.AgentMessage, previousSummary string) (*agent.CompactionResult, error) {
	c.mu.Lock()
	sess := c.sess
	comp := c.compactor
	c.mu.Unlock()

	if sess == nil || comp == nil {
		return &agent.CompactionResult{Messages: messages}, nil
	}

	sessionResult, err := sess.Compact(comp)
	if err != nil {
		if session.IsNonActionableCompactionError(err) {
			return &agent.CompactionResult{Messages: messages}, nil
		}
		return nil, err
	}

	return &agent.CompactionResult{
		Summary:      sessionResult.Summary,
		Messages:     sess.GetMessages(),
		TokensBefore: sessionResult.TokensBefore,
		TokensAfter:  sessionResult.TokensAfter,
	}, nil
}

func (c *clawCompactor) CalculateDynamicThreshold() int {
	c.mu.Lock()
	comp := c.compactor
	c.mu.Unlock()

	if comp == nil {
		return 0
	}
	return comp.CalculateDynamicThreshold()
}

func (c *clawCompactor) EstimateContextTokens(messages []agentctx.AgentMessage) int {
	c.mu.Lock()
	comp := c.compactor
	c.mu.Unlock()

	if comp == nil {
		return 0
	}
	return comp.EstimateContextTokens(messages)
}

// Config 是 AgentLoop 的配置
type AppConfig struct {
	Model        string          // 模型 ID，如 "claude-3-5-sonnet-20241022"
	Provider     string          // 提供商，如 "anthropic"
	APIKey       string          // API 密钥
	APIURL       string          // API URL (可选)
	API          string          // API 类型，如 "anthropic-messages"
	SystemPrompt string          // 系统提示词
	Tools        []agentctx.Tool // 工具列表
	ClawDir      string          // claw 配置目录 (~/.aiclaw)

	// 语音支持
	Transcriber voice.Transcriber // 语音转录器（可选）

	// 飞书配置（用于下载语音文件）
	FeishuAppID     string
	FeishuAppSecret string

	// Cron 服务（可选）
	CronService *cron.CronService

	// Skills (可选)
	Skills []skill.Skill
}

// NewAgentLoop 创建一个新的 AgentLoop
func NewAgentLoop(cfg *AppConfig, msgBus *bus.MessageBus) *AgentLoop {
	model := resolveModel(cfg)

	slog.Info("[AgentLoop] Model resolved",
		"id", model.ID,
		"provider", model.Provider,
		"baseUrl", model.BaseURL,
		"contextWindow", model.ContextWindow)

	// 创建 sessions 目录
	sessionsDir := ""
	if cfg.ClawDir != "" {
		sessionsDir = filepath.Join(cfg.ClawDir, "sessions")
		if err := os.MkdirAll(sessionsDir, 0755); err != nil {
			slog.Warn("[AgentLoop] Failed to create sessions dir", "error", err)
		}
	}

	// 创建 compactor
	compactorCfg := compact.DefaultConfig()
	compactor := compact.NewCompactor(
		compactorCfg,
		model,
		cfg.APIKey,
		cfg.SystemPrompt,
		model.ContextWindow,
	)

	// 创建飞书客户端（用于下载语音文件）
	var feishuClient *lark.Client
	if cfg.FeishuAppID != "" && cfg.FeishuAppSecret != "" {
		feishuClient = lark.NewClient(cfg.FeishuAppID, cfg.FeishuAppSecret)
		slog.Info("[AgentLoop] Feishu client created for voice download")
	}

	// 创建命令注册表
	commands := NewCommandRegistry()

	loop := &AgentLoop{
		bus:             msgBus,
		sessions:        make(map[string]*Session),
		model:           model,
		apiKey:          cfg.APIKey,
		systemPrompt:    cfg.SystemPrompt,
		tools:           cfg.Tools,
		sessionsDir:     sessionsDir,
		compactor:       compactor,
		transcriber:     cfg.Transcriber,
		feishuClient:    feishuClient,
		feishuAppID:     cfg.FeishuAppID,
		feishuAppSecret: cfg.FeishuAppSecret,
		cronService:     cfg.CronService,
		skills:          cfg.Skills,
		thinkingLevel:   "off", // Default to off
		commands:        commands,
		startTime:       time.Now(),
	}

	// 注册基础命令
	loop.registerBuiltinCommands()

	return loop
}

// ModelSpec aliases shared model specification to avoid duplicate schema definitions.
type ModelSpec = aiconfig.ModelSpec

// resolveModel 从 claw 配置目录加载模型配置
func resolveModel(cfg *AppConfig) llm.Model {
	model := llm.Model{
		ID:       cfg.Model,
		Provider: cfg.Provider,
		BaseURL:  cfg.APIURL,
		API:      cfg.API,
	}

	// 尝试从 ~/.aiclaw/models.json 加载完整模型配置（包括 API 类型）
	if cfg.ClawDir != "" {
		modelsPath := filepath.Join(cfg.ClawDir, "models.json")
		specs, err := aiconfig.LoadModelSpecs(modelsPath)
		if err == nil {
			// 查找匹配的模型
			for _, spec := range specs {
				if spec.ID == cfg.Model {
					// 使用 models.json 中的配置补充缺失字段
					if model.Provider == "" {
						model.Provider = spec.Provider
					}
					if model.BaseURL == "" {
						model.BaseURL = spec.BaseURL
					}
					// 始终使用 models.json 中的 API 类型（如果存在）
					if spec.API != "" {
						model.API = spec.API
					}
					model.ContextWindow = spec.ContextWindow
					slog.Info("[AgentLoop] Loaded model config from models.json",
						"id", spec.ID,
						"provider", spec.Provider,
						"baseUrl", spec.BaseURL,
						"api", model.API)
					return model
				}
			}
		} else {
			slog.Warn("[AgentLoop] Failed to load models.json", "error", err, "path", modelsPath)
		}
	}

	// 如果没有找到 models.json 或模型不在其中，使用现有配置
	slog.Info("[AgentLoop] Using config from config.json",
		"id", model.ID,
		"provider", model.Provider,
		"baseUrl", model.BaseURL,
		"api", model.API)
	return model
}

// Run 启动消息处理循环
func (a *AgentLoop) Run(ctx context.Context) error {
	slog.Info("[AgentLoop] Starting")
	a.running.Store(true)
	defer a.running.Store(false)

	for a.running.Load() {
		select {
		case <-ctx.Done():
			slog.Info("[AgentLoop] Stopped by context")
			return nil
		default:
			msg, ok := a.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			response, err := a.processMessage(ctx, msg)
			if err != nil {
				slog.Error("[AgentLoop] Error processing message", "error", err)
				response = fmt.Sprintf("Error: %v", err)
			}

			if response != "" {
				a.bus.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: response,
				})
			}
		}
	}

	return nil
}

// Stop 停止消息处理循环
func (a *AgentLoop) Stop() {
	a.running.Store(false)
}

// processMessage 处理单条消息
func (a *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// 生成 session key
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = msg.Channel + ":" + msg.ChatID
	}

	slog.Info("[AgentLoop] Processing message",
		"channel", msg.Channel,
		"chat_id", msg.ChatID,
		"session_key", sessionKey,
		"content_preview", truncate(msg.Content, 80),
		"media_count", len(msg.Media))

	// 处理媒体文件（语音/音频）
	content := msg.Content
	hasVoiceMedia := false

	// 1. 处理 msg.Media 中的音频文件
	if len(msg.Media) > 0 {
		for _, mediaPath := range msg.Media {
			if voice.IsAudioFile(mediaPath) {
				hasVoiceMedia = true
				if a.transcriber != nil && a.transcriber.IsAvailable() {
					result, err := a.transcriber.Transcribe(ctx, mediaPath)
					if err != nil {
						slog.Warn("[AgentLoop] Voice transcription failed", "error", err, "file", mediaPath)
						continue
					}
					slog.Info("[AgentLoop] Voice transcribed", "text_length", len(result.Text), "language", result.Language)
					if content != "" {
						content = "[voice transcription: " + result.Text + "]" + "\n" + content
					} else {
						content = "[voice transcription: " + result.Text + "]"
					}
				}
			}
		}
	}

	// 2. 处理飞书语音消息（msg.Content 是 JSON 元数据）
	if msg.Channel == "feishu" && content != "" {
		if transcribed, err := a.handleFeishuVoice(ctx, content, msg.MessageID, msg.ChatID, msg.Metadata); err != nil {
			slog.Warn("[AgentLoop] Failed to handle feishu voice", "error", err)
		} else if transcribed != "" {
			hasVoiceMedia = true
			content = transcribed
		}
	}

	// 如果只有语音消息但无法转录，跳过处理
	if content == "" && hasVoiceMedia {
		slog.Info("[AgentLoop] Skipping voice message (no transcriber available)")
		return "", nil
	}

	// 获取或创建会话
	sess, err := a.getOrCreateSession(sessionKey)
	if err != nil {
		return "", fmt.Errorf("failed to get/create session: %w", err)
	}

	// 检查是否是控制指令（/ 前缀）
	if strings.HasPrefix(content, "/") {
		cmd := strings.TrimSpace(strings.TrimPrefix(content, "/"))
		response, err := a.handleControlCommand(ctx, cmd, sessionKey, sess)
		if err != nil {
			return fmt.Sprintf("Command error: %v", err), nil
		}
		return response, nil
	}

	// 保存用户消息到 session
	userMsg := agentctx.NewUserMessage(content)
	sess.Session.AppendMessage(userMsg)

	// 发送消息给 agent
	// 如果 agent 正在处理，使用 FollowUp 而不是等待超时
	if err := sess.Agent.Prompt(content); err != nil {
		if errors.Is(err, agent.ErrAgentBusy) {
			// Agent busy, use FollowUp instead
			slog.Info("[AgentLoop] Agent busy, using follow-up", "session_key", sessionKey)
			if followErr := sess.Agent.FollowUp(content); followErr != nil {
				return "", fmt.Errorf("agent busy and follow-up queue full: %w", followErr)
			}
			return fmt.Sprintf("(Agent is processing, your message has been queued: %s)", truncate(content, 50)), nil
		}
		return "", fmt.Errorf("agent prompt failed: %w", err)
	}

	// 收集响应
	var response strings.Builder
	var assistantMsg *agentctx.AgentMessage
	var agentErrors []string

	for event := range sess.Agent.Events() {
		switch event.Type {
		case agent.EventTurnEnd:
			if event.Message != nil {
				response.WriteString(event.Message.ExtractText())
				assistantMsg = event.Message
			}
		case agent.EventAgentEnd:
			break
		case agent.EventError:
			if event.Error != "" {
				slog.Error("[AgentLoop] Agent error", "error", event.Error)
				agentErrors = append(agentErrors, event.Error)
			}
		}
		if event.Type == agent.EventAgentEnd {
			break
		}
	}

	// 如果有错误且没有正常响应，返回错误信息
	result := response.String()
	if result == "" && len(agentErrors) > 0 {
		// 返回最后一个错误（通常是最相关的）
		lastError := agentErrors[len(agentErrors)-1]
		return fmt.Sprintf("Error: %s", lastError), nil
	}
	// 如果有响应但也有错误，在响应后附加错误信息
	if result != "" && len(agentErrors) > 0 {
		// 将错误附加到响应后面
		result += fmt.Sprintf("\n\n[Errors occurred: %d]", len(agentErrors))
		for _, e := range agentErrors {
			result += fmt.Sprintf("\n- %s", truncate(e, 100))
		}
	}

	// 保存助手消息到 session
	if assistantMsg != nil {
		sess.Session.AppendMessage(*assistantMsg)
	}

	slog.Info("[AgentLoop] Response", "session_key", sessionKey, "length", len(result), "errors", len(agentErrors))
	return result, nil
}

// getOrCreateSession 获取或创建会话
func (a *AgentLoop) getOrCreateSession(sessionKey string) (*Session, error) {
	a.sessionsMu.RLock()
	sess, exists := a.sessions[sessionKey]
	a.sessionsMu.RUnlock()

	if exists {
		return sess, nil
	}

	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	// 再次检查
	if sess, exists := a.sessions[sessionKey]; exists {
		return sess, nil
	}

	// 创建新会话
	sess, err := a.createSession(sessionKey)
	if err != nil {
		return nil, err
	}

	a.sessions[sessionKey] = sess
	return sess, nil
}

// createSession 创建新会话
func (a *AgentLoop) createSession(sessionKey string) (*Session, error) {
	// 创建 session 目录
	var sess *session.Session

	if a.sessionsDir != "" {
		// 使用安全的目录名（替换特殊字符）
		safeKey := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, sessionKey)

		sessionDir := filepath.Join(a.sessionsDir, safeKey)
		if err := os.MkdirAll(sessionDir, 0755); err != nil {
			slog.Warn("[AgentLoop] Failed to create session dir", "error", err, "path", sessionDir)
		} else {
			// 尝试加载现有 session
			var err error
			sess, err = session.LoadSessionLazy(sessionDir, session.DefaultLoadOptions())
			if err != nil {
				slog.Warn("[AgentLoop] Failed to load session, creating new", "error", err)
				sess = nil
			} else {
				slog.Info("[AgentLoop] Loaded existing session", "path", sessionDir, "messages", len(sess.GetMessages()))
			}
		}
	}

	if sess == nil {
		sess = session.NewSession("", nil) // 无持久化
	}

	// 创建 agent context
	agentCtx := agentctx.NewAgentContext(a.systemPrompt)

	// 从 session 恢复消息
	existingMessages := sess.GetMessages()
	if len(existingMessages) > 0 {
		agentCtx.Messages = existingMessages
		slog.Info("[AgentLoop] Restored messages from session", "count", len(existingMessages))
	}

	// 恢复最后的 compaction summary
	agentCtx.LastCompactionSummary = sess.GetLastCompactionSummary()

	// 创建并设置 compactor
	clawComp := &clawCompactor{
		sess:      sess,
		compactor: a.compactor,
	}

	// 从 AgentLoop 的配置构建 LoopConfig
	loopCfg := a.loopConfig(clawComp)

	// 创建 agent
	ag := agent.NewAgentFromConfigWithContext(a.model, a.apiKey, agentCtx, loopCfg)

	// 注册工具
	for _, tool := range a.tools {
		ag.AddTool(tool)
	}

	return &Session{
		Key:       sessionKey,
		Agent:     ag,
		Session:   sess,
		Compactor: clawComp,
	}, nil
}

// loopConfig builds LoopConfig from AgentLoop's configuration.
func (a *AgentLoop) loopConfig(compactor agent.Compactor) *agent.LoopConfig {
	// Start with config defaults (if available)
	var cfg *agent.LoopConfig
	if a.appConfig != nil {
		cfg = a.appConfig.ToLoopConfig(
			aiconfig.WithCompactor(compactor),
			aiconfig.WithContextWindow(a.model.ContextWindow),
		)
	}
	// Fallback if appConfig is nil or ToLoopConfig returned nil
	if cfg == nil {
		cfg = agent.DefaultLoopConfig()
		cfg.Compactor = compactor
		cfg.ContextWindow = a.model.ContextWindow
	}

	// Override with runtime thinking level
	a.thinkingLevelMu.RLock()
	cfg.ThinkingLevel = a.thinkingLevel
	a.thinkingLevelMu.RUnlock()

	return cfg
}

// GetSession 获取会话
func (a *AgentLoop) GetSession(sessionKey string) (*Session, bool) {
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	sess, ok := a.sessions[sessionKey]
	return sess, ok
}

// ListSessions 列出所有会话 key
func (a *AgentLoop) ListSessions() []string {
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()

	keys := make([]string, 0, len(a.sessions))
	for k := range a.sessions {
		keys = append(keys, k)
	}
	return keys
}

// CloseSession 关闭会话
func (a *AgentLoop) CloseSession(sessionKey string) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	if sess, ok := a.sessions[sessionKey]; ok {
		sess.Agent.Shutdown()
		delete(a.sessions, sessionKey)
	}
}

// Close 关闭所有会话
func (a *AgentLoop) Close() {
	a.Stop()

	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	for _, sess := range a.sessions {
		sess.Agent.Shutdown()
	}
	a.sessions = make(map[string]*Session)
}

// RegisterCommand registers a custom control command.
// name: the command name (without the "/" prefix)
// handler: the function to handle the command
func (a *AgentLoop) RegisterCommand(name string, handler CommandHandler) {
	a.commands.Register(name, handler)
}

// registerBuiltinCommands registers the built-in control commands.
func (a *AgentLoop) registerBuiltinCommands() {
	a.commands.Register("help", func(args string, sess *Session) (string, error) {
		return a.cmdHelp(), nil
	})
	// /commands or /skills - list available skills (aliases)
	a.commands.Register("commands", func(args string, sess *Session) (string, error) {
		return a.cmdCommands(), nil
	})
	a.commands.Register("skills", func(args string, sess *Session) (string, error) {
		return a.cmdCommands(), nil
	})
	a.commands.Register("session", func(args string, sess *Session) (string, error) {
		return a.cmdSession(sess), nil
	})
	a.commands.Register("history", func(args string, sess *Session) (string, error) {
		return a.cmdHistory(sess), nil
	})
	a.commands.Register("messages", func(args string, sess *Session) (string, error) {
		return a.cmdHistory(sess), nil
	})
	a.commands.Register("clear", func(args string, sess *Session) (string, error) {
		return a.cmdClear(sess), nil
	})
	a.commands.Register("model", func(args string, sess *Session) (string, error) {
		return a.cmdModel(args, sess)
	})
	a.commands.Register("traceevent", func(args string, sess *Session) (string, error) {
		return a.cmdTraceevent(args), nil
	})
	// show commands - show settings and usage
	a.commands.Register("show", func(args string, sess *Session) (string, error) {
		return a.cmdShow(args, sess), nil
	})
	// thinking command - toggle thinking mode
	a.commands.Register("thinking", func(args string, sess *Session) (string, error) {
		return a.cmdThinking(args, sess), nil
	})
}

// Helper

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s[:maxLen]
}

// FeishuVoicePayload 飞书语音消息的 JSON 结构
type FeishuVoicePayload struct {
	FileKey  string `json:"file_key"`
	Duration int    `json:"duration"`
}

// handleFeishuVoice 处理飞书语音消息
// 返回转录后的文本，如果不是语音消息则返回空字符串
func (a *AgentLoop) handleFeishuVoice(
	ctx context.Context,
	content string,
	inboundMessageID string,
	chatID string,
	metadata map[string]string,
) (string, error) {
	// 检查是否是飞书语音消息格式
	var voicePayload FeishuVoicePayload
	if err := json.Unmarshal([]byte(content), &voicePayload); err != nil {
		return "", nil // 不是 JSON，不是语音消息
	}

	// 检查是否是语音消息
	if voicePayload.FileKey == "" {
		return "", nil // 没有 file_key，不是语音消息
	}

	slog.Info("[AgentLoop] Detected feishu voice message", "file_key", voicePayload.FileKey, "duration", voicePayload.Duration)

	// 检查是否有转录器
	if a.transcriber == nil || !a.transcriber.IsAvailable() {
		slog.Warn("[AgentLoop] No transcriber available for feishu voice")
		return "", nil
	}

	// 获取 message_id（飞书语音下载需要）
	// In picoclaw v0.2.0+, message ID is carried on bus.InboundMessage.MessageID.
	messageID := strings.TrimSpace(inboundMessageID)
	if metadata != nil {
		if messageID == "" {
			messageID = strings.TrimSpace(metadata["message_id"])
		}
		if messageID == "" {
			messageID = strings.TrimSpace(metadata["open_message_id"])
		}
	}
	if messageID == "" {
		slog.Warn("[AgentLoop] No message id found for feishu voice",
			"inbound_message_id", inboundMessageID,
			"chat_id", chatID,
			"metadata", metadata)

		// Fallback: query recent chat messages and match by file_key.
		// This handles cases where upstream metadata omits message_id.
		if chatID != "" {
			foundID, err := a.findFeishuAudioMessageID(ctx, chatID, voicePayload.FileKey)
			if err != nil {
				slog.Warn("[AgentLoop] Failed to resolve feishu message id by file_key",
					"error", err,
					"chat_id", chatID,
					"file_key", voicePayload.FileKey)
			} else if foundID != "" {
				messageID = foundID
				slog.Info("[AgentLoop] Resolved feishu message id by file_key",
					"chat_id", chatID,
					"file_key", voicePayload.FileKey,
					"message_id", messageID)
			}
		}
	}

	// 下载飞书音频文件
	audioPath, err := a.downloadFeishuAudio(ctx, voicePayload.FileKey, messageID)
	if err != nil {
		// 如果下载失败，返回提示信息而不是错误
		slog.Warn("[AgentLoop] Failed to download feishu audio", "error", err, "message_id", messageID)
		return "[Voice message - download failed. Note: Feishu voice requires message_id in metadata.]", nil
	}
	defer os.Remove(audioPath) // 清理临时文件

	slog.Info("[AgentLoop] Downloaded feishu audio", "path", audioPath)

	// 转录
	result, err := a.transcriber.Transcribe(ctx, audioPath)
	if err != nil {
		// Zhipu may reject OGG/Opus with code 1214. Try local ffmpeg conversion fallback.
		if isUnsupportedAudioFormatError(err) {
			convertedPath, convErr := transcodeAudioToMP3(ctx, audioPath)
			if convErr == nil {
				defer os.Remove(convertedPath)
				slog.Info("[AgentLoop] Retrying feishu voice transcription with converted audio",
					"path", convertedPath)
				result, err = a.transcriber.Transcribe(ctx, convertedPath)
			} else {
				slog.Warn("[AgentLoop] Failed to convert feishu audio for transcription", "error", convErr)
			}
		}
		if err != nil {
			// Degrade gracefully: do not pass raw JSON payload to the LLM.
			return "[Voice message - transcription failed: unsupported audio format for current ASR provider]", nil
		}
	}

	slog.Info("[AgentLoop] Feishu voice transcribed", "text_length", len(result.Text), "language", result.Language)
	return "[voice transcription: " + result.Text + "]", nil
}

// downloadFeishuAudio 下载飞书音频文件
func (a *AgentLoop) downloadFeishuAudio(ctx context.Context, fileKey string, messageID string) (string, error) {
	if a.feishuAppID == "" || a.feishuAppSecret == "" {
		return "", fmt.Errorf("feishu app_id or app_secret not configured")
	}
	if messageID == "" {
		return "", fmt.Errorf("missing message_id for feishu audio download")
	}

	// 创建临时文件
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "feishu_voice_"+fileKey+".ogg")

	// 使用 HTTP API 下载飞书文件
	// 1. 先获取 tenant_access_token
	token, err := a.getFeishuAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get feishu access token: %w", err)
	}

	// 2. 下载消息资源文件（语音属于消息资源）
	// https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message-resources/get
	downloadURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/resources/%s?type=file", messageID, fileKey)
	var req *http.Request

	req, err = http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	slog.Info("[AgentLoop] Downloading feishu audio", "url", downloadURL, "has_message_id", messageID != "")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download feishu file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("feishu API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// 3. 写入临时文件
	out, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}

	return tmpFile, nil
}

type feishuMessageListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []struct {
			MessageID   string `json:"message_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"items"`
	} `json:"data"`
}

// findFeishuAudioMessageID tries to resolve open_message_id by matching file_key
// from recent chat messages.
func (a *AgentLoop) findFeishuAudioMessageID(ctx context.Context, chatID, fileKey string) (string, error) {
	if chatID == "" || fileKey == "" {
		return "", nil
	}

	token, err := a.getFeishuAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get feishu access token: %w", err)
	}

	q := url.Values{}
	q.Set("container_id_type", "chat")
	q.Set("container_id", chatID)
	q.Set("sort_type", "ByCreateTimeDesc")
	q.Set("page_size", "20")

	listURL := "https://open.feishu.cn/open-apis/im/v1/messages?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create list messages request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to list feishu messages: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read list messages response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list messages API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var listResp feishuMessageListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return "", fmt.Errorf("failed to parse list messages response: %w", err)
	}
	if listResp.Code != 0 {
		return "", fmt.Errorf("list messages API error: code=%d msg=%s", listResp.Code, listResp.Msg)
	}

	for _, item := range listResp.Data.Items {
		if item.MessageID == "" {
			continue
		}
		if item.MessageType != "audio" {
			continue
		}
		if strings.Contains(item.Content, fileKey) {
			return item.MessageID, nil
		}
	}

	return "", nil
}

func isUnsupportedAudioFormatError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "\"code\":\"1214\"") ||
		strings.Contains(msg, "不支持当前文件格式") ||
		strings.Contains(msg, "unsupported") && strings.Contains(msg, "format")
}

func transcodeAudioToMP3(ctx context.Context, inputPath string) (string, error) {
	if strings.TrimSpace(inputPath) == "" {
		return "", fmt.Errorf("input path is empty")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH")
	}

	outPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "_transcoded.mp3"
	cmd := exec.CommandContext(
		ctx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", inputPath,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		outPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg transcode failed: %w, output: %s", err, string(output))
	}
	return outPath, nil
}

// feishuTokenResponse 飞书 token 响应
type feishuTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// feishuTokenCache 缓存飞书 token
type feishuTokenCache struct {
	token      string
	expireTime time.Time
	mu         sync.Mutex
}

var globalFeishuTokenCache feishuTokenCache

// getFeishuAccessToken 获取飞书 tenant_access_token
func (a *AgentLoop) getFeishuAccessToken(ctx context.Context) (string, error) {
	globalFeishuTokenCache.mu.Lock()
	defer globalFeishuTokenCache.mu.Unlock()

	// 检查缓存是否有效
	if globalFeishuTokenCache.token != "" && time.Now().Before(globalFeishuTokenCache.expireTime) {
		return globalFeishuTokenCache.token, nil
	}

	// 获取新 token
	url := "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	payload := map[string]string{
		"app_id":     a.feishuAppID,
		"app_secret": a.feishuAppSecret,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tokenResp feishuTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}

	if tokenResp.Code != 0 {
		return "", fmt.Errorf("feishu auth error: code=%d msg=%s", tokenResp.Code, tokenResp.Msg)
	}

	// 缓存 token（提前 5 分钟过期）
	globalFeishuTokenCache.token = tokenResp.TenantAccessToken
	globalFeishuTokenCache.expireTime = time.Now().Add(time.Duration(tokenResp.Expire-300) * time.Second)

	return tokenResp.TenantAccessToken, nil
}

// ProcessDirect 处理直接调用的消息（如 cron 触发）
func (a *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    "cron",
		ChatID:     "cron",
		SessionKey: sessionKey,
		Content:    content,
		SenderID:   "cron",
	}
	return a.processMessage(ctx, msg)
}

// handleControlCommand 处理控制指令
func (a *AgentLoop) handleControlCommand(ctx context.Context, cmdLine, sessionKey string, sess *Session) (string, error) {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := fields[0]
	args := strings.TrimSpace(strings.TrimPrefix(cmdLine, cmd))

	// 从命令注册表中查找处理器
	handler, ok := a.commands.Get(cmd)
	if ok {
		return handler(args, sess)
	}

	// 命令未找到
	return fmt.Sprintf("Unknown command: %s\nUse /help for available commands", cmd), nil
}

// cmdHelp 显示帮助信息
func (a *AgentLoop) cmdHelp() string {
	// Define command descriptions
	descriptions := map[string]string{
		"help":       "Show this help message",
		"commands":   "List available skills (alias: /skills)",
		"skills":     "List available skills (alias: /commands)",
		"session":    "Show current session info",
		"history":    "Show message history (alias: /messages)",
		"messages":   "Show message history (alias: /history)",
		"clear":      "Clear current session messages",
		"model":      "List or switch AI models",
		"traceevent": "Manage trace events for debugging",
		"cron":       "Manage cron jobs",
		"show":       "Show settings or usage (usage: /show [settings|usage])",
		"thinking":   "Toggle or set thinking level (usage: /thinking [off|minimal|low|medium|high|xhigh])",
	}

	commands := a.commands.List()
	sort.Strings(commands)

	var b strings.Builder
	b.WriteString("Control Commands:\n\n")
	for _, cmd := range commands {
		desc := descriptions[cmd]
		if desc != "" {
			b.WriteString(fmt.Sprintf("  /%-15s %s\n", cmd, desc))
		} else {
			b.WriteString(fmt.Sprintf("  /%s\n", cmd))
		}
	}
	b.WriteString("\nNormal messages (without / prefix) will be sent to the agent.")
	return strings.TrimSpace(b.String())
}

// cmdCommands lists available skills
func (a *AgentLoop) cmdCommands() string {
	if len(a.skills) == 0 {
		return "No skills available"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Available Skills (%d):\n\n", len(a.skills)))

	for i, s := range a.skills {
		b.WriteString(fmt.Sprintf("[%d] /%s\n", i, s.Name))
		if s.Description != "" {
			b.WriteString(fmt.Sprintf("    %s\n", s.Description))
		}
	}

	return strings.TrimSpace(b.String())
}

// cmdSession 显示当前会话信息
func (a *AgentLoop) cmdSession(sess *Session) string {
	if sess == nil {
		return "No active session"
	}

	messages := sess.Session.GetMessages()
	var userMsgs, asstMsgs, toolMsgs int
	for _, m := range messages {
		switch m.Role {
		case "user":
			userMsgs++
		case "assistant":
			asstMsgs++
		case "tool":
			toolMsgs++
		}
	}

	return fmt.Sprintf(`Session Info:
  Key: %s
  Messages: %d total (user: %d, assistant: %d, tool: %d)
  Model: %s
  Provider: %s`,
		sess.Key,
		len(messages),
		userMsgs,
		asstMsgs,
		toolMsgs,
		a.model.ID,
		a.model.Provider,
	)
}

// cmdHistory 显示消息历史
func (a *AgentLoop) cmdHistory(sess *Session) string {
	if sess == nil {
		return "No active session"
	}

	messages := sess.Session.GetMessages()
	if len(messages) == 0 {
		return "No messages in session"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Message History (%d messages):\n\n", len(messages)))

	// 显示最近 20 条消息
	start := 0
	if len(messages) > 20 {
		start = len(messages) - 20
		b.WriteString(fmt.Sprintf("(Showing last %d messages)\n\n", 20))
	}

	for i := start; i < len(messages); i++ {
		m := messages[i]
		role := m.Role
		text := m.ExtractText()
		if text == "" {
			text = fmt.Sprintf("[%d content blocks]", len(m.Content))
		}
		preview := truncate(text, 100)
		b.WriteString(fmt.Sprintf("[%d] %s: %s\n", i, role, preview))
	}

	return strings.TrimSpace(b.String())
}

// cmdClear 清空当前会话消息
func (a *AgentLoop) cmdClear(sess *Session) string {
	if sess == nil {
		return "No active session"
	}

	// 获取当前消息数量
	messages := sess.Session.GetMessages()
	msgCount := len(messages)

	// 创建新的 session 来清空消息
	sess.Session = session.NewSession(sess.Session.GetDir(), nil)

	// 同时重置 agent context
	if sess.Agent != nil {
		agentCtx := agentctx.NewAgentContext(a.systemPrompt)
		sess.Agent = agent.NewAgentWithContext(a.model, a.apiKey, agentCtx)
		sess.Agent.SetCompactor(sess.Compactor)
		sess.Agent.SetContextWindow(a.model.ContextWindow)
		for _, tool := range a.tools {
			sess.Agent.AddTool(tool)
		}

		// 设置思考级别
		a.thinkingLevelMu.RLock()
		thinkingLevel := a.thinkingLevel
		a.thinkingLevelMu.RUnlock()
		sess.Agent.SetThinkingLevel(thinkingLevel)
	}

	return fmt.Sprintf("Cleared %d messages from session", msgCount)
}

// cmdModel lists available models or switches to a specific model
// Usage:
//
//	/model          - list all available models
//	/model <id>     - switch to the specified model
func (a *AgentLoop) cmdModel(args string, sess *Session) (string, error) {
	args = strings.TrimSpace(args)

	// If no args, list available models
	if args == "" {
		return a.listModels(), nil
	}

	// Otherwise, try to switch to the specified model
	err := a.switchModel(args, sess)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Model switched to: %s (provider: %s)", a.model.ID, a.model.Provider), nil
}

// listModels lists all available models from ~/.aiclaw/models.json
func (a *AgentLoop) listModels() string {
	// Try to load models from ~/.aiclaw/models.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Sprintf("Model Info (current):\n  ID: %s\n  Provider: %s\n\nCannot load models list: failed to get home directory",
			a.model.ID, a.model.Provider)
	}

	modelsPath := filepath.Join(homeDir, ".aiclaw", "models.json")
	specs, err := aiconfig.LoadModelSpecs(modelsPath)
	if err != nil {
		return fmt.Sprintf("Model Info (current):\n  ID: %s\n  Provider: %s\n\nCannot load models list: %v\n\nExpected file: %s",
			a.model.ID, a.model.Provider, err, modelsPath)
	}

	if len(specs) == 0 {
		return fmt.Sprintf("Model Info (current):\n  ID: %s\n  Provider: %s\n\nNo models found in %s",
			a.model.ID, a.model.Provider, modelsPath)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Available Models (%d):\n\n", len(specs)))

	// Display with numeric indices for easy selection
	for i, spec := range specs {
		isCurrent := spec.ID == a.model.ID
		prefix := "  "
		if isCurrent {
			prefix = "* "
		}
		displayName := spec.ID
		if spec.Name != "" && spec.Name != spec.ID {
			displayName = fmt.Sprintf("%s (%s)", spec.Name, spec.ID)
		}
		b.WriteString(fmt.Sprintf("%s[%d] %s\n", prefix, i, displayName))
		if isCurrent {
			b.WriteString("       <- current\n")
		}
	}

	b.WriteString("\nUsage: /model <number> to switch (e.g., /model 0)")

	return strings.TrimSpace(b.String())
}

// switchModel switches to the specified model
func (a *AgentLoop) switchModel(modelID string, sess *Session) error {
	modelID = strings.TrimSpace(modelID)

	// Load available models
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	modelsPath := filepath.Join(homeDir, ".aiclaw", "models.json")
	specs, err := aiconfig.LoadModelSpecs(modelsPath)
	if err != nil {
		return fmt.Errorf("failed to load models: %w (path: %s)", err, modelsPath)
	}

	// Try to parse as numeric index first
	var targetSpec ModelSpec
	if idx, parseErr := strconv.Atoi(modelID); parseErr == nil {
		if idx < 0 || idx >= len(specs) {
			return fmt.Errorf("model index out of range: %d\nUse /model to list available models", idx)
		}
		targetSpec = specs[idx]
	} else {
		targetSpec, err = modelselect.SelectByQuery(specs, modelID, func(spec ModelSpec) modelselect.Keys {
			return modelselect.Keys{
				Provider: spec.Provider,
				ID:       spec.ID,
				Name:     spec.Name,
			}
		})
		if err != nil {
			return fmt.Errorf("%w\nUse /model to list available models", err)
		}
	}

	// Resolve API key for the provider
	apiKey, err := a.resolveAPIKey(targetSpec.Provider)
	if err != nil {
		return fmt.Errorf("failed to resolve API key for %s: %w", targetSpec.Provider, err)
	}

	// Create new model
	newModel := llm.Model{
		ID:            targetSpec.ID,
		Provider:      targetSpec.Provider,
		BaseURL:       targetSpec.BaseURL,
		API:           targetSpec.API,
		ContextWindow: targetSpec.ContextWindow,
	}

	// Update the agent loop's model
	a.model = newModel
	a.apiKey = apiKey

	// Create new compactor with the new model
	compactorCfg := compact.DefaultConfig()
	newCompactor := compact.NewCompactor(
		compactorCfg,
		newModel,
		apiKey,
		a.systemPrompt,
		newModel.ContextWindow,
	)
	a.compactor = newCompactor

	// Update all existing sessions with the new model
	a.sessionsMu.Lock()
	for _, s := range a.sessions {
		s.Agent.SetModel(newModel)
		s.Agent.SetAPIKey(apiKey)
		s.Agent.SetCompactor(&clawCompactor{sess: s.Session, compactor: newCompactor})
		s.Agent.SetContextWindow(newModel.ContextWindow)
		s.Compactor.Update(s.Session, newCompactor)
	}
	a.sessionsMu.Unlock()

	// Update config file
	if err := a.saveModelConfig(newModel); err != nil {
		slog.Warn("[AgentLoop] Failed to save model config", "error", err)
	}

	slog.Info("[AgentLoop] Model switched",
		"id", newModel.ID,
		"provider", newModel.Provider,
		"baseUrl", newModel.BaseURL,
		"contextWindow", newModel.ContextWindow)

	return nil
}

// resolveAPIKey resolves API key from environment or auth.json
func (a *AgentLoop) resolveAPIKey(provider string) (string, error) {
	// Try environment variable first
	envVar := strings.ToUpper(provider) + "_API_KEY"
	if key := os.Getenv(envVar); key != "" {
		return key, nil
	}

	// Try auth.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	authPath := filepath.Join(homeDir, ".aiclaw", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return "", fmt.Errorf("failed to read auth.json (set %s env var): %w", envVar, err)
	}

	var auth map[string]map[string]string
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", fmt.Errorf("failed to parse auth.json: %w", err)
	}

	if providerAuth, ok := auth[provider]; ok {
		for _, keyField := range []string{"apiKey", "api_key", "key", "token"} {
			if key, ok := providerAuth[keyField]; ok && key != "" {
				return key, nil
			}
		}
	}

	return "", fmt.Errorf("API key not found for provider %s in auth.json (set %s env var)", provider, envVar)
}

// saveModelConfig saves the current model config to ~/.aiclaw/config.json
func (a *AgentLoop) saveModelConfig(model llm.Model) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(homeDir, ".aiclaw", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	// Parse existing config to preserve other fields
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	// Update model section
	if cfg == nil {
		cfg = make(map[string]any)
	}
	modelCfg := map[string]any{
		"id":       model.ID,
		"provider": model.Provider,
		"baseUrl":  model.BaseURL,
	}
	cfg["model"] = modelCfg

	// Preserve voice and channels if they exist
	if _, ok := cfg["voice"]; !ok && data != nil {
		var voiceCfg map[string]any
		var originalCfg map[string]any
		if err := json.Unmarshal(data, &originalCfg); err == nil {
			if v, ok := originalCfg["voice"]; ok {
				cfg["voice"] = v
			}
		}
		_ = voiceCfg
	}

	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, newData, 0644)
}

// cmdTraceevent manages trace event settings
// Usage:
//
//	/traceevent              - list enabled events
//	/traceevent default      - reset to default set
//	/traceevent on           - enable default working set of events
//	/traceevent all          - enable ALL events (including high-frequency)
//	/traceevent off          - disable all events
//	/traceevent <events>     - set specific events (e.g., llm, tool, event)
//	/traceevent enable <events>   - enable additional events
//	/traceevent disable <events>  - disable specific events
func (a *AgentLoop) cmdTraceevent(args string) string {
	args = strings.TrimSpace(args)

	// Ensure trace handler is initialized when traceevent commands are used
	ensureTraceHandler()

	if args == "" {
		// List enabled events
		events := traceevent.GetEnabledEvents()
		if len(events) == 0 {
			return "Trace events: disabled (use /traceevent on to enable default events, or /traceevent all for all events)"
		}
		return fmt.Sprintf("Trace events (%d): %s", len(events), strings.Join(events, ", "))
	}

	fields := strings.Fields(args)
	op := fields[0]

	switch op {
	case "default":
		events := traceevent.ResetToDefaultEvents()
		return fmt.Sprintf("Reset to default events (%d): %s", len(events), strings.Join(events, ", "))

	case "on":
		// "on" enables the default working set (not all events, to avoid high-frequency noise)
		events := traceevent.ResetToDefaultEvents()
		return fmt.Sprintf("Enabled default events (%d)", len(events))

	case "all":
		// "all" enables ALL known events, including high-frequency ones
		traceevent.DisableAllEvents()
		expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
		for _, eventName := range expanded {
			traceevent.EnableEvent(eventName)
		}
		events := traceevent.GetEnabledEvents()
		return fmt.Sprintf("Enabled all events (%d)", len(events))

	case "off", "none":
		traceevent.DisableAllEvents()
		return "All trace events disabled"

	case "enable":
		if len(fields) < 2 {
			return "Usage: /traceevent enable <events>\nExample: /traceevent enable llm tool"
		}
		selectors := fields[1:]
		expanded, unknown := traceevent.ExpandEventSelectors(selectors)
		if len(unknown) > 0 {
			return fmt.Sprintf("Unknown trace events: %s\nAvailable selectors: all, llm, tool, event, log", strings.Join(unknown, ", "))
		}
		for _, eventName := range expanded {
			traceevent.EnableEvent(eventName)
		}
		events := traceevent.GetEnabledEvents()
		return fmt.Sprintf("Enabled events (%d): %s", len(events), strings.Join(events, ", "))

	case "disable":
		if len(fields) < 2 {
			return "Usage: /traceevent disable <events>\nExample: /traceevent disable tool"
		}
		selectors := fields[1:]
		expanded, unknown := traceevent.ExpandEventSelectors(selectors)
		if len(unknown) > 0 {
			return fmt.Sprintf("Unknown trace events: %s", strings.Join(unknown, ", "))
		}
		for _, eventName := range expanded {
			traceevent.DisableEvent(eventName)
		}
		events := traceevent.GetEnabledEvents()
		if len(events) == 0 {
			return "Disabled all events"
		}
		return fmt.Sprintf("Remaining events (%d): %s", len(events), strings.Join(events, ", "))

	default:
		// Treat as list of events to set (replace current set)
		traceevent.DisableAllEvents()
		expanded, unknown := traceevent.ExpandEventSelectors(fields)
		if len(unknown) > 0 {
			return fmt.Sprintf("Unknown trace events: %s\nAvailable selectors: all, llm, tool, event, log", strings.Join(unknown, ", "))
		}
		for _, eventName := range expanded {
			traceevent.EnableEvent(eventName)
		}
		events := traceevent.GetEnabledEvents()
		return fmt.Sprintf("Set events (%d): %s", len(events), strings.Join(events, ", "))
	}
}

// cmdShow displays settings or usage statistics
// Usage:
//
//	/show              - show settings (default)
//	/show settings     - show current settings
//	/show usage        - show usage statistics
func (a *AgentLoop) cmdShow(args string, sess *Session) string {
	args = strings.TrimSpace(args)

	if args == "" || args == "settings" {
		return a.cmdShowSettings(sess)
	}

	if args == "usage" {
		return a.cmdShowUsage()
	}

	return fmt.Sprintf("Unknown show command: %s\nUsage: /show [settings|usage]", args)
}

// cmdShowSettings shows current configuration settings
func (a *AgentLoop) cmdShowSettings(sess *Session) string {
	var sb strings.Builder

	sb.WriteString("Current Settings:\n")

	// Model info
	sb.WriteString(fmt.Sprintf("  Model: %s (provider: %s)\n", a.model.ID, a.model.Provider))
	if a.model.BaseURL != "" {
		sb.WriteString(fmt.Sprintf("  Base URL: %s\n", a.model.BaseURL))
	}

	// Thinking level
	a.thinkingLevelMu.RLock()
	thinkingLevel := a.thinkingLevel
	a.thinkingLevelMu.RUnlock()
	sb.WriteString(fmt.Sprintf("  Thinking Level: %s\n", thinkingLevel))

	// Session info
	if sess != nil {
		sb.WriteString(fmt.Sprintf("  Session: %s\n", sess.Key))
		if sess.Session != nil {
			msgCount := len(sess.Session.GetMessages())
			sb.WriteString(fmt.Sprintf("  Session Messages: %d\n", msgCount))
		}
	}

	// Skills
	if len(a.skills) > 0 {
		sb.WriteString(fmt.Sprintf("  Skills: %d loaded\n", len(a.skills)))
	}

	// Voice
	if a.transcriber != nil {
		sb.WriteString("  Voice: enabled\n")
	} else {
		sb.WriteString("  Voice: disabled\n")
	}

	// Cron
	if a.cronService != nil {
		sb.WriteString("  Cron: enabled\n")
	}

	return strings.TrimSpace(sb.String())
}

// ensureTraceHandler ensures that a trace handler is initialized if not already set.
// This allows trace events to be captured even when -trace flag was not used at startup.
func ensureTraceHandler() {
	if traceevent.GetHandler() != nil {
		return
	}

	// Initialize trace handler on demand
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("Failed to get home directory for trace handler", "error", err)
		return
	}

	tracesDir := filepath.Join(homeDir, ".aiclaw", "traces")
	handler, err := traceevent.NewFileHandler(tracesDir)
	if err != nil {
		slog.Warn("Failed to create trace handler", "dir", tracesDir, "error", err)
		return
	}

	traceevent.SetHandler(handler)
	slog.Info("Trace handler initialized on demand", "dir", tracesDir)
}

// cmdThinking toggles or sets the thinking level
// Usage:
//
//	/thinking              - toggle to next level
//	/thinking <level>      - set specific level (off, minimal, low, medium, high, xhigh)
func (a *AgentLoop) cmdThinking(args string, sess *Session) string {
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	levelMap := make(map[string]string)
	for _, level := range levels {
		levelMap[level] = level
	}

	args = strings.TrimSpace(args)

	var newLevel string

	if args == "" {
		// Toggle to next level
		a.thinkingLevelMu.RLock()
		currentLevel := a.thinkingLevel
		a.thinkingLevelMu.RUnlock()

		// Find current level index
		currentIndex := 0
		for i, level := range levels {
			if level == currentLevel {
				currentIndex = i
				break
			}
		}

		// Move to next level (wrap around)
		nextIndex := (currentIndex + 1) % len(levels)
		newLevel = levels[nextIndex]
	} else {
		// Set specific level
		if _, ok := levelMap[args]; !ok {
			return fmt.Sprintf("Invalid thinking level: %s\nValid levels: %s", args, strings.Join(levels, ", "))
		}
		newLevel = args
	}

	// Update thinking level
	a.thinkingLevelMu.Lock()
	a.thinkingLevel = newLevel
	a.thinkingLevelMu.Unlock()

	// Update all existing sessions with new thinking level
	a.sessionsMu.RLock()
	for _, sess := range a.sessions {
		if sess.Agent != nil {
			sess.Agent.SetThinkingLevel(newLevel)
		}
	}
	a.sessionsMu.RUnlock()

	return fmt.Sprintf("Thinking level: %s", newLevel)
}
