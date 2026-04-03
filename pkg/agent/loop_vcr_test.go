// Package agent provides VCR-based tests for conversation loop behavior.
// These tests use VCR (Video Cassette Recorder) to record and replay LLM API calls,
// allowing fast, deterministic tests without consuming API quota.
package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// vcrShouldRecord returns true if the VCR_RECORD environment variable is set to "1".
// By default, VCR tests only run the Replay phase (fast, no network calls).
// To re-record cassettes, run: VCR_RECORD=1 go test ./pkg/agent -v -run TestVCR
func vcrShouldRecord() bool {
	return os.Getenv("VCR_RECORD") == "1"
}

// vcrMaybeRun runs the Record subtest only if VCR_RECORD=1 is set.
// Otherwise, skips it. This ensures tests only re-record when explicitly requested.
func vcrMaybeRun(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	if vcrShouldRecord() {
		t.Run(name, fn)
	} else {
		t.Run(name, func(t *testing.T) {
			t.Skip("Skipping Record phase (set VCR_RECORD=1 to re-record cassettes)")
		})
	}
}

// ============================================================================
// VCR Test: Duplicate Tool Call Detection
// ============================================================================

// TestVCR_DuplicateToolCallDetection_20DifferentCalls_ShouldPass
// Tests that 20 consecutive DIFFERENT tool calls should succeed.
// This verifies that the duplicate detection only fails on SAME tool+parameters repeated.
func TestVCR_DuplicateToolCallDetection_20DifferentCalls_ShouldPass(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping VCR test in short mode")
	}

	// Create mock server that simulates 20 different tool calls
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// First call: user message
		if requestCount == 1 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I'll make 20 different bash calls."}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else if requestCount <= 21 {
			// Calls 2-21: Return a tool call with incrementing command
			cmdNum := requestCount - 1
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_%d","type":"function","function":{"name":"bash","arguments":"{\"command\":\"echo iteration %d\"}"}}]}}]}`+"\n\n", cmdNum, cmdNum)
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			// Final call: return a completion message
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"All 20 iterations completed."}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer server.Close()

	cassetteDir := filepath.Join("testdata", "vcr", "duplicate_detection")

	// Phase 1: Record (only runs when VCR_RECORD=1 is set)
	vcrMaybeRun(t, "Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "20_different_calls")
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		// Create agent
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-20-calls")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-20-calls", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		// Execute turn with a request that will trigger many tool calls
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// This should succeed even with 20 different tool calls
		err = ag.ExecuteNormalMode(ctx, "Make 20 different bash calls with incrementing numbers")
		require.NoError(t, err, "20 different tool calls should succeed")

		snapshot := ag.GetSnapshot()
		assert.GreaterOrEqual(t, snapshot.AgentState.TotalTurns, 1)
	})

	// Phase 2: Replay (runs fast without API calls)
	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "20_different_calls")
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-20-calls-replay")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  "http://does-not-exist", // Should use recorded response
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-20-calls-replay", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err = ag.ExecuteNormalMode(ctx, "Make 20 different bash calls")
		require.NoError(t, err, "Replay should also succeed with 20 different calls")
	})
}

// TestVCR_DuplicateToolCallDetection_7SameCalls_ShouldFail
// Tests that 7 consecutive SAME tool calls (same tool + same parameters) should fail.
// This verifies the infinite loop detection is working.
func TestVCR_DuplicateToolCallDetection_7SameCalls_ShouldFail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping VCR test in short mode")
	}

	// Create mock server that always returns the same tool call
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Always return the same tool call (simulating stuck agent)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Let me check..."}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_stuck","type":"function","function":{"name":"bash","arguments":"{\"command\":\"echo stuck\"}"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer server.Close()

	cassetteDir := filepath.Join("testdata", "vcr", "duplicate_detection")

	t.Run("Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "7_same_calls")
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-stuck")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-stuck", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// This should fail after 7 duplicate tool calls
		err = ag.ExecuteNormalMode(ctx, "Check the status")
		require.Error(t, err, "Should fail with duplicate tool call error")

		// Verify the error message mentions the loop
		assert.Contains(t, err.Error(), "stuck in a loop",
			"Error should indicate the agent is stuck")
		assert.Contains(t, err.Error(), "bash",
			"Error should mention the problematic tool")
		assert.Contains(t, err.Error(), "7",
			"Error should mention the repeat count")
	})

	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "7_same_calls")
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-stuck-replay")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  "http://does-not-exist",
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-stuck-replay", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err = ag.ExecuteNormalMode(ctx, "Check the status")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stuck in a loop")
	})
}

// TestVCR_MaxTurns_WithLimit_ShouldFail
// Tests that when maxTurns is set, the agent respects the limit.
func TestVCR_MaxTurns_WithLimit_ShouldFail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping VCR test in short mode")
	}

	// Create mock server that returns tool calls indefinitely
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Return different tool calls each time (so duplicate check won't trigger)
		fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_%d","type":"function","function":{"name":"bash","arguments":"{\"command\":\"echo %d\"}"}}]}}]}`+"\n\n", requestCount, requestCount)
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer server.Close()

	cassetteDir := filepath.Join("testdata", "vcr", "duplicate_detection")

	t.Run("Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "max_turns_5")
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-max-turns")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-max-turns", &model, "test-key")
		require.NoError(t, err)

		// Set max turns to 5
		ag.SetMaxTurns(5)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Should fail after 5 turns
		err = ag.ExecuteNormalMode(ctx, "Keep checking")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum turns")
		assert.Contains(t, err.Error(), "5")
	})

	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "max_turns_5")
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-max-turns-replay")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  "http://does-not-exist",
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-max-turns-replay", &model, "test-key")
		require.NoError(t, err)
		ag.SetMaxTurns(5)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err = ag.ExecuteNormalMode(ctx, "Keep checking")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum turns")
	})
}

// TestVCR_MaxTurns_NoLimit_ShouldPass
// Tests that without maxTurns set, the agent can run indefinitely.
func TestVCR_MaxTurns_NoLimit_ShouldPass(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping VCR test in short mode")
	}

	// Create mock server that eventually stops after 15 turns
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if requestCount <= 15 {
			// Return different tool calls
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_%d","type":"function","function":{"name":"bash","arguments":"{\"command\":\"echo %d\"}"}}]}}]}`+"\n\n", requestCount, requestCount)
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			// Finally complete
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Done!"}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer server.Close()

	cassetteDir := filepath.Join("testdata", "vcr", "duplicate_detection")

	t.Run("Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "no_max_turns_15")
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-no-max")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-no-max", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		// Don't set maxTurns (default is 0 = unlimited)
		assert.Equal(t, 0, ag.GetMaxTurns(), "maxTurns should be 0 (unlimited)")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Should succeed even with 15 turns (no maxTurns limit)
		err = ag.ExecuteNormalMode(ctx, "Run 15 iterations then stop")
		require.NoError(t, err, "Should succeed without maxTurns limit")
	})

	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "no_max_turns_15")
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "sessions", "test-no-max-replay")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  "http://does-not-exist",
			API:      "openai-completions",
		}

		ag, err := NewAgentForE2E(sessionDir, "test-no-max-replay", &model, "test-key")
		require.NoError(t, err)
		defer ag.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err = ag.ExecuteNormalMode(ctx, "Run 15 iterations")
		require.NoError(t, err)
	})
}
