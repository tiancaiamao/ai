package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

// CommandHandler processes a BridgeCommand and returns a BridgeResponse.
type CommandHandler func(cmd BridgeCommand) BridgeResponse

// SocketServer listens on a Unix domain socket and dispatches commands to a handler.
type SocketServer struct {
	sockPath string
	handler  CommandHandler
	listener net.Listener
	done     chan struct{}
}

// NewSocketServer creates a new SocketServer that will listen on sockPath.
func NewSocketServer(sockPath string, handler CommandHandler) *SocketServer {
	return &SocketServer{
		sockPath: sockPath,
		handler:  handler,
		done:     make(chan struct{}),
	}
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

// acceptLoop accepts one connection at a time, reads a BridgeCommand,
// dispatches to the handler, writes back the BridgeResponse, and closes
// the connection. Errors on individual connections are logged but do not
// stop the loop.
func (s *SocketServer) acceptLoop() {
	defer close(s.done)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed — normal shutdown path.
			return
		}

		s.handleConn(conn)
	}
}

// handleConn reads one newline-delimited JSON command, dispatches it, and
// writes the JSON response back before closing the connection.
func (s *SocketServer) handleConn(conn net.Conn) {
	defer conn.Close()

	// Set read deadline to prevent hanging on malformed clients.
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read until newline, max 1MB to prevent OOM.
	var buf bytes.Buffer
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf.Write(recvBuf[:n])
			if buf.Len() > 1<<20 {
				log.Printf("bridge: command too large (>1MB), discarding")
				s.writeResponse(conn, BridgeResponse{OK: false, Error: "command too large"})
				return
			}
			if bytes.IndexByte(buf.Bytes(), '\n') >= 0 {
				break
			}
		}
		if err != nil {
			log.Printf("bridge: read from %s: %v", conn.RemoteAddr(), err)
			return
		}
	}

	// Trim the trailing newline before unmarshaling.
	data := bytes.TrimRight(buf.Bytes(), "\n")

	var cmd BridgeCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Printf("bridge: unmarshal command: %v", err)
		s.writeResponse(conn, BridgeResponse{
			OK:    false,
			Error: fmt.Sprintf("invalid command: %v", err),
		})
		return
	}

	resp := s.handler(cmd)
	s.writeResponse(conn, resp)
}

// writeResponse marshals and writes a BridgeResponse as JSON followed by a
// newline.
func (s *SocketServer) writeResponse(conn net.Conn, resp BridgeResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("bridge: marshal response: %v", err)
		return
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		log.Printf("bridge: write response: %v", err)
	}
}