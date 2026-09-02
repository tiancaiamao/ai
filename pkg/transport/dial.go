package transport

import (
	"fmt"
	"net"
	"time"
)

// DialUnix connects to a Unix-domain socket and applies newline framing.
func DialUnix(path string) (Conn, error) {
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial unix socket %s: %w", path, err)
	}
	return NewNetConn(conn), nil
}
