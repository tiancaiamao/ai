package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeAgentOutput(t *testing.T) {
	output := `=== Turn 1 ===
Tool calls:
  • bash: [command=grep -n BUG app.py]
  • read: [path=setup/app.py]
  • edit: [file=setup/app.py]
  • bash: [command=python3 -m pytest tests/test_app.py -v]
`

	metrics := analyzeAgentOutput(output)

	if metrics.TotalToolCalls != 4 {
		t.Fatalf("expected 4 tool calls, got %d", metrics.TotalToolCalls)
	}
	if metrics.GrepLikeCalls != 1 {
		t.Fatalf("expected grep-like count 1, got %d", metrics.GrepLikeCalls)
	}
	if metrics.ReadCalls != 1 {
		t.Fatalf("expected read count 1, got %d", metrics.ReadCalls)
	}
	if metrics.EditCalls != 1 {
		t.Fatalf("expected edit count 1, got %d", metrics.EditCalls)
	}
	if metrics.TestRuns != 1 {
		t.Fatalf("expected test runs 1, got %d", metrics.TestRuns)
	}
}

func TestEvaluateTaskConstraints(t *testing.T) {
	taskDir := t.TempDir()
	spec := ConstraintSpec{
		MaxSteps:     3,
		MustUseTools: []string{"grep"},
		SuccessCriteria: map[string]any{
			"test_passed":      true,
			"total_tool_calls": "<=3",
		},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "constraints.json"), data, 0644); err != nil {
		t.Fatalf("write constraints: %v", err)
	}

	output := `Tool calls:
  • grep: [pattern=BUG path=app.py]
  • read: [path=app.py]
  • edit: [file=app.py]
  • test: [command=pytest]
`
	metrics := analyzeAgentOutput(output)

	bench := &Benchmark{MaxStepsMode: "hard"}
	violations, softViolations, score, checked, err := bench.evaluateTaskConstraints(Task{Dir: taskDir}, metrics, true)
	if err != nil {
		t.Fatalf("evaluate constraints: %v", err)
	}
	if !checked {
		t.Fatalf("expected constraints to be checked")
	}
	if len(violations) == 0 {
		t.Fatalf("expected violations, got none")
	}
	if len(softViolations) != 0 {
		t.Fatalf("expected no soft violations in hard mode, got %v", softViolations)
	}
	if score >= 100 {
		t.Fatalf("expected score penalty, got %.1f", score)
	}
}

func TestAnalyzeAgentOutputWithCodexJSON(t *testing.T) {
	output := `{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc 'grep -n BUG app.py'","status":"in_progress"}}
{"type":"item.started","item":{"id":"item_3","type":"command_execution","command":"/bin/zsh -lc 'python3 -m pytest tests/test_app.py -v'","status":"in_progress"}}`

	metrics := analyzeAgentOutput(output)
	if metrics.TotalToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %d", metrics.TotalToolCalls)
	}
	if metrics.ToolCounts["bash"] != 2 {
		t.Fatalf("expected 2 bash tool calls, got %d", metrics.ToolCounts["bash"])
	}
	if metrics.GrepLikeCalls < 1 {
		t.Fatalf("expected grep-like call detected, got %d", metrics.GrepLikeCalls)
	}
	if metrics.TestRuns < 1 {
		t.Fatalf("expected test run detected, got %d", metrics.TestRuns)
	}
}

func TestAnalyzeAgentOutputWithCodexFileChangeAndReadSignals(t *testing.T) {
	output := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc 'nl -ba stage1_load.py'","status":"in_progress"}}
{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc 'bash ../verify.sh'","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_3","type":"file_change","changes":[{"path":"/tmp/task/setup/stage5_save.py","kind":"update"}],"status":"completed"}}`

	metrics := analyzeAgentOutput(output)
	if metrics.ReadCalls < 1 {
		t.Fatalf("expected read call inferred from bash command, got %d", metrics.ReadCalls)
	}
	if metrics.EditCalls < 1 {
		t.Fatalf("expected edit call inferred from file_change, got %d", metrics.EditCalls)
	}
	if !metrics.ReadStage1 {
		t.Fatalf("expected stage1 read signal")
	}
	if !metrics.EditedStage5 {
		t.Fatalf("expected stage5 edit signal")
	}
	if len(metrics.EditedFiles) != 1 {
		t.Fatalf("expected 1 edited file, got %d", len(metrics.EditedFiles))
	}
	if capabilityUsageCount(metrics, "test") < 1 {
		t.Fatalf("expected test capability from verify.sh")
	}
}

func TestEvaluateTaskConstraintsReadRequirementWithBashCommands(t *testing.T) {
	taskDir := t.TempDir()
	spec := ConstraintSpec{
		MustUseTools: []string{"read"},
		SuccessCriteria: map[string]any{
			"test_passed": true,
		},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "constraints.json"), data, 0644); err != nil {
		t.Fatalf("write constraints: %v", err)
	}

	output := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc 'cat app.py'","status":"in_progress"}}`
	metrics := analyzeAgentOutput(output)

	bench := &Benchmark{MaxStepsMode: "soft"}
	violations, softViolations, score, checked, err := bench.evaluateTaskConstraints(Task{Dir: taskDir}, metrics, true)
	if err != nil {
		t.Fatalf("evaluate constraints: %v", err)
	}
	if !checked {
		t.Fatalf("expected constraints to be checked")
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
	if len(softViolations) != 0 {
		t.Fatalf("expected no soft violations, got %v", softViolations)
	}
	if score != 100 {
		t.Fatalf("expected full score, got %.1f", score)
	}
}

func TestEvaluateTaskConstraintsSoftMaxSteps(t *testing.T) {
	taskDir := t.TempDir()
	spec := ConstraintSpec{
		MaxSteps: 3,
		SuccessCriteria: map[string]any{
			"test_passed": true,
		},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "constraints.json"), data, 0644); err != nil {
		t.Fatalf("write constraints: %v", err)
	}

	output := `Tool calls:
  • read: [path=app.py]
  • edit: [file=app.py]
  • test: [command=pytest]
  • test: [command=pytest]
`
	metrics := analyzeAgentOutput(output)
	bench := &Benchmark{MaxStepsMode: "soft"}
	violations, softViolations, score, checked, err := bench.evaluateTaskConstraints(Task{Dir: taskDir}, metrics, true)
	if err != nil {
		t.Fatalf("evaluate constraints: %v", err)
	}
	if !checked {
		t.Fatalf("expected constraints to be checked")
	}
	if len(violations) != 0 {
		t.Fatalf("expected no hard violations, got %v", violations)
	}
	if len(softViolations) == 0 {
		t.Fatalf("expected soft max_steps violation")
	}
	if score >= 100 {
		t.Fatalf("expected score penalty, got %.1f", score)
	}
}

func TestEvaluateTaskConstraintsTaskHardOverride(t *testing.T) {
	taskDir := t.TempDir()
	spec := ConstraintSpec{
		MaxSteps:     3,
		MaxStepsMode: "hard",
		SuccessCriteria: map[string]any{
			"test_passed": true,
		},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "constraints.json"), data, 0644); err != nil {
		t.Fatalf("write constraints: %v", err)
	}

	output := `Tool calls:
  • read: [path=app.py]
  • edit: [file=app.py]
  • test: [command=pytest]
  • test: [command=pytest]
`
	metrics := analyzeAgentOutput(output)
	bench := &Benchmark{MaxStepsMode: "soft"}
	violations, softViolations, _, checked, err := bench.evaluateTaskConstraints(Task{Dir: taskDir}, metrics, true)
	if err != nil {
		t.Fatalf("evaluate constraints: %v", err)
	}
	if !checked {
		t.Fatalf("expected constraints to be checked")
	}
	if len(violations) == 0 {
		t.Fatalf("expected hard violation for task-level hard override")
	}
	if len(softViolations) != 0 {
		t.Fatalf("expected no soft violations in hard override mode, got %v", softViolations)
	}
}

func TestFilterTasksByManifestPreservesOrder(t *testing.T) {
	tasks := []Task{
		{ID: "agent_001_forced_exploration"},
		{ID: "agent_002_rollback"},
		{ID: "agent_003_hidden_dep"},
	}
	manifest := TaskManifest{
		Tasks: []string{
			"agent_003_hidden_dep",
			"agent_001_forced_exploration",
		},
	}

	got, err := filterTasksByManifest(tasks, manifest)
	if err != nil {
		t.Fatalf("filterTasksByManifest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(got))
	}
	if got[0].ID != "agent_003_hidden_dep" || got[1].ID != "agent_001_forced_exploration" {
		t.Fatalf("unexpected order: %s, %s", got[0].ID, got[1].ID)
	}
}

func TestFilterTasksByManifestMissingTask(t *testing.T) {
	tasks := []Task{{ID: "agent_001_forced_exploration"}}
	manifest := TaskManifest{
		Tasks: []string{"agent_001_forced_exploration", "agent_404_missing"},
	}

	_, err := filterTasksByManifest(tasks, manifest)
	if err == nil {
		t.Fatal("expected error when manifest references missing task")
	}
}

func TestLoadTaskManifest(t *testing.T) {
	taskDir := t.TempDir()
	path := filepath.Join(taskDir, "manifest.json")
	raw := `{
  "version":"v1",
  "tasks":["agent_001_forced_exploration"],
  "global_defaults":{"max_steps_mode":"soft"}
}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest, err := loadTaskManifest(path)
	if err != nil {
		t.Fatalf("loadTaskManifest: %v", err)
	}
	if manifest.Version != "v1" {
		t.Fatalf("expected version v1, got %s", manifest.Version)
	}
	if len(manifest.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(manifest.Tasks))
	}
	if manifest.GlobalDefaults.MaxStepsMode != "soft" {
		t.Fatalf("expected max_steps_mode soft, got %s", manifest.GlobalDefaults.MaxStepsMode)
	}
}

type fakeAgentRunner struct {
	output string
	err    error
}

func (f *fakeAgentRunner) Run(taskDir string, prompt string) (string, error) {
	return f.output, f.err
}

func (f *fakeAgentRunner) Name() string {
	return "fake-agent"
}

func TestRunTaskKilledAfterCompletionSetsAgenticScore(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "task")
	initDir := filepath.Join(taskDir, "init")
	if err := os.MkdirAll(initDir, 0755); err != nil {
		t.Fatalf("mkdir init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("demo task"), 0644); err != nil {
		t.Fatalf("write task.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "verify.sh"), []byte("#!/bin/bash\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write verify.sh: %v", err)
	}

	bench := NewBenchmark(root, filepath.Join(root, "results"), &fakeAgentRunner{
		output: "9 passed",
		err:    errors.New("signal: killed"),
	})

	result := bench.RunTask(Task{
		ID:          "demo",
		Name:        "demo",
		Description: "demo",
		Dir:         taskDir,
	})

	if !result.Passed {
		t.Fatal("expected passed=true for killed-after-completion path")
	}
	if result.AgenticScore != 100 {
		t.Fatalf("expected agentic_score 100, got %.1f", result.AgenticScore)
	}
}

func TestTaskNeedsAppShim(t *testing.T) {
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("write to /app/out.txt"), 0644); err != nil {
		t.Fatalf("write task.md: %v", err)
	}
	needs, err := taskNeedsAppShim(taskDir)
	if err != nil {
		t.Fatalf("taskNeedsAppShim: %v", err)
	}
	if !needs {
		t.Fatal("expected task to require /app shim")
	}
}

func TestRewriteLegacyAppPaths(t *testing.T) {
	taskDir := t.TempDir()
	setupDir := filepath.Join(taskDir, "setup")
	if err := os.MkdirAll(setupDir, 0755); err != nil {
		t.Fatalf("mkdir setup: %v", err)
	}

	textPath := filepath.Join(taskDir, "verify.sh")
	text := "#!/bin/bash\ncat /app/input.txt > /app/output.txt\n"
	if err := os.WriteFile(textPath, []byte(text), 0755); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	binPath := filepath.Join(taskDir, "blob.bin")
	bin := []byte{0, 1, 2, 3, '/'}
	if err := os.WriteFile(binPath, bin, 0644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	if err := rewriteLegacyAppPaths(taskDir, setupDir); err != nil {
		t.Fatalf("rewriteLegacyAppPaths: %v", err)
	}

	rewritten, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("read rewritten file: %v", err)
	}
	got := string(rewritten)
	if strings.Contains(got, "/app") {
		t.Fatalf("expected /app to be rewritten, got: %s", got)
	}
	if !strings.Contains(got, setupDir) {
		t.Fatalf("expected rewritten file to contain setup dir %s", setupDir)
	}
}

func TestRunTaskWithAppShim(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "task")
	initDir := filepath.Join(taskDir, "init")
	if err := os.MkdirAll(initDir, 0755); err != nil {
		t.Fatalf("mkdir init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.md"), []byte("create /app/out.txt"), 0644); err != nil {
		t.Fatalf("write task.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(initDir, "out.txt"), []byte("ok"), 0644); err != nil {
		t.Fatalf("write init out.txt: %v", err)
	}
	verify := "#!/bin/bash\nset -e\n[ -f /app/out.txt ]\n"
	if err := os.WriteFile(filepath.Join(taskDir, "verify.sh"), []byte(verify), 0755); err != nil {
		t.Fatalf("write verify.sh: %v", err)
	}

	bench := NewBenchmark(root, filepath.Join(root, "results"), &fakeAgentRunner{})
	result := bench.RunTask(Task{
		ID:           "shim-demo",
		Name:         "shim-demo",
		Description:  "shim demo",
		Dir:          taskDir,
		NeedsAppShim: true,
	})
	if !result.Passed {
		t.Fatalf("expected pass with /app shim, got error: %s, output: %s", result.Error, result.Output)
	}
}
