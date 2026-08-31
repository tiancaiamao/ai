package rpc

// ACP (Agent Client Protocol) agent over stdio.
//
// Framing is newline-delimited JSON-RPC 2.0 (the same NDJSON transport used
// by `ai rpc`), so the read loop is structurally identical to Server.
//
// Implemented surface (minimal, per spec baseline):
//   - initialize                 -> protocolVersion 1 + capabilities
//   - session/new                -> session id (maps to the app's single session),
//                                   followed by an available_commands_update
//                                   notification advertising slash commands
//   - session/prompt             -> agent turn; responds with StopReason when
//                                   the turn completes (agent_end event).
//                                   Text whose first token names a REGISTERED
//                                   command runs synchronously and answers right
//                                   away with end_turn; /skill:<name> goes through
//                                   skill expansion; everything else is raw text.
//   - session/cancel (notify)    -> aborts the running turn
//   - session/update (notify)    -> agent_message_chunk / tool_call /
//                                   available_commands_update
//   - session/set_config_option  -> switches the active model (aliases:
//                                   session/set_config_options,
//                                   session/set_model, config/set_option);
//                                   the updated model catalog is returned
//                                   under configOptions + config_options.
//
// The model catalog is advertised in the session/new and session/load results
// as a category "model" select config option so hosts can render a model
// selector (also on the cross-restart resume path).

//
// Everything else (fs/*, terminal/*, image/audio content, MCP transports) is
// deliberately NOT advertised and rejected as method not found.
// mcpServers in session/new are accepted and ignored (logged).

import (
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
	"github.com/tiancaiamao/ai/pkg/command"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/transport"
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
	// RawInput carries the tool call arguments (spec: ToolCallUpdate.rawInput)
	// so hosts like AionUi can render the invocation parameters.
	RawInput map[string]any        `json:"rawInput,omitempty"`
	Commands []acpAvailableCommand `json:"commands,omitempty"`
	// Meta carries implementation-specific extension data for `_`-prefixed
	// sessionUpdate values. Per ACP extensibility, custom data lives in _meta;
	// standard ACP clients ignore it.
	Meta any `json:"_meta,omitempty"`
}

// acpAvailableCommand is one entry of available_commands_update (spec:
// AvailableCommand). The optional input hint is omitted: our registry has no
// argument schema.
type acpAvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	conn   transport.Conn

	sessionID string

	pendingMu     sync.Mutex
	pendingPrompt json.RawMessage // id of the in-flight session/prompt (nil when idle)
	cancelled     bool            // set when the pending turn was cancelled
}

// RunACP runs the agent as an ACP server over the given transport conn. Setup
// mirrors RunRPC: same config, session, tools, compactor and agent; only the
// protocol layer differs. The conn may be a stdio channel (NewStdio) or a hub
// multiplexing several unix-socket peers (NewHub); the ACP core is identical
// either way.
func RunACP(conn transport.Conn, sessionPath string, debugAddr string, customSystemPrompt string, maxTurns int, timeout time.Duration, role string, modelOverride string, runID string) error {
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
	srv := &acpServer{app: app, conn: conn}
	srv.ctx, srv.cancel = context.WithCancel(context.Background())
	defer srv.cancel()
	// Unblock the transport read on shutdown: canceling the ctx closes the conn,
	// which yields io.EOF from ReadMessage so the run loop exits cleanly.
	go func() {
		<-srv.ctx.Done()
		conn.Close()
	}()

	// Command registry: reuse the NDJSON Server purely for slash-command
	// registration (handlePrompt dispatches /commands through it). Events go
	// through srv.emit, never through app.server.
	server := NewServer()
	server.SetOutput(io.Discard)
	app.server = server
	app.registerAllHandlers()
	app.buildSkillCommands()

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
		srv.markCancelled()
		ag.Abort()
		srv.cancel()
	}()

	// --- Debug server if enabled ---
	app.startDebugServer()

	slog.Info("ACP server started", "model", app.model.ID, "cwd", app.cwd)
	runErr := srv.run()

	slog.Info("ACP server stopped, waiting for cleanup...")
	ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	slog.Info("Agent completed, exiting...")
	return runErr
}

// run reads JSON-RPC messages from the transport until EOF or context
// cancellation.
func (s *acpServer) run() error {
	for {
		msg, err := s.conn.ReadMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req acpRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			s.sendError(nil, acpErrInvalidRequest, fmt.Sprintf("failed to parse message: %v", err))
			continue
		}
		if req.JSONRPC != "2.0" {
			s.sendError(req.ID, acpErrInvalidRequest, "invalid jsonrpc version")
			continue
		}
		s.handleRequest(req)
	}
}

func (s *acpServer) handleRequest(req acpRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "session/new":
		s.handleSessionNew(req)
	case "session/load":
		s.handleSessionLoad(req)
	case "session/prompt":
		s.handlePrompt(req)
	case "session/cancel":
		s.handleCancel(req)
	// Model switching requested by the host. session/set_config_option is the
	// official ACP v1 method; the rest are defensive aliases because the
	// exact name used by some hosts (e.g. aioncore) is not observable.
	case "session/set_config_option", "session/set_config_options",
		"session/set_model", "config/set_option":
		s.handleSetConfig(req)

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
			// Cross-restart resume: session/load replays persisted history.
			"loadSession": true,
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
	result := map[string]any{"sessionId": s.sessionID}
	if catalog := s.app.acpModelCatalog(); len(catalog) > 0 {
		// configOptions is the ACP v1 field; config_options is the snake_case
		// spelling read by hosts like aioncore (captured into handshake meta).
		result["configOptions"] = catalog
		result["config_options"] = catalog
		result["_meta"] = map[string]any{"config_options": catalog}
	}
	s.sendResult(req.ID, result)

	s.sendAvailableCommands()
}

// handleSessionLoad resumes a previously persisted session (cross-restart
// resume). The ACP sessionId is the internal session id (see handleSessionNew),
// so the session is located on disk via the SessionManager, swapped into the
// app (agent context, compactor), and its history is replayed to the client as
// a series of session/update notifications before the response.
func (s *acpServer) handleSessionLoad(req acpRequest) {
	var params struct {
		SessionID  string `json:"sessionId"`
		CWD        string `json:"cwd"`
		MCPServers []any  `json:"mcpServers"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("invalid session/load params: %v", err))
		return
	}
	if params.SessionID == "" {
		s.sendError(req.ID, acpErrInvalidParams, "session/load requires sessionId")
		return
	}

	// A load swaps the agent context; doing so while a prompt turn is running
	// would race with the loop and interleave replay updates into that turn.
	s.pendingMu.Lock()
	busy := len(s.pendingPrompt) > 0
	s.pendingMu.Unlock()
	if busy {
		s.sendError(req.ID, acpErrInvalidRequest, "cannot load session while a prompt is in flight")
		return
	}

	// Existence check first: GetSession silently returns an empty session for
	// unknown ids (missing messages.jsonl is treated as a fresh session).
	if _, err := s.app.sessionMgr.GetMeta(params.SessionID); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("session not found: %s", params.SessionID))
		return
	}
	newSess, err := s.app.sessionMgr.GetSession(params.SessionID)
	if err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("failed to load session %s: %v", params.SessionID, err))
		return
	}
	if err := s.app.sessionMgr.SetCurrent(params.SessionID); err != nil {
		slog.Info("[ACP] failed to set current session", "id", params.SessionID, "error", err)
	}
	if err := s.app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	newName := resolveSessionName(s.app.sessionMgr, params.SessionID)
	s.sessionID = params.SessionID
	s.app.setSession(newSess, params.SessionID, newName)
	slog.Info("[ACP] session loaded", "id", params.SessionID, "messages", len(newSess.GetMessages()))

	s.replayHistory(newSess.GetMessages())
	s.sendAvailableCommands()
	// Same model catalog as session/new: the resume path must keep the host's
	// model selector (and set_config_option values) discoverable.
	s.sendResult(req.ID, acpResultWithCatalog(map[string]any{"stopReason": acpStopEndTurn}, s.app.acpModelCatalog()))
}

// replayHistory converts persisted conversation history into ACP session/update
// notifications: user messages become user_message_chunk, assistant text
// becomes agent_message_chunk, assistant thinking becomes agent_thought_chunk,
// tool calls become tool_call + tool_call_update.
// Compaction summaries are internal bookkeeping and are skipped. Tool calls
// left without a result (e.g. the process died mid-turn) are closed out as
// completed so clients don't render an endless spinner.
func (s *acpServer) replayHistory(messages []agentctx.AgentMessage) {
	pendingCalls := make(map[string]bool)
	for _, msg := range messages {
		if msg.Metadata != nil && msg.Metadata.Kind == "compactionSummary" {
			continue
		}
		switch msg.Role {
		case "user":
			s.sendReplayText("user_message_chunk", msg.Content)
		case "assistant":
			s.sendReplayText("agent_message_chunk", msg.Content)
			for _, block := range msg.Content {
				tc, ok := block.(agentctx.ToolCallContent)
				if !ok || tc.ID == "" {
					continue
				}
				pendingCalls[tc.ID] = true
				s.sendUpdate(acpUpdate{
					SessionUpdate: "tool_call",
					ToolCallID:    tc.ID,
					Title:         tc.Name,
					Kind:          toolCallKind(tc.Name),
					Status:        "pending",
					RawInput:      tc.Arguments,
				})
			}
		case "toolResult":
			status := "completed"
			if msg.IsError {
				status = "error"
			}
			delete(pendingCalls, msg.ToolCallID)
			u := acpUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    msg.ToolCallID,
				Title:         msg.ToolName,
				Status:        status,
			}
			if text := msg.ExtractText(); text != "" {
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
			s.sendUpdate(u)
		}
	}
	// Close dangling tool calls from an interrupted turn.
	for id := range pendingCalls {
		s.sendUpdate(acpUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    id,
			Status:        "completed",
		})
	}
}

// sendReplayText emits one session/update per text content block of a message.
// Thinking blocks of assistant messages are replayed as agent_thought_chunk so
// hosts can render the reasoning history; user messages never carry them.
func (s *acpServer) sendReplayText(updateKind string, blocks []agentctx.ContentBlock) {
	for _, block := range blocks {
		switch cb := block.(type) {
		case agentctx.TextContent:
			if cb.Text == "" {
				continue
			}
			s.sendUpdate(acpUpdate{
				SessionUpdate: updateKind,
				Content:       map[string]string{"type": "text", "text": cb.Text},
			})
		case agentctx.ThinkingContent:
			if cb.Thinking == "" || updateKind != "agent_message_chunk" {
				continue
			}
			s.sendUpdate(acpUpdate{
				SessionUpdate: "agent_thought_chunk",
				Content:       map[string]string{"type": "text", "text": cb.Thinking},
			})
		}
	}
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

	// Slash-command interception is allow-listed: only text whose first token
	// names a REGISTERED command is dispatched as a command, and it answers
	// synchronously (no agent turn involved). Skill prompts (/skill:<name>)
	// are excluded here — they run through the normal prompt path below where
	// handlePrompt expands them into a full skill block.
	if name, args, ok := matchACPCommand(s.app.server, message); ok {
		s.dispatchACPCommand(req.ID, name, args)
		return
	}

	// Register the pending prompt; the response is deferred until the turn
	// completes (agent_end event), see emit.
	s.pendingMu.Lock()
	s.pendingPrompt = req.ID
	s.cancelled = false
	s.pendingMu.Unlock()

	// Echo the prompt to every attached peer as user_message_chunk so live
	// watchers (ai watch, TUI) see the user's text — the load-time replay is
	// not visible to clients that were already attached.
	s.sendUpdate(acpUpdate{
		SessionUpdate: "user_message_chunk",
		Content:       map[string]string{"type": "text", "text": message},
	})

	// Everything else stays raw free text: ACP prompts may legitimately start
	// with '/' without being a command (a Go comment does too), so only an
	// explicit /skill:<name> prefix opts into skill-command parsing.
	raw := !skill.IsSkillCommand(message)
	if _, err := s.app.handlePrompt(RPCCommand{Type: "prompt", Message: message, Raw: raw}); err != nil {
		s.pendingMu.Lock()
		s.pendingPrompt = nil
		s.pendingMu.Unlock()
		s.sendError(req.ID, acpErrInvalidParams, err.Error())
	}
}

// matchACPCommand reports whether msg invokes a slash command REGISTERED in
// the server's registry, returning its name and argument string. Unregistered
// '/...' text (e.g. a comment) returns false so the caller keeps treating it
// as raw free text. Skill prompts are intentionally excluded: they are not
// registry entries and need the expansion path in app.handlePrompt.
func matchACPCommand(server *Server, msg string) (name, args string, ok bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		// Some hosts prepend their own context blocks to a prompt (e.g. AionUi
		// injects an "[Assistant Rules]" skills preamble before the user's
		// first message). Fall back to the final line: if it invokes a
		// registered command, treat the whole prompt as that command.
		if idx := strings.LastIndex(msg, "\n"); idx >= 0 {
			last := strings.TrimSpace(msg[idx+1:])
			if !strings.HasPrefix(last, "/") {
				return "", "", false
			}
			msg = last
		} else {
			return "", "", false
		}
	}
	if skill.IsSkillCommand(msg) {
		return "", "", false
	}
	cmdName, rest, err := command.ParseSlashCommand(msg)
	if err != nil {
		return "", "", false
	}
	if _, found := server.GetSlashHandler(cmdName); !found {
		return "", "", false
	}
	return cmdName, rest, true
}

// dispatchACPCommand runs a registered slash command synchronously and answers
// the session/prompt request immediately: the result is emitted as one
// agent_message_chunk followed by stopReason "end_turn". No agent turn runs,
// so no pendingPrompt is registered — the deferred-response path in emit never
// fires for commands.
func (s *acpServer) dispatchACPCommand(id json.RawMessage, name, args string) {
	handler, _ := s.app.server.GetSlashHandler(name)
	result, err := handler(args)
	if err != nil {
		s.sendError(id, acpErrInvalidParams, fmt.Sprintf("/%s failed: %v", name, err))
		return
	}
	if text := formatACPCommandResult(name, result); text != "" {
		s.sendUpdate(acpUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       map[string]string{"type": "text", "text": text},
		})
	}
	s.sendResult(id, map[string]string{"stopReason": acpStopEndTurn})
}

// formatACPCommandResult renders a slash-handler result for display. The
// shared FormatCommandResult renderers produce human-readable text for UI
// clients; strings pass through verbatim and everything else falls back to
// pretty-printed JSON.
func formatACPCommandResult(name string, result any) string {
	switch v := result.(type) {
	case nil:
		return ""
	case string:
		return v
	}
	if text := FormatCommandResult(name, result); text != "" {
		return text
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", result)
	}
	return string(data)
}

// sendAvailableCommands advertises the agent's slash commands via a
// session/update notification (available_commands_update), sent right after
// session/new so clients can render their command menu. Registry entries are
// joined by one item per installed skill (/skill:<name>). Safe to call again
// later to refresh the list.
func (s *acpServer) sendAvailableCommands() {
	listed := s.app.server.ListSlashCommands()
	commands := make([]acpAvailableCommand, 0, len(listed)+len(s.app.skillCommands))
	for _, c := range listed {
		commands = append(commands, acpAvailableCommand{Name: c.Name, Description: c.Description})
	}
	// app.skillCommands already carries the /skill:<name> entries (see
	// registerSkillHandlers); strip the leading slash because ACP clients add
	// it themselves when rendering the menu.
	if s.app.skillResult != nil {
		for _, sk := range s.app.skillResult.Skills {
			commands = append(commands, acpAvailableCommand{
				Name:        "skill:" + sk.Name,
				Description: sk.Description,
			})
		}
	}
	s.sendUpdate(acpUpdate{
		SessionUpdate: "available_commands_update",
		Commands:      commands,
	})
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
		// Streaming deltas. Thinking deltas arrive here too, tagged with the
		// inner AssistantMessageEvent type; they surface as agent_thought_chunk
		// so hosts like AionUi can render the model's thinking process.
		ae, ok := event.AssistantMessageEvent.(agent.AssistantMessageEvent)
		if !ok || ae.Delta == "" {
			return
		}
		switch ae.Type {
		case "text_delta":
			s.sendUpdate(acpUpdate{
				SessionUpdate: "agent_message_chunk",
				Content:       map[string]string{"type": "text", "text": ae.Delta},
			})
		case "thinking_delta":
			s.sendUpdate(acpUpdate{
				SessionUpdate: "agent_thought_chunk",
				Content:       map[string]string{"type": "text", "text": ae.Delta},
			})
		}

	case agent.EventToolExecutionStart:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "tool_call",
			ToolCallID:    event.ToolCallID,
			Title:         event.ToolName,
			Kind:          toolCallKind(event.ToolName),
			Status:        "pending",
			RawInput:      event.Args,
		})

	case agent.EventToolExecutionEnd:
		status := "completed"
		if event.IsError {
			status = "error"
		}
		u := acpUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    event.ToolCallID,
			Title:         event.ToolName,
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

	case agent.EventCompactionStart, agent.EventCompactionEnd:
		status := "start"
		if event.Type == agent.EventCompactionEnd {
			status = "end"
		}
		s.sendUpdate(acpUpdate{
			SessionUpdate: "_compaction",
			Meta:          map[string]any{"status": status, "info": event.Compaction},
		})

	case agent.EventError:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "_error",
			Meta: map[string]any{
				"error":      event.Error,
				"errorStack": event.ErrorStack,
			},
		})

	case agent.EventLLMRetry:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "_llm_retry",
			Meta:          event.LLMRetry,
		})

	case agent.EventLoopGuardTriggered:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "_loop_guard",
			Meta:          event.LoopGuard,
		})

	case agent.EventToolCallRecovery:
		s.sendUpdate(acpUpdate{
			SessionUpdate: "_tool_call_recovery",
			Meta:          event.ToolCallRecovery,
		})

	case agent.EventAgentEnd:
		// ACP extension: universal turn-end signal. Emitted before the prompt
		// result so every client observes it before the response arrives.
		// Consumers: `ai watch` / `ai send --wait` exit detection, `ai ls`
		// idle detection (via the mirrored events.jsonl), TUI status.
		meta := map[string]any{"success": event.Error == ""}
		if event.Error != "" {
			meta["error"] = event.Error
		}
		s.sendUpdate(acpUpdate{SessionUpdate: "_turn_end", Meta: meta})

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
	if err := s.conn.WriteMessage(data); err != nil {
		slog.Error("[ACP] failed to write message", "error", err)
	}
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
