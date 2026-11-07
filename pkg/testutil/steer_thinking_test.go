package testutil_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// TestSteerMidThinking_NextRequestCarriesPartialThinking drives a steer in the
// middle of a streamed reasoning response and inspects the second LLM request
// body: it must carry the salvaged partial thinking from the aborted run.
//
// Regression test: the salvage path (buildAbortedMessage in
// pkg/agent/llm_stream.go) produces an "aborted" assistant message with the
// partial thinking, but it is delivered via TurnEnd/AgentEnd events.
// processPrompt's stream.Iterator(ctx) used to return on ctx.Done() without
// draining those events (pkg/llm/eventstream.go), so the aborted message never
// reached Agent.RecentMessages and the steered request was built from history
// plus both user messages only.
func TestSteerMidThinking_NextRequestCarriesPartialThinking(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	firstDeltaWritten := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		idx := len(bodies)
		bodies = append(bodies, string(body))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if idx == 0 {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me think hard\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(firstDeltaWritten)
			// Hold the stream open until the client cancels (steer).
			<-r.Context().Done()
			return
		}
		io.WriteString(w, testutil.TextResponse("steered response"))
	}))
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgent(model, "test-key", "You are helpful.")

	if err := a.Prompt("initial"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	select {
	case <-firstDeltaWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first reasoning delta")
	}
	// Give the consumer time to process the delta and park in its event select.
	time.Sleep(100 * time.Millisecond)

	a.Steer("go east")
	a.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}

	if !strings.Contains(bodies[1], "initial") {
		t.Errorf("second request lost the original user message")
	}
	if !strings.Contains(bodies[1], "go east") {
		t.Errorf("second request missing the steer message")
	}
	if !strings.Contains(bodies[1], "let me think hard") {
		t.Errorf("second request missing the partial thinking from the aborted run; body: %s", bodies[1])
	}
}
