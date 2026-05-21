package main

import (
	"encoding/json"
	"testing"

		"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

func newTestApp(t *testing.T) *rpcApp {
	t.Helper()
	model := llm.Model{ID: "test-model", ContextWindow: 4096}
	agentCtx := agentctx.NewAgentContext("test system prompt")
	ag := agent.NewAgentFromConfigWithContext(model, "test-key", agentCtx, agent.DefaultLoopConfig())

	// Drain the trace goroutine so temp dirs get cleaned up.
	t.Cleanup(func() {
		traceevent.SetActiveTraceBuf(nil)
	})

	return &rpcApp{
		ag:                ag,
		steeringMode:      "all",
		followUpMode:      "all",
		expandSkillCommands: func(text string) string { return text },
		compactBeforeRequest: func(trigger string) {},
	}
}

// --- handleSteer tests ---

func TestHandleSteer_EmptyMessage(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleSteer(rpc.RPCCommand{Message: ""})
	if err == nil {
		t.Fatal("expected error for empty steer message")
	}
	if err.Error() != "empty steer message" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSteer_WhitespaceOnly(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleSteer(rpc.RPCCommand{Message: "   \t\n  "})
	if err == nil {
		t.Fatal("expected error for whitespace-only steer message")
	}
}

func TestHandleSteer_ValidMessage(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleSteer(rpc.RPCCommand{Message: "go left"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSteer_MessageFromData(t *testing.T) {
	app := newTestApp(t)
	data, _ := json.Marshal(map[string]string{"message": "from data field"})
	_, err := app.handleSteer(rpc.RPCCommand{Data: data})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSteer_InvalidData(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleSteer(rpc.RPCCommand{Data: []byte("not json")})
	if err == nil {
		t.Fatal("expected error for invalid JSON data")
	}
}

func TestHandleSteer_OneAtATime_PendingBlocks(t *testing.T) {
	app := newTestApp(t)
	app.steeringMode = "one-at-a-time"
	app.pendingSteer = true

	_, err := app.handleSteer(rpc.RPCCommand{Message: "blocked"})
	if err == nil {
		t.Fatal("expected error when steer already pending in one-at-a-time mode")
	}
	if err.Error() != "steer already pending" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSteer_OneAtATime_NoPending(t *testing.T) {
	app := newTestApp(t)
	app.steeringMode = "one-at-a-time"
	app.pendingSteer = false

	_, err := app.handleSteer(rpc.RPCCommand{Message: "allowed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSteer_SetsPendingFlag(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleSteer(rpc.RPCCommand{Message: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !app.pendingSteer {
		t.Fatal("expected pendingSteer to be true after successful steer")
	}
}

// --- handleFollowUp tests ---

func TestHandleFollowUp_EmptyMessage(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleFollowUp(rpc.RPCCommand{Message: ""})
	if err == nil {
		t.Fatal("expected error for empty follow-up message")
	}
	if err.Error() != "empty follow-up message" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleFollowUp_WhitespaceOnly(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleFollowUp(rpc.RPCCommand{Message: "   "})
	if err == nil {
		t.Fatal("expected error for whitespace-only follow-up message")
	}
}

func TestHandleFollowUp_ValidMessage(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleFollowUp(rpc.RPCCommand{Message: "do more"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleFollowUp_MessageFromData(t *testing.T) {
	app := newTestApp(t)
	data, _ := json.Marshal(map[string]string{"message": "from data"})
	_, err := app.handleFollowUp(rpc.RPCCommand{Data: data})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleFollowUp_InvalidData(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleFollowUp(rpc.RPCCommand{Data: []byte("{bad")})
	if err == nil {
		t.Fatal("expected error for invalid JSON data")
	}
}

func TestHandleFollowUp_OneAtATime_PendingBlocks(t *testing.T) {
	app := newTestApp(t)
	app.followUpMode = "one-at-a-time"

	// Fill the agent's follow-up queue (capacity 100).
	for i := 0; i < 100; i++ {
		_ = app.ag.FollowUp("fill")
	}

	_, err := app.handleFollowUp(rpc.RPCCommand{Message: "blocked"})
	if err == nil {
		t.Fatal("expected error when follow-up queue full in one-at-a-time mode")
	}
}

func TestHandleFollowUp_OneAtATime_EmptyQueue(t *testing.T) {
	app := newTestApp(t)
	app.followUpMode = "one-at-a-time"

	_, err := app.handleFollowUp(rpc.RPCCommand{Message: "allowed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- handleAbort tests ---

func TestHandleAbort(t *testing.T) {
	app := newTestApp(t)
	_, err := app.handleAbort(rpc.RPCCommand{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}