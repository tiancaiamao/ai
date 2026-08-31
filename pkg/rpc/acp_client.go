package rpc

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiancaiamao/ai/pkg/transport"
)

// ACP client: talks the ACP protocol to an agent hosted over any transport
// (typically a unix socket served by `ai serve`). It is the local counterpart
// of acpServer and is used by `ai run`, `ai watch` and `ai send` so that all
// clients share one protocol with agent-shell and other ACP hosts.
//
// Concurrency model: one read loop owns conn.ReadMessage. Outbound messages
// (requests + notifications) go through conn.WriteMessage, which serializes.
// Responses are routed to the matching pending request; session/update
// notifications are pushed onto the Updates channel for the caller to consume.

// ACPUpdate mirrors the server's session/update payload (pkg/rpc acpUpdate).
type ACPUpdate struct {
	SessionUpdate string         `json:"sessionUpdate"`
	Content       any            `json:"content,omitempty"`
	ToolCallID    string         `json:"toolCallId,omitempty"`
	Title         string         `json:"title,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Status        string         `json:"status,omitempty"`
	RawInput      map[string]any `json:"rawInput,omitempty"`
	Commands      []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"commands,omitempty"`
	Meta any `json:"_meta,omitempty"`
}

// ACPUpdateSessionLoadEnd is an internal update emitted after a successful
// session/load response. It is not sent on the wire and lets stream consumers
// distinguish replay completion from a closed connection.
const ACPUpdateSessionLoadEnd = "_session_load_end"

// ACPClient is a single-connection ACP client.
type pendingRequest struct {
	ch     chan json.RawMessage
	method string
}

type ACPClient struct {
	conn     transport.Conn
	mu       sync.Mutex
	pending  map[int64]pendingRequest
	nextID   int64
	updates  chan ACPUpdate
	readDone chan struct{}
}

// NewACPClient starts the read loop over conn. The caller must Close the
// client when done.
func NewACPClient(conn transport.Conn) *ACPClient {
	c := &ACPClient{
		conn:     conn,
		pending:  make(map[int64]pendingRequest),
		nextID:   1,
		updates:  make(chan ACPUpdate, 1024),
		readDone: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Updates returns the channel of incoming session/update notifications.
func (c *ACPClient) Updates() <-chan ACPUpdate { return c.updates }

// Close closes the underlying connection; the read loop exits on EOF and the
// Updates channel is closed. Pending requests fail with "connection closed".
func (c *ACPClient) Close() error {
	err := c.conn.Close()
	<-c.readDone
	return err
}

// Initialize performs the ACP handshake.
func (c *ACPClient) Initialize() error {
	var raw json.RawMessage
	if err := c.request("initialize", map[string]any{
		"protocolVersion":    acpProtocolVersion,
		"clientCapabilities": map[string]any{},
	}, &raw); err != nil {
		return err
	}
	return nil
}

// NewSession returns the sessionId of the agent's current session. (The agent
// binds one session per process; this call establishes the protocol-level
// association and receives the available-commands catalog.)
func (c *ACPClient) NewSession() (string, error) {
	var raw json.RawMessage
	if err := c.request("session/new", map[string]any{}, &raw); err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse session/new result: %w", err)
	}
	return result.SessionID, nil
}

// LoadSession resumes a persisted session and blocks until the history
// replay finishes. Replay updates are still pushed onto the Updates channel.
func (c *ACPClient) LoadSession(sessionID string) error {
	return c.request("session/load", map[string]any{
		"sessionId": sessionID,
	}, nil)
}

// Prompt sends a text prompt and blocks until the turn completes, returning
// the stop reason ("end_turn" or "cancelled").
func (c *ACPClient) Prompt(sessionID, text string) (string, error) {
	var raw json.RawMessage
	if err := c.request("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []acpContentBlock{{Type: "text", Text: text}},
	}, &raw); err != nil {
		return "", err
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse prompt result: %w", err)
	}
	return result.StopReason, nil
}

// PromptAsync sends a text prompt without waiting for the turn to complete.
// The deferred response is dropped by the read loop (no pending entry is
// registered); turn completion is observed via the _turn_end update on the
// Updates channel.
func (c *ACPClient) PromptAsync(sessionID, text string) error {
	id := atomic.AddInt64(&c.nextID, 1)
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: "session/prompt", Params: map[string]any{
		"sessionId": sessionID,
		"prompt":    []acpContentBlock{{Type: "text", Text: text}},
	}})
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(body)
}

// Cancel aborts the in-flight turn (notification; no response).
func (c *ACPClient) Cancel(sessionID string) error {
	return c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// DialACP connects to a unix socket serving an ACP agent, performs the
// handshake (initialize + session/new) and returns the client together with
// the agent's session id. Callers must Close the client when done.
func DialACP(sockPath string) (*ACPClient, string, error) {
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, "", err
	}
	c := NewACPClient(transport.NewNetConn(conn))
	if err := c.Initialize(); err != nil {
		c.Close()
		return nil, "", err
	}
	sid, err := c.NewSession()
	if err != nil {
		c.Close()
		return nil, "", err
	}
	return c, sid, nil
}

// --- internals ---

type acpClientResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// request sends a request and blocks until the response arrives.
func (c *ACPClient) request(method string, params any, result *json.RawMessage) error {
	id := atomic.AddInt64(&c.nextID, 1)

	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}

	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[id] = pendingRequest{ch: ch, method: method}
	c.mu.Unlock()

	if err := c.conn.WriteMessage(body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case raw, ok := <-ch:
		if !ok {
			return fmt.Errorf("connection closed while waiting for %s", method)
		}
		var resp acpClientResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return err
		}
		if resp.Error != nil {
			return fmt.Errorf("%s failed: %s", method, resp.Error.Message)
		}
		if result != nil {
			*result = resp.Result
		}
		return nil
	case <-c.readDone:
		return fmt.Errorf("connection closed while waiting for %s", method)
	}
}

// notify sends a fire-and-forget notification.
func (c *ACPClient) notify(method string, params any) error {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(body)
}

// readLoop reads messages until the connection dies, then closes all pending
// requests and the Updates channel.
func (c *ACPClient) readLoop() {
	defer close(c.readDone)
	defer close(c.updates)

	failPending := func() {
		c.mu.Lock()
		chs := c.pending
		c.pending = make(map[int64]pendingRequest)
		c.mu.Unlock()
		for _, pending := range chs {
			close(pending.ch)
		}
	}

	for {
		msg, err := c.conn.ReadMessage()
		if err != nil {
			failPending()
			return
		}

		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}

		// Response: carries an id, no method.
		if envelope.Method == "" {
			var id int64
			if err := json.Unmarshal(envelope.ID, &id); err != nil {
				continue
			}
			c.mu.Lock()
			pending, ok := c.pending[id]
			if ok {
				delete(c.pending, id)
			}
			c.mu.Unlock()
			if ok {
				if pending.method == "session/load" {
					var response acpClientResponse
					if err := json.Unmarshal(msg, &response); err == nil && response.Error == nil {
						// The server sends replay updates before the session/load response.
						// Queue the internal boundary after all replay updates in Updates().
						pending.ch <- msg
						c.updates <- ACPUpdate{SessionUpdate: ACPUpdateSessionLoadEnd}
						continue
					}
				}
				pending.ch <- msg
			}
			continue
		}

		// Notification: session/update.
		if envelope.Method != "session/update" {
			continue
		}
		var params struct {
			SessionID string    `json:"sessionId"`
			Update    ACPUpdate `json:"update"`
		}
		if err := json.Unmarshal(envelope.Params, &params); err != nil {
			continue
		}
		c.updates <- params.Update
	}
}
