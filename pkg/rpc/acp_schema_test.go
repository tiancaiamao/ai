package rpc

// Conformance tests: validate every message this agent emits against the
// official ACP v1 JSON Schema (downloaded from
// github.com/agentclientprotocol/agent-client-protocol, schema/v1/schema.json).
// This catches structural drift from the spec (wrong field names, missing
// required fields, wrong content block shapes) without needing a live client.
//
// The embedded schema is draft 2020-12; $defs resolve within the document.

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
var acpSchemaFS embed.FS

// newACPSchemaCompiler builds a validator over the official v1 schema.
func newACPSchemaCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	data, err := acpSchemaFS.ReadFile("testdata/acp_schema_v1.json")
	if err != nil {
		t.Fatalf("read embedded schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("acp.json", bytes.NewReader(data)); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	return c
}

// assertACPMessageValid validates one emitted message against the schema.
func assertACPMessageValid(t *testing.T, c *jsonschema.Compiler, msg json.RawMessage) {
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

// TestACPSchemaResponses validates the responses produced by a live server
// (initialize, session/new, and error paths) against the official schema.
func TestACPSchemaResponses(t *testing.T) {
	compiler := newACPSchemaCompiler(t)
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
		// Unknown session id -> invalid params error.
		`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"bogus","prompt":[{"type":"text","text":"hi"}]}}`,
		// Empty prompt -> invalid params error.
		`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"","prompt":[{"type":"text","text":"  "}]}}`,
		// Unknown method -> method not found error.
		`{"jsonrpc":"2.0","id":5,"method":"fs/read_text_file","params":{}}`,
	})
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d: %v", len(msgs), msgs)
	}
	for i, m := range msgs {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message %d: %v", i, err)
		}
		assertACPMessageValid(t, compiler, raw)
	}
}

// TestACPSchemaNotifications validates the session/update notifications that
// the event translator (acpServer.emit) produces, using synthetic agent
// events — no live LLM involved.
func TestACPSchemaNotifications(t *testing.T) {
	compiler := newACPSchemaCompiler(t)

	var buf bytes.Buffer
	srv := &acpServer{conn: transport.NewStdio(strings.NewReader(""), &buf), sessionID: "sess-test"}

	// agent_message_chunk
	srv.emit(agent.NewMessageUpdateEvent(
		context.AgentMessage{},
		agent.AssistantMessageEvent{Type: "text_delta", Delta: "hello "},
	))
	// agent_thought_chunk (streaming reasoning)
	srv.emit(agent.NewMessageUpdateEvent(
		context.AgentMessage{},
		agent.AssistantMessageEvent{Type: "thinking_delta", Delta: "let me think..."},
	))
	// tool_call (pending)
	srv.emit(agent.NewToolExecutionStartEvent("tool-1", "bash", map[string]interface{}{"command": "ls"}))
	// tool_call_update (completed, with tool output)
	srv.emit(agent.NewToolExecutionEndEvent("tool-1", "bash", &context.AgentMessage{
		Content: []context.ContentBlock{context.TextContent{Text: "file1.txt"}},
	}, false))
	// tool_call_update (error path)
	srv.emit(agent.NewToolExecutionEndEvent("tool-2", "read", &context.AgentMessage{
		Content: []context.ContentBlock{context.TextContent{Text: "permission denied"}},
	}, true))

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 5 {
		t.Fatalf("expected 5 notifications, got %d: %s", len(lines), buf.String())
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("notification %d not valid JSON: %v", i, err)
		}
		if m["method"] != "session/update" {
			t.Errorf("notification %d: expected method session/update, got %v", i, m["method"])
		}
		assertACPMessageValid(t, compiler, line)
	}

	// The thought chunk must carry the delta as an agent_thought_chunk text
	// content block so hosts like AionUi render it as a thinking card.
	var thoughtMsg map[string]any
	if err := json.Unmarshal(lines[1], &thoughtMsg); err != nil {
		t.Fatalf("notification 1 not valid JSON: %v", err)
	}
	thoughtUpd := thoughtMsg["params"].(map[string]any)["update"].(map[string]any)
	if kind, _ := thoughtUpd["sessionUpdate"].(string); kind != "agent_thought_chunk" {
		t.Errorf("expected agent_thought_chunk, got %v", thoughtUpd)
	}
	content, _ := thoughtUpd["content"].(map[string]any)
	if text, _ := content["text"].(string); text != "let me think..." || content["type"] != "text" {
		t.Errorf("agent_thought_chunk: expected text block with delta, got %v", thoughtUpd["content"])
	}

	// The pending tool_call must surface its arguments as rawInput — hosts
	// like AionUi read this field to render the invocation parameters.
	var startMsg map[string]any
	if err := json.Unmarshal(lines[2], &startMsg); err != nil {
		t.Fatalf("notification 2 not valid JSON: %v", err)
	}
	toolUpd := startMsg["params"].(map[string]any)["update"].(map[string]any)
	rawInput, ok := toolUpd["rawInput"].(map[string]any)
	if !ok {
		t.Fatalf("tool_call: expected rawInput object, got %v", toolUpd)
	}
	if cmd, _ := rawInput["command"].(string); cmd != "ls" {
		t.Errorf("tool_call: expected rawInput command \"ls\", got %v", rawInput)
	}

	var endMsg map[string]any
	if err := json.Unmarshal(lines[3], &endMsg); err != nil {
		t.Fatalf("notification 3 not valid JSON: %v", err)
	}
	endUpd := endMsg["params"].(map[string]any)["update"].(map[string]any)
	if title, _ := endUpd["title"].(string); title != "bash" {
		t.Errorf("tool_call_update: expected title bash, got %v", endUpd["title"])
	}
}

// TestACPSchemaEmbeddedSchemaSource pins the schema provenance so a stale
// embedded copy can't silently diverge from upstream.
func TestACPSchemaEmbeddedSchemaSource(t *testing.T) {
	data, err := acpSchemaFS.ReadFile("testdata/acp_schema_v1.json")
	if err != nil {
		t.Fatalf("read embedded schema: %v", err)
	}
	var meta struct {
		Title  string `json:"title"`
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal schema header: %v", err)
	}
	if meta.Title != "Agent Client Protocol" {
		t.Errorf("unexpected schema title %q", meta.Title)
	}
	if !strings.Contains(meta.Schema, "2020-12") {
		t.Errorf("expected draft 2020-12 schema, got %q", meta.Schema)
	}
}
