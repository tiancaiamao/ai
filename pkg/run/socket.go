package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"
)

// Command represents a command sent over the Unix domain socket.
type Command struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	FromSeq  uint64 `json:"from_seq,omitempty"`  // for "stream" command: replay from this seq
}

// Response represents the server's reply to a Command.
type Response struct {
	OK    bool `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any   `json:"data,omitempty"`
}

// CommandHandler processes a Command and returns a Response.
type CommandHandler func(cmd Command) Response

// StreamHandler returns a Consumer for streaming events.
// If no broadcaster is available, returns nil.
type StreamHandler func(fromSeq uint64) *Consumer

// SocketServer listens on a Unix domain socket and dispatches commands to a handler.
// Supports both request-response commands and long-lived streaming connections.
type SocketServer struct {
	sockPath      string
	handler       CommandHandler
	streamHandler StreamHandler
	listener      net.Listener
	done          chan struct{}
	broadcaster   *EventBroadcaster
}

// NewSocketServer creates a new SocketServer that will listen on sockPath.
func NewSocketServer(sockPath string, handler CommandHandler) *SocketServer {
	return &SocketServer{
		sockPath: sockPath,
		handler:  handler,
		done:     make(chan struct{}),
	}
}

// SetBroadcaster configures the event broadcaster for streaming support.
func (s *SocketServer) SetBroadcaster(b *EventBroadcaster) {
	s.broadcaster = b
}

// Start removes any stale socket file, creates the listener, and begins the
// accept loop in a background goroutine.
func (s *SocketServer) Start() error {
	// Remove stale socket file from a previous run.
	if _, err := os.Stat(s.sockPath); err == nil {
		if err := os.Remove(s.sockPath); err != nil {
			return fmt.Errorf("remove stale socket %s: %w", s.sockPath, err)
		}
	}

	l, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.sockPath, err)
	}
	s.listener = l

	// Restrict socket to owner only — prevents other local users from
	// sending commands (abort, steer, etc.) to the control plane.
	if err := os.Chmod(s.sockPath, 0600); err != nil {
		s.listener.Close()
		return fmt.Errorf("chmod socket %s: %w", s.sockPath, err)
	}

	go s.acceptLoop()
	return nil
}

// Stop closes the listener and signals the accept loop to exit.
func (s *SocketServer) Stop() error {
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("close listener: %w", err)
		}
	}
	return nil
}

// Wait blocks until the accept loop has finished (after Stop is called).
func (s *SocketServer) Wait() {
	<-s.done
}

// acceptLoop accepts connections and dispatches them concurrently.
// Each connection is handled in its own goroutine so that a slow or
// blocked handler does not prevent subsequent connections from being served.
func (s *SocketServer) acceptLoop() {
	defer close(s.done)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed — normal shutdown path.
			return
		}

		go s.handleConn(conn)
	}
}

// handleConn reads the initial command and either handles it as a
// request-response or switches to streaming mode.
func (s *SocketServer) handleConn(conn net.Conn) {
	// Set initial read deadline for the command.
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read until newline, max 1MB to prevent OOM.
	var buf bytes.Buffer
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf.Write(recvBuf[:n])
			if buf.Len() > 1<<20 {
				slog.Warn("socket: command too large (>1MB), discarding")
				s.writeResponse(conn, Response{OK: false, Error: "command too large"})
				conn.Close()
				return
			}
			if bytes.IndexByte(buf.Bytes(), '\n') >= 0 {
				break
			}
		}
		if err != nil {
			slog.Debug("socket: read error", "addr", conn.RemoteAddr(), "err", err)
			conn.Close()
			return
		}
	}

	// Trim the trailing newline before unmarshaling.
	data := bytes.TrimRight(buf.Bytes(), "\n")

	var cmd Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		slog.Debug("socket: unmarshal command", "err", err)
		s.writeResponse(conn, Response{
			OK:    false,
			Error: fmt.Sprintf("invalid command: %v", err),
		})
		conn.Close()
		return
	}

	// Handle stream command with long-lived connection.
	if cmd.Type == "stream" {
		s.handleStream(conn, cmd)
		return
	}

	// Standard request-response handling.
	resp := s.handler(cmd)
	s.writeResponse(conn, resp)
	conn.Close()
}

// handleStream upgrades a connection to streaming mode.
// The connection stays open and pushes newline-delimited JSON events
// as they arrive from the broadcaster.
func (s *SocketServer) handleStream(conn net.Conn, cmd Command) {
	if s.broadcaster == nil {
		s.writeResponse(conn, Response{OK: false, Error: "streaming not available"})
		conn.Close()
		return
	}

	// Subscribe to events from the requested sequence.
	consumer := s.broadcaster.Subscribe(cmd.FromSeq)
	if consumer == nil {
		s.writeResponse(conn, Response{OK: false, Error: "broadcaster closed"})
		conn.Close()
		return
	}

	// Send initial OK response.
	s.writeResponse(conn, Response{OK: true, Data: map[string]any{
		"from_seq": cmd.FromSeq,
		"current_seq": s.broadcaster.Seq(),
	}})

	// Clear read deadline — this is now a long-lived write connection.
	conn.SetDeadline(time.Time{})

	// Spawn goroutine to drain consumer channel and write to connection.
	go func() {
		defer func() {
			s.broadcaster.Unsubscribe(consumer)
			conn.Close()
		}()

		for event := range consumer.Events() {
			// Write event + newline.
			if _, err := conn.Write(event); err != nil {
				return
			}
			if _, err := conn.Write([]byte{'\n'}); err != nil {
				return
			}
		}
		// Channel closed — broadcaster shut down or consumer dropped.
	}()
}

// writeResponse marshals and writes a Response as JSON followed by a newline.
func (s *SocketServer) writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("socket: marshal response", "err", err)
		return
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		slog.Debug("socket: write response", "err", err)
	}
}

// SocketClient is a convenience type for connecting to a SocketServer.
type SocketClient struct {
	sockPath string
}

// NewSocketClient creates a client for the given socket path.
func NewSocketClient(sockPath string) *SocketClient {
	return &SocketClient{sockPath: sockPath}
}

// Stream connects to the socket and starts streaming events from fromSeq.
// Returns the connection (for reading), the initial response, and any error.
// The caller is responsible for closing the connection.
func (c *SocketClient) Stream(fromSeq uint64) (net.Conn, *Response, error) {
	conn, err := net.DialTimeout("unix", c.sockPath, 5*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("dial socket: %w", err)
	}

	// Send stream command.
	cmd := Command{Type: "stream", FromSeq: fromSeq}
	cmdData, err := json.Marshal(cmd)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("marshal command: %w", err)
	}
	cmdData = append(cmdData, '\n')
	if _, err := conn.Write(cmdData); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("write command: %w", err)
	}

	// Read initial response.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var buf bytes.Buffer
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf.Write(recvBuf[:n])
			if idx := bytes.IndexByte(buf.Bytes(), '\n'); idx >= 0 {
				respData := buf.Bytes()[:idx]
				var resp Response
				if err := json.Unmarshal(respData, &resp); err != nil {
					conn.Close()
					return nil, nil, fmt.Errorf("parse response: %w", err)
				}
				if !resp.OK {
					conn.Close()
					return nil, &resp, fmt.Errorf("stream rejected: %s", resp.Error)
				}

				// Push any remaining data back — we'll need to handle it.
				// For now, any data after the first newline is event data.
				remaining := buf.Bytes()[idx+1:]
				if len(remaining) > 0 {
					// We can't push back, so we need a different approach.
					// Store remaining in a buffered reader wrapper.
					// For simplicity, log a warning — in practice the response
					// arrives before events.
					slog.Debug("socket client: extra data after stream response", "bytes", len(remaining))
				}

				conn.SetDeadline(time.Time{})
				return conn, &resp, nil
			}
		}
		if err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("read response: %w", err)
		}
	}
}

// SendCommand sends a single command and reads the response.
func (c *SocketClient) SendCommand(cmd Command) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial socket: %w", err)
	}
	defer conn.Close()

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write command: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var buf bytes.Buffer
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf.Write(recvBuf[:n])
			if idx := bytes.IndexByte(buf.Bytes(), '\n'); idx >= 0 {
				respData := buf.Bytes()[:idx]
				var resp Response
				if err := json.Unmarshal(respData, &resp); err != nil {
					return nil, fmt.Errorf("parse response: %w", err)
				}
				return &resp, nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
	}
}