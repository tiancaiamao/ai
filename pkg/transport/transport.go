// Package transport abstracts the byte channel the ACP protocol runs over.
//
// The ACP server (pkg/rpc) reads and writes whole JSON-RPC messages through a
// Conn and knows nothing about the underlying medium. Today the medium is
// stdin/stdout (NewStdio); a unix socket (UnixSocket) is added so the same
// protocol code can also serve local attach/watch clients.
package transport

import "encoding/json"

// Conn is a bidirectional, newline-delimited JSON-RPC channel to a single ACP
// peer. The newline framing and concurrent-write serialization are handled
// here, so protocol code stays transport-agnostic.
type Conn interface {
	// ReadMessage blocks until the next JSON-RPC message is available. It
	// returns io.EOF when the peer closes the channel, or the underlying error
	// otherwise.
	ReadMessage() (json.RawMessage, error)
	// WriteMessage writes one JSON-RPC message framed with a trailing newline.
	// Concurrent callers are serialized.
	WriteMessage(msg json.RawMessage) error
	// Close releases the underlying connection.
	Close() error
}
