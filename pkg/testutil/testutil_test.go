package testutil_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// ============================================================================
// Test 1: VCR RoundTripper - Record and Replay
// ============================================================================

func TestVCRRoundTripper_RecordAndReplay(t *testing.T) {
	// Set up a fake SSE server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello world\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cassetteDir := filepath.Join(t.TempDir(), "testdata")
	cassetteName := "roundtrip_test"

	// === Phase 1: Record ===
	t.Run("Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, cassetteName)
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)

		// Make a real HTTP request through the VCR transport
		client := &http.Client{Transport: rt}
		resp, err := client.Post(server.URL+"/chat/completions", "application/json",
			strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		result := string(body[:n])

		assert.Contains(t, result, "hello world")
		assert.Equal(t, 1, vcr.InteractionCount())
	})

	// === Phase 2: Replay ===
	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, cassetteName)
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)

		// Make HTTP request - should return recorded response without network
		client := &http.Client{Transport: rt}
		resp, err := client.Post("http://does-not-exist/chat/completions", "application/json",
			strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		result := string(body[:n])

		assert.Contains(t, result, "hello world")
	})
}

// ============================================================================
// Test 2: Mock Tools
// ============================================================================

func TestMockTool_StaticResult(t *testing.T) {
	tool := testutil.NewMockTool("bash").
		WithStaticResult("command output: hello")

	ctx := context.Background()
	result, err := tool.Execute(ctx, map[string]any{"command": "echo hello"})
	require.NoError(t, err)
	require.Len(t, result, 1)

	text := result[0].(agentctx.TextContent)
	assert.Equal(t, "command output: hello", text.Text)
	assert.Equal(t, 1, tool.CallCount())
	assert.True(t, tool.WasCalled())
}

func TestMockTool_CustomHandler(t *testing.T) {
	tool := testutil.NewMockTool("read").
		WithHandler(func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
			path, _ := args["path"].(string)
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("contents of %s", path)},
			}, nil
		})

	ctx := context.Background()
	result, err := tool.Execute(ctx, map[string]any{"path": "/tmp/test.go"})
	require.NoError(t, err)

	text := result[0].(agentctx.TextContent)
	assert.Contains(t, text.Text, "contents of /tmp/test.go")
}

func TestMockTool_Error(t *testing.T) {
	tool := testutil.NewMockTool("bash").
		WithError("command timed out")

	ctx := context.Background()
	_, err := tool.Execute(ctx, map[string]any{"command": "sleep 999"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestMockToolRegistry(t *testing.T) {
	registry := testutil.SetupStandardTools(t)
	tools := registry.All()

	assert.NotEmpty(t, tools)

	// Check all expected tools exist
	expectedTools := []string{"bash", "read", "write", "edit", "grep", "change_workspace"}
	for _, name := range expectedTools {
		tool, ok := registry.Get(name)
		assert.True(t, ok, "tool %q should be registered", name)
		assert.Equal(t, name, tool.Name())
	}
}

// ============================================================================
// Test 3: Snapshot Helpers
// ============================================================================

func TestSnapshotHelpers(t *testing.T) {
	h := testutil.SnapshotHelpers(t)

	snapshot := &agentctx.ContextSnapshot{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("how are you"),
		},
	}
	// Add text to the assistant message
	snapshot.RecentMessages[1].Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "Hi! How can I help you?"},
	}

	t.Run("AssertMessageCount", func(t *testing.T) {
		h.AssertMessageCount(snapshot, 3)
	})

	t.Run("AssertLastAssistantContains", func(t *testing.T) {
		h.AssertLastAssistantContains(snapshot, "How can I help")
	})
}

// ============================================================================
// Test 4: Full MockAgent with fake SSE server
// ============================================================================

func TestMockAgent_WithFakeLLM(t *testing.T) {
	// Set up a fake LLM server that returns a tool call first, then completion
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// First request: return tool call
		if requestCount == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"echo hello\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			// Subsequent requests: return completion (no more tool calls)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Command completed successfully.\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()

	// Create cassette directory
	cassetteDir := filepath.Join(t.TempDir(), "testdata")

	// Phase 1: Record
	t.Run("Record", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "mock_agent_test")
		vcr.Record()
		defer vcr.Cleanup()

		rt := testutil.NewVCRRoundTripper(vcr)

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		}

		cfg := testutil.MockAgentConfig{
			SessionID:    "test-session",
			TempDir:      t.TempDir(),
			Model:        model,
			APIKey:       "test-key",
			Mode:         testutil.ModeRecord,
			CassetteDir:  cassetteDir,
			CassetteName: "mock_agent_test",
			MockTools: map[string]*testutil.MockTool{
				"bash": testutil.NewMockTool("bash").WithStaticResult("hello\n"),
			},
		}

		agent := testutil.NewMockAgent(t, cfg)
		defer agent.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Replace transport so StreamLLM goes through VCR
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		err := agent.ExecuteTurn(ctx, "run echo hello")
		require.NoError(t, err)

		snapshot := agent.GetSnapshot()
		assert.GreaterOrEqual(t, len(snapshot.RecentMessages), 2) // user + assistant
		assert.Equal(t, 2, agent.LLMCallCount()) // 1 for tool call + 1 for completion
		assert.GreaterOrEqual(t, agent.ToolCallCount(), 1)
	})

	// Phase 2: Replay
	t.Run("Replay", func(t *testing.T) {
		vcr := testutil.NewVCR(t, cassetteDir, "mock_agent_test")
		vcr.Replay()

		rt := testutil.NewVCRRoundTripper(vcr)

		model := llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  "http://does-not-exist", // Should not be called
			API:      "openai-completions",
		}

		cfg := testutil.MockAgentConfig{
			SessionID:    "test-session-replay",
			TempDir:      t.TempDir(),
			Model:        model,
			APIKey:       "test-key",
			Mode:         testutil.ModeReplay,
			CassetteDir:  cassetteDir,
			CassetteName: "mock_agent_test",
			MockTools: map[string]*testutil.MockTool{
				"bash": testutil.NewMockTool("bash").WithStaticResult("hello\n"),
			},
		}

		agent := testutil.NewMockAgent(t, cfg)
		defer agent.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Replace transport
		origTransport := http.DefaultTransport
		http.DefaultTransport = rt
		defer func() { http.DefaultTransport = origTransport }()

		err := agent.ExecuteTurn(ctx, "run echo hello")
		require.NoError(t, err)

		snapshot := agent.GetSnapshot()
		assert.GreaterOrEqual(t, len(snapshot.RecentMessages), 2)
	})
}

// ============================================================================
// Test 5: Multi-turn conversation with VCR
// ============================================================================

func TestMockAgent_MultiTurn(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if requestCount == 1 {
			// First turn: simple response (no tool calls)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"I can help with that.\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else if requestCount == 2 {
			// Second turn: response with tool call
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Let me check.\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_456\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			// After tool call: completion message
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Files listed above.\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()

	cassetteDir := filepath.Join(t.TempDir(), "testdata")

	// Record phase
	vcr := testutil.NewVCR(t, cassetteDir, "multi_turn")
	vcr.Record()
	defer vcr.Cleanup()

	rt := testutil.NewVCRRoundTripper(vcr)
	origTransport := http.DefaultTransport
	http.DefaultTransport = rt
	defer func() { http.DefaultTransport = origTransport }()

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	cfg := testutil.MockAgentConfig{
		SessionID:    "multi-turn-session",
		TempDir:      t.TempDir(),
		Model:        model,
		APIKey:       "test-key",
		Mode:         testutil.ModeRecord,
		CassetteDir:  cassetteDir,
		CassetteName: "multi_turn",
		MockTools: map[string]*testutil.MockTool{
			"bash": testutil.NewMockTool("bash").WithStaticResult("file1.go\nfile2.go\n"),
		},
	}

	agent := testutil.NewMockAgent(t, cfg)
	defer agent.Close()

	ctx := context.Background()

	// Turn 1: simple completion (1 LLM call)
	err := agent.ExecuteTurn(ctx, "Can you help me?")
	require.NoError(t, err)
	assert.Equal(t, 1, agent.LLMCallCount())

	// Turn 2: tool call + completion (2 more LLM calls = 3 total)
	err = agent.ExecuteTurn(ctx, "List files")
	require.NoError(t, err)
	assert.Equal(t, 3, agent.LLMCallCount()) // 1 + 2 = 3

	snapshot := agent.GetSnapshot()
	assert.Equal(t, 2, int(snapshot.AgentState.TotalTurns))
}

// ============================================================================
// Test 6: Cassette save and load
// ============================================================================

func TestCassette_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	journal := testutil.NewToolJournal(dir, "test_cassette")

	// Record some tool calls
	journal.Record("call_1", "bash", `{"command":"echo hi"}`, "hi\n", false)
	journal.Record("call_2", "read", `{"path":"/tmp/test.go"}`, "package main", false)
	journal.Record("call_3", "write", `{"path":"/tmp/out.go"}`, "error: permission denied", true)

	// Save
	err := journal.Save()
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(filepath.Join(dir, "test_cassette_tools.yaml"))
	require.NoError(t, err)

	// Load
	journal2 := testutil.NewToolJournal(dir, "test_cassette")
	records, err := journal2.Load()
	require.NoError(t, err)

	require.Len(t, records, 3)
	assert.Equal(t, "call_1", records[0].ToolCallID)
	assert.Equal(t, "bash", records[0].ToolName)
	assert.Equal(t, "hi\n", records[0].Result)
	assert.False(t, records[0].IsError)

	assert.Equal(t, "call_3", records[2].ToolCallID)
	assert.True(t, records[2].IsError)
}
