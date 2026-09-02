package protocol

import (
	"bytes"
	"embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/transport"
)

//go:embed testdata/acp_schema_v1.json
var acpInternalSchemaFS embed.FS

func newInternalSchemaCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	data, err := acpInternalSchemaFS.ReadFile("testdata/acp_schema_v1.json")
	if err != nil {
		t.Fatalf("read embedded schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("acp.json", bytes.NewReader(data)); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	return c
}

func assertInternalMessageValid(t *testing.T, c *jsonschema.Compiler, msg json.RawMessage) {
	t.Helper()
	sch, err := c.Compile("acp.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	var v any
	if err := json.Unmarshal(msg, &v); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if err := sch.Validate(v); err != nil {
		t.Errorf("message violates official ACP v1 schema:\n%v\nmessage: %s", err, msg)
	}
}

func TestACPSchemaNotifications(t *testing.T) {
	compiler := newInternalSchemaCompiler(t)
	var buf bytes.Buffer
	srv := &acpServer{
		app:       &testRuntime{contextWindow: 128000},
		conn:      transport.NewStdio(strings.NewReader(""), &buf),
		sessionID: "sess-test",
	}
	srv.emit(agent.NewMessageUpdateEvent(context.AgentMessage{}, agent.AssistantMessageEvent{Type: "text_delta", Delta: "hello "}))
	srv.emit(agent.NewMessageUpdateEvent(context.AgentMessage{}, agent.AssistantMessageEvent{Type: "thinking_delta", Delta: "let me think..."}))
	srv.emit(agent.NewMessageEndEvent(context.AgentMessage{Role: "assistant", Usage: &context.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125}}))
	srv.emit(agent.NewToolExecutionStartEvent("tool-1", "bash", map[string]interface{}{"command": "ls"}))
	srv.emit(agent.NewToolExecutionEndEvent("tool-1", "bash", &context.AgentMessage{Content: []context.ContentBlock{context.TextContent{Text: "file1.txt"}}}, false))
	srv.emit(agent.NewToolExecutionEndEvent("tool-2", "read", &context.AgentMessage{Content: []context.ContentBlock{context.TextContent{Text: "permission denied"}}}, true))

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 6 {
		t.Fatalf("expected 6 notifications, got %d: %s", len(lines), buf.String())
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("notification %d not valid JSON: %v", i, err)
		}
		if m["method"] != "session/update" {
			t.Errorf("notification %d: expected method session/update, got %v", i, m["method"])
		}
		assertInternalMessageValid(t, compiler, line)
	}
}
