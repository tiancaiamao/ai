//go:build e2e

package e2e

import (
	"os"
	"testing"
	"time"
)

// Reproduces the BusyAndAbort event ordering: the abort response and agent_end
// arrive in the same drain batch; the first match must not lose the second.
func TestLRRepro_SameBatchEvents(t *testing.T) {
	r, w, _ := os.Pipe()
	lr := newLogReader(r, t)
	defer w.Close()

	write := func(s string) { w.Write([]byte(s)) }

	write(`{"type":"response","command":"abort","success":true,"data":{"status":"aborting"}}` + "\n")
	write(`{"type":"agent_end","eventAt":1,"messages":[]}` + "\n")

	// rpcAck-style wait: consumes the abort response.
	resp := lr.waitEvent("response", "command", "abort", 5*time.Second)
	if resp == "" {
		t.Fatal("abort response not found")
	}

	// The agent_end from the same batch must still be findable.
	if ev := lr.waitEvent("agent_end", "", "", 5*time.Second); ev == "" {
		t.Fatal("agent_end lost after abort response consumed")
	}
}
