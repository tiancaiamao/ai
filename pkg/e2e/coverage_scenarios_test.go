//go:build e2e

// Coverage-extension scenarios: middleware hooks (destructive guard) and
// skill loading. These complement the behavioral scenarios in scenarios_test.go.

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

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
