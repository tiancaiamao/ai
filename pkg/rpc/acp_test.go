package rpc

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runACPSmoke starts RunACP with the given NDJSON lines, returns all
// JSON-RPC messages (responses and notifications) read from stdout.
func runACPSmoke(t *testing.T, tmpDir string, lines []string) []map[string]any {
	t.Helper()

	// Ensure API key is set so resolveModelAndKey doesn't fail in CI.
	t.Setenv("ZAI_API_KEY", "test-key")

	// Isolate config to avoid polluting ~/.ai/config.json.
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("AI_CONFIG_PATH", configPath)

	reader, writer := io.Pipe()
	outReader, outWriter := io.Pipe()

	go func() {
		for _, l := range lines {
			writer.Write([]byte(l + "\n"))
		}
		writer.Close()
	}()

	respCh := make(chan []map[string]any, 1)
	go func() {
		var results []map[string]any
		scanner := bufio.NewScanner(outReader)
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var m map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				continue
			}
			results = append(results, m)
		}
		respCh <- results
	}()

	_ = RunACP(tmpDir, "", reader, outWriter, "", 0, 5*time.Second, "", "", "acp-smoke")
	outWriter.Close()

	return <-respCh
}

func TestACPInitialize(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %v", len(msgs), msgs)
	}
	m := msgs[0]
	if m["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", m["jsonrpc"])
	}
	if id, _ := m["id"].(float64); id != 1 {
		t.Errorf("expected id 1, got %v", m["id"])
	}
	result, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", m)
	}
	if v, _ := result["protocolVersion"].(float64); v != acpProtocolVersion {
		t.Errorf("expected protocolVersion %d, got %v", acpProtocolVersion, result["protocolVersion"])
	}
	caps, ok := result["agentCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected agentCapabilities object, got %v", result)
	}
	promptCaps, ok := caps["promptCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected promptCapabilities object, got %v", caps)
	}
	if embedded, _ := promptCaps["embeddedContext"].(bool); !embedded {
		t.Errorf("expected embeddedContext capability true, got %v", promptCaps["embeddedContext"])
	}
}

func TestACPSessionNewAndPromptErrors(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
		// Unknown session id must be rejected.
		`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"bogus","prompt":[{"type":"text","text":"hi"}]}}`,
		// Empty prompt must be rejected before any LLM call.
		`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"","prompt":[{"type":"text","text":"   "}]}}`,
	})
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(msgs), msgs)
	}

	// initialize
	result, _ := msgs[0]["result"].(map[string]any)
	if v, _ := result["protocolVersion"].(float64); v != acpProtocolVersion {
		t.Errorf("initialize: unexpected protocolVersion: %v", result)
	}

	// session/new
	result, _ = msgs[1]["result"].(map[string]any)
	sid, _ := result["sessionId"].(string)
	if sid == "" {
		t.Errorf("session/new: expected non-empty sessionId, got %v", result)
	}

	// unknown session id -> invalid params error
	if errObj, ok := msgs[2]["error"].(map[string]any); !ok {
		t.Errorf("prompt with unknown sessionId: expected error, got %v", msgs[2])
	} else if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
		t.Errorf("expected error code %d, got %v", acpErrInvalidParams, errObj["code"])
	}

	// empty prompt -> invalid params error
	if errObj, ok := msgs[3]["error"].(map[string]any); !ok {
		t.Errorf("empty prompt: expected error, got %v", msgs[3])
	} else if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
		t.Errorf("expected error code %d, got %v", acpErrInvalidParams, errObj["code"])
	}
}

func TestACPUnknownMethodAndNotification(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`,
		// Unknown method -> method not found.
		`{"jsonrpc":"2.0","id":3,"method":"fs/read_text_file","params":{}}`,
		// session/cancel is a notification: no response expected.
		`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"sess"}}`,
		// Malformed JSON -> invalid request error (no id).
		`this is not json`,
	})
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(msgs), msgs)
	}

	errObj, ok := msgs[2]["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown method: expected error, got %v", msgs[2])
	}
	if code, _ := errObj["code"].(float64); code != acpErrMethodNotFound {
		t.Errorf("expected method not found code %d, got %v", acpErrMethodNotFound, errObj["code"])
	}

	// Cancel notification produced no message; the malformed line produced
	// an invalid request error.
	errObj, ok = msgs[3]["error"].(map[string]any)
	if !ok {
		t.Fatalf("malformed json: expected error, got %v", msgs[3])
	}
	if code, _ := errObj["code"].(float64); code != acpErrInvalidRequest {
		t.Errorf("expected invalid request code %d, got %v", acpErrInvalidRequest, errObj["code"])
	}
}

func TestBuildACPMessage(t *testing.T) {
	msg := buildACPMessage([]acpContentBlock{
		{Type: "text", Text: "Analyze this:"},
		{Type: "resource", Resource: &acpEmbeddedResource{URI: "file:///tmp/a.go", Text: "package main"}},
		{Type: "resource_link", URI: "file:///tmp/b.go"},
	})
	for _, want := range []string{"Analyze this:", "=== file:///tmp/a.go ===", "package main", "[file: /tmp/b.go]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("buildACPMessage output missing %q:\n%s", want, msg)
		}
	}
}
