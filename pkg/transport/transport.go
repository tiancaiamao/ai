package transport

import "encoding/json"

// Conn is a bidirectional, newline-delimited message channel to a single peer.
type Conn interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

// rawObject is used internally by the hub to inspect JSON without interpreting
// byte fields as base64 strings.
type rawObject map[string]json.RawMessage
