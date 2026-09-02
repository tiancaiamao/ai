package transport

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"
)

// Hub is a Conn that multiplexes many ACP peers onto one logical connection,
// so a single ACP server (one agent) can serve several local clients at once
// (e.g. an `ai send` driver plus several `ai watch` observers over unix
// sockets).
//
// Routing rules:
//   - inbound requests (have an id) are rewritten to a hub-unique id before
//     they reach the server; the original id and owning conn are remembered so
//     the server's response can be routed back to exactly that conn.
//   - outbound responses (have an id) are routed to the originating conn with
//     the original id restored.
//   - outbound notifications (session/update, etc., no id) are broadcast to
//     every attached conn.
//
// The hub has no notion of "all clients gone => stop": it stays open until
// Close, which is what a long-lived serve process needs (clients may attach
// and detach at any time).
type Hub struct {
	mu         sync.Mutex
	closed     bool
	conns      []Conn
	routes     map[int64]route
	activeLoad *loadRoute
	nextID     int64
	incoming   chan inbound
	done       chan struct{}
}

// route remembers, for a hub-assigned id, which conn to answer on and what the
// client's original id was.
type route struct {
	connIdx int
	origID  []byte
	load    bool
	aborted bool
}

type loadRoute struct {
	uid     int64
	connIdx int
	updates [][]byte
	aborted bool
}

// inbound is one message read off a peer conn.
type inbound struct {
	connIdx int
	msg     []byte
}

// NewHub returns an empty hub ready to accept peers via AddConn.
func NewHub() *Hub {
	return &Hub{
		routes:   make(map[int64]route),
		incoming: make(chan inbound),
		done:     make(chan struct{}),
	}
}

// AddConn attaches a peer and starts reading from it in a background goroutine.
// It returns the conn's index, or -1 (with the conn closed) if the hub is
// already closed.
func (h *Hub) AddConn(c Conn) int {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		c.Close()
		return -1
	}
	idx := len(h.conns)
	h.conns = append(h.conns, c)
	h.mu.Unlock()

	go h.readLoop(idx, c)
	return idx
}

// readLoop drains one peer until it errors or the hub is closed, pushing
// (id-rewritten) messages onto the incoming channel.
func (h *Hub) readLoop(idx int, c Conn) {
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			h.removeConn(idx)
			return
		}

		out := msg
		if origID, isReq := extractRequestID(msg); isReq {
			load := requestMethod(msg) == "session/load"
			h.mu.Lock()
			if h.closed {
				h.mu.Unlock()
				return
			}
			h.nextID++
			uid := h.nextID
			h.routes[uid] = route{connIdx: idx, origID: origID, load: load}

			h.mu.Unlock()

			if rewritten, err := rewriteID(msg, uid); err == nil {
				out = rewritten
			}
		}

		select {
		case h.incoming <- inbound{connIdx: idx, msg: out}:
		case <-h.done:
			return
		}
	}
}

// ReadMessage implements Conn. It blocks until the next peer message is
// available, returning io.EOF once the hub is closed.
func (h *Hub) ReadMessage() ([]byte, error) {
	select {
	case ib := <-h.incoming:
		if uid, ok := requestID(ib.msg); ok && requestMethod(ib.msg) == "session/load" {
			h.mu.Lock()
			if rt, exists := h.routes[uid]; exists {
				h.activeLoad = &loadRoute{uid: uid, connIdx: rt.connIdx, aborted: rt.aborted}
			}
			h.mu.Unlock()
		}
		return ib.msg, nil

	case <-h.done:
		return nil, io.EOF
	}
}

// WriteMessage implements Conn. It classifies the server's outbound message and
// routes it: responses back to their originating conn (original id restored),
// notifications broadcast to all conns.
func (h *Hub) WriteMessage(msg []byte) error {
	m, err := parseObject(msg)
	if err != nil {
		return err
	}

	_, hasMethod := m["method"]
	id := m["id"]
	switch {
	case hasMethod:
		if requestMethod(msg) == "session/update" {
			return h.routeUpdate(msg)
		}
		h.broadcast(msg)
		return nil
	case len(id) == 0 || string(id) == "null":
		// Unroutable response (e.g. a parse-failure error with a null id): no
		// owning conn, drop it.
		return nil
	default:
		return h.routeResponse(msg, id)
	}
}

func (h *Hub) routeUpdate(msg []byte) error {
	h.mu.Lock()
	if h.activeLoad != nil {
		if h.activeLoad.aborted {
			h.mu.Unlock()
			return nil
		}
		if h.activeLoad.connIdx < len(h.conns) && h.conns[h.activeLoad.connIdx] != nil {
			h.activeLoad.updates = append(h.activeLoad.updates, append([]byte(nil), msg...))
			h.mu.Unlock()
			return nil
		}
		h.activeLoad.aborted = true
		h.activeLoad.updates = nil
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	h.broadcast(msg)
	return nil
}

// routeResponse sends a server response to the conn that issued the matching
// request, restoring the client's original id.
func (h *Hub) routeResponse(msg []byte, id []byte) error {
	uid, err := int64ID(id)
	if err != nil {
		slog.Debug("[Hub] unroutable response id", "id", string(id), "error", err)
		return nil
	}

	h.mu.Lock()
	rt, ok := h.routes[uid]
	if ok {
		delete(h.routes, uid)
	}
	var c Conn
	var replay [][]byte
	var aborted bool
	if ok && rt.load && h.activeLoad != nil && h.activeLoad.uid == uid {
		replay = h.activeLoad.updates
		aborted = h.activeLoad.aborted
		h.activeLoad = nil
	}
	if ok && rt.connIdx < len(h.conns) {
		c = h.conns[rt.connIdx]
	}
	h.mu.Unlock()
	if !ok {
		return nil
	}

	if rt.load {
		if !aborted && responseHasError(msg) {
			for _, update := range replay {
				h.broadcast(update)
			}
		} else if !aborted && c != nil {
			for _, update := range replay {
				if err := c.WriteMessage(update); err != nil {
					return err
				}
			}
		}
	}
	if c == nil {
		// Stale route (conn already left): nothing to deliver.
		return nil
	}

	out, err := rewriteID(msg, rt.origID)
	if err != nil {
		return err
	}
	return c.WriteMessage(out)
}

// broadcast fans a notification out to every currently attached conn.
func (h *Hub) broadcast(msg []byte) {
	h.mu.Lock()
	conns := make([]Conn, len(h.conns))
	copy(conns, h.conns)
	h.mu.Unlock()

	for _, c := range conns {
		if c == nil {
			continue
		}
		if err := c.WriteMessage(msg); err != nil {
			slog.Debug("[Hub] broadcast write failed", "error", err)
		}
	}
}

// removeConn marks a conn slot as gone and drops any of its in-flight routes.
func (h *Hub) removeConn(idx int) {
	h.mu.Lock()
	if idx < len(h.conns) {
		h.conns[idx] = nil
	}
	for uid, rt := range h.routes {
		if rt.connIdx == idx {
			if rt.load {
				rt.aborted = true
				h.routes[uid] = rt
			} else {
				delete(h.routes, uid)
			}
		}
	}
	if h.activeLoad != nil && h.activeLoad.connIdx == idx {
		h.activeLoad.aborted = true
		h.activeLoad.updates = nil
	}

	h.mu.Unlock()
}

// Close stops the hub and closes all attached conns. It is idempotent.
func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	close(h.done)
	conns := append([]Conn(nil), h.conns...)
	h.mu.Unlock()

	for _, c := range conns {
		if c != nil {
			c.Close()
		}
	}
	return nil
}

// --- JSON helpers ---

// parseObject decodes a JSON-RPC message into its raw fields.
func parseObject(msg []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// extractRequestID returns the raw id and true if msg is a request (carries a
// non-null id). Notifications (no id) return false.
func extractRequestID(msg []byte) ([]byte, bool) {
	m, err := parseObject(msg)
	if err != nil {
		return nil, false
	}
	id := m["id"]
	if len(id) == 0 || string(id) == "null" {
		return nil, false
	}
	return id, true
}

// requestID returns the hub-assigned numeric request id.
func requestID(msg []byte) (int64, bool) {
	id, ok := extractRequestID(msg)
	if !ok {
		return 0, false
	}
	uid, err := int64ID(id)
	return uid, err == nil
}

// requestMethod returns the JSON-RPC method, if present.
func requestMethod(msg []byte) string {
	m, err := parseObject(msg)
	if err != nil {
		return ""
	}
	var method string
	_ = json.Unmarshal(m["method"], &method)
	return method
}

// int64ID parses a JSON number id.
func int64ID(id []byte) (int64, error) {
	var n int64
	if err := json.Unmarshal(id, &n); err != nil {
		return 0, err
	}
	return n, nil
}

func responseHasError(msg []byte) bool {
	m, err := parseObject(msg)
	return err == nil && len(m["error"]) > 0 && string(m["error"]) != "null"
}

// rewriteID returns msg with its "id" field replaced by v, preserving all other
// fields verbatim (as raw JSON).
func rewriteID(msg []byte, v any) ([]byte, error) {
	m, err := parseObject(msg)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if id, ok := v.([]byte); ok {
		raw = append(json.RawMessage(nil), id...)
	} else {
		raw, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	m["id"] = raw
	return json.Marshal(m)
}
