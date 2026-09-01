package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// UnixSocket listens for ACP peers on a unix domain socket. Each accepted
// connection becomes an independent Conn (newline-delimited JSON-RPC), so the
// same ACP protocol code can serve multiple local clients (attach/watch)
// concurrently.
//
// The listener mechanics mirror the TUI socket server: stale-file cleanup,
// owner-only (0600) permissions, and one Conn per connection.
type UnixSocket struct {
	path     string
	listener net.Listener
}

// NewUnixSocket removes any stale socket file and starts listening on path.
func NewUnixSocket(path string) (*UnixSocket, error) {
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	// Restrict the socket to the owner so other local users cannot connect.
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", path, err)
	}
	return &UnixSocket{path: path, listener: l}, nil
}

// Accept blocks until the next peer connects and returns a Conn for it. It
// returns an error when the socket has been closed.
func (u *UnixSocket) Accept() (Conn, error) {
	conn, err := u.listener.Accept()
	if err != nil {
		return nil, err
	}
	return &socketConn{conn: conn, br: bufio.NewReader(conn)}, nil
}

// Close stops accepting and removes the socket file.
func (u *UnixSocket) Close() error {
	err := u.listener.Close()
	if rmErr := os.Remove(u.path); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

// Path returns the socket file path, for clients to dial.
func (u *UnixSocket) Path() string { return u.path }

// socketConn is a Conn over a single unix-socket connection using
// newline-delimited framing.
type socketConn struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

// ReadMessage returns the next newline-delimited message, or an error (io.EOF
// on close) when no more data is available.
func (s *socketConn) ReadMessage() (json.RawMessage, error) {
	line, err := s.br.ReadBytes('\n')
	if len(line) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	trimmed := bytes.TrimRight(line, "\r\n")
	// Return the line even if empty so blank-line handling stays consistent
	// with the stdio path (a blank line yields a parse error upstream).
	msg := make(json.RawMessage, len(trimmed))
	copy(msg, trimmed)
	return msg, nil
}

// WriteMessage writes msg followed by a newline, serialized across callers.
func (s *socketConn) WriteMessage(msg json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.conn.Write(msg); err != nil {
		return err
	}
	_, err := s.conn.Write([]byte{'\n'})
	return err
}

// Close closes the underlying connection.
func (s *socketConn) Close() error { return s.conn.Close() }
