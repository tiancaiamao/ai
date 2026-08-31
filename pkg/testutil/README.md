# pkg/testutil

Test utilities for agent integration testing. Provides a fully-wired test harness with a mock LLM server, event collection, and mock tools.

## Usage

```go
func TestMyFeature(t *testing.T) {
    h := testutil.NewAgentHarness(t,
        []string{testutil.TextResponse("hello world")},
        testutil.WithTools(testutil.EchoTool("echo")),
        testutil.WithMaxTurns(5),
    )
    defer h.Close()

    h.Prompt("test input")
    h.Wait(10 * time.Second)

    assert.True(t, h.Events.HasEvent(agent.EventAgentEnd))
}
```

## Key Types

| Type | Description |
|------|-------------|
| `AgentHarness` | Fully-wired test harness for `agent.Agent` with mock LLM |
| `EventCollector` | Collects and queries agent events |
| `MockTool` | Configurable mock tool for testing |
| `SSEBuilder` | Builds SSE response strings for mock LLM server |
| `HarnessOption` | Functional option for configuring `AgentHarness` |

## Mock LLM Responses

```go
// Text response
testutil.TextResponse("hello")

// Tool call response
testutil.ToolCallResponse("call-1", "bash", map[string]any{"command": "ls"}, "tool_use")

// Custom SSE sequence
testutil.NewSSEBuilder().
    Text("thinking...").
    Thinking("internal reasoning").
    ToolCall("call-1", "read", map[string]any{"path": "/tmp"}).
    Finish("tool_use", testutil.UsageFields{})
```

## Mock Tools

| Constructor | Description |
|-------------|-------------|
| `EchoTool(name)` | Returns its arguments as text |
| `SlowTool(name, delay)` | Delays before responding |
| `FailingTool(name, err)` | Always returns error |
| `CountingTool(name, n, err)` | Fails first N calls, then succeeds |

## Key Files

| File | Description |
|------|-------------|
| `agent_harness.go` | `AgentHarness` — test harness with mock LLM server |
| `event_collector.go` | `EventCollector` — event recording and querying |
| `mock_tool.go` | `MockTool` and tool constructors (Echo, Slow, Failing, Counting) |
| `sse_server.go` | `SSEBuilder`, `LLMServer()` — mock LLM SSE response builder |