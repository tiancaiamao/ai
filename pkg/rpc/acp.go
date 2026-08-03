package rpc

// ACP (Agent Client Protocol) agent over stdio.
//
// Framing is newline-delimited JSON-RPC 2.0 (the same NDJSON transport used
// by `ai rpc`), so the read loop is structurally identical to Server.
//
// Implemented surface (minimal, per spec baseline):
//   - initialize                 -> protocolVersion 1 + capabilities
//   - session/new                -> session id (maps to the app's single session)
//   - session/prompt             -> agent turn; responds with StopReason when
//                                   the turn completes (agent_end event)
//   - session/cancel (notify)    -> aborts the running turn
//   - session/update (notify)    -> agent_message_chunk / tool_call updates
//
// Everything else (fs/*, terminal/*, session/load, image/audio content, MCP
// transports) is deliberately NOT advertised and rejected as method not found.
// mcpServers in session/new are accepted and ignored (logged).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// acpProtocolVersion is the ACP major protocol version this agent implements.
const acpProtocolVersion = 1

// JSON-RPC 2.0 error codes.
const (
	acpErrInvalidRequest = -32600
	acpErrMethodNotFound = -32601
	acpErrInvalidParams  = -32602
)

// ACP stop reasons (subset of the spec enum).
const (
	acpStopEndTurn   = "end_turn"
	acpStopCancelled = "cancelled"
)

// acpRequest is an incoming JSON-RPC request or notification.
type acpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether the message has no id (fire-and-forget).
func (r *acpRequest) isNotification() bool {
	return len(r.ID) == 0
}

// acpResponse is a JSON-RPC response.
type acpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *acpErrorObject `json:"error,omitempty"`
}

type acpErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// acpNotification is an outbound JSON-RPC notification.
type acpNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// acpSessionUpdateParams is the payload of session/update notifications.
type acpSessionUpdateParams struct {
	SessionID string    `json:"sessionId"`
	Update    acpUpdate `json:"update"`
}

// acpUpdate is the "update" discriminator of session/update. Only the fields
// relevant to the implemented update kinds are modeled; unused ones are
// omitted via json tags.
type acpUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Content       any    `json:"content,omitempty"`
	ToolCallID    string `json:"toolCallId,omitempty"`
	Title         string `json:"title,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
}

// acpContentBlock is a prompt content block (text / embedded resource /
// resource link — the baseline types plus embeddedContext).
type acpContentBlock struct {
	Type     string               `json:"type"`
	Text     string               `json:"text"`
	URI      string               `json:"uri"`
	Resource *acpEmbeddedResource `json:"resource"`
}

type acpEmbeddedResource struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

// acpServer serves the ACP protocol on stdin/stdout.
type acpServer struct {
	app    *rpcApp
	ctx    context.Context
	cancel context.CancelFunc
	out    io.Writer
	mu     sync.Mutex // serializes writes to out

	sessionID string

	pendingMu     sync.Mutex
	pendingPrompt json.RawMessage // id of the in-flight session/prompt (nil when idle)
	cancelled     bool            // set when the pending turn was cancelled
}

// RunACP runs the agent as an ACP server over stdin/stdout. Setup mirrors
// RunRPC: same config, session, tools, compactor and agent; only the protocol
// layer differs.
func RunACP(sessionPath string, debugAddr string, input io.Reader, output io.Writer, customSystemPrompt string, maxTurns int, timeout time.Duration, role string, modelOverride string, runID string) error {
	// --- Construct rpcApp (config, model, session, tools, compactor, skills) ---
	app, err := newRPCApp(sessionPath, rpcAppSetupParams{
		customSystemPrompt: customSystemPrompt,
		maxTurns:           maxTurns,
		debugAddr:          debugAddr,
		role:               role,
		modelOverride:      modelOverride,
		runID:              runID,
	})
	if err != nil {
		return err
	}

	// --- Create agent ---
	ag, sessionWriter, err := app.setupAgent(maxTurns)
	if err != nil {
		return err
	}
	defer sessionWriter.Close()
	defer ag.Shutdown()

	// --- ACP server ---
	srv := &acpServer{app: app, out: output}
	srv.ctx, srv.cancel = context.WithCancel(context.Background())
	defer srv.cancel()

	// Command registry: reuse the NDJSON Server purely for slash-command
	// registration (handlePrompt dispatches /commands through it). Events go
	// through srv.emit, never through app.server.
	server := NewServer()
	server.SetOutput(io.Discard)
	app.server = server
	app.registerAllHandlers()

	// --- Start event emitter (state tracking + persistence + ACP translation) ---
	shutdownEmitter, eventEmitterDone := app.initEventEmitter(srv.emit)

	// --- Timeout watchdog ---
	if timeout > 0 {
		go func() {
			<-time.After(timeout)
			slog.Warn("[ACP] Timeout reached, aborting agent", "timeout", timeout)
			srv.markCancelled()
			ag.Abort()
			srv.cancel()
		}()
	}

	// --- External abort signal (SIGINT/SIGTERM from the CLI wrapper) ---
	go func() {
		<-agentAbortSignal
		slog.Info("[ACP] External abort signal received, aborting agent")
		ag.Abort()
		srv.cancel()
	}()

	// --- Debug server if enabled ---
	app.startDebugServer()

	slog.Info("ACP server started", "model", app.model.ID, "cwd", app.cwd)
	runErr := srv.run(input)

	slog.Info("ACP server stopped, waiting for cleanup...")
	ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	slog.Info("Agent completed, exiting...")
	return runErr
}

// run reads JSON-RPC messages from input until EOF or context cancellation.
func (s *acpServer) run(input io.Reader) error {
	cr := &contextReader{reader: input, ctx: s.ctx}
	scanner := bufio.NewScanner(cr)
	buf := make([]byte, 0, 4*1024*1024) // 4MB
	scanner.Buffer(buf, 16*1024*1024)   // 16MB max

	for scanner.Scan() {
		line := scanner.Bytes()

		var req acpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, acpErrInvalidRequest, fmt.Sprintf("failed to parse message: %v", err))
			continue
		}
		if req.JSONRPC != "2.0" {
			s.sendError(req.ID, acpErrInvalidRequest, "invalid jsonrpc version")
			continue
		}
		s.handleRequest(req)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func (s *acpServer) handleRequest(req acpRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "session/new":
		s.handleSessionNew(req)
	case "session/prompt":
		s.handlePrompt(req)
	case "session/cancel":
		s.handleCancel(req)
	default:
		s.sendError(req.ID, acpErrMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *acpServer) handleInitialize(req acpRequest) {
	// Client version is ignored: we answer with the latest version we
	// support and let the client decide whether to proceed.
	s.sendResult(req.ID, map[string]any{
		"protocolVersion": acpProtocolVersion,
		"agentCapabilities": map[string]any{
			// Baseline text + resource_link support is implicit (MUST).
			// embeddedContext is the one optional prompt capability we honor.
			"promptCapabilities": map[string]bool{
				"embeddedContext": true,
			},
		},
	})
}

func (s *acpServer) handleSessionNew(req acpRequest) {
	var params struct {
		CWD        string `json:"cwd"`
		MCPServers []any  `json:"mcpServers"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("invalid session/new params: %v", err))
		return
	}
	if len(params.MCPServers) > 0 {
		slog.Info("[ACP] mcpServers ignored (MCP client not implemented)", "count", len(params.MCPServers))
	}
	if params.CWD != "" && params.CWD != s.app.cwd {
		// The workspace is bound at startup; a different requested cwd is
		// logged but not honored in this version.
		slog.Info("[ACP] session cwd differs from process cwd, keeping process cwd",
			"requested", params.CWD, "actual", s.app.cwd)
	}

	s.sessionID = s.app.sessionID
	s.sendResult(req.ID, map[string]string{"sessionId": s.sessionID})
}

func (s *acpServer) handlePrompt(req acpRequest) {
	var params struct {
		SessionID string            `json:"sessionId"`
		Prompt    []acpContentBlock `json:"prompt"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("invalid session/prompt params: %v", err))
		return
	}
	if params.SessionID != "" && params.SessionID != s.sessionID {
		s.sendError(req.ID, acpErrInvalidParams, "unknown sessionId")
		return
	}
	message := buildACPMessage(params.Prompt)
	if message == "" {
		s.sendError(req.ID, acpErrInvalidParams, "empty prompt")
		return
	}

	// Register the pending prompt; the response is deferred until the turn
	// completes (agent_end event), see emit.
	s.pendingMu.Lock()
	s.pendingPrompt = req.ID
	s.cancelled = false
	s.pendingMu.Unlock()

	if _, err := s.app.handlePrompt(RPCCommand{Type: "prompt", Message: message}); err != nil {
		s.pendingMu.Lock()
		s.pendingPrompt = nil
		s.pendingMu.Unlock()
		s.sendError(req.ID, acpErrInvalidParams, err.Error())
	}
}

// handleCancel handles the session/cancel notification: abort the running
// turn. The pending session/prompt gets its response from emit when the
// agent_end event arrives.
func (s *acpServer) handleCancel(req acpRequest) {
	s.pendingMu.Lock()
	s.cancelled = true
	hasPending := len(s.pendingPrompt) > 0
	s.pendingMu.Unlock()
	if hasPending {
		s.app.ag.Abort()
	}
}

// markCancelled flags the pending turn as cancelled (used by the timeout
// watchdog so the client receives a "cancelled" stop reason).
func (s *acpServer) markCancelled() {
	s.pendingMu.Lock()
	s.cancelled = true
	s.pendingMu.Unlock()
}

// emit translates agent events into ACP session/update notifications. It is
// invoked from the shared event emitter after state tracking + persistence.
func (s *acpServer) emit(event agent.AgentEvent) {
	switch event.Type {
	case agent.EventMessageUpdate:
		ae, ok := event.AssistantMessageEvent.(agent.AssistantMessageEvent)
		if !ok || ae.Type != "text_delta" || ae.Delta == "" {
			return
		}
		s.sendUpdate(acpUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       map[string]string{"type": "text", "text": ae.Delta},
		})

	case agent.EventToolExecutionStart:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "tool_call",
			ToolCallID:    event.ToolCallID,
			Title:         event.ToolName,
			Kind:          toolCallKind(event.ToolName),
			Status:        "pending",
		})

	case agent.EventToolExecutionEnd:
		status := "completed"
		if event.IsError {
			status = "error"
		}
		u := acpUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    event.ToolCallID,
			Status:        status,
		}
		if event.Result != nil {
			if text := event.Result.ExtractText(); text != "" {
				u.Content = []any{
					map[string]any{
						"type": "content",
						"content": map[string]string{
							"type": "text",
							"text": text,
						},
					},
				}
			}
		}
		s.sendUpdate(u)

	case agent.EventAgentEnd:
		// A turn completed: answer the pending session/prompt request.
		s.pendingMu.Lock()
		id := s.pendingPrompt
		cancelled := s.cancelled
		s.pendingPrompt = nil
		s.cancelled = false
		s.pendingMu.Unlock()
		if len(id) > 0 {
			stopReason := acpStopEndTurn
			if cancelled {
				stopReason = acpStopCancelled
			}
			s.sendResult(id, map[string]string{"stopReason": stopReason})
		}
	}
}

// sendUpdate emits a session/update notification for the current session.
func (s *acpServer) sendUpdate(update acpUpdate) {
	s.sendNotification("session/update", acpSessionUpdateParams{
		SessionID: s.sessionID,
		Update:    update,
	})
}

func (s *acpServer) sendResult(id json.RawMessage, result any) {
	s.writeMessage(acpResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *acpServer) sendError(id json.RawMessage, code int, message string) {
	s.writeMessage(acpResponse{JSONRPC: "2.0", ID: id, Error: &acpErrorObject{Code: code, Message: message}})
}

func (s *acpServer) sendNotification(method string, params any) {
	s.writeMessage(acpNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// writeMessage serializes a message as one newline-delimited JSON line.
func (s *acpServer) writeMessage(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("[ACP] failed to marshal message", "error", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}

// buildACPMessage flattens ACP prompt content blocks into the text message
// consumed by the agent's prompt channel.
func buildACPMessage(blocks []acpContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case "resource":
			// Embedded resource: inline the content with a file label.
			if b.Resource != nil {
				label := b.Resource.URI
				if label == "" {
					label = "embedded resource"
				}
				if b.Resource.Text != "" {
					parts = append(parts, "=== "+label+" ===\n"+b.Resource.Text)
				} else {
					parts = append(parts, "=== "+label+" ===")
				}
			}
		case "resource_link":
			// Reference only: the agent fetches the file itself via its tools.
			if u, err := url.Parse(b.URI); err == nil && u.Scheme == "file" {
				parts = append(parts, "[file: "+u.Path+"]")
			} else if b.URI != "" {
				parts = append(parts, "["+b.URI+"]")
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// toolCallKind maps ai tool names to ACP tool_call kinds for client icons.
func toolCallKind(toolName string) string {
	switch toolName {
	case "read":
		return "read"
	case "write", "edit":
		return "edit"
	case "bash":
		return "execute"
	case "grep":
		return "search"
	default:
		return "other"
	}
}
