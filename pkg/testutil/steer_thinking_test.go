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

// TestSteerMidThinking_NextRequestCarriesContext drives a steer in the middle
// of a streamed reasoning response and inspects the second LLM request body:
// it must carry the first response (including its reasoning) plus the steer
// message.
//
// Queue-based steer never cancels the in-flight request: the first response
// completes, and the steer message is injected at the next model-call
// boundary. (Salvage of partial thinking after a cancellation is Abort-only
// now and covered by the processPrompt drain tests in pkg/agent.)
func TestSteerMidThinking_NextRequestCarriesContext(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	firstDeltaWritten := make(chan struct{})
	releaseFirst := make(chan struct{})

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
			// Hold the first response open until the test has steered —
			// steer must queue, not cancel this request.
			<-releaseFirst
			io.WriteString(w, testutil.TextResponse("first response"))
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

	// The first request is still in flight, so this steer is queued for the
	// next model-call boundary instead of cancelling anything.
	if err := a.Steer("go east"); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}
	close(releaseFirst)
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
		t.Errorf("second request missing the first response's reasoning; body: %s", bodies[1])
	}
	if !strings.Contains(bodies[1], "first response") {
		t.Errorf("second request missing the completed first response; body: %s", bodies[1])
	}
}
