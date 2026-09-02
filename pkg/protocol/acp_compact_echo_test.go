package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/transport"
)

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestACPCompactEchoesBeforeRunning(t *testing.T) {
	output := &synchronizedBuffer{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	commands := command.New()
	rt := &testRuntime{commands: commands, sessionID: "sess"}
	srv := &acpServer{
		app:       rt,
		conn:      transport.NewStdio(strings.NewReader(""), output),
		sessionID: "sess",
	}
	rt.emit = srv.emit
	commands.Register("compact", "Compact conversation", func(string) (any, error) {
		rt.emit(agent.NewCompactionStartEvent(agent.CompactionInfo{Trigger: "manual_command"}))
		close(started)
		<-release
		rt.emit(agent.NewCompactionEndEvent(agent.CompactionInfo{After: 1}))
		return map[string]any{"status": "done"}, nil
	})

	params, err := json.Marshal(map[string]any{
		"sessionId": "sess",
		"prompt":    []acpContentBlock{{Type: "text", Text: "/compact"}},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	go func() {
		srv.handlePrompt(acpRequest{ID: json.RawMessage(`3`), Params: params})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("compact handler was not started")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected user echo and compaction start before completion, got %q", output.String())
	}
	assertACPUpdateKind(t, lines[0], "user_message_chunk")
	assertACPUpdateKind(t, lines[1], "_compaction")

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("compact handler did not complete")
	}
	lines = strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected compaction end, result, and response after completion, got %q", output.String())
	}
	assertACPUpdateKind(t, lines[2], "_compaction")
	assertACPUpdateKind(t, lines[3], "agent_message_chunk")
	var response map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &response); err != nil {
		t.Fatalf("decode final response: %v", err)
	}
	if response["id"] != float64(3) {
		t.Fatalf("expected response id 3, got %v", response["id"])
	}
}

func assertACPUpdateKind(t *testing.T, line, want string) {
	t.Helper()
	var message struct {
		Params struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
			} `json:"update"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatalf("decode ACP update: %v", err)
	}
	if message.Params.Update.SessionUpdate != want {
		t.Fatalf("expected ACP update %q, got %q", want, message.Params.Update.SessionUpdate)
	}
}
