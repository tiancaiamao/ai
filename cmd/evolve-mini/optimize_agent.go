package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

const optimizerTimeout = 20 * time.Minute

// OptimizeAgent runs a headless coding agent in a worktree to improve compact behavior.
type OptimizeAgent struct {
	BaseRepo string
	SuiteDir string
}

// NewOptimizeAgent creates a new optimization agent.
func NewOptimizeAgent(baseRepo, suiteDir string) *OptimizeAgent {
	return &OptimizeAgent{BaseRepo: baseRepo, SuiteDir: suiteDir}
}

// RunOptimizer runs a headless agent in the worktree to make code improvements.
func (oa *OptimizeAgent) RunOptimizer(ctx context.Context, ws *GenerationWorkspace, history *EvolutionHistory) error {
	// Write task file with RELATIVE paths so agent edits worktree files
	taskContent := oa.buildTaskFile(ws, history)
	taskPath := filepath.Join(ws.Worktree, "evolve-task.txt")
	if err := os.WriteFile(taskPath, []byte(taskContent), 0o644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}

	// Write a system prompt that constrains the optimizer
	systemPrompt := `You are an optimizer agent improving the mini compact behavior of this codebase.
You may ONLY modify files within the current working directory (this worktree).
Do NOT read or modify files outside the current working directory.
Use RELATIVE paths for all file operations.
Focus on reading source code, understanding problems, and making minimal targeted fixes.
After making changes, run "go build ./..." to verify compilation. Fix any errors.
Do NOT explore unrelated code, benchmarks, scripts, or evaluation code.
Do NOT read cmd/evolve-mini/ or cmd/evolve-mini-worker/ directories.
Concentrate on: pkg/compact/, pkg/prompt/, pkg/tools/context_mgmt/
`
	systemPromptPath := filepath.Join(ws.Worktree, "evolve-system-prompt.txt")
	if err := os.WriteFile(systemPromptPath, []byte(systemPrompt), 0o644); err != nil {
		return fmt.Errorf("write system prompt: %w", err)
	}

	// Use start_subagent_tmux.sh for reliable process management
	subagentBin := os.Getenv("HOME") + "/.ai/skills/subagent/bin/start_subagent_tmux.sh"
	outputFile := filepath.Join(ws.Worktree, "optimizer-output.txt")

	if _, err := os.Stat(subagentBin); err != nil {
		// Fallback: direct ai invocation
		return oa.runDirect(ctx, ws, taskPath, systemPromptPath, outputFile)
	}

	return oa.runViaSubagent(ws, taskPath, systemPromptPath, outputFile, subagentBin)
}

// runViaSubagent uses start_subagent_tmux.sh for reliable process management.
func (oa *OptimizeAgent) runViaSubagent(ws *GenerationWorkspace, taskPath, systemPromptPath, outputFile, subagentBin string) error {
	// Build task description
	desc := fmt.Sprintf("Read task from %s and follow instructions. System prompt at %s. Output progress to %s",
		taskPath, systemPromptPath, outputFile)

	cmd := exec.Command(subagentBin,
		outputFile,
		"20m",
		fmt.Sprintf("@%s", systemPromptPath),
		desc,
	)
	cmd.Dir = ws.Worktree
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("  Running optimizer via subagent in %s ...\n", ws.Worktree)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("optimizer subagent: %w\n%s", err, string(output))
	}

	return oa.checkWorktreeChanges(ws)
}

// runDirect is a fallback when subagent script is unavailable.
func (oa *OptimizeAgent) runDirect(ctx context.Context, ws *GenerationWorkspace, taskPath, systemPromptPath, outputFile string) error {
	ctx, cancel := context.WithTimeout(ctx, optimizerTimeout)
	defer cancel()

	aiBinary, err := exec.LookPath("ai")
	if err != nil {
		return fmt.Errorf("ai binary not found: %w", err)
	}

	args := []string{
		"--mode", "headless",
		"--timeout", "20m",
		fmt.Sprintf("--system-prompt=@%s", systemPromptPath),
		fmt.Sprintf("@%s", taskPath),
	}

	cmd := exec.CommandContext(ctx, aiBinary, args...)
	cmd.Dir = ws.Worktree // CWD = worktree so relative paths resolve correctly

	// Capture output to file AND stdout
	logFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = ioMultiWriter(os.Stdout, logFile)
	cmd.Stderr = ioMultiWriter(os.Stderr, logFile)

	fmt.Printf("  Running optimizer agent in %s ...\n", ws.Worktree)
	if err := cmd.Run(); err != nil {
		// Timeout is ok, check if changes were made
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  Optimizer timed out, checking for changes...")
		} else {
			return fmt.Errorf("optimizer agent: %w", err)
		}
	}

	return oa.checkWorktreeChanges(ws)
}

// checkWorktreeChanges verifies the optimizer made changes in the worktree.
func (oa *OptimizeAgent) checkWorktreeChanges(ws *GenerationWorkspace) error {
	// git add -A to stage any uncommitted changes
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = ws.Worktree
	_, _ = addCmd.CombinedOutput()

	// Check diff (staged)
	diffCmd := exec.Command("git", "diff", "--cached", "--stat")
	diffCmd.Dir = ws.Worktree
	output, _ := diffCmd.CombinedOutput()
	if len(strings.TrimSpace(string(output))) == 0 {
		return fmt.Errorf("optimizer made no changes")
	}
	fmt.Printf("  Changes:\n%s\n", string(output))
	return nil
}

// buildTaskFile creates the prompt for the optimizer agent.
// Uses relative paths to ensure edits go to worktree files.
func (oa *OptimizeAgent) buildTaskFile(ws *GenerationWorkspace, history *EvolutionHistory) string {
	var sb strings.Builder

	sb.WriteString("You are an optimizer agent improving the mini compact behavior of this codebase.\n")
	sb.WriteString("You can modify any file: source code (.go), prompts (.md), config — anything.\n")
	sb.WriteString("IMPORTANT: Use RELATIVE paths only. You are in a worktree copy of the codebase.\n\n")

	// Score context
	if history.BestScore != nil {
		sb.WriteString(fmt.Sprintf("## Baseline Score (gen %d)\n", history.BestGeneration))
		sb.WriteString(fmt.Sprintf("Weighted Average: %.1f / 55\n\n", history.BestScore.WeightedAverage))

		sb.WriteString("### Per-Case Scores\n")
		for _, cs := range history.BestScore.CaseScores {
			sb.WriteString(fmt.Sprintf("- %s: total=%d/55 (retain=%d, exec=%d, decision=%d, accuracy=%d, efficiency=%d)\n",
				cs.SnapshotID, cs.WeightedTotal,
				cs.Scores.InfoRetention, cs.Scores.TaskExecutability,
				cs.Scores.DecisionCorrectness, cs.Scores.ContextAccuracy,
				cs.Scores.TokenEfficiency))
		}
		sb.WriteString("\n")

		// Worst cases with assessments
		if len(history.BestScore.WorstCaseIDs) > 0 {
			sb.WriteString("### Worst Cases (focus improvements here)\n")
			for _, id := range history.BestScore.WorstCaseIDs {
				for _, cs := range history.BestScore.CaseScores {
					if cs.SnapshotID == id {
						sb.WriteString(fmt.Sprintf("- %s (score=%d): %s\n", id, cs.WeightedTotal, cs.OverallAssessment))
					}
				}
			}
			sb.WriteString("\n")
		}

		// Dimension averages
		sb.WriteString("### Dimension Averages\n")
		for dim, avg := range history.BestScore.DimensionScores {
			sb.WriteString(fmt.Sprintf("- %s: %.1f/5\n", dim, avg))
		}
		sb.WriteString("\n")
	}

	// Past generation attempts (experience for learning)
	if len(history.AllGenerations) > 1 {
		sb.WriteString("### Past Generation Attempts (learn from these)\n")
		for _, gen := range history.AllGenerations[1:] { // skip gen 0
			status := "REJECTED"
			if gen.Status == "accepted" {
				status = "ACCEPTED"
			}
			sb.WriteString(fmt.Sprintf("- Gen %d [%s]: %s\n", gen.Generation, status, gen.OptimizeRationale))
		}
		sb.WriteString("\n")
	}

	// Key files — relative paths!
	sb.WriteString("## Key Source Files (use relative paths)\n")
	sb.WriteString("- pkg/compact/llm_mini_compact.go\n")
	sb.WriteString("- pkg/prompt/llm_mini_compact_system.md\n")
	sb.WriteString("- pkg/tools/context_mgmt/truncate_messages.go\n")
	sb.WriteString("- pkg/tools/context_mgmt/update_llm_context.go\n")
	sb.WriteString("- pkg/prompt/mini_compact.md\n\n")

	// Instructions
	sb.WriteString("## Instructions\n")
	sb.WriteString("1. Read the source code of the key files listed above.\n")
	sb.WriteString("2. Identify the root causes of low scores (bad truncation, empty LLM context, hallucination).\n")
	sb.WriteString("3. Make minimal, targeted fixes. Do NOT rewrite everything.\n")
	sb.WriteString("4. Focus on the biggest problems first.\n")
	sb.WriteString("5. **After making changes, run `go build ./...` to verify compilation succeeds.** Fix any errors.\n")
	sb.WriteString("6. Do NOT explore cmd/evolve-mini/, cmd/evolve-mini-worker/, benchmark/, or scripts/.\n")

	return sb.String()
}

// EvolutionHistory wraps evolvemini types with helpers.
type EvolutionHistory struct {
	CurrentGeneration int                          `json:"currentGeneration"`
	BestGeneration    int                          `json:"bestGeneration"`
	BestScore        *evolvemini.SuiteScore        `json:"bestScore"`
	AllGenerations  []evolvemini.GenerationRecord  `json:"allGenerations"`
}

// ioMultiWriter duplicates writes to multiple writers.
type multiWriter struct {
	writers []interface{ Write([]byte) (int, error) }
}

func ioMultiWriter(writers ...interface{ Write([]byte) (int, error) }) *multiWriter {
	return &multiWriter{writers: writers}
}

func (mw *multiWriter) Write(p []byte) (int, error) {
	for _, w := range mw.writers {
		n, err := w.Write(p)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}