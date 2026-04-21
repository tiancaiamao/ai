// Package web provides a web server for claw that integrates with PicoClaw's web backend.
// It serves the embedded web UI and provides WebSocket chat functionality.
package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels/weixin"
	"github.com/tiancaiamao/ai/claw/pkg/adapter"
	aiconfig "github.com/tiancaiamao/ai/pkg/config"
	"gopkg.in/yaml.v3"
	"rsc.io/qr"
)

const (
	weixinFlowTTL   = 5 * time.Minute
	weixinFlowGCAge = 30 * time.Minute
	weixinBaseURL   = "https://ilinkai.weixin.qq.com/"
	weixinBotType   = "3"
)

const (
	weixinStatusWait      = "wait"
	weixinStatusScanned   = "scaned"
	weixinStatusConfirmed = "confirmed"
	weixinStatusExpired   = "expired"
	weixinStatusError     = "error"
)

// weixinFlow represents a WeChat QR login flow session.
type weixinFlow struct {
	ID        string
	Qrcode    string // qrcode token from WeChat API (used for status polling)
	QRDataURI string // base64 PNG data URI for display
	AccountID string // IlinkBotID returned on confirmed
	Status    string // wait / scaned / confirmed / expired / error
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
}

type weixinFlowResponse struct {
	FlowID    string `json:"flow_id"`
	Status    string `json:"status"`
	QRDataURI string `json:"qr_data_uri,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Server is the web server that serves the claw web UI.
type Server struct {
	addr         string
	agentLoop    *adapter.AgentLoop
	msgBus       *bus.MessageBus
	httpServer   *http.Server
	configPath   string
	securityPath string
	clawDir      string
	shutdownOnce sync.Once
	// Weixin flow state
	weixinMu     sync.RWMutex
	weixinFlows  map[string]*weixinFlow
}

// Config is the configuration for the web server.
type Config struct {
	Port    int    // Port to listen on (default: 18800)
	Public  bool   // Listen on all interfaces instead of localhost only
	Enabled bool   // Whether the web server is enabled
}

// DefaultConfig returns the default web server configuration.
func DefaultConfig() *Config {
	return &Config{
		Port:    18800,
		Public:  false,
		Enabled: true,
	}
}

// NewServer creates a new web server.
func NewServer(cfg *Config, agentLoop *adapter.AgentLoop, msgBus *bus.MessageBus, configPath, securityPath, clawDir string) *Server {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Determine listen address
	var addr string
	if cfg.Public {
		addr = fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	} else {
		addr = fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	}

	return &Server{
		addr:         addr,
		agentLoop:    agentLoop,
		msgBus:       msgBus,
		configPath:   configPath,
		securityPath: securityPath,
		clawDir:      clawDir,
		weixinFlows:  make(map[string]*weixinFlow),
	}
}

// Start starts the web server in a background goroutine.
// Returns the URL of the web server and any error that occurred during startup.
func (s *Server) Start() (string, error) {
	if s == nil {
		return "", fmt.Errorf("web server is not configured")
	}

	mux := http.NewServeMux()

	// Register API routes
	s.registerRoutes(mux)

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		fmt.Printf("\n")
		fmt.Printf("╔═══════════════════════════════════════════════════════════════╗\n")
		fmt.Printf("║  🌐 Claw Web Server                                         ║\n")
		fmt.Printf("╠═══════════════════════════════════════════════════════════════╣\n")
		fmt.Printf("║  Web UI: http://localhost:%d                              ║\n", 18800)
		fmt.Printf("║                                                           ║\n")
		fmt.Printf("║  Press Ctrl+C to stop the server                          ║\n")
		fmt.Printf("╚═══════════════════════════════════════════════════════════════╝\n")
		fmt.Printf("\n")

		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[Web Server] Error: %v\n", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	return fmt.Sprintf("http://localhost:%d", 18800), nil
}

// Stop stops the web server.
func (s *Server) Stop() error {
	if s.httpServer == nil {
		return nil
	}

	var err error
	s.shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = s.httpServer.Shutdown(ctx)
	})

	return err
}

// registerRoutes registers all HTTP routes.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// CORS preflight
	mux.HandleFunc("OPTIONS /api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.WriteHeader(http.StatusNoContent)
	})

	// Pico Channel WebSocket endpoint (for web UI chat)
	mux.HandleFunc("/pico/ws", s.handlePicoWebSocket)

	// Session API
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("GET /api/sessions/", s.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/", s.handleDeleteSession)

	// Pico token API
	mux.HandleFunc("GET /api/pico/token", s.handleGetPicoToken)
	mux.HandleFunc("POST /api/pico/token", s.handleRegenPicoToken)
	mux.HandleFunc("POST /api/pico/setup", s.handlePicoSetup)

	// Status API
	mux.HandleFunc("GET /api/status", s.handleStatus)

	// Gateway API (compatibility with PicoClaw frontend)
	mux.HandleFunc("GET /api/gateway/status", s.handleGatewayStatus)
	mux.HandleFunc("POST /api/gateway/start", s.handleGatewayStart)
	mux.HandleFunc("POST /api/gateway/stop", s.handleGatewayStop)
	mux.HandleFunc("POST /api/gateway/restart", s.handleGatewayRestart)
	mux.HandleFunc("GET /api/gateway/logs", s.handleGatewayLogs)

	// Models API
	mux.HandleFunc("GET /api/models", s.handleListModels)
	mux.HandleFunc("POST /api/models", s.handleAddModel)
	mux.HandleFunc("PUT /api/models/", s.handleUpdateModel)
	mux.HandleFunc("DELETE /api/models/", s.handleDeleteModel)
	mux.HandleFunc("POST /api/models/default", s.handleSetDefaultModel)

	// Config API
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateConfig)
	mux.HandleFunc("PATCH /api/config", s.handlePatchConfig)

	// Channels API
	mux.HandleFunc("GET /api/channels/catalog", s.handleChannelsCatalog)
	mux.HandleFunc("GET /api/channels", s.handleListChannels)
	mux.HandleFunc("GET /api/channels/", s.handleGetChannel)
	mux.HandleFunc("PUT /api/channels/", s.handleUpdateChannel)

	// WeChat/Weixin flow API (not fully supported in claw)
	mux.HandleFunc("POST /api/weixin/flows", s.handleWeixinFlow)
	mux.HandleFunc("GET /api/weixin/flows/", s.handleWeixinFlowPoll)

	// Serve static files (frontend) - this will be handled by embedded files
	// For now, return a simple message
	mux.HandleFunc("/", s.handleIndex)
}

// handleIndex serves a simple index page with connection info.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
	<title>Claw Web UI</title>
	<style>
		body { font-family: system-ui, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
		.card { background: #f5f5f5; padding: 20px; border-radius: 8px; margin-bottom: 20px; }
		.code { background: #27272a; color: #fafafa; padding: 15px; border-radius: 4px; font-family: monospace; }
		button { background: #22c55e; color: white; border: none; padding: 10px 20px; border-radius: 4px; cursor: pointer; }
		button:hover { background: #16a34a; }
	</style>
</head>
<body>
	<h1>🦞 Claw Web Server</h1>

	<div class="card">
		<h2>📡 WebSocket Connection</h2>
		<p>Connect to the claw agent via WebSocket:</p>
		<div class="code">ws://localhost:18800/pico/ws?token=<your-token></div>
	</div>

	<div class="card">
		<h2>🔑 Get Your Token</h2>
		<p>Get your authentication token:</p>
		<div class="code">curl http://localhost:18800/api/pico/token</div>
		<button onclick="copyToken()">Copy Token Command</button>
	</div>

	<div class="card">
		<h2>📚 API Endpoints</h2>
		<ul>
		<li><code>GET /api/pico/token</code> - Get WebSocket token</li>
		<li><code>POST /api/pico/token</code> - Regenerate token</li>
		<li><code>GET /api/sessions</code> - List sessions</li>
		<li><code>GET /api/sessions/{id}</code> - Get session details</li>
		<li><code>DELETE /api/sessions/{id}</code> - Delete session</li>
		<li><code>GET /api/status</code> - Get server status</li>
	</ul>
	</div>

	<script>
	function copyToken() {
			navigator.clipboard.writeText('curl http://localhost:18800/api/pico/token');
			alert('Command copied to clipboard!');
		}
	</script>
</body>
</html>`)
}

// handlePicoWebSocket handles WebSocket connections from the web UI.
func (s *Server) handlePicoWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract token from WebSocket subprotocol (format: "token.{actual_token}")
	var token string
	subprotocols := websocket.Subprotocols(r)
	for _, sp := range subprotocols {
		if strings.HasPrefix(sp, "token.") {
			token = strings.TrimPrefix(sp, "token.")
			break
		}
	}

	// Fallback: try query parameter
	if token == "" {
		token = r.URL.Query().Get("token")
	}

	if token == "" {
		http.Error(w, "Missing token", http.StatusUnauthorized)
		return
	}

	// TODO: Validate token against config
	// For now, we'll accept any non-empty token

	// Upgrade to WebSocket
	upgrader := &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for development
		},
		Subprotocols: subprotocols, // Echo back the subprotocols
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[Web Server] WebSocket upgrade failed: %v\n", err)
		return
	}

	fmt.Printf("[Web Server] New WebSocket connection from %s (token: %s)\n", r.RemoteAddr, token)

	// Handle WebSocket connection
	go s.handleWebSocketConnection(conn, token)
}

// handleWebSocketConnection handles the WebSocket connection after upgrade.
func (s *Server) handleWebSocketConnection(conn *websocket.Conn, token string) {
	defer conn.Close()
	fmt.Printf("[Web Server] handleWebSocketConnection START, token=%s\n", token)

	// Generate a session ID for this connection
	sessionID := generateSessionID()
	fmt.Printf("[Web Server] Generated session_id=%s\n", sessionID)

	// Send welcome message (Pico protocol format)
	if err := conn.WriteJSON(map[string]any{
		"type":    "connected",
		"payload": map[string]any{
			"message":    "Connected to Claw Web Server",
			"session_id": sessionID,
		},
	}); err != nil {
		fmt.Printf("[Web Server] Error sending welcome message: %v\n", err)
		return
	}
	fmt.Printf("[Web Server] Welcome message sent\n")

	// Read messages from WebSocket
	msgCount := 0
	for {
		msgCount++
		fmt.Printf("[Web Server] Waiting for message #%d...\n", msgCount)

		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			fmt.Printf("[Web Server] WebSocket read error: %v\n", err)
			break
		}

		// Debug: log all received messages
		msgJSON, _ := json.Marshal(msg)
		fmt.Printf("[Web Server] Received message: %s\n", string(msgJSON))

		// Handle different message types
		msgType, _ := msg["type"].(string)
		msgID, _ := msg["id"].(string)
		switch msgType {
		case "ping":
			pong := map[string]any{
				"type": "pong",
			}
			if msgID != "" {
				pong["id"] = msgID
			}
			conn.WriteJSON(pong)

		case "message", "message.send":
			// Extract content from different message formats
			var content string

			if msgType == "message.send" {
				// PicoClaw frontend format: { type: "message.send", payload: { content: "..." } }
				if payload, ok := msg["payload"].(map[string]any); ok {
					content, _ = payload["content"].(string)
				}
			} else {
				// Simple format: { type: "message", content: "..." }
				content, _ = msg["content"].(string)
			}

			if content == "" {
				fmt.Printf("[Web Server] Warning: empty content from message type %s\n", msgType)
				continue
			}

			sessionKey := fmt.Sprintf("pico:web:%s", sessionID)

			fmt.Printf("[Web Server] Processing message from session %s: %s\n", sessionKey, content)

			// Send typing start indicator
			conn.WriteJSON(map[string]any{
				"type": "typing.start",
			})

			// Process message through AgentLoop in background
			go func() {
				ctx := context.Background()
				response, err := s.agentLoop.ProcessDirect(ctx, content, sessionKey)

				// Stop typing indicator
				conn.WriteJSON(map[string]any{
					"type": "typing.stop",
				})

				if err != nil {
					fmt.Printf("[Web Server] Error processing message: %v\n", err)
					conn.WriteJSON(map[string]any{
						"type":    "error",
						"payload": map[string]any{
							"code":    "processing_error",
							"message": fmt.Sprintf("Error: %v", err),
						},
					})
					return
				}

				fmt.Printf("[Web Server] Sending response: %d chars\n", len(response))

				// Send response as message.create (Pico protocol format)
				if err := conn.WriteJSON(map[string]any{
					"type":    "message.create",
					"payload": map[string]any{
						"content": response,
					},
				}); err != nil {
					fmt.Printf("[Web Server] Error sending response: %v\n", err)
				}
			}()

		case "session_resume":
			// Resume existing session
			requestedSessionID, _ := msg["session_id"].(string)
			if requestedSessionID != "" {
				sessionID = requestedSessionID
				conn.WriteJSON(map[string]any{
					"type":    "session_resumed",
					"session_id": sessionID,
				})
			}
		}
	}

	fmt.Printf("[Web Server] WebSocket connection closed\n")
}

// handleListSessions lists all sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get sessions from AgentLoop
	sessionKeys := s.agentLoop.ListSessions()

	// Convert to JSON format
	result := make([]map[string]any, 0)
	for _, key := range sessionKeys {
		result = append(result, map[string]any{
			"id":   key,
			"key":  key,
		})
	}

	json.NewEncoder(w).Encode(result)
}

// handleGetSession gets a specific session.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Extract session ID from path
	// URL format: /api/sessions/{id}
	// We need to parse this
	http.Error(w, "Not implemented yet", http.StatusNotImplemented)
}

// handleDeleteSession deletes a session.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	http.Error(w, "Not implemented yet", http.StatusNotImplemented)
}

// handleGetPicoToken returns the current Pico token.
func (s *Server) handleGetPicoToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// TODO: Read actual token from config
	token := "claw-dev-token-" + generateSessionID()[:8]

	json.NewEncoder(w).Encode(map[string]any{
		"token":   token,
		"ws_url":  fmt.Sprintf("ws://localhost:18800/pico/ws"),
		"enabled": true,
	})
}

// handleRegenPicoToken regenerates the Pico token.
func (s *Server) handleRegenPicoToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// TODO: Save new token to config
	token := "claw-dev-token-" + generateSessionID()[:8]

	json.NewEncoder(w).Encode(map[string]any{
		"token":  token,
		"ws_url": fmt.Sprintf("ws://localhost:18800/pico/ws"),
	})
}

// handlePicoSetup ensures Pico channel is configured.
func (s *Server) handlePicoSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// TODO: Setup Pico channel in config
	token := "claw-dev-token-" + generateSessionID()[:8]

	json.NewEncoder(w).Encode(map[string]any{
		"token":   token,
		"ws_url":  fmt.Sprintf("ws://localhost:18800/pico/ws"),
		"enabled": true,
		"changed": true,
	})
}

// handleStatus returns the server status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "running",
		"version": "1.0.0",
		"name":    "Claw Web Server",
	})
}

// Gateway API handlers (for PicoClaw frontend compatibility)

// handleGatewayStatus returns the gateway status.
func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("[PANIC] handleGatewayStatus: %v\n", err)
			http.Error(w, fmt.Sprintf("Internal error: %v", err), 500)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

		// Read default model name from agents.defaults.model_name (picoclaw format)
	currentModelName := ""
	if configData, err := os.ReadFile(s.configPath); err == nil {
		var config map[string]any
		if json.Unmarshal(configData, &config) == nil {
			if agents, ok := config["agents"].(map[string]any); ok {
				if defaults, ok := agents["defaults"].(map[string]any); ok {
					if mn, ok := defaults["model_name"].(string); ok {
						currentModelName = mn
					}
				}
			}
		}
	}

	response := map[string]any{
		"gateway_status":           "running",
		"gateway_start_allowed":    false,
		"gateway_restart_required": false,
		"pid":                      os.Getpid(),
		"boot_default_model":       currentModelName,
		"config_default_model":     currentModelName,
	}

	json.NewEncoder(w).Encode(response)
}

// handleGatewayStart handles gateway start requests (no-op for claw).
func (s *Server) handleGatewayStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Claw is already running",
	})
}

// handleGatewayStop handles gateway stop requests (no-op for claw).
func (s *Server) handleGatewayStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Claw cannot be stopped from API",
	})
}

// handleGatewayRestart handles gateway restart requests (no-op for claw).
func (s *Server) handleGatewayRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Claw restart not supported",
	})
}

// handleGatewayLogs returns gateway logs.
func (s *Server) handleGatewayLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"logs":     []string{},
		"position": 0,
	})
}

// Models API handlers

// handleListModels returns the available models.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Read config file
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse config
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

		// Get default model name from agents.defaults.model_name (picoclaw format)
	defaultModelName := ""
	if agents, ok := config["agents"].(map[string]any); ok {
		if defaults, ok := agents["defaults"].(map[string]any); ok {
			if mn, ok := defaults["model_name"].(string); ok {
				defaultModelName = mn
			}
		}
	}

	// Try to use model_list from config first (for picoclaw frontend compatibility)
		// Load auth.json for masked API key display
	authKeys := s.loadAuthKeys()

	modelList := []map[string]any{}
	hasModelList := false

	if models, ok := config["model_list"].([]any); ok && len(models) > 0 {
		hasModelList = true
		for i, m := range models {
			if modelMap, ok := m.(map[string]any); ok {
				modelName, _ := modelMap["model_name"].(string)

				// Derive provider for API key lookup
				provider, _ := modelMap["provider"].(string)
				if provider == "" {
					provider = deriveProvider(modelMap)
				}

				modelInfo := map[string]any{
					"index":       i,
					"model_name":  modelMap["model_name"],
					"model":       modelMap["model"],
					"api_base":    modelMap["api_base"],
					"provider":    provider,
					"configured":  true,
					"is_default":  false,
					"is_virtual":  false,
				}

				// Add masked API key if available
				if key, ok := authKeys[provider]; ok && key != "" {
					modelInfo["api_key"] = maskAPIKey(key)
				} else {
					modelInfo["api_key"] = ""
				}

				// Check if this is the default model by model_name
				if modelName == defaultModelName {
					modelInfo["is_default"] = true
				}

				modelList = append(modelList, modelInfo)
			}
		}
	}

	// If no model_list in config, load from models.json
	if !hasModelList {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			modelsPath := filepath.Join(homeDir, ".aiclaw", "models.json")
			specs, err := aiconfig.LoadModelSpecs(modelsPath)
			if err == nil {
				for i, spec := range specs {
					displayName := spec.ID
					if spec.Name != "" && spec.Name != spec.ID {
						displayName = spec.Name
					}

					modelInfo := map[string]any{
						"index":       i,
						"model_name":  displayName,
						"model":       spec.ID,
						"api_base":    spec.BaseURL,
						"provider":    spec.Provider,
						"configured":  true,
						"is_default":  false,
						"is_virtual":  false,
					}

					// Add masked API key if available
					if key, ok := authKeys[spec.Provider]; ok && key != "" {
						modelInfo["api_key"] = maskAPIKey(key)
					} else {
						modelInfo["api_key"] = ""
					}

					// Check if this is the default model by model_name
					if displayName == defaultModelName {
						modelInfo["is_default"] = true
					}

					modelList = append(modelList, modelInfo)
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"models":        modelList,
		"total":         len(modelList),
		"default_model": defaultModelName,
	})
}

// handleSetDefaultModel sets the default model.
func (s *Server) handleSetDefaultModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Parse request body
	var req struct {
		ModelName string `json:"model_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.ModelName == "" {
		http.Error(w, "model_name is required", http.StatusBadRequest)
		return
	}

	// Use AgentLoop's SwitchModel method to dynamically switch the model
	// This supports both numeric index and model name
	if err := s.agentLoop.SwitchModel(req.ModelName, nil); err != nil {
		http.Error(w, fmt.Sprintf("Failed to switch model: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"message":       fmt.Sprintf("Model switched to '%s'", req.ModelName),
		"default_model": req.ModelName,
	})
}

// handleAddModel adds a new model (not supported for claw).
func (s *Server) handleAddModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "error",
		"message": "Model management through UI not supported. Configure model in ~/.aiclaw/config.json",
	})
}

// handleUpdateModel updates a model entry in model_list and saves api_key to auth.json.
// PUT /api/models/{index}
func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract index from path: /api/models/{index}
	pathIndex := strings.TrimPrefix(r.URL.Path, "/api/models/")
	idx, err := strconv.Atoi(pathIndex)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": "Invalid model index"})
		return
	}

	// Parse request body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": "Failed to read request body"})
		return
	}
	defer r.Body.Close()

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}

	// Read current config
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Failed to read config: %v", err)})
		return
	}

	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Failed to parse config: %v", err)})
		return
	}

	// Get model_list
	modelList, ok := config["model_list"].([]any)
	if !ok || idx < 0 || idx >= len(modelList) {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Model index %d out of range", idx)})
		return
	}

	modelEntry, ok := modelList[idx].(map[string]any)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": "Invalid model entry"})
		return
	}

	// Extract api_key from request
	newAPIKey, _ := req["api_key"].(string)

	// Update model fields in config.json (except api_key, which goes to auth.json)
	updatableFields := []string{"model_name", "model", "api_base", "proxy", "auth_method",
		"connect_mode", "workspace", "rpm", "max_tokens_field", "request_timeout",
		"thinking_level", "extra_body", "provider"}
	for _, field := range updatableFields {
		if val, exists := req[field]; exists {
			modelEntry[field] = val
		}
	}

	// Save api_key to auth.json if provided
	if newAPIKey != "" {
		provider, _ := modelEntry["provider"].(string)
		if provider == "" {
			provider = s.deriveProviderFromModel(modelEntry)
		}
		if provider != "" {
			if err := s.saveAPIKeyToAuth(provider, newAPIKey); err != nil {
				slog.Warn("Failed to save API key to auth.json", "provider", provider, "error", err)
			} else {
				slog.Info("API key saved to auth.json", "provider", provider, "model_index", idx)
				// Refresh AgentLoop's cached API key so next message uses the new key
				if err := s.agentLoop.RefreshAPIKey(); err != nil {
					slog.Warn("Failed to refresh agent API key", "error", err)
				}
			}
		}
	}

	// Save updated config
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Failed to marshal config: %v", err)})
		return
	}
	if err := os.WriteFile(s.configPath, updatedData, 0644); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": fmt.Sprintf("Failed to save config: %v", err)})
		return
	}

		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
}

// handleDeleteModel deletes a model (not supported for claw).
func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Extract model index from path
	_ = r.URL.Path[len("/api/models/"):]

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "error",
		"message": "Model deletion through UI not supported. Configure model in ~/.aiclaw/config.json",
	})
}

// generateSessionID generates a unique session ID.
func generateSessionID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// formatJSON formats a value as JSON for logging.
func formatJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// RunAsStandalone runs the web server in standalone mode (for testing).
func RunAsStandalone(port int, public bool) error {
	cfg := &Config{
		Port:   port,
		Public: public,
		Enabled: true,
	}

	srv := NewServer(cfg, nil, nil, "", "", "")
	url, err := srv.Start()
	if err != nil {
		return err
	}

	fmt.Printf("Web server started at %s\n", url)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down web server...")
	return srv.Stop()
}

// handleGetConfig returns the current configuration.
// Returns the full config.json content with sensitive fields redacted.
// Secret placeholders are injected from .security.yml so the frontend
// knows which fields have been configured.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Read config file
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse config
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Inject ***REDACTED*** placeholders from .security.yml so frontend knows secrets exist
	s.injectChannelSecrets(config)

	// Filter other sensitive data (API keys, passwords, etc.)
	filterSensitiveFields(config)

	// Return raw config object directly (picoclaw frontend compatibility)
	json.NewEncoder(w).Encode(config)
}

// handleUpdateConfig updates the configuration and restarts the service.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Parse request body
	var req struct {
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Read current config to validate merge
	currentData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read current config: %v", err), http.StatusInternalServerError)
		return
	}

	var currentConfig map[string]any
	if err := json.Unmarshal(currentData, &currentConfig); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse current config: %v", err), http.StatusInternalServerError)
		return
	}

	// Merge configs (simple overwrite for now)
	// In production, you'd want more sophisticated merge logic
	mergedConfig := mergeConfigs(currentConfig, req.Config)

	// Validate merged config
	if err := validateConfig(mergedConfig); err != nil {
		http.Error(w, fmt.Sprintf("Config validation failed: %v", err), http.StatusBadRequest)
		return
	}

	// Write updated config
	updatedData, err := json.MarshalIndent(mergedConfig, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(s.configPath, updatedData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

// Note: Service restart is required for config changes to take effect
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"message": "Configuration updated. Service restart is required for changes to take effect.",
		"config_path": s.configPath,
	})
}

// handlePatchConfig partially updates the configuration using JSON Merge Patch (RFC 7396).
// Only the fields present in the request body will be updated; all other fields remain unchanged.
// Sensitive channel fields (app_secret, token, etc.) are extracted and saved to .security.yml.
//
//	PATCH /api/config
func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Parse patch body
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Read current config
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Recursively merge patch into config (JSON Merge Patch semantics)
	slog.Info("PATCH config", "patch", patch)
	mergeMap(config, patch)

	// Debug: show merged feishu config
	if ch, ok := config["channels"].(map[string]any); ok {
		if fs, ok := ch["feishu"].(map[string]any); ok {
			slog.Info("PATCH merged feishu", "fields", fs)
		}
	}

	// Extract sensitive fields from channel configs and save to .security.yml
	channelSecrets := extractChannelSecrets(config)
	slog.Info("PATCH extracted secrets", "channels", channelSecrets, "count", len(channelSecrets))
	if len(channelSecrets) > 0 {
		slog.Info("PATCH updating security.yml", "path", s.securityPath)
		if err := s.updateSecurityYaml(channelSecrets); err != nil {
			slog.Warn("Failed to update security.yml", "error", err)
		} else {
			slog.Info("PATCH security.yml updated successfully")
		}
	}

	// Write updated config (secrets already removed by extractChannelSecrets)
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(s.configPath, updatedData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"message": "Configuration updated. Service restart is required for changes to take effect.",
	})
}

// mergeMap recursively merges src into dst (JSON Merge Patch semantics).
func mergeMap(dst, src map[string]any) {
	for key, srcVal := range src {
		if srcVal == nil {
			delete(dst, key)
			continue
		}
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeMap(dstMap, srcMap)
		} else {
			dst[key] = srcVal
		}
	}
}

// Sensitive field names that should be stored in .security.yml instead of config.json
var channelSecretFields = []string{
	"app_secret", "encrypt_key", "verification_token",
	"token", "client_secret", "corp_secret", "channel_secret",
	"channel_access_token", "access_token", "bot_token", "app_token",
	"encoding_aes_key", "password", "nickserv_password", "sasl_password",
}

// loadSecurityYaml reads and parses .security.yml into a generic map structure.
func (s *Server) loadSecurityYaml() map[string]any {
	result := make(map[string]any)
	if s.securityPath == "" {
		return result
	}
	data, err := os.ReadFile(s.securityPath)
	if err != nil {
		return result
	}
	var sec map[string]any
	if err := yaml.Unmarshal(data, &sec); err != nil {
		return result
	}
	return sec
}

// saveSecurityYaml writes the security map back to .security.yml.
func (s *Server) saveSecurityYaml(sec map[string]any) error {
	if s.securityPath == "" {
		return nil
	}
	data, err := yaml.Marshal(sec)
	if err != nil {
		return err
	}
	return os.WriteFile(s.securityPath, data, 0644)
}

// extractChannelSecrets extracts sensitive fields from channel configs,
// removes them from the config map, and returns them organized by channel name.
// e.g., {"feishu": {"app_secret": "xxx"}, "telegram": {"token": "xxx"}}
func extractChannelSecrets(config map[string]any) map[string]map[string]any {
	secrets := make(map[string]map[string]any)
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		return secrets
	}
	secretSet := make(map[string]bool, len(channelSecretFields))
	for _, f := range channelSecretFields {
		secretSet[f] = true
	}
	for chName, chVal := range channels {
		chMap, ok := chVal.(map[string]any)
		if !ok {
			continue
		}
		for _, fieldName := range channelSecretFields {
			val, exists := chMap[fieldName]
			if !exists {
				continue
			}
			strVal, ok := val.(string)
			if !ok || strVal == "" || strVal == "***REDACTED***" {
				// Don't extract empty or placeholder values
				continue
			}
			if secrets[chName] == nil {
				secrets[chName] = make(map[string]any)
			}
			secrets[chName][fieldName] = strVal
			delete(chMap, fieldName) // Remove from config.json data
		}
	}
	return secrets
}

// injectChannelSecrets reads secrets from .security.yml and injects them into
// the config map under the respective channel entries (as ***REDACTED*** placeholders).
func (s *Server) injectChannelSecrets(config map[string]any) {
	sec := s.loadSecurityYaml()
	chSec, ok := sec["channels"].(map[string]any)
	if !ok {
		return
	}
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		return
	}
	for chName, chSecVal := range chSec {
		chSecMap, ok := chSecVal.(map[string]any)
		if !ok {
			continue
		}
		chMap, ok := channels[chName].(map[string]any)
		if !ok {
			chMap = make(map[string]any)
			channels[chName] = chMap
		}
		for field := range chSecMap {
			if _, exists := chMap[field]; !exists {
				chMap[field] = "***REDACTED***"
			}
		}
	}
}

// updateSecurityYaml merges extracted channel secrets into .security.yml.
func (s *Server) updateSecurityYaml(channelSecrets map[string]map[string]any) error {
	if len(channelSecrets) == 0 {
		return nil
	}
	sec := s.loadSecurityYaml()
	chSec, ok := sec["channels"].(map[string]any)
	if !ok {
		chSec = make(map[string]any)
		sec["channels"] = chSec
	}
	for chName, fields := range channelSecrets {
		existing, ok := chSec[chName].(map[string]any)
		if !ok {
			existing = make(map[string]any)
			chSec[chName] = existing
		}
		for k, v := range fields {
			existing[k] = v
		}
	}
	return s.saveSecurityYaml(sec)
}

// filterSensitiveFields removes sensitive fields from config (like API keys)
func filterSensitiveFields(config map[string]any) {
	// List of sensitive field patterns to redact
	sensitivePatterns := []string{
		"api_key", "apiKey", "api-key",
		"secret", "password", "token",
	}

	var redactRecursive func(m map[string]any)
	redactRecursive = func(m map[string]any) {
		for key, value := range m {
			switch v := value.(type) {
			case map[string]any:
				redactRecursive(v)
			case string:
				lowerKey := strings.ToLower(key)
				for _, pattern := range sensitivePatterns {
					if strings.Contains(lowerKey, pattern) {
						m[key] = "***REDACTED***"
						break
					}
				}
			}
		}
	}

	redactRecursive(config)
}

// mergeConfigs merges new config values into current config
func mergeConfigs(current, new map[string]any) map[string]any {
	result := make(map[string]any)

	// Copy current config
	for k, v := range current {
		result[k] = v
	}

	// Overwrite with new values
	for k, v := range new {
		if newVal, ok := v.(map[string]any); ok {
			if curVal, ok := result[k].(map[string]any); ok {
				// Merge nested maps
				merged := mergeConfigs(curVal, newVal)
				result[k] = merged
			} else {
				result[k] = v
			}
		} else {
			result[k] = v
		}
	}

	return result
}

// validateConfig performs basic validation on config structure
func validateConfig(config map[string]any) error {
	// Check required fields
	if _, ok := config["version"]; !ok {
		return fmt.Errorf("missing required field: version")
	}

	// Validate model config if present
	if model, ok := config["model"].(map[string]any); ok {
		if id, ok := model["id"].(string); !ok || id == "" {
			return fmt.Errorf("model.id is required")
		}
		if provider, ok := model["provider"].(string); !ok || provider == "" {
			return fmt.Errorf("model.provider is required")
		}
	}

	return nil
}

// WeChat/Weixin Flow API handlers

// handleWeixinFlow starts a new WeChat QR login flow.
// POST /api/weixin/flows
func (s *Server) handleWeixinFlow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	api, err := weixin.NewApiClient(weixinBaseURL, "", "")
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create weixin client: %v", err), http.StatusInternalServerError)
		return
	}

	qrResp, err := api.GetQRCode(ctx, weixinBotType)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get QR code: %v", err), http.StatusInternalServerError)
		return
	}

	dataURI, err := generateQRDataURI(qrResp.QrcodeImgContent)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to generate QR image: %v", err), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	flow := &weixinFlow{
		ID:        newWeixinFlowID(),
		Qrcode:    qrResp.Qrcode,
		QRDataURI: dataURI,
		Status:    weixinStatusWait,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(weixinFlowTTL),
	}
	s.storeWeixinFlow(flow)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(weixinFlowResponse{
		FlowID:    flow.ID,
		Status:    flow.Status,
		QRDataURI: flow.QRDataURI,
	})
}

// handleWeixinFlowPoll polls the WeChat API for QR code status and updates the flow.
// GET /api/weixin/flows/{id}
func (s *Server) handleWeixinFlowPoll(w http.ResponseWriter, r *http.Request) {
	// Extract flow ID from path using PathValue for Go 1.22+ router
	flowID := r.PathValue("id")
	if flowID == "" {
		// Fallback for older router: extract from path manually
		path := r.URL.Path
		prefix := "/api/weixin/flows/"
		if !strings.HasPrefix(path, prefix) {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}
		flowID = strings.TrimPrefix(path, prefix)
	}

	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		http.Error(w, "missing flow id", http.StatusBadRequest)
		return
	}

	flow, ok := s.getWeixinFlow(flowID)
	if !ok {
		http.Error(w, "flow not found", http.StatusNotFound)
		return
	}

	// Return terminal states directly without polling WeChat again
	if flow.Status == weixinStatusConfirmed ||
		flow.Status == weixinStatusExpired ||
		flow.Status == weixinStatusError {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(weixinFlowResponse{
			FlowID: flow.ID,
			Status: flow.Status,
			Error:  flow.Error,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	api, err := weixin.NewApiClient(weixinBaseURL, "", "")
	if err != nil {
		s.setWeixinFlowError(flowID, fmt.Sprintf("client error: %v", err))
		flow, _ = s.getWeixinFlow(flowID)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(weixinFlowResponse{FlowID: flow.ID, Status: flow.Status, Error: flow.Error})
		return
	}

	statusResp, err := api.GetQRCodeStatus(ctx, flow.Qrcode)
	if err != nil {
		// Transient error — keep current status, return it
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(weixinFlowResponse{
			FlowID:    flow.ID,
			Status:    flow.Status,
			QRDataURI: flow.QRDataURI,
		})
		return
	}

	switch statusResp.Status {
	case weixinStatusWait:
		// no change

	case weixinStatusScanned:
		s.updateWeixinFlowStatus(flowID, weixinStatusScanned)

	case weixinStatusConfirmed:
		if statusResp.BotToken == "" {
			s.setWeixinFlowError(flowID, "login confirmed but missing bot_token")
			break
		}
		if saveErr := s.saveWeixinBinding(statusResp.BotToken, statusResp.IlinkBotID); saveErr != nil {
			s.setWeixinFlowError(flowID, fmt.Sprintf("failed to save token: %v", saveErr))
			break
		}
		s.setWeixinFlowConfirmed(flowID, statusResp.IlinkBotID)

	case weixinStatusExpired:
		s.updateWeixinFlowStatus(flowID, weixinStatusExpired)

	default:
		// unknown status, keep as-is
	}

	flow, _ = s.getWeixinFlow(flowID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	resp := weixinFlowResponse{
		FlowID:    flow.ID,
		Status:    flow.Status,
		AccountID: flow.AccountID,
		Error:     flow.Error,
	}
	if flow.Status == weixinStatusWait || flow.Status == weixinStatusScanned {
		resp.QRDataURI = flow.QRDataURI
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// saveWeixinBinding writes the token/account ID and enables the Weixin channel.
func (s *Server) saveWeixinBinding(token, accountID string) error {
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	channels, ok := config["channels"].(map[string]any)
	if !ok {
		return fmt.Errorf("channels not found in config")
	}

	weixinCfg, ok := channels["weixin"].(map[string]any)
	if !ok {
		return fmt.Errorf("weixin channel not found in config")
	}

	// Update weixin config
	weixinCfg["enabled"] = true
	weixinCfg["account_id"] = accountID

	// Note: token is stored separately via SetToken in picoclaw's config,
	// but for claw we'll just enable the channel with account_id
	// The token will need to be handled by the picoclaw channel factory

	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(s.configPath, updatedData, 0644); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

// generateQRDataURI encodes content as a QR code PNG and returns a data URI.
func generateQRDataURI(content string) (string, error) {
	code, err := qr.Encode(content, qr.L)
	if err != nil {
		return "", fmt.Errorf("qr encode: %w", err)
	}
	pngBytes := code.PNG()
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	return "data:image/png;base64," + encoded, nil
}

func newWeixinFlowID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("wx_%d", time.Now().UnixNano())
	}
	return "wx_" + hex.EncodeToString(buf)
}

func (s *Server) storeWeixinFlow(flow *weixinFlow) {
	s.weixinMu.Lock()
	defer s.weixinMu.Unlock()
	s.gcWeixinFlowsLocked(time.Now())
	s.weixinFlows[flow.ID] = flow
}

func (s *Server) getWeixinFlow(flowID string) (*weixinFlow, bool) {
	s.weixinMu.Lock()
	defer s.weixinMu.Unlock()
	s.gcWeixinFlowsLocked(time.Now())
	flow, ok := s.weixinFlows[flowID]
	if !ok {
		return nil, false
	}
	cp := *flow
	return &cp, true
}

func (s *Server) updateWeixinFlowStatus(flowID, status string) {
	s.weixinMu.Lock()
	defer s.weixinMu.Unlock()
	if flow, ok := s.weixinFlows[flowID]; ok {
		flow.Status = status
		flow.UpdatedAt = time.Now()
	}
}

func (s *Server) setWeixinFlowConfirmed(flowID, accountID string) {
	s.weixinMu.Lock()
	defer s.weixinMu.Unlock()
	if flow, ok := s.weixinFlows[flowID]; ok {
		flow.Status = weixinStatusConfirmed
		flow.AccountID = accountID
		flow.UpdatedAt = time.Now()
	}
}

func (s *Server) setWeixinFlowError(flowID, errMsg string) {
	s.weixinMu.Lock()
	defer s.weixinMu.Unlock()
	if flow, ok := s.weixinFlows[flowID]; ok {
		flow.Status = weixinStatusError
		flow.Error = errMsg
		flow.UpdatedAt = time.Now()
	}
}

func (s *Server) gcWeixinFlowsLocked(now time.Time) {
	for id, flow := range s.weixinFlows {
		if flow.Status == weixinStatusWait || flow.Status == weixinStatusScanned {
			if !flow.ExpiresAt.IsZero() && now.After(flow.ExpiresAt) {
				flow.Status = weixinStatusExpired
				flow.UpdatedAt = now
			}
		}
		if flow.Status != weixinStatusWait &&
			flow.Status != weixinStatusScanned &&
			now.Sub(flow.UpdatedAt) > weixinFlowGCAge {
			delete(s.weixinFlows, id)
		}
	}
}

// Channels API handlers

// handleChannelsCatalog returns the catalog of supported channels.
func (s *Server) handleChannelsCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Read config file
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse config
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Extract channels
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{
			"channels": []map[string]any{},
		})
		return
	}

	// Build catalog with supported channels
	supportedChannels := []map[string]any{}

	// Display names for channels
	displayNames := map[string]string{
		"dingtalk":  "DingTalk",
		"discord":   "Discord",
		"feishu":     "Feishu/Lark",
		"irc":       "IRC",
		"line":      "LINE",
		"maixcam":   "MaixCAM",
		"matrix":    "Matrix",
		"onebot":    "OneBot",
		"pico":      "PicoClaw Web",
		"pico_client": "PicoClaw Client",
		"qq":        "QQ",
		"slack":     "Slack",
		"telegram":  "Telegram",
		"wecom":     "WeCom",
		"wecom_aibot": "WeCom AiBot",
		"wecom_app": "WeCom App",
		"weixin":    "Weixin",
		"whatsapp":  "WhatsApp",
	}

	for name, cfg := range channels {
		if cfgMap, ok := cfg.(map[string]any); ok {
			channelInfo := map[string]any{
				"name":        name,
				"display_name": displayNames[name],
				"config_key":  name,
				"enabled":     cfgMap["enabled"],
			}
			supportedChannels = append(supportedChannels, channelInfo)
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"channels": supportedChannels,
	})
}

// handleListChannels returns the list of all channels and their status.
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Read config file
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse config
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Extract channels
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{
			"channels": map[string]any{},
			"total":    0,
		})
		return
	}

	// Filter sensitive fields from each channel
	filterSensitiveFields(channels)

	// Convert to response format
	channelList := make([]map[string]any, 0)
	for name, cfg := range channels {
		if cfgMap, ok := cfg.(map[string]any); ok {
			channelInfo := map[string]any{
				"name": name,
			}
			// Copy all fields from channel config
			for k, v := range cfgMap {
				channelInfo[k] = v
			}
			channelList = append(channelList, channelInfo)
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"channels": channelList,
		"total":    len(channelList),
	})
}

// handleGetChannel returns a specific channel's configuration.
func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Extract channel name from path
	// URL format: /api/channels/{channel_name}
	path := r.URL.Path
	prefix := "/api/channels/"
	if !strings.HasPrefix(path, prefix) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	channelName := strings.TrimPrefix(path, prefix)
	if channelName == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		return
	}

	// Read config file
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse config
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Extract channels
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		http.Error(w, "Channels not found in config", http.StatusNotFound)
		return
	}

	// Get specific channel
	channel, ok := channels[channelName]
	if !ok {
		http.Error(w, fmt.Sprintf("Channel '%s' not found", channelName), http.StatusNotFound)
		return
	}

	// Filter sensitive fields
	if channelMap, ok := channel.(map[string]any); ok {
		filterSensitiveFields(channelMap)
		json.NewEncoder(w).Encode(map[string]any{
			"name":     channelName,
			"config":   channelMap,
		})
	} else {
		json.NewEncoder(w).Encode(map[string]any{
			"name":   channelName,
			"config": channel,
		})
	}
}

// handleUpdateChannel updates a specific channel's configuration.
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Extract channel name from path
	path := r.URL.Path
	prefix := "/api/channels/"
	if !strings.HasPrefix(path, prefix) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	channelName := strings.TrimPrefix(path, prefix)
	if channelName == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		return
	}

	// Parse request body
	var req struct {
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Read current config
	configData, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read config: %v", err), http.StatusInternalServerError)
		return
	}

	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse config: %v", err), http.StatusInternalServerError)
		return
	}

	// Get channels map
	channels, ok := config["channels"].(map[string]any)
	if !ok {
		channels = make(map[string]any)
		config["channels"] = channels
	}

	// Update channel config
	channels[channelName] = req.Config

	// Write updated config
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(s.configPath, updatedData, 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Channel '%s' updated. Service restart is required for changes to take effect.", channelName),
		"channel": channelName,
	})
}

// Main function for standalone mode
func main() {
	port := flag.Int("port", 18800, "Port to listen on")
	public := flag.Bool("public", false, "Listen on all interfaces")
	flag.Parse()

		if err := RunAsStandalone(*port, *public); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// deriveProviderFromModel tries to infer the provider name from the model entry.
// It uses the "model" field format (e.g., "anthropic/claude-sonnet-4.6" -> "anthropic")
// or the api_base URL as fallback.
func (s *Server) deriveProviderFromModel(entry map[string]any) string {
	// If model field has provider/ prefix (e.g., "anthropic/claude-sonnet-4.6")
	if modelID, ok := entry["model"].(string); ok {
		if parts := strings.SplitN(modelID, "/", 2); len(parts) == 2 {
			return parts[0]
		}
	}

	// Fallback: derive from api_base
	if apiBase, ok := entry["api_base"].(string); ok && apiBase != "" {
		u, err := url.Parse(apiBase)
		if err == nil {
			host := u.Hostname()
			// Map known hosts to provider names
			hostToProvider := map[string]string{
				"api.openai.com":              "openai",
				"api.anthropic.com":           "anthropic",
				"api.deepseek.com":            "deepseek",
				"api.moonshot.cn":             "moonshot",
				"dashscope.aliyuncs.com":      "qwen",
				"generativelanguage.googleapis.com": "google",
				"api.minimaxi.com":            "minimax",
				"openrouter.ai":               "openrouter",
				"api.cerebras.ai":             "cerebras",
				"ark.cn-beijing.volces.com":   "volcengine",
				"api.shengsuanyun.com":        "shengsuanyun",
				"api.z.ai":                    "zai",
				"api.zhipuai.com":             "zhipu",
			}
			for hostPattern, provider := range hostToProvider {
				if strings.Contains(host, hostPattern) {
					return provider
				}
			}
			// Generic: use first part of hostname
			parts := strings.Split(host, ".")
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}

	return ""
}

// saveAPIKeyToAuth saves an API key for a provider to ~/.aiclaw/auth.json.
func (s *Server) saveAPIKeyToAuth(provider, apiKey string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}

	authPath := filepath.Join(homeDir, ".aiclaw", "auth.json")

	// Read existing auth.json or start fresh
	auth := make(map[string]map[string]string)
	if data, err := os.ReadFile(authPath); err == nil {
		json.Unmarshal(data, &auth)
	}
	if auth[provider] == nil {
		auth[provider] = make(map[string]string)
	}
	auth[provider]["apiKey"] = apiKey

	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth.json: %w", err)
	}
		return os.WriteFile(authPath, data, 0644)
}

// loadAuthKeys reads auth.json and returns a map of provider -> apiKey.
func (s *Server) loadAuthKeys() map[string]string {
	keys := make(map[string]string)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return keys
	}
	authPath := filepath.Join(homeDir, ".aiclaw", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return keys
	}
	var auth map[string]map[string]string
	if err := json.Unmarshal(data, &auth); err != nil {
		return keys
	}
	for provider, fields := range auth {
		for _, keyField := range []string{"apiKey", "api_key", "key", "token"} {
			if key, ok := fields[keyField]; ok && key != "" {
				keys[provider] = key
				break
			}
		}
	}
	return keys
}

// deriveProvider tries to infer the provider name from a model entry map.
func deriveProvider(entry map[string]any) string {
	if modelID, ok := entry["model"].(string); ok {
		if parts := strings.SplitN(modelID, "/", 2); len(parts) == 2 {
			return parts[0]
		}
	}
	if apiBase, ok := entry["api_base"].(string); ok && apiBase != "" {
		u, err := url.Parse(apiBase)
		if err == nil {
			host := u.Hostname()
			hostToProvider := map[string]string{
				"api.openai.com":                         "openai",
				"api.anthropic.com":                      "anthropic",
				"api.deepseek.com":                       "deepseek",
				"api.moonshot.cn":                        "moonshot",
				"dashscope.aliyuncs.com":                 "qwen",
				"generativelanguage.googleapis.com":       "google",
				"api.minimaxi.com":                       "minimax",
				"openrouter.ai":                          "openrouter",
				"api.cerebras.ai":                        "cerebras",
				"ark.cn-beijing.volces.com":              "volcengine",
				"api.shengsuanyun.com":                   "shengsuanyun",
				"api.z.ai":                               "zai",
				"api.zhipuai.com":                        "zhipu",
			}
			for hostPattern, provider := range hostToProvider {
				if strings.Contains(host, hostPattern) {
					return provider
				}
			}
			parts := strings.Split(host, ".")
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	return ""
}

// maskAPIKey returns a masked version of an API key for safe display.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	if len(key) <= 12 {
		return key[:3] + "****" + key[len(key)-2:]
	}
	return key[:3] + "****" + key[len(key)-4:]
}
