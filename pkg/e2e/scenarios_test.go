//go:build e2e

// E2E scenario tests: drive the instrumented `ai rpc` binary black-box and
// collect whole-app coverage.
//
// TestE2E_RealTask — realistic coding task with pre-seeded buggy code;
//
//	verify results by running the fixed code (read/edit/bash/write)
//
// TestE2E_SlashCommands — full slash-command walkthrough on one server:
//
//	protocol errors, tool turns, large prompts, /compact, /fork, /rewind,
//	/new, /resume, /help, /model, /set, /thinking, /toggle, /show
//
// TestE2E_BusyAndAbort — streaming-time policies, abort
// TestE2E_TimeoutWatchdog — -timeout watchdog
// TestE2E_FlagsAndRoles — CLI flags, agent.yaml roles, failure exits
// TestE2E_Subcommands — ai models / serve → ls → send → kill → watch
package e2e

import (
	"encoding/json"
	"encoding/xml"
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

// --- TestE2E_RealTask: realistic coding task with pre-seeded buggy code ---

func TestE2E_RealTask(t *testing.T) {
	m := requireEndpoint(t)
	workDir := t.TempDir()

	// --- Seed setup: buggy Go code + task description ---

	// Task 1: off-by-one in SumRange
	task1Dir := filepath.Join(workDir, "task1")
	if err := os.MkdirAll(task1Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task1Dir, "main.go"), []byte(`package main

import "fmt"

// SumRange returns the sum of numbers from 1 to n (inclusive).
func SumRange(n int) int {
	sum := 0
	for i := 1; i < n; i++ {
		sum += i
	}
	return sum
}

func main() {
	fmt.Println(SumRange(5))
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Task 2: race condition in Counter
	task2Dir := filepath.Join(workDir, "task2")
	if err := os.MkdirAll(task2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task2Dir, "counter.go"), []byte(`package main

import (
	"fmt"
	"sync"
)

type Counter struct {
	value int
}

func (c *Counter) Increment() { c.value++ }
func (c *Counter) Value() int { return c.value }

func main() {
	var c Counter
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.Increment()
			}
		}()
	}
	wg.Wait()
	if c.Value() == 1000000 {
		fmt.Println("SUCCESS")
	} else {
		fmt.Println("FAIL:", c.Value())
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Task description
	if err := os.WriteFile(filepath.Join(workDir, "task.txt"), []byte(`Complete the following tasks in this workspace:

## Task 1: Fix Off-by-One Error
The file task1/main.go contains a SumRange function with an off-by-one error.
It should return the sum of numbers from 1 to n (inclusive), but currently
it only sums from 1 to n-1. Fix the bug, then run "go run main.go" in the
task1/ directory to verify it outputs "15".

## Task 2: Fix Race Condition
The file task2/counter.go has a race condition. Fix it so that 1000 goroutines
each incrementing 1000 times results in exactly 1000000. Run
"go run -race counter.go" in the task2/ directory to verify it outputs "SUCCESS".

## Task 3: Create Files
1. Create pelican.svg — a valid SVG file with a <title>Pelican</title> element.
2. Create README.md — a brief document that mentions "pelican".

When all tasks are complete, reply with the single word: ok
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- Run the agent ---
	rs := startRPCServer(t, m, workDir)
	reply := rs.promptAndWait(t, "Read task.txt in the workspace and complete all tasks described in it. Verify each task's success criteria before moving on. When done, reply with the single word: ok")
	if !strings.Contains(reply, "ok") {
		t.Fatalf("agent did not reply ok, got: %q", reply)
	}

	// --- Verify Task 1: off-by-one fix ---
	out1, err := exec.Command("go", "run", filepath.Join(task1Dir, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatalf("task1 go run failed: %v\n%s", err, out1)
	}
	if got := strings.TrimSpace(string(out1)); got != "15" {
		t.Fatalf("task1 SumRange(5) = %q, want %q", got, "15")
	}

	// --- Verify Task 2: race condition fix ---
	out2, err := exec.Command("go", "run", "-race", filepath.Join(task2Dir, "counter.go")).CombinedOutput()
	if err != nil {
		t.Fatalf("task2 go run -race failed: %v\n%s", err, out2)
	}
	if !strings.Contains(string(out2), "SUCCESS") {
		t.Fatalf("task2 race fix did not print SUCCESS: %s", out2)
	}

	// --- Verify Task 3: SVG + README ---
	svgPath := filepath.Join(workDir, "pelican.svg")
	svgData, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatalf("pelican.svg not created: %v", err)
	}
	verifySVG(t, svgData)

	readmePath := filepath.Join(workDir, "README.md")
	readmeData, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("README.md not created: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(readmeData)), "pelican") {
		t.Fatalf("README.md does not mention pelican:\n%s", readmeData)
	}
}

// verifySVG checks that data is valid XML with a <title> containing "Pelican".
func verifySVG(t *testing.T, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var found bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "title" {
			var content string
			if err := dec.DecodeElement(&content, &se); err == nil {
				if strings.Contains(content, "Pelican") {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("SVG does not contain <title>Pelican</title>:\n%s", data)
	}
}

// --- TestE2E_SlashCommands: full slash-command walkthrough on one server ---

func TestE2E_SlashCommands(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")

	// ---- Phase 1: Protocol errors (fast, no model) ----

	// Malformed JSON.
	rs.send(t, `{"type":`)
	assertRawError(t, rs, "Failed to parse command")

	// Unknown command type.
	rs.rpcErr(t, "nope", "", "No nope handler registered")

	// Prompt validation errors.
	rs.rpcErr(t, "prompt", "", "empty prompt message")
	rs.send(t, `{"type":"prompt","message":"x","data":{"images":["i"]}}`)
	assertRawError(t, rs, "images are not supported")
	rs.rpcErr(t, "prompt", "/definitely-not-a-command", "unknown command")

	// ---- Phase 2: Simple introspection commands ----
	for _, cmd := range []string{"help", "skills", "context", "session", "messages",
		"get_session_stats", "get_last_assistant_text", "get_tree", "get_trace_events"} {
		rs.rpcAck(t, cmd, "")
	}

	// ---- Phase 3: Model commands ----
	rs.rpcAck(t, "model", "")
	rs.rpcAck(t, "set_model", m.provider+" "+m.id)
	rs.rpcErr(t, "set_model", "nonexistent-provider xyz", "model not found")

	// ---- Phase 4: /set matrix ----
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

	// ---- Phase 5: Dedicated toggles / shows ----
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

	// ---- Phase 6: Hidden backward-compatible forwarders ----
	rs.rpcAck(t, "set_auto_compaction", "on")
	rs.rpcAck(t, "set_thinking_level", "low")
	rs.rpcAck(t, "set_trace_events", "off")

	// ---- Phase 7: Known unsupported command ----
	rs.rpcErr(t, "export_html", "", "not supported")

	// ---- Phase 8: Tool turns (build context) ----
	rs.promptAndWait(t, `Use the write tool to create a file named notes.md in the current directory containing 40 lines about the Go programming language. After it finishes, reply with the single word ok.`)
	rs.promptAndWait(t, `Use the edit tool to append a section about error handling to notes.md. After it finishes, reply with the single word ok.`)

	// ---- Phase 9: Session lifecycle (new / fork / rewind / resume) ----
	// Do this BEFORE large prompts / compact: compact compresses the tree
	// and invalidates entry IDs, so fork/rewind must run on a stable tree.
	sess2 := rs.rpcAck(t, "new", "e2e-fork")
	if sess2["sessionId"] == "" {
		t.Fatalf("new session returned no sessionId: %v", sess2)
	}
	rs.rpcAck(t, "session", "")

	// Resume: list, by index, errors.
	list := rs.rpcAck(t, "resume", "")
	if sessions, _ := list["sessions"].([]any); len(sessions) < 2 {
		t.Fatalf("expected >= 2 sessions, got %d", len(sessions))
	}
	res0 := rs.rpcAck(t, "resume", "0")
	t.Logf("resume 0 -> %v", res0)
	rs.rpcErr(t, "resume", "99", "out of range")
	rs.rpcErr(t, "resume", "zz-does-not-exist", "")

	// Fork / rewind on a real entry.
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
	rs.rpcAck(t, "get_fork_messages", "ignored")
	rs.rpcAck(t, "rewind", forkID)
	rs.rpcAck(t, "rewind", "root")
	rs.rpcAck(t, "fork", forkID)
	rs.rpcAck(t, "get_tree", "")

	// ---- Phase 10: Large prompts (push context past compaction threshold) ----
	blob := strings.Repeat("The quick brown fox jumps over the lazy dog and keeps running through the meadow without stopping to rest. ", 500)
	for i := 1; i <= 2; i++ {
		rs.promptAndWait(t, fmt.Sprintf(
			"Message number %d. Acknowledge receipt of the following text, then reply with the single word: ack\n\n%s", i, blob))
	}

	// ---- Phase 11: /compact ----
	rs.send(t, `{"type":"compact"}`)
	compactResp := rs.log.waitEvent("response", "command", "compact", 5*time.Minute)
	if compactResp == "" {
		t.Fatalf("no compact response. stderr:\n%s", rs.logTail())
	}
	t.Logf("compact response: %s", compactResp)

	// Verify compact actually reduced token count.
	var cr struct {
		Success bool `json:"success"`
		Data    struct {
			TokensBefore int `json:"tokensBefore"`
			TokensAfter  int `json:"tokensAfter"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(compactResp), &cr); err == nil && cr.Success {
		if cr.Data.TokensBefore > 0 && cr.Data.TokensAfter >= cr.Data.TokensBefore {
			t.Fatalf("compact did not reduce tokens: before=%d after=%d",
				cr.Data.TokensBefore, cr.Data.TokensAfter)
		}
		t.Logf("compact: %d → %d tokens", cr.Data.TokensBefore, cr.Data.TokensAfter)
	}

	// ---- Phase 12: Final prompt to confirm session still works ----
	reply := rs.promptAndWait(t, "Reply with exactly: slash-ok")
	if !strings.Contains(reply, "slash-ok") {
		t.Fatalf("final prompt reply: %q", reply)
	}

	// ---- Phase 13: Clean shutdown ----
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

// --- Streaming-time policies, abort and the timeout watchdog ---

func TestE2E_BusyAndAbort(t *testing.T) {
	m := requireEndpoint(t)
	rs := startRPCServer(t, m, "")

	// A streaming prompt we can probe mid-flight.
	rs.send(t, `{"type":"prompt","message":"Count from 1 to 30, listing every number."}`)
	if ack := rs.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack. stderr:\n%s", rs.logTail())
	}
	time.Sleep(500 * time.Millisecond)

	// Busy-mode policy: reject while streaming.
	rs.send(t, `{"type":"prompt","message":"ignored","data":{"streamingBehavior":"reject"}}`)
	assertRawError(t, rs, "rejected by busy-mode policy")

	// Abort the stream.
	rs.rpcAck(t, "abort", "")
	if ev := rs.log.waitEvent("agent_end", "", "", 3*time.Minute); ev == "" {
		t.Fatalf("no agent_end after abort. emitted lines:\n%s\nstate: %s",
			rs.log.History(), rs.log.DebugState())
	}

	// Server must still be usable.
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
	stdin.Close()
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("no process state; output:\n%s", out)
	}
	return cmd.ProcessState.ExitCode()
}

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
	// Use short paths to avoid macOS Unix socket path length limit (104 bytes).
	workDir, err := os.MkdirTemp("", "ai-e2e-w")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })

	home, err := os.MkdirTemp("", "ai-e2e-h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"HOME="+home,
		"GOCOVERDIR="+covDataDir,
		"OLLAMA_API_KEY=e2e",
		"AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"),
	)

	// ai models must list the configured model.
	out, err := exec.Command(binaryPath, "models").CombinedOutput()
	if err != nil {
		t.Fatalf("ai models failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), m.id) {
		t.Fatalf("ai models output does not contain %q:\n%s", m.id, out)
	}

	// Start ai serve in background.
	idFile := filepath.Join(workDir, "run-id.txt")
	serveArgs := []string{
		"serve", "-model", m.provider + "/" + m.id,
		"-session", filepath.Join(workDir, "s.jsonl"),
		"--name", "e2e-serve",
		"--id-file", idFile,
		"--input", "Reply with the single word: pong",
	}
	serve := exec.Command(binaryPath, serveArgs...)
	serve.Dir = workDir
	serve.Env = env
	serveOut := &syncBuffer{}
	serve.Stderr = serveOut
	if err := serve.Start(); err != nil {
		t.Fatalf("ai serve start: %v", err)
	}
	defer func() {
		serve.Process.Kill()
		serve.Wait()
	}()

	// Wait for the id file to appear (server started, run created).
	waitFor := time.After(30 * time.Second)
	for {
		data, err := os.ReadFile(idFile)
		if err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" {
				testSubcommandsWithID(t, env, id)
				return
			}
		}
		select {
		case <-waitFor:
			t.Fatalf("ai serve did not create run ID file after 30s.\nserve stderr:\n%s", serveOut.String())
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func testSubcommandsWithID(t *testing.T, env []string, id string) {
	t.Helper()

	runCmd := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// ai ls: the running serve must be listed.
	out, err := runCmd("ls")
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("ai ls failed: %v\n%s", err, out)
	}

	// ai ls --json: the run must appear as running.
	out, err = runCmd("ls", "--json")
	if err != nil || !strings.Contains(out, "running") {
		t.Fatalf("ai ls --json failed: %v\n%s", err, out)
	}

	// ai ls --all --json: the run must appear.
	out, err = runCmd("ls", "--all", "--json")
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("ai ls --all --json failed: %v\n%s", err, out)
	}

	// ai watch --follow (raw): must contain "agent_end" marker.
	if out, err := runCmd("watch", "--id", id, "--follow", "--timeout", "90s"); err != nil || !strings.Contains(out, "agent_end") {
		t.Fatalf("ai watch --follow (raw) failed: %v\n%s", err, out)
	}

	// ai watch --follow --pretty: must contain "__seq:" markers.
	if out, err := runCmd("watch", "--id", id, "--follow", "--pretty", "--timeout", "90s"); err != nil || !strings.Contains(out, "__seq:") {
		t.Fatalf("ai watch --follow --pretty failed: %v\n%s", err, out)
	}

	// ai watch --follow --pretty --summary: must contain final assistant text.
	if out, err := runCmd("watch", "--id", id, "--follow", "--pretty", "--summary", "--timeout", "90s"); err != nil || !strings.Contains(out, "pong") {
		t.Fatalf("ai watch --follow --summary should print final assistant text: %v\n%s", err, out)
	}

	// ai send: must succeed.
	if out, err := runCmd("send", "--id", id, "Reply with the single word: ok"); err != nil {
		t.Fatalf("ai send failed: %v\n%s", err, out)
	}

	// ai send to unknown run: must fail.
	if out, err := runCmd("send", "--id", "no-such-run", "hello"); err == nil {
		t.Fatalf("ai send to unknown run unexpectedly succeeded:\n%s", out)
	}

	// ai kill: must succeed.
	if out, err := runCmd("kill", "--id", id); err != nil {
		t.Fatalf("ai kill failed: %v\n%s", err, out)
	}

	// After kill: ls (non --all) must no longer list the run.
	time.Sleep(500 * time.Millisecond)
	if out, err := runCmd("ls"); err != nil || strings.Contains(out, id) {
		t.Fatalf("ai ls should hide dead run %q: %v\n%s", id, err, out)
	}
	if out, err := runCmd("ls", "--all", "--json"); err != nil || (!strings.Contains(out, `"status": "killed"`) && !strings.Contains(out, `"status": "failed"`)) {
		t.Fatalf("ai ls --all --json should report dead status for %q: %v\n%s", id, err, out)
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
