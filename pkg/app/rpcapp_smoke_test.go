package app

import (
	"bufio"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// commandResponses filters out non-response events (server_start, streaming, etc.)
func commandResponses(responses []map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range responses {
		rtype, _ := r["type"].(string)
		if rtype == "response" {
			out = append(out, r)
		}
	}
	return out
}

func sendCommands(w io.WriteCloser, cmds ...string) {
	for _, c := range cmds {
		w.Write([]byte(c + "\n"))
	}
	w.Close()
}

func readResponses(r io.Reader) []map[string]any {
	var results []map[string]any
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		results = append(results, m)
	}
	return results
}

// runRPCSmoke starts RunRPC with the given commands, returns command responses.
func runRPCSmoke(t *testing.T, tmpDir string, cmds []string, modelOverride string) []map[string]any {
	t.Helper()

	// Ensure API key is set so resolveModelAndKey doesn't fail in CI.
	t.Setenv("ZAI_API_KEY", "test-key")

	reader, writer := io.Pipe()
	outReader, outWriter := io.Pipe()

	go sendCommands(writer, cmds...)

	respCh := make(chan []map[string]any, 1)
	go func() {
		respCh <- readResponses(outReader)
	}()

	_ = RunRPC(tmpDir, "", reader, outWriter, "", 0, 5*time.Second, "", modelOverride, "smoke-test")
	outWriter.Close()

	all := <-respCh
	return commandResponses(all)
}

func assertCmdSuccess(t *testing.T, resp map[string]any, context string) {
	t.Helper()
	success, _ := resp["success"].(bool)
	if !success {
		cmd, _ := resp["command"].(string)
		t.Errorf("%s: command %q was not successful: %v", context, cmd, resp)
	}
}

// --- Tests ---

func TestRPCAppSmoke(t *testing.T) {
	cmds := []string{
		`{"type":"help"}`,
		`{"type":"session"}`,
		`{"type":"model"}`,
		`{"type":"context"}`,
		`{"type":"show"}`,
		`{"type":"skills"}`,
		`{"type":"toggle","message":"thinking"}`,
		`{"type":"thinking","message":"low"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 6 {
		t.Fatalf("expected at least 6 command responses, got %d", len(responses))
	}
	for _, resp := range responses {
		assertCmdSuccess(t, resp, "smoke")
	}
}

func TestRPCAppPing(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"ping"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected ping response")
	}
	if cmd, _ := responses[0]["command"].(string); cmd != "ping" {
		t.Errorf("expected command 'ping', got %q", cmd)
	}
}

func TestRPCAppHelpContent(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"help"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected help response")
	}
	data, ok := responses[0]["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data field in help response")
	}
	cmds, _ := data["commands"].([]any)
	if len(cmds) == 0 {
		t.Error("expected non-empty commands list")
	}
}

func TestRPCAppModelList(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"model"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected model response")
	}
	assertCmdSuccess(t, responses[0], "model list")
}

func TestRPCAppModelSwitch(t *testing.T) {
	cmds := []string{
		`{"type":"model","message":"zai/glm-4.5-air"}`,
		`{"type":"model"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}
	for _, resp := range responses {
		assertCmdSuccess(t, resp, "model switch")
	}
}

func TestRPCAppSet(t *testing.T) {
	cmds := []string{
		`{"type":"set","message":"auto-compaction off"}`,
		`{"type":"set","message":"thinking-level medium"}`,
		`{"type":"set","message":"trace-events on"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(responses))
	}
	for _, resp := range responses {
		assertCmdSuccess(t, resp, "set")
	}
}

func TestRPCAppSessionInfo(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"session"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected session response")
	}
	if data, ok := responses[0]["data"].(map[string]any); ok {
		if model, ok := data["model"].(map[string]any); !ok || model == nil {
			t.Error("expected non-nil model in session response")
		}
	}
}

func TestRPCAppContext(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"context"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected context response")
	}
	assertCmdSuccess(t, responses[0], "context")
}

func TestRPCAppAbort(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"abort"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected abort response")
	}
	assertCmdSuccess(t, responses[0], "abort")
}

func TestRPCAppShowPipeline(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"show","message":"pipeline"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected show pipeline response")
	}
}

func TestRPCAppMessages(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"messages"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected messages response")
	}
}

func TestRPCAppExportHTML(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"export_html"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected export_html response")
	}
}

func TestRPCAppShowSettings(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"show","message":"settings"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected settings response")
	}
	assertCmdSuccess(t, responses[0], "show settings")
	// Verify settings structure
	if data, ok := responses[0]["data"].(map[string]any); ok {
		// The response contains a nested data field
		if innerData, ok := data["data"].(map[string]any); ok {
			// Verify data map contains expected fields
			if _, ok := innerData["model"]; !ok {
				t.Error("expected model field in settings response")
			}
		} else {
			t.Error("expected nested data field in settings response")
		}
	}
}

func TestRPCAppSetFollowUpMode(t *testing.T) {
	cmds := []string{
		`{"type":"set","message":"follow-up-mode one-at-a-time"}`,
		`{"type":"set","message":"follow-up-mode all"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}
	for i, resp := range responses {
		assertCmdSuccess(t, resp, "set follow-up-mode")
		t.Logf("response %d: %+v", i, resp)
	}
}

func TestRPCAppSetSteeringMode(t *testing.T) {
	cmds := []string{
		`{"type":"set","message":"steering-mode all"}`,
		`{"type":"set","message":"steering-mode one-at-a-time"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}
	for i, resp := range responses {
		assertCmdSuccess(t, resp, "set steering-mode")
		t.Logf("response %d: %+v", i, resp)
	}
}

func TestRPCAppSetToolCallCutoff(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"set","message":"tool-call-cutoff 10"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected tool-call-cutoff response")
	}
	assertCmdSuccess(t, responses[0], "set tool-call-cutoff")
}

func TestRPCAppSetToolSummaryAutomation(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"set","message":"tool-summary-automation fallback"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected tool-summary-automation response")
	}
	assertCmdSuccess(t, responses[0], "set tool-summary-automation")
}

func TestRPCAppSetSessionName(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"set","message":"session-name Test Session"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected session-name response")
	}
	assertCmdSuccess(t, responses[0], "set session-name")
}

func TestRPCAppCompact(t *testing.T) {
	cmds := []string{
		`{"type":"compact","message":"now"}`,
		`{"type":"compact","message":"off"}`,
	}
	responses := runRPCSmoke(t, t.TempDir(), cmds, "")
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}
	for i, resp := range responses {
		assertCmdSuccess(t, resp, "compact")
		t.Logf("compact response %d: %+v", i, resp)
	}
}

func TestRPCAppResume(t *testing.T) {
	// First, try to resume with no sessions (should list available sessions)
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"resume","message":""}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected resume response")
	}
	// Resume may fail if no sessions exist, but should return a response
	t.Logf("resume response: %+v", responses[0])
}

func TestRPCAppNewSession(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"new_session"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected new_session response")
	}
	t.Logf("new_session response: %+v", responses[0])
}

func TestRPCAppRetry(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"retry"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected retry response")
	}
	t.Logf("retry response: %+v", responses[0])
}

func TestRPCAppUndo(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"undo"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected undo response")
	}
	t.Logf("undo response: %+v", responses[0])
}

func TestRPCAppRedo(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"redo"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected redo response")
	}
	t.Logf("redo response: %+v", responses[0])
}

func TestRPCAppContinue(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"continue"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected continue response")
	}
	t.Logf("continue response: %+v", responses[0])
}

func TestRPCAppInvalidCommand(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"nonexistent_command_xyz"}`}, "")
	if len(responses) == 0 {
		t.Fatal("expected a response for invalid command")
	}
}

func TestRPCAppModelOverride(t *testing.T) {
	responses := runRPCSmoke(t, t.TempDir(), []string{`{"type":"model"}`}, "claude-sonnet-4-20250514")
	if len(responses) == 0 {
		t.Fatal("expected model response with override")
	}
	assertCmdSuccess(t, responses[0], "model override")
}
