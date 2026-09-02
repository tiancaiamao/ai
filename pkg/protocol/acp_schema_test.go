package protocol_test

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
