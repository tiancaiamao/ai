package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tiancaiamao/ai/pkg/run"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	aiconfig "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

// RPCConn manages a single `ai --mode rpc` subprocess, providing a client-side
// interface to the stdin/stdout JSON-RPC protocol.
type RPCConn struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	cancel  context.CancelFunc
	done    chan struct{} // closed when the reader goroutine exits
	alive   atomic.Bool

	mu       sync.Mutex // protects stdin writes
	promptMu sync.Mutex // serializes Prompt calls to prevent event mixing
	pending  map[string]chan *rpcResponseOrEvent

	// eventsCh receives non-response lines (events) from the subprocess.
	// Prompt callers consume these to collect turn_end text.
	eventsCh chan json.RawMessage
}

// rpcResponseOrEvent is a discriminated union used to deliver either a
// protocol response (matched by ID) or an event to waiting callers.
type rpcResponseOrEvent struct {
	resp *rpc.RPCResponse
	raw  json.RawMessage
}

// StartRPC launches an `ai --mode rpc` subprocess configured with the given
// session key, sessions directory, and system prompt file.
//
// The subprocess command is:
//
//	ai --mode rpc --session <sessionsDir>/<safeKey> --system-prompt @<systemPromptFile>
func StartRPC(sessionKey, sessionsDir, systemPromptFile, workingDir string) (*RPCConn, error) {
	sessionPath := sessionsDir + "/" + sessionKey
	systemPromptArg := "@" + systemPromptFile

		cmd := exec.Command("ai", "rpc",
		"--session", sessionPath,
		"--system-prompt", systemPromptArg,
	)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("rpc_client: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rpc_client: stdout pipe: %w", err)
	}
	// Route stderr to this process's stderr so subprocess logs are visible.
	cmd.Stderr = os.Stderr

	// Set working directory for the ai subprocess so it uses clawDir
	// instead of inheriting aiclaw's process CWD.
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rpc_client: start subprocess: %w", err)
	}

		_, cancel := context.WithCancel(context.Background())
	c := &RPCConn{
		cmd:     cmd,
		stdin:   stdinPipe,
		stdout:  stdoutPipe,
		cancel:  cancel,
		done:    make(chan struct{}),
		pending: make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	c.alive.Store(true)

	go c.readLoop()

	slog.Info("rpc_client: subprocess started", "pid", cmd.Process.Pid)
	return c, nil
}

// readLoop reads JSON lines from the subprocess stdout and dispatches them.
// Lines with a non-empty "id" field and type "response" are routed to the
// pending request channel. All other lines are forwarded to eventsCh.
func (c *RPCConn) readLoop() {
	defer close(c.done)
	scanner := bufio.NewScanner(c.stdout)
	// Match server buffer sizes for large payloads.
	buf := make([]byte, 0, 4*1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Peek at the type/id to classify the line.
		var hdr struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &hdr); err != nil {
			slog.Warn("rpc_client: unparseable line", "err", err, "line", string(line))
			continue
		}

		// Protocol responses carry an ID and type "response".
		if hdr.ID != "" && hdr.Type == "response" {
			var resp rpc.RPCResponse
			if err := json.Unmarshal(line, &resp); err != nil {
				slog.Warn("rpc_client: failed to decode response", "err", err)
				continue
			}
			c.dispatchResponse(hdr.ID, &resp)
			continue
		}

		// Everything else is an event.
		raw := make(json.RawMessage, len(line))
		copy(raw, line)
		select {
		case c.eventsCh <- raw:
		default:
			slog.Warn("rpc_client: events channel full, dropping event", "type", hdr.Type)
		}
	}

	// Scanner stopped — subprocess exited or stdout closed.
	c.alive.Store(false)
	slog.Info("rpc_client: readLoop exiting", "err", scanner.Err())
}

// dispatchResponse delivers a response to the goroutine waiting on the
// matching request ID.
func (c *RPCConn) dispatchResponse(id string, resp *rpc.RPCResponse) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if !ok {
		slog.Warn("rpc_client: response with no pending request", "id", id)
		return
	}
	ch <- &rpcResponseOrEvent{resp: resp}
}

// registerPending creates a channel and registers it under the given ID.
// The caller must call unregisterPending if the wait is abandoned.
func (c *RPCConn) registerPending(id string) <-chan *rpcResponseOrEvent {
	ch := make(chan *rpcResponseOrEvent, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

// unregisterPending removes a pending channel (e.g. on context cancellation).
func (c *RPCConn) unregisterPending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// sendCommand serialises and writes a JSON command to the subprocess stdin.
func (c *RPCConn) sendCommand(cmd any) error {
	if !c.alive.Load() {
		return fmt.Errorf("rpc_client: subprocess is not alive")
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("rpc_client: marshal command: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(data)
	if err != nil {
		return fmt.Errorf("rpc_client: write command: %w", err)
	}
	return nil
}

// Prompt sends a prompt command to the subprocess and waits for the agent to
// finish processing. It buffers text from turn_end events and returns the
// concatenated result once agent_end is received.
//
// Prompt calls are serialized per-connection via promptMu to prevent event
// mixing when multiple callers send prompts concurrently on the same RPCConn.
func (c *RPCConn) Prompt(ctx context.Context, message string) (string, error) {
	c.promptMu.Lock()
	defer c.promptMu.Unlock()

	if !c.alive.Load() {
		return "", fmt.Errorf("rpc_client: subprocess is not alive")
	}

	id := fmt.Sprintf("req-%d", time.Now().UnixNano())
	ch := c.registerPending(id)
	defer c.unregisterPending(id)

	// Build and send the prompt command.
	cmd := rpc.RPCCommand{
		ID:   id,
		Type: rpc.CommandPrompt,
		Data: mustMarshal(rpc.PromptRequest{Message: message}),
	}
	if err := c.sendCommand(cmd); err != nil {
		return "", err
	}

		// Wait for the initial response (acknowledgement).
	select {
	case msg := <-ch:
		if msg.resp == nil {
			return "", fmt.Errorf("rpc_client: unexpected event while waiting for response ack")
		}
		if !msg.resp.Success {
			return "", fmt.Errorf("rpc_client: prompt rejected: %s", msg.resp.Error)
		}
				// Slash commands (e.g. /model, /clear) return their result directly
		// in the response data without triggering the agent loop.
		// No turn_end/agent_end events will follow.
				if msg.resp.Data != nil {
			return run.FormatResponseData(msg.resp.Data), nil
		}
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.done:
		return "", fmt.Errorf("rpc_client: subprocess exited while waiting for response")
	}

	// Now buffer events until we see agent_end.
	var textParts []string
	for {
		select {
		case raw := <-c.eventsCh:
			et, err := parseEventType(raw)
			if err != nil {
				continue
			}
			switch et {
			case agent.EventTurnEnd:
				msg := extractTextFromTurnEnd(raw)
				if msg != "" {
					textParts = append(textParts, msg)
				}
			case agent.EventAgentEnd:
				// Agent finished — return concatenated text.
				result := ""
				for i, s := range textParts {
					if i > 0 {
						result += "\n"
					}
					result += s
				}
				return result, nil
			case agent.EventError:
				errMsg := extractErrorFromEvent(raw)
				if errMsg != "" {
					return "", fmt.Errorf("rpc_client: agent error: %s", errMsg)
				}
			}
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.done:
			return "", fmt.Errorf("rpc_client: subprocess exited during agent processing")
		}
	}
}

// Close sends a quit command to the subprocess, waits briefly for it to exit,
// then kills it if necessary.
func (c *RPCConn) Close() error {
	// Best-effort quit command.
	_ = c.sendCommand(map[string]string{"type": "quit"})

	c.cancel()

	// Close stdin to signal the subprocess.
	c.mu.Lock()
	_ = c.stdin.Close()
	c.mu.Unlock()

	// Wait for the reader goroutine to finish (with timeout).
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		slog.Warn("rpc_client: reader goroutine did not exit in time, killing subprocess")
	}

	// Kill the subprocess if still running.
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	c.alive.Store(false)
	return nil
}

// IsAlive returns true if the subprocess is believed to still be running.
func (c *RPCConn) IsAlive() bool {
	return c.alive.Load()
}

// --- helpers ---

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// parseEventType extracts the "type" field from a raw JSON message.
func parseEventType(raw json.RawMessage) (string, error) {
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return "", err
	}
	return peek.Type, nil
}

// extractTextFromTurnEnd extracts the assistant text from a turn_end event.
// The event shape is: {"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"..."}],...}}
func extractTextFromTurnEnd(raw json.RawMessage) string {
	var evt agent.AgentEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return ""
	}
	if evt.Message == nil {
		return ""
	}
	return extractTextFromAgentMessage(evt.Message)
}

// extractTextFromAgentMessage concatenates all text content blocks.
func extractTextFromAgentMessage(msg *aiconfig.AgentMessage) string {
	var result string
	for _, block := range msg.Content {
		if tc, ok := block.(aiconfig.TextContent); ok {
			result += tc.Text
		}
	}
	return result
}

// extractErrorFromEvent returns the error message from an error event.
func extractErrorFromEvent(raw json.RawMessage) string {
	var evt agent.AgentEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return ""
	}
	return evt.Error
}

// ---------------------------------------------------------------------------
// ConnManager — multi-connection pool keyed by sessionKey
// ---------------------------------------------------------------------------

// ConnManager manages a pool of RPCConn instances, one per sessionKey.
// It provides lazy creation and restart-on-failure with a single retry.
type ConnManager struct {
	conns       map[string]*RPCConn
	mu          sync.RWMutex
	sessionsDir string
	sysprompt   string // system prompt content (written to temp file per conn)
	workingDir  string // working directory for ai subprocess (clawDir)
}

// NewConnManager creates a new connection manager.
func NewConnManager(sessionsDir, sysprompt, workingDir string) *ConnManager {
	return &ConnManager{
		conns:       make(map[string]*RPCConn),
		sessionsDir: sessionsDir,
		sysprompt:   sysprompt,
		workingDir:  workingDir,
	}
}

// safeSessionKey replaces characters that are unsafe for filesystem paths.
func safeSessionKey(key string) string {
	s := key
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	return s
}

// Prompt sends a message to the ai subprocess for the given sessionKey.
// Lazy-creates the connection if needed. On failure, restarts and retries once.
func (m *ConnManager) Prompt(ctx context.Context, sessionKey, message string) (string, error) {
	conn, err := m.getOrCreateConn(sessionKey)
	if err != nil {
		return "", fmt.Errorf("conn_manager: failed to get connection for %q: %w", sessionKey, err)
	}

		result, err := conn.Prompt(ctx, message)
	if err == nil {
		return result, nil
	}

	// Do not retry on context cancellation/timeout — the prompt may already
	// be executing remotely and retrying would duplicate side effects.
	if ctx.Err() != nil {
		return "", fmt.Errorf("conn_manager: prompt failed for %q: %w", sessionKey, err)
	}

	// First attempt failed — restart and retry once.
	slog.Warn("conn_manager: prompt failed, restarting connection",
		"sessionKey", sessionKey, "err", err)
	m.restartConn(sessionKey)

	conn, err = m.getOrCreateConn(sessionKey)
	if err != nil {
		return "", fmt.Errorf("conn_manager: failed to recreate connection for %q: %w", sessionKey, err)
	}

	result, err = conn.Prompt(ctx, message)
	if err != nil {
		return "", fmt.Errorf("conn_manager: prompt failed after restart for %q: %w", sessionKey, err)
	}
	return result, nil
}

// CloseAll shuts down all managed connections.
func (m *ConnManager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for key, conn := range m.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		slog.Info("conn_manager: closed connection", "sessionKey", key)
		delete(m.conns, key)
	}
		return firstErr
}

// ListConnections returns all active session keys.
func (m *ConnManager) ListConnections() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.conns))
	for key := range m.conns {
		keys = append(keys, key)
	}
	return keys
}

// getOrCreateConn returns an existing connection or creates a new one.
func (m *ConnManager) getOrCreateConn(sessionKey string) (*RPCConn, error) {
	// Fast path: read lock to check for existing connection.
	m.mu.RLock()
	conn, ok := m.conns[sessionKey]
	if ok && conn.IsAlive() {
		m.mu.RUnlock()
		return conn, nil
	}
	m.mu.RUnlock()

	// Slow path: write lock to create or replace.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	conn, ok = m.conns[sessionKey]
	if ok && conn.IsAlive() {
		return conn, nil
	}

	// Create session directory.
	safeKey := safeSessionKey(sessionKey)
	sessionDir := m.sessionsDir + "/" + safeKey
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("conn_manager: create session dir %q: %w", sessionDir, err)
	}

		// Clean up stale system prompt temp files from previous connections.
	if oldFiles, _ := filepath.Glob(filepath.Join(sessionDir, "sysprompt-*.txt")); len(oldFiles) > 0 {
		for _, f := range oldFiles {
			os.Remove(f)
		}
	}

	// Write system prompt to a temp file in the session directory.
	promptFile, err := os.CreateTemp(sessionDir, "sysprompt-*.txt")
	if err != nil {
		return nil, fmt.Errorf("conn_manager: create temp prompt file: %w", err)
	}
	if _, err := promptFile.WriteString(m.sysprompt); err != nil {
		promptFile.Close()
		return nil, fmt.Errorf("conn_manager: write system prompt: %w", err)
	}
	if err := promptFile.Close(); err != nil {
		return nil, fmt.Errorf("conn_manager: close prompt file: %w", err)
	}

	// Launch the subprocess.
	conn, err = StartRPC(safeKey, m.sessionsDir, promptFile.Name(), m.workingDir)
	if err != nil {
		return nil, fmt.Errorf("conn_manager: start rpc for %q: %w", sessionKey, err)
	}

	m.conns[sessionKey] = conn
	slog.Info("conn_manager: created new connection", "sessionKey", sessionKey)
	return conn, nil
}

// restartConn kills the existing connection for sessionKey and removes it from the pool.
func (m *ConnManager) restartConn(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, ok := m.conns[sessionKey]
	if !ok {
		return
	}
	if err := conn.Close(); err != nil {
		slog.Warn("conn_manager: error closing connection during restart",
			"sessionKey", sessionKey, "err", err)
	}
		delete(m.conns, sessionKey)
	slog.Info("conn_manager: removed connection for restart", "sessionKey", sessionKey)
}

