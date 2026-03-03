// Package adapter provides an AgentLoop implementation that uses ai agent core.
// It consumes messages from picoclaw's MessageBus and processes them with ai's agent.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
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
	model        llm.Model
	apiKey       string
	systemPrompt string
	tools        []agentctx.Tool
	sessionsDir  string // session storage directory
	compactor    *compact.Compactor

	// Voice transcription support
	transcriber voice.Transcriber

	// Feishu configuration for voice file download
	feishuClient    *lark.Client
	feishuAppID     string
	feishuAppSecret string

	// Cron service
	cronService *cron.CronService

	// Command registry
	commands *CommandRegistry
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

// Config 是 AgentLoop 的配置
type Config struct {
	Model        string          // 模型 ID，如 "claude-3-5-sonnet-20241022"
	Provider     string          // 提供商，如 "anthropic"
	APIKey       string          // API 密钥
	APIURL       string          // API URL (可选)
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
}

// NewAgentLoop 创建一个新的 AgentLoop
func NewAgentLoop(cfg *Config, msgBus *bus.MessageBus) *AgentLoop {
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
		commands:        commands,
	}

	// 注册基础命令
	loop.registerBuiltinCommands()

	return loop
}

// ModelSpec 模型规格定义
type ModelSpec struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl"`
	API           string `json:"api"`
	ContextWindow int    `json:"contextWindow"`
}

// resolveModel 从 claw 配置目录加载模型配置
func resolveModel(cfg *Config) llm.Model {
	model := llm.Model{
		ID:       cfg.Model,
		Provider: cfg.Provider,
		BaseURL:  cfg.APIURL,
	}

	// 如果已经有完整配置，直接返回
	if model.Provider != "" && model.BaseURL != "" {
		return model
	}

	// 尝试从 ~/.aiclaw/models.json 加载模型配置
	if cfg.ClawDir == "" {
		return model
	}

	modelsPath := filepath.Join(cfg.ClawDir, "models.json")
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		slog.Warn("[AgentLoop] Failed to read models.json", "error", err, "path", modelsPath)
		return model
	}

	var specs []ModelSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		slog.Warn("[AgentLoop] Failed to parse models.json", "error", err, "path", modelsPath)
		return model
	}

	// 查找匹配的模型
	for _, spec := range specs {
		if spec.ID == cfg.Model {
			if model.Provider == "" {
				model.Provider = spec.Provider
			}
			if model.BaseURL == "" {
				model.BaseURL = spec.BaseURL
			}
			model.API = spec.API
			model.ContextWindow = spec.ContextWindow
			slog.Info("[AgentLoop] Found model in config",
				"id", spec.ID,
				"provider", spec.Provider,
				"baseUrl", spec.BaseURL)
			return model
		}
	}

	slog.Warn("[AgentLoop] Model not found in config, using defaults", "id", cfg.Model)
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
	if err := sess.Agent.Prompt(content); err != nil {
		return "", fmt.Errorf("agent prompt failed: %w", err)
	}

	// 收集响应
	var response strings.Builder
	var assistantMsg *agentctx.AgentMessage

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
			}
		}
		if event.Type == agent.EventAgentEnd {
			break
		}
	}

	// 保存助手消息到 session
	if assistantMsg != nil {
		sess.Session.AppendMessage(*assistantMsg)
	}

	result := response.String()
	slog.Info("[AgentLoop] Response", "session_key", sessionKey, "length", len(result))
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

	// 创建 agent
	ag := agent.NewAgentWithContext(a.model, a.apiKey, agentCtx)

	// 创建并设置 compactor
	clawComp := &clawCompactor{
		sess:      sess,
		compactor: a.compactor,
	}
	ag.SetCompactor(clawComp)
	ag.SetContextWindow(a.model.ContextWindow)

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
	// Register aliases for commands that have multiple names
	a.commands.Register("help", func(args string, sess *Session) (string, error) {
		return a.cmdHelp(), nil
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
	a.commands.Register("sessions", func(args string, sess *Session) (string, error) {
		return a.cmdSessions(), nil
	})
	a.commands.Register("clear", func(args string, sess *Session) (string, error) {
		return a.cmdClear(sess), nil
	})
	a.commands.Register("compact", func(args string, sess *Session) (string, error) {
		return "", a.cmdCompact(sess)
	})
	a.commands.Register("model", func(args string, sess *Session) (string, error) {
		return a.cmdModel(), nil
	})
	a.commands.Register("tools", func(args string, sess *Session) (string, error) {
		return a.cmdTools(), nil
	})
	a.commands.Register("stats", func(args string, sess *Session) (string, error) {
		return a.cmdStats(sess), nil
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
	commands := a.commands.List()
	// Sort commands alphabetically
	sort.Strings(commands)

	var b strings.Builder
	b.WriteString("Control Commands:\n\n")
	for _, cmd := range commands {
		b.WriteString(fmt.Sprintf("  /%s\n", cmd))
	}
	b.WriteString("\nNormal messages (without / prefix) will be sent to the agent.")
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

// cmdSessions 列出所有活跃会话
func (a *AgentLoop) cmdSessions() string {
	sessions := a.ListSessions()
	if len(sessions) == 0 {
		return "No active sessions"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Active Sessions (%d):\n\n", len(sessions)))
	for i, key := range sessions {
		if sess, ok := a.GetSession(key); ok {
			msgCount := len(sess.Session.GetMessages())
			b.WriteString(fmt.Sprintf("[%d] %s (%d messages)\n", i, key, msgCount))
		} else {
			b.WriteString(fmt.Sprintf("[%d] %s\n", i, key))
		}
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
	}

	return fmt.Sprintf("Cleared %d messages from session", msgCount)
}

// cmdCompact 压缩会话
func (a *AgentLoop) cmdCompact(sess *Session) error {
	if sess == nil {
		return fmt.Errorf("no active session")
	}

	// 触发 agent 进行压缩
	// 这需要 agent 暴露压缩方法，暂时返回提示
	return fmt.Errorf("compact command: use normal prompt to trigger compaction when needed")
}

// cmdModel 显示当前模型信息
func (a *AgentLoop) cmdModel() string {
	return fmt.Sprintf(`Model Info:
  ID: %s
  Provider: %s
  BaseURL: %s
  Context Window: %d`,
		a.model.ID,
		a.model.Provider,
		a.model.BaseURL,
		a.model.ContextWindow,
	)
}

// cmdTools 列出可用工具
func (a *AgentLoop) cmdTools() string {
	if len(a.tools) == 0 {
		return "No tools configured"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Available Tools (%d):\n\n", len(a.tools)))
	for i, tool := range a.tools {
		b.WriteString(fmt.Sprintf("[%d] %s\n", i, tool.Name))
		if desc := tool.Description(); desc != "" {
			b.WriteString(fmt.Sprintf("    %s\n", desc))
		}
	}
	return strings.TrimSpace(b.String())
}

// cmdStats 显示会话统计
func (a *AgentLoop) cmdStats(sess *Session) string {
	if sess == nil {
		return "No active session"
	}

	messages := sess.Session.GetMessages()
	summary := sess.Session.GetLastCompactionSummary()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Session Statistics:\n"))
	b.WriteString(fmt.Sprintf("  Total Messages: %d\n", len(messages)))
	if summary != "" {
		b.WriteString(fmt.Sprintf("  Last Compaction: %s\n", truncate(summary, 100)))
	} else {
		b.WriteString("  Last Compaction: never\n")
	}

	// 统计 token 估算
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.ExtractText())
	}
	estimatedTokens := totalChars / 4 // 粗略估算
	b.WriteString(fmt.Sprintf("  Estimated Tokens: ~%d", estimatedTokens))

	return strings.TrimSpace(b.String())
}
