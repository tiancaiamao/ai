//go:build e2e

// Coverage-extension scenarios: middleware hooks (destructive guard) and
// skill loading. These complement the behavioral scenarios in scenarios_test.go.

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_PreLLMCompaction runs the server with a tiny declared context
// window (2048 → LLMDecide soft=512, hard=1536 tokens). The system prompt +
// tool schemas alone exceed the hard limit, so the pre-LLM threshold check
// (loop_state.performCompaction via ShouldCompact's hard-limit branch) fires
// deterministically on the first turn — no askLLM, no tool-call interval.
// It asserts a successful compaction_end event with trigger pre_llm_threshold.
func TestE2E_PreLLMCompaction(t *testing.T) {
	m := requireEndpoint(t)
	home := t.TempDir()
	if err := writeE2EModelsWindow(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id, 2048); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}

	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, t.TempDir())
	defer rs.closeStdin()

	rs.send(t, typedJSON("prompt", "Reply with the single word ok."))
	if ack := rs.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack. stderr:\n%s", rs.logTail())
	}

	ev := rs.log.waitEvent("compaction_end", "", "", 5*time.Minute)
	if ev == "" {
		t.Fatalf("no compaction_end event. stderr:\n%s", rs.logTail())
	}
	var c struct {
		Compaction struct {
			Trigger string `json:"trigger"`
			Error   string `json:"error"`
			Before  int    `json:"before"`
			After   int    `json:"after"`
			Type    string `json:"type"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal([]byte(ev), &c); err != nil {
		t.Fatalf("parse compaction_end: %v\n%s", err, ev)
	}
	if c.Compaction.Trigger != "pre_llm_threshold" {
		t.Fatalf("compaction trigger = %q, want pre_llm_threshold", c.Compaction.Trigger)
	}
	if c.Compaction.Error != "" {
		t.Fatalf("compaction failed: %s", c.Compaction.Error)
	}
	// before/after counts may be equal here: with a fresh session the
	// conversation is shorter than KeepRecent, so compaction summarizes but
	// keeps every message. The behavioral assertions are trigger + success.
	t.Logf("pre-LLM compaction: before=%d after=%d messages", c.Compaction.Before, c.Compaction.After)

	if e := rs.log.waitEvent("agent_end", "", "", 5*time.Minute); e == "" {
		t.Fatalf("no agent_end after compaction. stderr:\n%s", rs.logTail())
	}
}

// TestE2E_CompactionToolPairing builds a session containing several bash
// tool_call/tool_result pairs under a normal context window, then restarts
// the server on the same session with a tiny window (2048). The pre-LLM
// hard-limit compaction fires on the first resumed turn, and with KeepRecent=5
// the split boundary lands inside the tool-pair history. This exercises the
// production tool_call/tool_result pairing repair (ensureToolCallPairingWithGrace):
// results whose tool_call was summarized into oldMessages get archived while
// the most recent result stays grace-protected.
func TestE2E_CompactionToolPairing(t *testing.T) {
	m := requireEndpoint(t)
	home := t.TempDir()
	modelsPath := filepath.Join(home, ".ai", "models.json")
	if err := writeE2EModels(modelsPath, m.provider, m.baseURL, m.id); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}
	workDir := t.TempDir()
	sessPath := filepath.Join(workDir, "session.jsonl")

	// Phase 1: normal window; build tool_call/tool_result history:
	// two large outputs (~1500 estimated tokens each) to push the session past
	// the keep-recent budget (25% of the 8000-token fallback threshold = 2000
	// tokens), then twelve small distinct calls so the post-split recent window
	// holds more than ToolCallCutoff (10) visible tool results. The tiny-window
	// phase below then exercises both the pairing repair and the cutoff
	// archiving on real production paths.
	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	rs.promptAndWait(t, `Use the bash tool exactly twice to run this same command: head -c 6000 /dev/zero | tr '\0' 'x'. Then use the bash tool 12 more times, one command per call, in order: echo ok1, echo ok2, echo ok3, echo ok4, echo ok5, echo ok6, echo ok7, echo ok8, echo ok9, echo ok10, echo ok11, echo ok12. After all fourteen calls finish, reply with the single word ok.`)
	rs.closeStdin()
	if err := rs.waitExit(30 * time.Second); err != nil {
		t.Fatalf("phase-1 server did not exit: %v", err)
	}

	// Phase 2: tiny window on the same session; hard-limit compaction fires.
	if err := writeE2EModelsWindow(modelsPath, m.provider, m.baseURL, m.id, 2048); err != nil {
		t.Fatalf("rewrite models.json with tiny window: %v", err)
	}
	rs2 := startRPCServerHome(t, home, m.provider+"/"+m.id, workDir, "-session", sessPath)
	defer rs2.closeStdin()

	rs2.send(t, typedJSON("prompt", "Reply with the single word ok."))
	if ack := rs2.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack. stderr:\n%s", rs2.logTail())
	}

	ev := rs2.log.waitEvent("compaction_end", "", "", 5*time.Minute)
	if ev == "" {
		t.Fatalf("no compaction_end event. stderr:\n%s", rs2.logTail())
	}
	var c struct {
		Compaction struct {
			Trigger string `json:"trigger"`
			Error   string `json:"error"`
			Before  int    `json:"before"`
			After   int    `json:"after"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal([]byte(ev), &c); err != nil {
		t.Fatalf("parse compaction_end: %v\n%s", err, ev)
	}
	if c.Compaction.Trigger != "pre_llm_threshold" {
		t.Fatalf("compaction trigger = %q, want pre_llm_threshold", c.Compaction.Trigger)
	}
	if c.Compaction.Error != "" {
		t.Fatalf("compaction failed: %s", c.Compaction.Error)
	}
	// Unlike the fresh-session case, the resumed session is longer than
	// KeepRecent, so compaction must actually shrink the visible history.
	if c.Compaction.After >= c.Compaction.Before {
		t.Errorf("compaction did not shrink messages: before=%d after=%d", c.Compaction.Before, c.Compaction.After)
	}
	t.Logf("tool-pairing compaction: before=%d after=%d messages", c.Compaction.Before, c.Compaction.After)

	if e := rs2.log.waitEvent("agent_end", "", "", 5*time.Minute); e == "" {
		t.Fatalf("no agent_end after compaction. stderr:\n%s", rs2.logTail())
	}
}

// TestE2E_DestructiveGuard starts the server with a role whose agent.yaml
// enables the destructive_guard middleware, then asks the agent to remove a
// scratch directory. The bash tool output matches the guard's rm -rf pattern,
// exercising the AfterToolHook warning path (pkg/middlewares).
func TestE2E_DestructiveGuard(t *testing.T) {
	m := requireEndpoint(t)
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}

	roleDir := filepath.Join(home, ".ai", "roles", "guard")
	roleFiles := map[string]string{
		"agent.yaml": `version: 1
system_prompt: ./system_prompt.md
memory: ./memory.md
tools:
  - name: read
    enabled: true
  - name: bash
    enabled: true
  - name: write
    enabled: true
  - name: grep
    enabled: true
  - name: edit
    enabled: true
  - name: change_workspace
    enabled: true
  - name: find_skill
    enabled: true
middlewares:
  - name: destructive_guard
    enabled: true
context_management:
  stale_annotation: false
`,
		"system_prompt.md": "You are a helpful assistant that follows tool instructions precisely.\n",
		"memory.md":        "",
	}
	if err := os.MkdirAll(roleDir, 0o755); err != nil {
		t.Fatalf("mkdir role dir: %v", err)
	}
	for name, content := range roleFiles {
		if err := os.WriteFile(filepath.Join(roleDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write role %s: %v", name, err)
		}
	}

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "sandbox"), 0o755); err != nil {
		t.Fatalf("mkdir sandbox: %v", err)
	}

	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, workDir, "--role", "guard")
	defer rs.closeStdin()
	rs.promptAndWait(t, `Use the bash tool to run the command: rm -rf sandbox (it is a harmless empty directory). After it finishes, reply with the single word ok.`)
}

// TestE2E_Skills seeds a skill into the isolated HOME and asks the agent to
// discover it with the find_skill tool, exercising skill loading, parsing,
// ranking and formatting (pkg/skill).
func TestE2E_Skills(t *testing.T) {
	m := requireEndpoint(t)
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}

	skillDir := filepath.Join(home, ".ai", "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	sk := `---
name: demo-skill
description: Tiny test skill used to verify skill discovery works.
---
# Demo Skill
This skill does nothing useful. Reply "hello from demo-skill" when asked about it.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, t.TempDir())
	defer rs.closeStdin()
	rs.promptAndWait(t, `Use the find_skill tool to search for a skill named demo-skill, then after it finishes reply with the single word ok.`)
}
