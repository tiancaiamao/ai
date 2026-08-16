//go:build e2e

// E2E scenario tests: drive the instrumented `ai rpc` binary black-box and
// collect whole-app coverage. Behavior is split across focused tests:
//
//	StreamingCompletion / MultiTurnContext / ToolExecution — real LLM prompts
//	RPCCommands    — protocol errors + the full slash-command surface
//	SessionLifecycle — session create/resume/fork/rewind/persist
//	BusyAndAbort   — streaming-time policies, abort, timeout watchdog
//	FlagsAndRoles  — CLI flags, agent.yaml roles, failure exits
//	Subcommands    — ai models / serve → ls → send → kill
//	Compaction     — manual compaction against a real session
package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Model resolution + isolated models.json ---

type e2eModel struct {
	provider string
	id       string
	baseURL  string
}

// requireEndpoint picks a model spec (E2E_BASE_URL overrides the endpoint,
// E2E_MODEL overrides the model id) and skips when nothing is reachable.
func requireEndpoint(t *testing.T) e2eModel {
	t.Helper()
	m := e2eModel{provider: "ollama", id: "laguna"}
	if v := os.Getenv("E2E_MODEL"); v != "" {
		m.id = v
	}
	hostBase := ""
	home, _ := os.UserHomeDir()
	if data, err := os.ReadFile(filepath.Join(home, ".ai", "models.json")); err == nil {
		var f struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
				Models  []struct {
					ID      string `json:"id"`
					BaseURL string `json:"baseUrl"`
				} `json:"models"`
			} `json:"providers"`
		}
		if json.Unmarshal(data, &f) == nil {
			for _, p := range []string{"ollama", "laguna"} {
				if pc, ok := f.Providers[p]; ok && len(pc.Models) > 0 {
					base := pc.Models[0].BaseURL
					if base == "" {
						base = pc.BaseURL
					}
					m = e2eModel{provider: p, id: m.id, baseURL: base}
					if m.baseURL == "" {
						m.id = pc.Models[0].ID
					}
					hostBase = m.baseURL
					break
				}
			}
		}
	} else if v := os.Getenv("E2E_BASE_URL"); v == "" {
		t.Skipf("no models.json and no E2E_BASE_URL: %v", err)
	}

	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		m.baseURL = v
	}
	if m.baseURL == "" {
		m.baseURL = hostBase
	}
	if m.baseURL == "" {
		t.Skip("no model endpoint configured (set E2E_BASE_URL)")
	}
	m = e2eModel{provider: m.provider, id: m.id, baseURL: m.baseURL}

	if v := os.Getenv("E2E_ALLOW_UNREACHABLE"); v == "" && !reachable(m.baseURL) {
		t.Skipf("model endpoint %s unreachable (set E2E_ALLOW_UNREACHABLE=1 to force)", m.baseURL)
	}
	return m
}

func reachable(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return true
}

// writeE2EModels writes an isolated models.json for a subprocess HOME.
func writeE2EModels(path, provider, baseURL, id string) error {
	doc := map[string]any{
		"providers": map[string]any{
			provider: map[string]any{
				"baseUrl": baseURL,
				"models": []map[string]any{
					{
						"id":            id,
						"name":          "e2e-" + id,
						"api":           "openai-completions",
						"reasoning":     true,
						"contextWindow": 32768,
						"maxTokens":     8192,
					},
				},
			},
		},
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- Core LLM scenarios ---

func TestE2E_StreamingCompletion(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")
	reply := rs.promptAndWait(t, "Reply with the single word: pong")
	if !strings.Contains(reply, "pong") {
		t.Fatalf("expected reply to contain %q, got %q", "pong", reply)
	}
}

func TestE2E_MultiTurnContext(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")
	rs.promptAndWait(t, "Remember the number 1978. Reply with exactly: got it")
	reply := rs.promptAndWait(t, "What number did I tell you to remember? Reply with only the number.")
	if !strings.Contains(reply, "1978") {
		t.Fatalf("expected reply to contain %q, got %q", "1978", reply)
	}
}

func TestE2E_ToolExecution(t *testing.T) {
	m := requireEndpoint(t)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "secret.txt"), []byte("emerald-42"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := startRPCServer(t, m, workDir)
	reply := rs.promptAndWait(t, "Read the file secret.txt in the workspace and tell me exactly what is inside it. It contains a marker of the form word-number.")
	if !strings.Contains(reply, "emerald-42") {
		t.Fatalf("expected reply to contain tool result %q, got %q", "emerald-42", reply)
	}
	// The binary must have actually executed a tool (not just hallucinated).
	if !strings.Contains(rs.log.History(), "emerald-42") {
		t.Fatalf("tool result never appeared on the wire:\n%s", rs.log.History())
	}
}

// --- Protocol + slash command surface ---

func TestE2E_RPCCommands(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")

	// --- Protocol-level error handling ---
	rs.send(t, `{"type":`) // malformed JSON
	assertRawError(t, rs, "Failed to parse command")

	// Unknown command type → handler error path.
	rs.rpcErr(t, "nope", "", "No nope handler registered")

	// prompt validation errors.
	rs.rpcErr(t, "prompt", "", "empty prompt message")
	rs.send(t, `{"type":"prompt","message":"x","data":{"images":["i"]}}`)
	assertRawError(t, rs, "images are not supported")
	rs.rpcErr(t, "prompt", "/definitely-not-a-command", "unknown command")

	// --- simple introspection commands ---
	for _, cmd := range []string{"help", "skills", "context", "session", "messages", "get_session_stats", "get_last_assistant_text", "get_tree", "get_trace_events"} {
		rs.rpcAck(t, cmd, "")
	}

	// --- model commands ---
	rs.rpcAck(t, "model", "")
	rs.rpcAck(t, "set_model", m.provider+" "+m.id)
	rs.rpcErr(t, "set_model", "nonexistent-provider xyz", "model not found")

	// --- /set matrix ---
	rs.rpcAck(t, "set", "help")
	rs.rpcAck(t, "set", "auto-retry on")
	for _, v := range []string{"on", "off"} {
		rs.rpcAck(t, "set", "auto-compaction "+v)
	}
	for _, v := range []string{"steer", "follow-up", "reject"} {
		rs.rpcAck(t, "set", "busy-mode "+v)
	}
	rs.rpcErr(t, "set", "busy-mode bogus", "usage")
	rs.rpcAck(t, "set", "follow-up-mode all")
	rs.rpcErr(t, "set", "follow-up-mode bogus", "invalid follow-up mode")
	for _, v := range []string{"on", "off", "toggle"} {
		rs.rpcAck(t, "set", "prefix-display "+v)
	}
	rs.rpcAck(t, "set", "thinking-display on")
	rs.rpcAck(t, "set", "thinking-level minimal")
	rs.rpcErr(t, "set", "thinking-level bogus", "invalid thinking level")
	rs.rpcAck(t, "set", "tool-call-cutoff 50")
	rs.rpcAck(t, "set", "tool-summary-automation fallback")
	rs.rpcErr(t, "set", "tool-summary-automation bogus", "invalid")
	rs.rpcAck(t, "set", "tools-display off")
	rs.rpcAck(t, "set", "session-name e2e-session")
	rs.rpcAck(t, "set", "trace-events on")
	rs.rpcErr(t, "set", "not-a-setting 1", "unknown setting")

	// --- dedicated toggles / shows ---
	for _, v := range []string{"off", "low", "medium", "high", "xhigh"} {
		rs.rpcAck(t, "thinking", v)
	}
	rs.rpcErr(t, "thinking", "ultra", "invalid thinking level")
	rs.rpcAck(t, "trace-events", "off")
	rs.rpcAck(t, "toggle", "thinking")
	rs.rpcAck(t, "toggle", "prefix")
	rs.rpcAck(t, "toggle", "tools")
	rs.rpcErr(t, "toggle", "bogus", "usage")
	rs.rpcAck(t, "show", "")
	rs.rpcAck(t, "show", "settings")
	rs.rpcErr(t, "show", "bogus", "usage")

	// --- hidden backward-compatible forwarders ---
	rs.rpcAck(t, "set_auto_compaction", "on")
	rs.rpcAck(t, "set_thinking_level", "low")
	rs.rpcAck(t, "set_trace_events", "off")

	// --- known unsupported command ---
	rs.rpcErr(t, "export_html", "", "not supported")

	// EOF shutdown.
	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

// assertRawError waits for any error response and checks the message.
func assertRawError(t *testing.T, rs *rpcServer, wantErr string) {
	t.Helper()
	resp := rs.log.waitEvent("response", "", "", 30*time.Second)
	if resp == "" {
		t.Fatalf("no error response (wanted %q)", wantErr)
	}
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		t.Fatalf("parse error response: %v\n%s", err, resp)
	}
	if r.Success {
		t.Fatalf("expected error response, got success: %s", resp)
	}
	if !strings.Contains(r.Error, wantErr) {
		t.Fatalf("error %q does not contain %q", r.Error, wantErr)
	}
}

// --- Session lifecycle (create → switch → resume → fork → rewind) ---

func TestE2E_SessionLifecycle(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")

	// Build some history so fork/rewind have messages to operate on.
	if reply := rs.promptAndWait(t, "Reply with exactly: ready"); !strings.Contains(reply, "ready") {
		t.Fatalf("unexpected reply: %q", reply)
	}

	// Create + switch to a named session.
	sess2 := rs.rpcAck(t, "new", "e2e-second")
	if sess2["sessionId"] == "" {
		t.Fatalf("new session returned no sessionId: %v", sess2)
	}

	// Current state reflects the switch.
	rs.rpcAck(t, "session", "")

	// Resume: list, then by index, then by name, then errors.
	list := rs.rpcAck(t, "resume", "")
	if sessions, _ := list["sessions"].([]any); len(sessions) < 2 {
		t.Fatalf("expected >= 2 sessions, got %d", len(sessions))
	}
	for _, s := range list["sessions"].([]any) {
		t.Logf("session list: %v", s)
	}
	res0 := rs.rpcAck(t, "resume", "0")
	t.Logf("resume 0 -> %v", res0)
	rs.rpcErr(t, "resume", "99", "out of range")
	rs.rpcErr(t, "resume", "zz-does-not-exist", "")

	// Messaging / stats / session-name bookkeeping.
	rs.rpcAck(t, "messages", "5")
	rs.rpcAck(t, "get_session_stats", "")

	// Pick a real entry ID from the conversation tree.
	tree := rs.rpcAck(t, "get_tree", "")
	var forkID string
	if entries, ok := tree["entries"].([]any); ok {
		for _, e := range entries {
			em, _ := e.(map[string]any)
			if em["role"] == "user" {
				forkID, _ = em["entryId"].(string)
				break
			}
		}
	}
	if forkID == "" {
		t.Fatalf("no user entry in tree: %v", tree)
	}

	// Inspect fork candidates, rewind onto the message, reset to root.
	rs.rpcAck(t, "get_fork_messages", "ignored")
	rs.rpcAck(t, "rewind", forkID)
	rs.rpcAck(t, "rewind", "root")

	// Fork; the app switches to the new forked session.
	rs.rpcAck(t, "fork", forkID)
	rs.rpcAck(t, "get_tree", "")
	rs.rpcAck(t, "messages", "5")

	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

// --- Streaming-time policies, abort and the timeout watchdog ---

func TestE2E_BusyAndAbort(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")

	// A streaming prompt we can probe mid-flight (fast enough to finish, slow
	// enough that we can inject interactions while it streams).
	rs.send(t, `{"type":"prompt","message":"Count from 1 to 30, listing every number."}`)
	if ack := rs.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack. stderr:\n%s", rs.logTail())
	}
	time.Sleep(500 * time.Millisecond) // let the agent start streaming

	// Busy-mode policy: reject while streaming.
	rs.send(t, `{"type":"prompt","message":"ignored","data":{"streamingBehavior":"reject"}}`)
	assertRawError(t, rs, "rejected by busy-mode policy")

	// Abort the stream; the agent ends either immediately or after the
	// in-flight generation completes.
	rs.rpcAck(t, "abort", "")
	if ev := rs.log.waitEvent("agent_end", "", "", 3*time.Minute); ev == "" {
		t.Fatalf("no agent_end after abort. emitted lines:\n%s\nstate: %s",
			rs.log.History(), rs.log.DebugState())
	}

	// The server must still be usable afterwards.
	rs.promptAndWait(t, "Reply with exactly: ok")

	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

func TestE2E_TimeoutWatchdog(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "", "-timeout", "4s")

	// Long task; the 4s watchdog aborts the agent and cancels the server.
	rs.send(t, `{"type":"prompt","message":"Write a very long essay, at least 3000 words, about the history of computing."}`)
	if ack := rs.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack")
	}
	if err := rs.waitExit(30 * time.Second); err != nil {
		t.Fatalf("timeout watchdog did not terminate the server: %v", err)
	}
}

// --- CLI flags, roles, failure exits ---

func TestE2E_FlagsAndRoles(t *testing.T) {
	m := requireEndpoint(t)

	// Role: agent.yaml under the isolated HOME.
	home := t.TempDir()
	roleDir := filepath.Join(home, ".ai", "roles", "e2e-role")
	agentYAML := "version: 1\nsystem_prompt: \"You are the E2E test role. Reply with exactly: role-ok\"\ntools:\n  - name: read\n    enabled: true\n"
	if err := os.MkdirAll(roleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "agent.yaml"), []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, "", "-role", "e2e-role")
	reply := rs.promptAndWait(t, "What is your role? Reply exactly: role-ok")
	if !strings.Contains(reply, "role-ok") {
		t.Fatalf("role system prompt not honored, reply: %q", reply)
	}
	rs.closeStdin()
	rs.waitExit(15 * time.Second)

	// System prompt from file (@file).
	spFile := filepath.Join(t.TempDir(), "sp.md")
	if err := os.WriteFile(spFile, []byte("custom system prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := startRPCServer(t, m, "", "-system-prompt", "@"+spFile)
	sp.closeStdin()
	sp.waitExit(15 * time.Second)

	// Debug HTTP server on an ephemeral port.
	httpSrv := startRPCServer(t, m, "", "-http", "127.0.0.1:0")
	httpSrv.closeStdin()
	httpSrv.waitExit(15 * time.Second)

	// Max turns: one turn then the agent stops.
	turns := startRPCServer(t, m, "", "-max-turns", "1")
	turns.send(t, `{"type":"prompt","message":"Reply with exactly: hi"}`)
	if ack := turns.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack")
	}
	if ev := turns.log.waitEvent("agent_end", "", "", 3*time.Minute); ev == "" {
		t.Fatalf("no agent_end with max-turns=1")
	}
	turns.closeStdin()
	turns.waitExit(15 * time.Second)

	// Run ID threading.
	rid := startRPCServer(t, m, "", "-runid", "e2e-run-abc")
	rid.closeStdin()
	rid.waitExit(15 * time.Second)

	// Unknown role → startup failure with nonzero exit.
	code := expectExit(t, m, "", "-role", "does-not-exist-role")
	if code == 0 {
		t.Fatalf("unknown role should exit nonzero, got 0")
	}

	// Missing API key → startup failure.
	code = expectExitNoKey(t, m, "")
	if code == 0 {
		t.Fatalf("missing API key should exit nonzero, got 0")
	}
}

func expectExit(t *testing.T, m e2eModel, workDir string, flags ...string) int {
	t.Helper()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath, append([]string{"rpc"}, append(flags, "-model", m.provider+"/"+m.id, "-session", filepath.Join(t.TempDir(), "s.jsonl"))...)...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+home, "OLLAMA_API_KEY=e2e", "AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"))
	stdin, _ := cmd.StdinPipe()
	stdin.Close() // immediate EOF → RunRPC exits as soon as setup fails
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("no process state; output:\n%s", out)
	}
	return cmd.ProcessState.ExitCode()
}

// expectExitNoKey verifies startup fails without any API key for the provider.
func expectExitNoKey(t *testing.T, m e2eModel, workDir string) int {
	t.Helper()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath, "rpc", "-model", m.provider+"/"+m.id, "-session", filepath.Join(t.TempDir(), "s.jsonl"))
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "HOME="+home, "AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"))
	stdin, _ := cmd.StdinPipe()
	stdin.Close()
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("no process state; output:\n%s", out)
	}
	return cmd.ProcessState.ExitCode()
}

// --- Other CLI entry points (subcommands) ---

func TestE2E_Subcommands(t *testing.T) {
	m := requireEndpoint(t)

	// The run-control unix socket lives under $HOME/.ai/runs/<id>/; keep the
	// path short (t.TempDir on macOS sits deep under /var/folders and the
	// socket name would exceed the 104-byte unix socket limit).
	home, err := os.MkdirTemp("/tmp", "ai-e2e-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	modelsPath := filepath.Join(home, ".ai", "models.json")
	if err := writeE2EModels(modelsPath, m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"HOME="+home,
		"GOCOVERDIR="+covDataDir,
		"OLLAMA_API_KEY=e2e",
		"AI_MODELS_PATH="+modelsPath,
	)

	// ai models: list + search, fully offline.
	for _, args := range [][]string{{"models"}, {"models", m.id}} {
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ai %v failed: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), m.id) {
			t.Fatalf("ai %v output missing model %q:\n%s", args, m.id, out)
		}
	}

	// ai serve: spawns an instrumented worker + socket; we drive it via ls/send/kill.
	workDir := t.TempDir()
	idFile := filepath.Join(workDir, "run.id")
	serve := exec.Command(binaryPath, "serve", "--name", "e2e-serve", "--id-file", idFile,
		"--model", m.provider+"/"+m.id,
		"--input", "Reply with the single word: pong")
	serve.Dir = workDir
	serve.Env = env

	var serveOut strings.Builder
	serve.Stdout = &serveOut
	serve.Stderr = &serveOut
	if err := serve.Start(); err != nil {
		t.Fatalf("ai serve start: %v", err)
	}

	// Wait for the run ID file.
	id := ""
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(idFile); err == nil {
			id = strings.TrimSpace(string(data))
			if id != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if id == "" {
		serve.Process.Kill()
		serve.Wait()
		t.Fatalf("no run id file; serve output:\n%s", serveOut.String())
	}

	runCmd := func(args ...string) (string, error) {
		cmd := exec.Command(binaryPath, args...)
		cmd.Dir = workDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := runCmd("ls"); err != nil || !strings.Contains(out, id) {
		t.Fatalf("ai ls failed: %v\n%s", err, out)
	}
	if out, err := runCmd("ls", "--all", "--json"); err != nil || !strings.Contains(out, id) {
		t.Fatalf("ai ls --json failed: %v\n%s", err, out)
	}
	if out, err := runCmd("send", "--id", id, "Reply with the single word: ok"); err != nil {
		t.Fatalf("ai send failed: %v\n%s", err, out)
	}
	if out, err := runCmd("kill", "--id", id); err != nil {
		t.Fatalf("ai kill failed: %v\n%s", err, out)
	}

	// serve exits once its worker is gone (graceful SIGTERM path).
	done := make(chan error, 1)
	go func() { done <- serve.Wait() }()
	select {
	case err := <-done:
		if err != nil && exitCode(err) == -1 {
			t.Fatalf("ai serve exited abnormally:\n%s", serveOut.String())
		}
	case <-time.After(30 * time.Second):
		serve.Process.Kill()
		<-done
		t.Fatalf("ai serve did not exit after kill:\n%s", serveOut.String())
	}
}

func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// --- Manual compaction over a real session ---

func TestE2E_Compaction(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")
	rs.promptAndWait(t, "Reply with exactly: alpha")
	rs.promptAndWait(t, "Reply with exactly: beta")

	// Manual compaction: LLMDecide compactor may decide either way; either
	// outcome exercises the full compaction path.
	rs.send(t, `{"type":"compact"}`)
	resp := rs.log.waitEvent("response", "command", "compact", 5*time.Minute)
	if resp == "" {
		t.Fatalf("no compact response. stderr:\n%s", rs.logTail())
	}
	t.Logf("compact response: %s", resp)

	// Session must still be usable.
	rs.promptAndWait(t, "Reply with exactly: done")

	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

// --- helpers for clean shutdown ---

func (rs *rpcServer) closeStdin() {
	rs.stdin.Close()
}

func (rs *rpcServer) waitExit(timeout time.Duration) error {
	select {
	case <-rs.stop:
		return nil
	case <-time.After(timeout):
		rs.cmd.Process.Kill()
		<-rs.stop
		return fmt.Errorf("process still alive after %s", timeout)
	}
}
