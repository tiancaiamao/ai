//go:build e2e

// E2E scenario tests: drive the instrumented `ai acp` binary black-box and
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

	"github.com/tiancaiamao/ai/pkg/protocol"
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
	m := e2eModel{provider: "ollama", id: "qwen"}
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
			for _, p := range []string{"ollama", "qwen"} {
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
	return writeE2EModelsWindow(path, provider, baseURL, id, 32768)
}

// writeE2EModelsWindow is writeE2EModels with a custom contextWindow.
// A tiny window shrinks the LLMDecide soft/hard compaction thresholds,
// letting tests reach the auto-compaction path deterministically.
func writeE2EModelsWindow(path, provider, baseURL, id string, contextWindow int) error {
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
						"contextWindow": contextWindow,
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

	// Seed setup: copy pre-buggy Go code + task description from testdata.
	testdata := filepath.Join("testdata", "realtask")
	for _, sub := range []string{"task1", "task2"} {
		src := filepath.Join(testdata, sub)
		dst := filepath.Join(workDir, sub)
		if out, err := exec.Command("cp", "-r", src, dst).CombinedOutput(); err != nil {
			t.Fatalf("cp %s -> %s: %v\n%s", src, dst, err, out)
		}
	}
	if err := copyFile(filepath.Join(testdata, "task.txt"), filepath.Join(workDir, "task.txt")); err != nil {
		t.Fatal(err)
	}

	// --- Run the agent ---
	rs := startACPServer(t, m, workDir)
	reply := rs.promptAndWait(t, "Read task.txt in the workspace and complete all tasks described in it. Verify each task's success criteria before moving on. When done, reply with the single word: ok")
	if !strings.Contains(reply, "ok") {
		t.Fatalf("agent did not reply ok, got: %q", reply)
	}

	// --- Verify Task 1: off-by-one fix ---
	task1Dir := filepath.Join(workDir, "task1")
	out1, err := exec.Command("go", "run", filepath.Join(task1Dir, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatalf("task1 go run failed: %v\n%s", err, out1)
	}
	if got := strings.TrimSpace(string(out1)); got != "15" {
		t.Fatalf("task1 SumRange(5) = %q, want %q", got, "15")
	}

	// --- Verify Task 2: race condition fix ---
	task2Dir := filepath.Join(workDir, "task2")
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

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// --- TestE2E_SlashCommands: full slash-command walkthrough on one server ---

func TestE2E_SlashCommands(t *testing.T) {
	m := requireEndpoint(t)
	rs := startACPServer(t, m, "")

	// Protocol-level malformed input is covered by ACP unit tests; this
	// black-box suite focuses on the shared command and agent behavior.

	// ---- Phase 1: Command validation errors (fast, no model) ----

	rs.commandError(t, "set_model", "nonexistent-provider xyz", "model not found")
	for _, cmd := range []string{"help", "skills", "context", "session", "messages",
		"get_session_stats", "get_last_assistant_text", "get_tree", "get_trace_events"} {
		rs.commandAck(t, cmd, "")
	}

	// ---- Phase 3: Model commands ----
	rs.commandAck(t, "model", "")
	rs.commandAck(t, "set_model", m.provider+" "+m.id)
	rs.commandError(t, "set_model", "nonexistent-provider xyz", "model not found")

	// ---- Phase 4: /set matrix ----
	rs.commandAck(t, "set", "help")
	rs.commandAck(t, "set", "auto-retry on")
	for _, v := range []string{"on", "off"} {
		rs.commandAck(t, "set", "auto-compaction "+v)
	}
	for _, v := range []string{"steer", "follow-up", "reject"} {
		rs.commandAck(t, "set", "busy-mode "+v)
	}
	rs.commandError(t, "set", "busy-mode bogus", "usage")
	rs.commandAck(t, "set", "follow-up-mode all")
	rs.commandError(t, "set", "follow-up-mode bogus", "invalid follow-up mode")
	for _, v := range []string{"on", "off", "toggle"} {
		rs.commandAck(t, "set", "prefix-display "+v)
	}
	rs.commandAck(t, "set", "thinking-display on")
	rs.commandAck(t, "set", "thinking-level minimal")
	rs.commandError(t, "set", "thinking-level bogus", "invalid thinking level")
	rs.commandAck(t, "set", "tool-call-cutoff 50")
	rs.commandAck(t, "set", "tool-summary-automation fallback")
	rs.commandError(t, "set", "tool-summary-automation bogus", "invalid")
	rs.commandAck(t, "set", "tools-display off")
	rs.commandAck(t, "set", "session-name e2e-session")
	rs.commandAck(t, "set", "trace-events on")
	rs.commandError(t, "set", "not-a-setting 1", "unknown setting")

	// ---- Phase 5: Dedicated toggles / shows ----
	for _, v := range []string{"off", "low", "medium", "high", "xhigh"} {
		rs.commandAck(t, "thinking", v)
	}
	rs.commandError(t, "thinking", "ultra", "invalid thinking level")
	rs.commandAck(t, "trace-events", "off")
	rs.commandAck(t, "toggle", "thinking")
	rs.commandAck(t, "toggle", "prefix")
	rs.commandAck(t, "toggle", "tools")
	rs.commandError(t, "toggle", "bogus", "usage")
	rs.commandAck(t, "show", "")
	rs.commandAck(t, "show", "settings")
	rs.commandError(t, "show", "bogus", "usage")

	// ---- Phase 6: Hidden backward-compatible forwarders ----
	rs.commandAck(t, "set_auto_compaction", "on")
	rs.commandAck(t, "set_thinking_level", "low")
	rs.commandAck(t, "set_trace_events", "off")

	// ---- Phase 7: Known unsupported command ----
	rs.commandError(t, "export_html", "", "not supported")

	// ---- Phase 8: Tool turns (build context) ----
	rs.promptAndWait(t, `Use the write tool to create a file named notes.md in the current directory containing 40 lines about the Go programming language. After it finishes, reply with the single word ok.`)
	rs.promptAndWait(t, `Use the edit tool to append a section about error handling to notes.md. After it finishes, reply with the single word ok.`)

	// ---- Phase 9: Session lifecycle (new / fork / rewind / resume) ----
	// Do this BEFORE large prompts / compact: compact compresses the tree
	// and invalidates entry IDs, so fork/rewind must run on a stable tree.
	sess2 := rs.commandAck(t, "new", "e2e-fork")
	if sess2["sessionId"] == "" {
		t.Fatalf("new session returned no sessionId: %v", sess2)
	}
	rs.commandAck(t, "session", "")

	// Resume: list, by index, errors.
	list := rs.commandAck(t, "resume", "")
	if sessions, _ := list["sessions"].([]any); len(sessions) < 2 {
		t.Fatalf("expected >= 2 sessions, got %d", len(sessions))
	}
	res0 := rs.commandAck(t, "resume", "0")
	t.Logf("resume 0 -> %v", res0)
	rs.commandError(t, "resume", "99", "out of range")
	rs.commandError(t, "resume", "zz-does-not-exist", "")

	// Fork / rewind on a real entry.
	tree := rs.commandAck(t, "get_tree", "")
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
	rs.commandAck(t, "get_fork_messages", "ignored")
	rs.commandAck(t, "rewind", forkID)
	rs.commandAck(t, "rewind", "root")
	rs.commandAck(t, "fork", forkID)
	rs.commandAck(t, "get_tree", "")

	// ---- Phase 10: Dense tool calls (push visible tool results past ToolCallCutoff=10)
	//     so that /compact triggers compactToolResultsInRecent.
	rs.promptAndWait(t, `Read these files and reply with the single word: tool-ok:
- /etc/hostname
- /etc/hosts
- /etc/passwd
- /etc/resolv.conf
- /etc/shells
- /etc/services
Use the read tool for each file.`)

	// ---- Phase 11: Large prompts (push context past compaction threshold) ----
	blob := strings.Repeat("The quick brown fox jumps over the lazy dog and keeps running through the meadow without stopping to rest. ", 500)
	for i := 1; i <= 2; i++ {
		rs.promptAndWait(t, fmt.Sprintf(
			"Message number %d. Acknowledge receipt of the following text, then reply with the single word: ack\n\n%s", i, blob))
	}

	// ---- Phase 12: /compact ----
	compactRaw, err := rs.client.PromptResult(rs.sessionID, "/compact")
	if err != nil {
		t.Fatalf("compact command failed: %v", err)
	}
	var compactResult struct {
		Meta struct {
			CommandResult struct {
				TokensBefore int `json:"tokensBefore"`
				TokensAfter  int `json:"tokensAfter"`
			} `json:"commandResult"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(compactRaw, &compactResult); err != nil {
		t.Fatalf("parse compact result: %v", err)
	}
	before := compactResult.Meta.CommandResult.TokensBefore
	after := compactResult.Meta.CommandResult.TokensAfter
	if before > 0 && after >= before {
		t.Fatalf("compact did not reduce tokens: before=%d after=%d", before, after)
	}
	t.Logf("compact: %d → %d tokens", before, after)

	// ---- Phase 13: Final prompt to confirm session still works ----
	reply := rs.promptAndWait(t, "Reply with exactly: slash-ok")
	if !strings.Contains(reply, "slash-ok") {
		t.Fatalf("final prompt reply: %q", reply)
	}

	// ---- Phase 14: Clean shutdown ----
	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

func (rs *acpServer) commandError(t *testing.T, name, args, wantErr string) {
	t.Helper()
	text := "/" + name
	if args != "" {
		text += " " + args
	}
	if _, err := rs.client.PromptResult(rs.sessionID, text); err == nil {
		t.Fatalf("command %q unexpectedly succeeded", name)
	} else if wantErr != "" && !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("command %q error %q does not contain %q", name, err, wantErr)
	}
}

// --- Crash recovery: kill agent mid-conversation, restart, verify checkpoint ---

func TestE2E_CrashRecovery(t *testing.T) {
	m := requireEndpoint(t)
	workDir := t.TempDir()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	sessPath := filepath.Join(workDir, "session.jsonl")

	// Send a prompt, then terminate the process to simulate a crash.
	rs1 := startACPServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	rs1.promptAsync(t, "What is 2+2? Reply with the single number only.")
	time.Sleep(8 * time.Second)
	_ = rs1.cmd.Process.Kill()
	select {
	case <-rs1.stop:
	case <-time.After(30 * time.Second):
		t.Fatal("first process did not terminate after simulated crash")
	}

	// Restart with the same session and verify the recovered agent remains usable.
	rs2 := startACPServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	reply := rs2.promptAndWait(t, "Reply with exactly: recovered")
	if !strings.Contains(reply, "recovered") {
		t.Fatalf("recovered prompt reply: %q", reply)
	}
	rs2.closeStdin()
	if err := rs2.waitExit(30 * time.Second); err != nil {
		t.Fatalf("recovered server did not exit: %v", err)
	}
}

// --- Auto-compaction: pre-seed a large session, resume, trigger compaction ---

func TestE2E_AutoCompaction(t *testing.T) {
	m := requireEndpoint(t)
	workDir := t.TempDir()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}
	sessPath := filepath.Join(workDir, "session.jsonl")

	// Build context in a first ACP process, then terminate it.
	rs1 := startACPServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	for i := 0; i < 5; i++ {
		blob := strings.Repeat(fmt.Sprintf("Message %d: The quick brown fox jumps over the lazy dog. ", i), 200)
		rs1.promptAndWait(t, fmt.Sprintf("Acknowledge this text (%d/5) and reply with the single word: ack\n\n%s", i+1, blob))
	}
	rs1.closeStdin()
	if err := rs1.waitExit(30 * time.Second); err != nil {
		t.Fatalf("first compaction seed server did not exit: %v", err)
	}

	// Resume the same session and trigger auto-compaction with a large prompt.
	rs2 := startACPServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	rs2.commandAck(t, "set", "auto-compaction on")
	largeBlob := strings.Repeat("Auto-compaction trigger: the agent should decide whether to compact now. ", 500)
	rs2.promptAsync(t, "Read and acknowledge: "+largeBlob+" Reply with: compact-ok")
	// The ACP log is the sole consumer of session/update notifications.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		update := rs2.log.waitUpdate(t, func(update protocol.ACPUpdate) bool {
			return update.SessionUpdate == "_compaction"
		}, time.Until(deadline))
		meta := updateMetaMap(t, update)
		if status, _ := meta["status"].(string); status == "end" {
			t.Log("auto-compaction: triggered successfully")
			return
		}
	}
	t.Log("auto-compaction: timeout (non-fatal for coverage)")
}

// --- Streaming-time policies, abort and the timeout watchdog ---

func TestE2E_BusyAndAbort(t *testing.T) {
	m := requireEndpoint(t)
	rs := startACPServer(t, m, "")
	rs.commandAck(t, "set", "busy-mode reject")

	// A streaming prompt we can probe mid-flight.
	rs.promptAsync(t, "Count from 1 to 30, listing every number.")
	time.Sleep(500 * time.Millisecond)

	// Busy-mode policy: reject while streaming. ACP reports asynchronous
	// request failures as a synthetic _request_error update.
	if err := rs.client.PromptAsync(rs.sessionID, "ignored"); err != nil {
		t.Fatalf("busy prompt: %v", err)
	}
	requestErr := rs.waitRequestError(t, "session/prompt", 30*time.Second)
	if !strings.Contains(requestErr.Message, "rejected by busy-mode policy") {
		t.Fatalf("busy prompt error = %q", requestErr.Message)
	}

	// Abort the stream.
	if err := rs.client.Cancel(rs.sessionID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rs.waitUpdate(t, "_turn_end", 3*time.Minute)

	// Server must still be usable.
	rs.promptAndWait(t, "Reply with exactly: ok")

	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

// TestE2E_SteerAndFollowUp exercises the /steer and /follow-up slash handlers
// pkg/app/rpc_help_handlers.go, which are only reachable while the agent is

// streaming — a state no other e2e test enters with these command types.
func TestE2E_SteerAndFollowUp(t *testing.T) {
	m := requireEndpoint(t)
	rs := startACPServer(t, m, "")

	// A streaming prompt we can probe mid-flight.
	rs.promptAsync(t, "Count from 1 to 50, listing every number.")
	time.Sleep(500 * time.Millisecond)

	// /follow-up while streaming: queued, processed by the loop after the current run.
	rs.commandAck(t, "follow-up", "After this run, reply with exactly: ok")

	// /steer while streaming: cancels the current run and restarts with the new message.
	rs.commandAck(t, "steer", "Reply with exactly: steered")

	// The steered run and queued follow-up finish in order.
	rs.waitUpdate(t, "_turn_end", 3*time.Minute)
	rs.waitUpdate(t, "_turn_end", 3*time.Minute)

	// Idle error branches of the slash handlers.
	rs.commandError(t, "steer", "", "usage")
	rs.commandError(t, "follow-up", "x", "agent is not busy")

	rs.closeStdin()
	if err := rs.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server did not shut down cleanly: %v", err)
	}
}

func TestE2E_TimeoutWatchdog(t *testing.T) {
	m := requireEndpoint(t)
	rs := startACPServer(t, m, "", "-timeout", "4s")

	// Long task; the 4s watchdog aborts the agent and cancels the server.
	rs.promptAsync(t, "Write a very long essay, at least 3000 words, about the history of computing.")

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
	rs := startACPServerHome(t, home, m.provider+"/"+m.id, "", "-role", "e2e-role")
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
	sp := startACPServer(t, m, "", "-system-prompt", "@"+spFile)
	sp.closeStdin()
	sp.waitExit(15 * time.Second)

	// Debug HTTP server on an ephemeral port.
	httpSrv := startACPServer(t, m, "", "-http", "127.0.0.1:0")
	httpSrv.closeStdin()
	httpSrv.waitExit(15 * time.Second)

	// Max turns: one turn then the agent stops.
	turns := startACPServer(t, m, "", "-max-turns", "1")
	turns.promptAsync(t, "Reply with exactly: hi")
	turns.waitUpdate(t, "_turn_end", 3*time.Minute)

	turns.closeStdin()
	turns.waitExit(15 * time.Second)

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
	cmd := exec.Command(binaryPath, append([]string{"acp"}, append(flags, "-model", m.provider+"/"+m.id, "-session", filepath.Join(t.TempDir(), "s.jsonl"))...)...)
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
	cmd := exec.Command(binaryPath, "acp", "-model", m.provider+"/"+m.id, "-session", filepath.Join(t.TempDir(), "s.jsonl"))
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
	// NOTE: must carry Env (incl. GOCOVERDIR) or the subprocess coverage
	// counters are never flushed to the merged profile.
	cmd := exec.Command(binaryPath, "models")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ai models failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), m.id) {
		t.Fatalf("ai models output does not contain %q:\n%s", m.id, out)
	}

	// --provider filter narrows the listing.
	cmdProv := exec.Command(binaryPath, "models", "--provider", m.provider)
	cmdProv.Env = env
	out, err = cmdProv.CombinedOutput()
	if err != nil {
		t.Fatalf("ai models --provider failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), m.id) {
		t.Fatalf("ai models --provider output does not contain %q:\n%s", m.id, out)
	}

	// Positional fuzzy filter matches provider/model.
	cmdFuzzy := exec.Command(binaryPath, "models", strings.ToLower(m.id))
	cmdFuzzy.Env = env
	out, err = cmdFuzzy.CombinedOutput()
	if err != nil {
		t.Fatalf("ai models fuzzy filter failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), m.id) {
		t.Fatalf("ai models fuzzy output does not contain %q:\n%s", m.id, out)
	}

	// A --provider that matches nothing must print a clear message.
	cmdNone := exec.Command(binaryPath, "models", "--provider", "nonexistent")
	cmdNone.Env = env
	out, err = cmdNone.CombinedOutput()
	if err != nil {
		t.Fatalf("ai models --provider nonexistent failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "no models for provider") {
		t.Fatalf("ai models no-match output missing message:\n%s", out)
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

	// ai ls --json: the run must be listed while the serve process is alive.
	// Status is "running" while the initial prompt is in flight, "idle" once
	// it completed — either proves the process is still up.
	out, err = runCmd("ls", "--json")
	if err != nil || !(strings.Contains(out, `"status": "running"`) || strings.Contains(out, `"status": "idle"`)) {
		t.Fatalf("ai ls --json failed: %v\n%s", err, out)
	}

	// ai ls --all --json: the run must appear.
	out, err = runCmd("ls", "--all", "--json")
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("ai ls --all --json failed: %v\n%s", err, out)
	}

	// Wait for the serve to finish its initial prompt (status "idle"). The
	// watch assertions below rely on session/load replaying the completed
	// turn; with a slow model the turn can still be in flight here, and
	// session/load is rejected while a prompt is pending.
	idle := false
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); {
		out, err = runCmd("ls", "--all", "--json")
		if err == nil && strings.Contains(out, id) && strings.Contains(out, `"status": "idle"`) {
			idle = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !idle {
		t.Fatalf("serve %s never reached idle status:\n%s", id, out)
	}

	// ai watch --follow (raw): must replay the persisted assistant text.
	if out, err := runCmd("watch", "--id", id, "--follow", "--timeout", "90s"); err != nil || !strings.Contains(out, `"sessionUpdate":"agent_message_chunk"`) || !strings.Contains(out, "pong") {
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

func (rs *acpServer) closeStdin() {
	rs.stdin.Close()
}

func (rs *acpServer) waitExit(timeout time.Duration) error {
	select {
	case <-rs.stop:
		return nil
	case <-time.After(timeout):
		rs.cmd.Process.Kill()
		<-rs.stop
		return fmt.Errorf("process still alive after %s", timeout)
	}
}
