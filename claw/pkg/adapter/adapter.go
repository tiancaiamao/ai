// Package adapter provides an AgentLoop implementation that delegates message
// processing to ai subprocesses via stdin/stdout JSON-RPC.
// It consumes messages from picoclaw's MessageBus and routes them through
// ConnManager to per-session ai subprocesses.
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
			"github.com/tiancaiamao/ai/claw/pkg/voice"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/skill"
)

// CommandHandler is the function signature for handling control commands.
// args: the remaining arguments after the command name.
// Returns the response text and an optional error.
type CommandHandler func(args string) (string, error)

// CommandRegistry stores registered control commands with descriptions.
type CommandRegistry struct {
	commands map[string]CommandHandler
	info     map[string]string // name -> description
	mu       sync.RWMutex
}

// NewCommandRegistry creates a new command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]CommandHandler),
		info:     make(map[string]string),
	}
}

// Register registers a command handler with a description.
func (r *CommandRegistry) Register(name, description string, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = handler
	r.info[name] = description
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

// ListInfo returns all registered commands with descriptions (sorted by name).
func (r *CommandRegistry) ListInfo() []command.CommandInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]command.CommandInfo, 0, len(r.commands))
	for name, desc := range r.info {
		result = append(result, command.CommandInfo{Name: name, Description: desc})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// AgentLoop implements the message processing loop using ai subprocess RPC.
// It is compatible with picoclaw's MessageBus interface.
type AgentLoop struct {
	bus     *bus.MessageBus
	running atomic.Bool

	// RPC connection manager — one ai subprocess per sessionKey
	connManager *ConnManager

	// Configuration
	sessionsDir string // session storage directory (passed to ConnManager)
	clawDir     string // claw config directory (~/.aiclaw)

	// Voice transcription support
	transcriber voice.Transcriber

	// Feishu configuration for voice file download
	feishuClient    *lark.Client
	feishuAppID     string
	feishuAppSecret string

	// Skills
	skills []skill.Skill

	// Command registry — only claw-local commands
	commands *CommandRegistry

	// Statistics
	messageCount atomic.Int64
	startTime    time.Time
}

// AppConfig holds the configuration for creating an AgentLoop.
// After the RPC refactor, claw no longer manages models, sessions,
// or compaction — those are delegated to ai subprocesses.
type AppConfig struct {
	SystemPrompt string // basic identity prompt passed to ai subprocess

	// Paths
	ClawDir string // claw config directory (~/.aiclaw)

	// Voice support
	Transcriber voice.Transcriber

	// Feishu configuration (for downloading voice files)
	FeishuAppID     string
	FeishuAppSecret string

	// Skills (optional)
	Skills []skill.Skill
}

// NewAgentLoop creates a new AgentLoop backed by ConnManager.
func NewAgentLoop(cfg *AppConfig, msgBus *bus.MessageBus) *AgentLoop {
	slog.Info("[AgentLoop] Initializing with RPC mode")

	// Create sessions directory
	sessionsDir := ""
	if cfg.ClawDir != "" {
		sessionsDir = filepath.Join(cfg.ClawDir, "sessions")
		if err := os.MkdirAll(sessionsDir, 0755); err != nil {
			slog.Warn("[AgentLoop] Failed to create sessions dir", "error", err)
		}
	}

	// Create ConnManager — the core of the RPC architecture
	connManager := NewConnManager(sessionsDir, cfg.SystemPrompt)

	// Create Feishu client (for voice file download)
	var feishuClient *lark.Client
	if cfg.FeishuAppID != "" && cfg.FeishuAppSecret != "" {
		feishuClient = lark.NewClient(cfg.FeishuAppID, cfg.FeishuAppSecret)
		slog.Info("[AgentLoop] Feishu client created for voice download")
	}

	// Create command registry
	commands := NewCommandRegistry()

	loop := &AgentLoop{
		bus:             msgBus,
		connManager:     connManager,
		sessionsDir:     sessionsDir,
		clawDir:         cfg.ClawDir,
		transcriber:     cfg.Transcriber,
		feishuClient:    feishuClient,
		feishuAppID:     cfg.FeishuAppID,
		feishuAppSecret: cfg.FeishuAppSecret,
		skills:          cfg.Skills,
		commands:        commands,
		startTime:       time.Now(),
	}

	// Register claw-local commands
	loop.registerBuiltinCommands()

	return loop
}

// Run starts the message processing loop.
func (a *AgentLoop) Run(ctx context.Context) error {
	slog.Info("[AgentLoop] Starting")
	a.running.Store(true)
	defer a.running.Store(false)

	for a.running.Load() {
		select {
		case <-ctx.Done():
			slog.Info("[AgentLoop] Stopped by context")
			return nil
		case msg, ok := <-a.bus.InboundChan():
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

// Stop stops the message processing loop.
func (a *AgentLoop) Stop() {
	a.running.Store(false)
}

// processMessage handles a single inbound message.
// Flow: voice transcription → command check → connManager.Prompt → response
func (a *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
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

	// Handle media files (voice/audio)
	content := msg.Content
	hasVoiceMedia := false

	// 1. Process audio from msg.Media
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

	// 2. Handle Feishu voice messages (content is JSON metadata)
	if msg.Channel == "feishu" && content != "" {
		if transcribed, err := a.handleFeishuVoice(ctx, content, msg.MessageID, msg.ChatID, msg.Metadata); err != nil {
			slog.Warn("[AgentLoop] Failed to handle feishu voice", "error", err)
		} else if transcribed != "" {
			hasVoiceMedia = true
			content = transcribed
		}
	}

	// Skip if voice-only message with no transcription
	if content == "" && hasVoiceMedia {
		slog.Info("[AgentLoop] Skipping voice message (no transcriber available)")
		return "", nil
	}

	// Check for claw-local commands (/ prefix)
	if strings.HasPrefix(content, "/") {
		cmd := strings.TrimSpace(strings.TrimPrefix(content, "/"))
		response, err := a.handleControlCommand(ctx, cmd, sessionKey)
		if err != nil {
			return fmt.Sprintf("Command error: %v", err), nil
		}
		return response, nil
	}

	// Delegate to ai subprocess via ConnManager
	response, err := a.connManager.Prompt(ctx, sessionKey, content)
	if err != nil {
		return "", fmt.Errorf("rpc prompt failed: %w", err)
	}

	slog.Info("[AgentLoop] Response", "session_key", sessionKey, "length", len(response))
	return response, nil
}

// ProcessDirect processes a direct message (e.g., cron trigger) without going through the bus.
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

// ListSessions returns all active session keys (from ConnManager's connection pool).
func (a *AgentLoop) ListSessions() []string {
	return a.connManager.ListConnections()
}

// Close shuts down all ai subprocess connections.
func (a *AgentLoop) Close() {
	a.Stop()
	if err := a.connManager.CloseAll(); err != nil {
		slog.Warn("[AgentLoop] Error closing connections", "error", err)
	}
}

// SwitchModel is a stub — model switching is handled by the ai subprocess via /model command.
func (a *AgentLoop) SwitchModel(modelID string, _ interface{}) error {
	return fmt.Errorf("model switching is handled by ai subprocess; use /model command instead")
}

// RefreshAPIKey is a stub — API key management is handled by ai subprocess.
func (a *AgentLoop) RefreshAPIKey() error {
	return fmt.Errorf("API key management is handled by ai subprocess")
}

// RegisterCommand registers a custom control command.
func (a *AgentLoop) RegisterCommand(name string, handler CommandHandler) {
	a.commands.Register(name, "Custom command: "+name, handler)
}

// registerBuiltinCommands registers the claw-local commands only.
// Commands not listed here are forwarded to the ai subprocess as regular messages.
func (a *AgentLoop) registerBuiltinCommands() {
	a.commands.Register("help", "Show available commands", func(args string) (string, error) {
		return a.cmdHelp(), nil
	})
	a.commands.Register("skills", "List available skills or reload", func(args string) (string, error) {
		if args == "reload" {
			count, warnings, err := a.ReloadSkills()
			if err != nil {
				return fmt.Sprintf("Failed to reload skills: %v", err), nil
			}
			result := fmt.Sprintf("Reloaded %d skills", count)
			if len(warnings) > 0 {
				result += "\nWarnings:\n" + strings.Join(warnings, "\n")
			}
			return result, nil
		}
		return a.cmdCommands(), nil
	})
	a.commands.Register("commands", "List available skills (alias: /skills)", func(args string) (string, error) {
		return a.cmdCommands(), nil
	})
}

// handleControlCommand processes claw-local commands.
// Commands not in the registry are forwarded to the ai subprocess.
func (a *AgentLoop) handleControlCommand(ctx context.Context, cmdLine, sessionKey string) (string, error) {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := fields[0]
	args := strings.TrimSpace(strings.TrimPrefix(cmdLine, cmd))

	// Check claw-local command registry
	handler, ok := a.commands.Get(cmd)
	if ok {
		return handler(args)
	}

	// Not a claw-local command — forward to ai subprocess as-is
	forwardMsg := "/" + cmdLine
	response, err := a.connManager.Prompt(ctx, sessionKey, forwardMsg)
	if err != nil {
		return "", fmt.Errorf("failed to forward command to ai: %w", err)
	}
	return response, nil
}

// cmdHelp shows claw-local commands + note about ai commands.
func (a *AgentLoop) cmdHelp() string {
	infos := a.commands.ListInfo()

	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString("Claw Local Commands:\n\n")
	for _, info := range infos {
		if info.Description != "" {
			b.WriteString(fmt.Sprintf("  /%-15s %s\n", info.Name, info.Description))
		} else {
			b.WriteString(fmt.Sprintf("  /%s\n", info.Name))
		}
	}
	b.WriteString("\nOther commands (e.g., /model, /clear, /history) are\n")
	b.WriteString("forwarded to the ai agent subprocess.\n")
	b.WriteString("```")
	return b.String()
}

// cmdCommands lists available skills.
func (a *AgentLoop) cmdCommands() string {
	if len(a.skills) == 0 {
		return "No skills available"
	}

	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString(fmt.Sprintf("Available Skills (%d):\n\n", len(a.skills)))

	for i, s := range a.skills {
		b.WriteString(fmt.Sprintf("[%d] /%s\n", i, s.Name))
		if s.Description != "" {
			b.WriteString(fmt.Sprintf("    %s\n", s.Description))
		}
	}

	b.WriteString("```")
	return b.String()
}

// ReloadSkills reloads skills from the skills directory.
func (a *AgentLoop) ReloadSkills() (int, []string, error) {
	if a.clawDir == "" {
		return 0, nil, fmt.Errorf("claw directory not configured")
	}

	skillLoader := skill.NewLoader(a.clawDir)
	result := skillLoader.Load(nil)

	var warnings []string
	for _, diag := range result.Diagnostics {
		warnings = append(warnings, fmt.Sprintf("%s: %s", diag.Path, diag.Message))
	}

	a.skills = result.Skills
	slog.Info("[AgentLoop] Skills reloaded", "count", len(result.Skills))
	return len(result.Skills), warnings, nil
}

// GetSkills returns the currently loaded skills.
func (a *AgentLoop) GetSkills() []skill.Skill {
	return a.skills
}

// ---------------------------------------------------------------------------
// Feishu voice handling (unchanged — claw layer responsibility)
// ---------------------------------------------------------------------------

// FeishuVoicePayload represents the JSON structure of a Feishu voice message.
type FeishuVoicePayload struct {
	FileKey  string `json:"file_key"`
	Duration int    `json:"duration"`
}

// handleFeishuVoice processes Feishu voice messages.
// Returns transcribed text, or empty string if not a voice message.
func (a *AgentLoop) handleFeishuVoice(
	ctx context.Context,
	content string,
	inboundMessageID string,
	chatID string,
	metadata map[string]string,
) (string, error) {
	var voicePayload FeishuVoicePayload
	if err := json.Unmarshal([]byte(content), &voicePayload); err != nil {
		return "", nil
	}

	if voicePayload.FileKey == "" {
		return "", nil
	}

	slog.Info("[AgentLoop] Detected feishu voice message", "file_key", voicePayload.FileKey, "duration", voicePayload.Duration)

	if a.transcriber == nil || !a.transcriber.IsAvailable() {
		slog.Warn("[AgentLoop] No transcriber available for feishu voice")
		return "", nil
	}

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

	audioPath, err := a.downloadFeishuAudio(ctx, voicePayload.FileKey, messageID)
	if err != nil {
		slog.Warn("[AgentLoop] Failed to download feishu audio", "error", err, "message_id", messageID)
		return "[Voice message - download failed. Note: Feishu voice requires message_id in metadata.]", nil
	}
	defer os.Remove(audioPath)

	slog.Info("[AgentLoop] Downloaded feishu audio", "path", audioPath)

	result, err := a.transcriber.Transcribe(ctx, audioPath)
	if err != nil {
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
			return "[Voice message - transcription failed: unsupported audio format for current ASR provider]", nil
		}
	}

	slog.Info("[AgentLoop] Feishu voice transcribed", "text_length", len(result.Text), "language", result.Language)
	return "[voice transcription: " + result.Text + "]", nil
}

// downloadFeishuAudio downloads a Feishu audio file to a temporary path.
func (a *AgentLoop) downloadFeishuAudio(ctx context.Context, fileKey string, messageID string) (string, error) {
	if a.feishuAppID == "" || a.feishuAppSecret == "" {
		return "", fmt.Errorf("feishu app_id or app_secret not configured")
	}
	if messageID == "" {
		return "", fmt.Errorf("missing message_id for feishu audio download")
	}

	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "feishu_voice_"+fileKey+".ogg")

	token, err := a.getFeishuAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get feishu access token: %w", err)
	}

	downloadURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/resources/%s?type=file",
		url.PathEscape(messageID), url.PathEscape(fileKey))

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download audio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download audio failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	f, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("write audio file: %w", err)
	}

	return tmpFile, nil
}

// findFeishuAudioMessageID queries Feishu API to find the message ID for a voice file.
func (a *AgentLoop) findFeishuAudioMessageID(ctx context.Context, chatID, fileKey string) (string, error) {
	token, err := a.getFeishuAccessToken(ctx)
	if err != nil {
		return "", err
	}

	listURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?container_id_type=chat&container_id=%s&page_size=20",
		url.PathEscape(chatID))

	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
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
		if item.MessageID == "" || item.MessageType != "audio" {
			continue
		}
		if strings.Contains(item.Content, fileKey) {
			return item.MessageID, nil
		}
	}

	return "", nil
}

// feishuTokenResponse represents the Feishu token API response.
type feishuTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// feishuTokenCache caches Feishu tenant access tokens.
type feishuTokenCache struct {
	token      string
	expireTime time.Time
	mu         sync.Mutex
}

var globalFeishuTokenCache feishuTokenCache

// getFeishuAccessToken retrieves a Feishu tenant access token (with caching).
func (a *AgentLoop) getFeishuAccessToken(ctx context.Context) (string, error) {
	globalFeishuTokenCache.mu.Lock()
	defer globalFeishuTokenCache.mu.Unlock()

	if globalFeishuTokenCache.token != "" && time.Now().Before(globalFeishuTokenCache.expireTime) {
		return globalFeishuTokenCache.token, nil
	}

	apiURL := "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	payload := map[string]string{
		"app_id":     a.feishuAppID,
		"app_secret": a.feishuAppSecret,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(string(payloadBytes)))
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

	globalFeishuTokenCache.token = tokenResp.TenantAccessToken
	globalFeishuTokenCache.expireTime = time.Now().Add(time.Duration(tokenResp.Expire-300) * time.Second)

	return tokenResp.TenantAccessToken, nil
}

// feishuMessageListResponse represents the Feishu message list API response.
type feishuMessageListResponse struct {
	Code int `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []struct {
			MessageID   string `json:"message_id"`
			MessageType string `json:"msg_type"`
			Content     string `json:"body.content"`
		} `json:"items"`
	} `json:"data"`
}

func isUnsupportedAudioFormatError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "\"code\":\"1214\"") ||
		strings.Contains(msg, "不支持当前文件格式") ||
		(strings.Contains(msg, "unsupported") && strings.Contains(msg, "format"))
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

