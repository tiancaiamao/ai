package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"sync"
)

// NewNetConn adapts an established net.Conn (e.g. a dialed unix-socket
// connection) to the Conn interface, using the same newline-delimited JSON-RPC
// framing as the server-side transports. This is what local ACP clients use to
// talk to an `ai serve` process over its control socket.
func NewNetConn(c net.Conn) Conn {
	return &netConn{conn: c, br: bufio.NewReader(c)}
}

type netConn struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

func (n *netConn) ReadMessage() (json.RawMessage, error) {
	line, err := n.br.ReadBytes('\n')
	if len(line) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	trimmed := bytes.TrimRight(line, "\r\n")
	msg := make(json.RawMessage, len(trimmed))
	copy(msg, trimmed)
	return msg, nil
}

func (n *netConn) WriteMessage(msg json.RawMessage) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, err := n.conn.Write(msg); err != nil {
		return err
	}
	_, err := n.conn.Write([]byte{'\n'})
	return err
}

func (n *netConn) Close() error { return n.conn.Close() }
