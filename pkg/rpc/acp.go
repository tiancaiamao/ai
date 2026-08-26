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
//
// Everything else (fs/*, terminal/*, image/audio content, MCP transports) is
// deliberately NOT advertised and rejected as method not found.
// mcpServers in session/new are accepted and ignored (logged).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/config"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
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
	SessionUpdate string                `json:"sessionUpdate"`
	Content       any                   `json:"content,omitempty"`
	ToolCallID    string                `json:"toolCallId,omitempty"`
	Title         string                `json:"title,omitempty"`
	Kind          string                `json:"kind,omitempty"`
	Status        string                `json:"status,omitempty"`
	Commands      []acpAvailableCommand `json:"commands,omitempty"`
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
	case "session/load":
		s.handleSessionLoad(req)
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
	s.sendResult(req.ID, map[string]string{"sessionId": s.sessionID})
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
	s.sendResult(req.ID, map[string]string{"stopReason": acpStopEndTurn})
}

// replayHistory converts persisted conversation history into ACP session/update
// notifications: user messages become user_message_chunk, assistant text
// becomes agent_message_chunk, tool calls become tool_call + tool_call_update.
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
func (s *acpServer) sendReplayText(updateKind string, blocks []agentctx.ContentBlock) {
	for _, block := range blocks {
		tc, ok := block.(agentctx.TextContent)
		if !ok || tc.Text == "" {
			continue
		}
		s.sendUpdate(acpUpdate{
			SessionUpdate: updateKind,
			Content:       map[string]string{"type": "text", "text": tc.Text},
		})
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

// formatACPCommandResult renders a slash-handler result for display. A
// per-command renderer (acpCommandRenderers) produces human-readable text for
// high-frequency read-only commands; strings pass through verbatim and
// everything else falls back to pretty-printed JSON.
func formatACPCommandResult(name string, result any) string {
	if r, ok := acpCommandRenderers[name]; ok {
		if s := r(result); s != "" {
			return s
		}
	}
	switch v := result.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

// acpCommandRenderer renders a slash-command result as human-readable text
// for ACP clients (GUI clients have no TUI-style renderer of their own).
// Renderers must be pure formatters: no app/server access, no business logic.
// Return "" on unexpected shapes so formatACPCommandResult falls back to JSON.
type acpCommandRenderer func(result any) string

var acpCommandRenderers = map[string]acpCommandRenderer{
	"context": renderContextText,
	"help":    renderHelpText,
	"model":   renderModelListText,
	"resume":  renderResumeText,
	"session": renderSessionStateText,
	"show":    renderShowSettingsText,
	"skills":  renderSkillsText,
}

// sessionStatusLine is the shared one-line summary:
// "Model: <model> · Session: <id[:8]> · Streaming: <status>".
func sessionStatusLine(state *SessionState) string {
	return fmt.Sprintf("Model: %s · Session: %s · Streaming: %s",
		modelDisplayName(state.Model), shortID(state.SessionID), streamingStatus(state))
}

// renderSessionStateText renders /session as the status line plus one detail
// line per non-empty field: session name, workspace, thinking level, message
// counts, and token usage when compaction state is known.
func renderSessionStateText(result any) string {
	state, ok := result.(*SessionState)
	if !ok || state == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(sessionStatusLine(state))
	if state.SessionName != "" {
		fmt.Fprintf(&b, "\nName: %s", state.SessionName)
	}
	if w := state.AIWorkingDir; w != "" {
		fmt.Fprintf(&b, "\nWorkspace: %s", w)
	}
	if state.ThinkingLevel != "" {
		fmt.Fprintf(&b, "\nThinking: %s", state.ThinkingLevel)
	}
	b.WriteString(fmt.Sprintf("\nMessages: %d", state.MessageCount))
	if state.PendingMessageCount > 0 {
		fmt.Fprintf(&b, " (%d pending)", state.PendingMessageCount)
	}
	if state.IsCompacting {
		b.WriteString("\nCompacting: running")
	} else if !state.AutoCompactionEnabled {
		b.WriteString("\nAuto-compaction: off")
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return ""
	}
	return out
}

// renderContextText renders /context as the session status line followed by
// message and token usage (the model list is /model's job).
func renderContextText(result any) string {
	m, ok := result.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	state, _ := m["state"].(*SessionState)
	stats, _ := m["stats"].(*SessionStats)
	if state == nil || stats == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(sessionStatusLine(state))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Messages: %d user · %d assistant · %d tool calls\n",
		statsUserMessages(stats), statsAssistantMessages(stats), stats.ToolCalls)
	b.WriteString(renderTokenUsageLine(state, stats))
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return ""
	}
	return out
}

// renderResumeText renders both /resume shapes: the no-arg session list as an
// aligned table (index usable as the /resume <index> argument), or a session
// switch confirmation.
func renderResumeText(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	if id, _ := m["sessionId"].(string); id != "" {
		name, _ := m["sessionName"].(string)
		if name != "" {
			return fmt.Sprintf("Switched to session %s (%s)", name, shortID(id))
		}
		return fmt.Sprintf("Switched to session %s", shortID(id))
	}
	sessions, ok := m["sessions"].([]session.SessionMeta)
	if !ok {
		return ""
	}
	rows := make([]nameDesc, 0, len(sessions))
	for i, s := range sessions {
		title := s.Title
		if title == "" {
			title = s.Name
		}
		if title == "" {
			title = "(untitled)"
		}
		rows = append(rows, nameDesc{
			name: fmt.Sprintf("%d. %s [%s]", i, truncateRunes(title, 48), shortID(s.ID)),
			desc: fmt.Sprintf("%d msgs, updated %s", s.MessageCount, s.UpdatedAt.Format("2006-01-02 15:04")),
		})
	}
	return renderNameDescTable("Sessions (resume with /resume <index>):", rows)
}

// truncateRunes caps s at max runes, appending "…" when truncated.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// renderTokenUsageLine renders one line of token usage from session stats,
// appending the share of the context window when both numbers are known.
func renderTokenUsageLine(state *SessionState, stats *SessionStats) string {
	t := stats.Tokens
	line := fmt.Sprintf("Tokens: in %d · out %d · cache %d/%d · total %d",
		t.Input, t.Output, t.CacheRead, t.CacheWrite, t.Total)
	if t.ActiveWindowTokens > 0 && state.Compaction != nil && state.Compaction.ContextWindow > 0 {
		line += fmt.Sprintf(" (%d%% of window)",
			100*t.ActiveWindowTokens/state.Compaction.ContextWindow)
	}
	return line
}

func statsUserMessages(stats *SessionStats) int {
	if stats == nil {
		return 0
	}
	return stats.UserMessages
}

func statsAssistantMessages(stats *SessionStats) int {
	if stats == nil {
		return 0
	}
	return stats.AssistantMessages
}

// renderShowSettingsText renders /show settings as aligned "key: value" lines.
// Only settings-shaped results render; anything else returns "" (JSON fallback).
func renderShowSettingsText(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	if t, _ := m["type"].(string); t != "settings" {
		return ""
	}
	data, ok := m["data"].(map[string]any)
	if !ok || len(data) == 0 {
		return ""
	}

	keys := make([]string, 0, len(data))
	width := 0
	for k := range data {
		keys = append(keys, k)
		if len(k) > width {
			width = len(k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%-*s: %v\n", width, k, data[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderHelpText renders /help as "<name>  —  <description>" per command.
func renderHelpText(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	cmds, ok := m["commands"].([]command.CommandInfo)
	if !ok {
		return ""
	}
	pairs := make([]nameDesc, 0, len(cmds))
	for _, c := range cmds {
		pairs = append(pairs, nameDesc{name: c.Name, desc: c.Description})
	}
	return renderNameDescTable("Available commands:", pairs)
}

// renderSkillsText renders /skills in the same table style as /help.
func renderSkillsText(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	skills, ok := m["commands"].([]SlashCommand)
	if !ok {
		return ""
	}
	pairs := make([]nameDesc, 0, len(skills))
	for _, s := range skills {
		pairs = append(pairs, nameDesc{name: s.Name, desc: s.Description})
	}
	return renderNameDescTable("Available skills:", pairs)
}

// nameDesc is one row of the shared help/skills table rendering.
type nameDesc struct {
	name string
	desc string
}

// renderNameDescTable aligns names in one column with descriptions after an
// em dash. Returns "" when there are no rows so callers fall back to JSON.
func renderNameDescTable(header string, rows []nameDesc) string {
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, r := range rows {
		if len(r.name) > width {
			width = len(r.name)
		}
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s  —  %s\n", width, r.name, r.desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// modelList is the extracted shape of a /model list result.
type modelList struct {
	models       []config.ModelInfo
	currentIndex int
}

// modelListFromResult extracts the models/currentIndex pair from a
// handleModelList-style result map; zero value on any shape mismatch.
func modelListFromResult(result any) modelList {
	m, ok := result.(map[string]any)
	if !ok {
		return modelList{}
	}
	models, ok := m["models"].([]config.ModelInfo)
	if !ok {
		return modelList{}
	}
	idx, _ := m["currentIndex"].(int)
	return modelList{models: models, currentIndex: idx}
}

// renderModelListText renders /model as one model per line, marking the
// current model with "*".
func renderModelListText(result any) string {
	list := modelListFromResult(result)
	if len(list.models) == 0 {
		return ""
	}
	return renderModelTable(list)
}

// renderModelTable renders the model list; the current model gets a "*".
func renderModelTable(list modelList) string {
	var b strings.Builder
	b.WriteString("Models (* marks current):")
	b.WriteByte('\n')
	for i, m := range list.models {
		marker := " "
		if i == list.currentIndex {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %s\n", marker, modelDisplayName(&m))
	}
	return strings.TrimRight(b.String(), "\n")
}

// modelDisplayName formats a model as "provider/id" (or just "id" when the
// provider is unset); unknown/nil models degrade to "unknown".
func modelDisplayName(m *config.ModelInfo) string {
	if m == nil || m.ID == "" {
		return "unknown"
	}
	if m.Provider == "" {
		return m.ID
	}
	return m.Provider + "/" + m.ID
}

// shortID truncates a session id to its first 8 characters for summaries.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// streamingStatus describes the streaming state in one word.
func streamingStatus(state *SessionState) string {
	switch {
	case state.IsStreaming:
		return "active"
	case state.IsCompacting:
		return "compacting"
	default:
		return "idle"
	}
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
