package transport

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// stdioConn is a Conn backed by a reader (stdin) and writer (stdout) using
// newline-delimited framing.
//
// The reader must already carry any cancellation semantics the caller wants:
// wrap it with a context-aware reader (see pkg/rpc contextReader) before
// passing it in.
type stdioConn struct {
	scanner *bufio.Scanner
	out     io.Writer
	mu      sync.Mutex
}

// NewStdio returns a Conn over the given reader and writer. The writer is
// serialized for concurrent writes.
func NewStdio(in io.Reader, out io.Writer) Conn {
	sc := bufio.NewScanner(in)
	buf := make([]byte, 0, 4*1024*1024) // 4MB initial
	sc.Buffer(buf, 16*1024*1024)        // 16MB max
	return &stdioConn{scanner: sc, out: out}
}

// ReadMessage returns the next newline-delimited message, or io.EOF on close.
func (c *stdioConn) ReadMessage() (json.RawMessage, error) {
	if c.scanner.Scan() {
		line := c.scanner.Bytes()
		msg := make(json.RawMessage, len(line))
		copy(msg, line)
		return msg, nil
	}
	if err := c.scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return nil, io.EOF
}

// WriteMessage writes msg followed by a newline, serialized across callers.
func (c *stdioConn) WriteMessage(msg json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.out.Write(msg); err != nil {
		return err
	}
	_, err := c.out.Write([]byte{'\n'})
	return err
}

// Close is a no-op for stdio: the process owns the file descriptors.
func (c *stdioConn) Close() error { return nil }
