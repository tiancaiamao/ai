package cmd

import (
	"github.com/genius/ag/internal/bridge"
)

// RunBridge runs the bridge process for an agent. This is called by
// 'ag bridge <id>' inside a tmux session. It:
//  1. Reads meta.json for spawn config
//  2. Starts ai --mode rpc with piped stdin/stdout
//  3. Writes initial prompt
//  4. Starts event reader goroutine (ai stdout → activity.json)
//  5. Starts socket server goroutine (Unix socket → ai stdin)
//  6. Waits for ai to exit
//  7. Writes final output and updates activity.json
func RunBridge(id string) error {
	return bridge.Run(id)
}

// Placeholder until bridge.Run is fully implemented.
var _ = bridge.Run