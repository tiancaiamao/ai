// Package adapter provides an AgentLoop implementation that uses ai agent core.
// It consumes messages from picoclaw's MessageBus and processes them with ai's agent.
package adapter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"log/slog"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
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
}

// Session 表示一个隔离的会话
type Session struct {
	Key     string
	Agent   *agent.Agent
	Context *agentctx.AgentContext
}

// Config 是 AgentLoop 的配置
type Config struct {
	Model        string          // 模型 ID，如 "claude-3-5-sonnet-20241022"
	Provider     string          // 提供商，如 "anthropic"
	APIKey       string          // API 密钥
	APIURL       string          // API URL (可选)
	SystemPrompt string          // 系统提示词
	Tools        []agentctx.Tool // 工具列表
}

// NewAgentLoop 创建一个新的 AgentLoop
// 使用 agent core 的配置系统加载模型配置 (~/.ai/models.json)
func NewAgentLoop(cfg *Config, msgBus *bus.MessageBus) *AgentLoop {
	model := resolveModel(cfg)

	slog.Info("[AgentLoop] Model resolved",
		"id", model.ID,
		"provider", model.Provider,
		"baseUrl", model.BaseURL)

	return &AgentLoop{
		bus:          msgBus,
		sessions:     make(map[string]*Session),
		model:        model,
		apiKey:       cfg.APIKey,
		systemPrompt: cfg.SystemPrompt,
		tools:        cfg.Tools,
	}
}

// resolveModel 从 agent core 配置中解析模型
func resolveModel(cfg *Config) llm.Model {
	model := llm.Model{
		ID:       cfg.Model,
		Provider: cfg.Provider,
		BaseURL:  cfg.APIURL,
	}

	// 尝试从 ~/.ai/models.json 加载模型配置
	modelsPath, err := config.ResolveModelsPath()
	if err != nil {
		slog.Warn("[AgentLoop] Failed to resolve models path", "error", err)
		return model
	}

	specs, err := config.LoadModelSpecs(modelsPath)
	if err != nil {
		slog.Warn("[AgentLoop] Failed to load model specs", "error", err, "path", modelsPath)
		return model
	}

	// 查找匹配的模型
	for _, spec := range specs {
		if spec.ID == cfg.Model {
			model.Provider = spec.Provider
			model.BaseURL = spec.BaseURL
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
	session, err := a.getOrCreateSession(sessionKey)
	if err != nil {
		return "", fmt.Errorf("failed to get/create session: %w", err)
	}

	// 发送消息给 agent
	if err := session.Agent.Prompt(msg.Content); err != nil {
		return "", fmt.Errorf("agent prompt failed: %w", err)
	}

	// 收集响应
	var response strings.Builder
	for event := range session.Agent.Events() {
		switch event.Type {
		case agent.EventTurnEnd:
			if event.Message != nil {
				response.WriteString(event.Message.ExtractText())
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

	result := response.String()
	slog.Info("[AgentLoop] Response", "session_key", sessionKey, "length", len(result))
	return result, nil
}

// getOrCreateSession 获取或创建会话
func (a *AgentLoop) getOrCreateSession(sessionKey string) (*Session, error) {
	a.sessionsMu.RLock()
	session, exists := a.sessions[sessionKey]
	a.sessionsMu.RUnlock()

	if exists {
		return session, nil
	}

	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	// 再次检查
	if session, exists := a.sessions[sessionKey]; exists {
		return session, nil
	}

	// 创建新会话
	session, err := a.createSession(sessionKey)
	if err != nil {
		return nil, err
	}

	a.sessions[sessionKey] = session
	return session, nil
}

// createSession 创建新会话
func (a *AgentLoop) createSession(sessionKey string) (*Session, error) {
	// 创建 agent context（不使用 working memory）
	agentCtx := agentctx.NewAgentContext(a.systemPrompt)

	// 创建 agent
	ag := agent.NewAgentWithContext(a.model, a.apiKey, agentCtx)

	// 注册工具
	for _, tool := range a.tools {
		ag.AddTool(tool)
	}

	return &Session{
		Key:     sessionKey,
		Agent:   ag,
		Context: agentCtx,
	}, nil
}

// GetSession 获取会话
func (a *AgentLoop) GetSession(sessionKey string) (*Session, bool) {
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	session, ok := a.sessions[sessionKey]
	return session, ok
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

	if session, ok := a.sessions[sessionKey]; ok {
		session.Agent.Shutdown()
		delete(a.sessions, sessionKey)
	}
}

// Close 关闭所有会话
func (a *AgentLoop) Close() {
	a.Stop()

	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	for _, session := range a.sessions {
		session.Agent.Shutdown()
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
