// Package adapter provides an AgentLoop implementation that uses ai agent core.
// It consumes messages from picoclaw's MessageBus and processes them with ai's agent.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"log/slog"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
)

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
	sessionsDir  string // session 存储目录
	compactor    *compact.Compactor
}

// Session 表示一个隔离的会话
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

	return &AgentLoop{
		bus:          msgBus,
		sessions:     make(map[string]*Session),
		model:        model,
		apiKey:       cfg.APIKey,
		systemPrompt: cfg.SystemPrompt,
		tools:        cfg.Tools,
		sessionsDir:  sessionsDir,
		compactor:    compactor,
	}
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
		"content_preview", truncate(msg.Content, 80))

	// 获取或创建会话
	sess, err := a.getOrCreateSession(sessionKey)
	if err != nil {
		return "", fmt.Errorf("failed to get/create session: %w", err)
	}

	// 保存用户消息到 session
	userMsg := agentctx.NewUserMessage(msg.Content)
	sess.Session.AppendMessage(userMsg)

	// 发送消息给 agent
	if err := sess.Agent.Prompt(msg.Content); err != nil {
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
