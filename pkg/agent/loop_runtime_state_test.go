package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestRunLoop_RuntimeStateAppendOnly verifies the codex-style prefix-cache
// contract: runtime_state snapshots are frozen as persisted messages at turn
// boundaries, and consecutive LLM requests are pure tail appends of the
// previous one. A new snapshot message is added only when the state actually
// changed (e.g. after change_workspace).
func TestRunLoop_RuntimeStateAppendOnly(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	workdir := "/tmp/worktree-a"
	var snapshots [][]string // message texts seen by each LLM call
	streamAssistantResponseFn = func(
		_ context.Context,
		agentCtx *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		contents := make([]string, 0, len(agentCtx.RecentMessages))
		for _, m := range agentCtx.RecentMessages {
			contents = append(contents, m.ExtractText())
		}
		snapshots = append(snapshots, contents)
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "ok"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	config := &LoopConfig{
		GetWorkingDir:  func() string { return workdir },
		GetStartupPath: func() string { return "/tmp/project-root" },
	}
	agentCtx := agentctx.NewAgentContext("sys")

	runTurn := func(prompt string) {
		stream := RunLoop(context.Background(), []agentctx.AgentMessage{agentctx.NewUserMessage(prompt)}, agentCtx, config)
		for item := range stream.Iterator(context.Background()) {
			if item.Value.Type == EventAgentEnd {
				// Keep caller-side history in sync like pkg/agent/agent.go.
				agentCtx.RecentMessages = append([]agentctx.AgentMessage(nil), item.Value.Messages...)
			}
			if item.Done {
				break
			}
		}
	}

	runTurn("u1")
	runTurn("u2")

	// Snapshot changed (cwd moved): next request must append a new frozen
	// message, not rewrite the old one.
	workdir = "/tmp/worktree-b"
	runTurn("u3")

	if len(snapshots) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(snapshots))
	}

	// Turn 1: frozen snapshot before the user prompt.
	if len(snapshots[0]) != 2 || !strings.HasPrefix(snapshots[0][0], "<agent:runtime_state/>") || !strings.Contains(snapshots[0][0], "/tmp/worktree-a") || snapshots[0][1] != "u1" {
		t.Fatalf("turn 1 request = %q, want [runtime_state(worktree-a), u1]", snapshots[0])
	}

	// Turn 2: pure append of the previous request, no new snapshot.
	want := append(append([]string{}, snapshots[0]...), "ok", "u2")
	if !reflect.DeepEqual(snapshots[1], want) {
		t.Fatalf("turn 2 request = %q, want pure append %q", snapshots[1], want)
	}

	// Turn 3: previous request untouched, new snapshot appended before u3.
	if !reflect.DeepEqual(snapshots[2][:len(snapshots[1])], snapshots[1]) {
		t.Fatalf("turn 3 rewrote the previous prefix: %q vs %q", snapshots[2][:len(snapshots[1])], snapshots[1])
	}
	tail := snapshots[2][len(snapshots[1]):]
	if len(tail) != 3 || tail[0] != "ok" || !strings.HasPrefix(tail[1], "<agent:runtime_state/>") || !strings.Contains(tail[1], "/tmp/worktree-b") || tail[2] != "u3" {
		t.Fatalf("turn 3 tail = %q, want [ok, runtime_state(worktree-b), u3]", tail)
	}

	// The frozen snapshots are persisted in history with the runtimeState kind.
	found := 0
	for _, m := range agentCtx.RecentMessages {
		if m.Metadata != nil && m.Metadata.Kind == runtimeStateKind {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected 2 persisted runtime_state messages, got %d", found)
	}
}
