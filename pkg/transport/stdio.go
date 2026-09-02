package transport

import (
	"bufio"
	"context"
	"io"
	"sync"
)

// stdioConn is a Conn backed by a reader (stdin) and writer (stdout) using
// newline-delimited framing.
//
// The reader is wrapped so that a blocked Read is unblocked when Close is
// called: Close cancels an internal context and the reader yields io.EOF, so a
// server loop parked on ReadMessage can exit cleanly on shutdown.
type stdioConn struct {
	scanner *bufio.Scanner
	out     io.Writer
	mu      sync.Mutex
	cancel  context.CancelFunc
}

// NewStdio returns a Conn over the given reader and writer. The writer is
// serialized for concurrent writes.
func NewStdio(in io.Reader, out io.Writer) Conn {
	ctx, cancel := context.WithCancel(context.Background())
	sc := bufio.NewScanner(&cancelEOFReader{r: in, ctx: ctx})
	buf := make([]byte, 0, 4*1024*1024) // 4MB initial
	sc.Buffer(buf, 16*1024*1024)        // 16MB max
	return &stdioConn{scanner: sc, out: out, cancel: cancel}
}

// ReadMessage returns the next newline-delimited message, or io.EOF on close.
func (c *stdioConn) ReadMessage() ([]byte, error) {
	if c.scanner.Scan() {
		line := c.scanner.Bytes()
		msg := make([]byte, len(line))
		copy(msg, line)
		return msg, nil
	}
	if err := c.scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return nil, io.EOF
}

// WriteMessage writes msg followed by a newline, serialized across callers.
func (c *stdioConn) WriteMessage(msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.out.Write(msg); err != nil {
		return err
	}
	_, err := c.out.Write([]byte{'\n'})
	return err
}

// Close cancels the reader so a blocked ReadMessage unblocks with io.EOF. It
// is idempotent; the underlying file descriptors are owned by the process.
func (c *stdioConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return nil
}

// cancelEOFReader wraps an io.Reader so a blocking Read is interrupted when ctx
// is canceled, returning io.EOF. The blocking read is raced against ctx in a
// goroutine because file/pipe reads cannot be interrupted directly.
type cancelEOFReader struct {
	r   io.Reader
	ctx context.Context
}

func (cr *cancelEOFReader) Read(p []byte) (int, error) {
	if cr.ctx.Err() != nil {
		return 0, io.EOF
	}
	done := make(chan readOutcome, 1)
	go func() {
		n, err := cr.r.Read(p)
		done <- readOutcome{n: n, err: err}
	}()
	select {
	case o := <-done:
		return o.n, o.err
	case <-cr.ctx.Done():
		return 0, io.EOF
	}
}

type readOutcome struct {
	n   int
	err error
}
