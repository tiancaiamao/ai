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
// notifications are queued for the caller without blocking response handling.

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

const ACPUpdateRequestError = "_request_error"

type ACPUpdateError struct {
	Method  string `json:"method"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ACPClient is a single-connection ACP client.
type pendingRequest struct {
	ch     chan json.RawMessage
	method string
	async  bool
}

type ACPClient struct {
	conn       transport.Conn
	mu         sync.Mutex
	pending    map[int64]pendingRequest
	nextID     int64
	updates    chan ACPUpdate
	updateMu   sync.Mutex
	updateQ    []ACPUpdate
	updateWake chan struct{}
	updateStop chan struct{}
	updateOnce sync.Once
	updateDone chan struct{}

	readDone chan struct{}

	closedOnce sync.Once
}

// NewACPClient starts the read loop over conn. The caller must Close the
// client when done.
func NewACPClient(conn transport.Conn) *ACPClient {
	c := &ACPClient{
		conn: conn, pending: make(map[int64]pendingRequest), nextID: 1,
		updates:    make(chan ACPUpdate, 1024),
		updateWake: make(chan struct{}, 1), updateStop: make(chan struct{}),
		updateDone: make(chan struct{}), readDone: make(chan struct{}),
	}
	go c.updateLoop()
	go c.readLoop()
	return c
}

// Updates returns the channel of incoming session/update notifications.
func (c *ACPClient) Updates() <-chan ACPUpdate { return c.updates }

// Close closes the underlying connection; the read loop exits on EOF and the
// Updates channel is closed. Pending requests fail with "connection closed".
func (c *ACPClient) Close() error {
	var err error
	c.closedOnce.Do(func() {
		c.updateOnce.Do(func() { close(c.updateStop) })
		err = c.conn.Close()
		<-c.readDone
		<-c.updateDone
	})
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

// PromptAsync sends a request without waiting for the turn to complete. A
// server-side error is delivered as an ACPUpdateRequestError update.
func (c *ACPClient) PromptAsync(sessionID, text string) error {
	return c.requestAsync("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []acpContentBlock{{Type: "text", Text: text}},
	})
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

	body, err := c.encodeRequest(id, method, params)
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

	raw, ok := <-ch
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
}

func (c *ACPClient) requestAsync(method string, params any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	body, err := c.encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.pending[id] = pendingRequest{method: method, async: true}
	c.mu.Unlock()
	if err := c.conn.WriteMessage(body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *ACPClient) encodeRequest(id int64, method string, params any) ([]byte, error) {
	return json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})
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

func (c *ACPClient) stopUpdates() {
	c.updateOnce.Do(func() { close(c.updateStop) })
}

func (c *ACPClient) enqueueUpdate(update ACPUpdate) {
	c.updateMu.Lock()
	c.updateQ = append(c.updateQ, update)
	c.updateMu.Unlock()
	select {
	case c.updateWake <- struct{}{}:
	default:
	}
}

func (c *ACPClient) updateLoop() {
	defer close(c.updateDone)
	defer close(c.updates)
	for {
		c.updateMu.Lock()
		if len(c.updateQ) > 0 {
			u := c.updateQ[0]
			c.updateQ[0] = ACPUpdate{}
			c.updateQ = c.updateQ[1:]
			c.updateMu.Unlock()
			select {
			case c.updates <- u:
			case <-c.readDone:
				select {
				case c.updates <- u:
				default:
					return
				}
			}
			continue
		}
		c.updateMu.Unlock()
		select {
		case <-c.updateWake:
		case <-c.updateStop:
			<-c.readDone
			for {
				c.updateMu.Lock()
				if len(c.updateQ) == 0 {
					c.updateMu.Unlock()
					return
				}
				u := c.updateQ[0]
				c.updateQ[0] = ACPUpdate{}
				c.updateQ = c.updateQ[1:]
				c.updateMu.Unlock()
				select {
				case c.updates <- u:
				default:
					return
				}
			}
		}
	}
}

func (c *ACPClient) readLoop() {
	defer close(c.readDone)
	defer c.stopUpdates()
	failPending := func() {
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]pendingRequest)
		c.mu.Unlock()
		for _, p := range pending {
			if p.ch != nil {
				close(p.ch)
			}
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
		if json.Unmarshal(msg, &envelope) != nil {
			continue
		}
		if envelope.Method == "" {
			var id int64
			if json.Unmarshal(envelope.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			p, ok := c.pending[id]
			if ok {
				delete(c.pending, id)
			}
			c.mu.Unlock()
			if !ok {
				continue
			}
			var response acpClientResponse
			if json.Unmarshal(msg, &response) != nil {
				if p.ch != nil {
					p.ch <- msg
				}
				continue
			}
			if p.async {
				if response.Error != nil {
					c.enqueueUpdate(ACPUpdate{SessionUpdate: ACPUpdateRequestError, Meta: ACPUpdateError{Method: p.method, Code: response.Error.Code, Message: response.Error.Message}})
				}
				continue
			}
			if p.method == "session/load" && response.Error == nil {
				c.enqueueUpdate(ACPUpdate{SessionUpdate: ACPUpdateSessionLoadEnd})
			}
			p.ch <- msg
			continue
		}
		if envelope.Method != "session/update" {
			continue
		}
		var params struct {
			SessionID string    `json:"sessionId"`
			Update    ACPUpdate `json:"update"`
		}
		if json.Unmarshal(envelope.Params, &params) == nil {
			c.enqueueUpdate(params.Update)
		}
	}
}
