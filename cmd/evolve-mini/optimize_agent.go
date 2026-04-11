package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

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
	// Build task file content
	taskContent := oa.buildTaskFile(ws, history)

	// Write task file to worktree
	taskPath := filepath.Join(ws.Worktree, "evolve-task.txt")
	if err := os.WriteFile(taskPath, []byte(taskContent), 0o644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}

	// Copy baseline worker + evolve-mini-worker source so build works
	// (worktree has full source from git, just needs compilation)

	// Run headless agent in worktree
	aiBinary, err := exec.LookPath("ai")
	if err != nil {
		return fmt.Errorf("ai binary not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, aiBinary,
		"--mode", "headless",
		"--max-turns", "0",
		"--timeout", "10m",
		fmt.Sprintf("@%s", taskPath),
	)
	cmd.Dir = ws.Worktree
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("  Running optimizer agent in %s ...\n", ws.Worktree)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("optimizer agent: %w", err)
	}

	// Check if anything changed
	diffCmd := exec.Command("git", "diff", "--stat")
	diffCmd.Dir = ws.Worktree
	output, _ := diffCmd.CombinedOutput()
	if len(strings.TrimSpace(string(output))) == 0 {
		return fmt.Errorf("optimizer made no changes")
	}
	fmt.Printf("  Changes:\n%s\n", string(output))
	return nil
}

// buildTaskFile creates the prompt for the optimizer agent.
func (oa *OptimizeAgent) buildTaskFile(ws *GenerationWorkspace, history *EvolutionHistory) string {
	var sb strings.Builder

	sb.WriteString("You are an optimizer agent improving the mini compact behavior of this codebase.\n")
	sb.WriteString("You can modify any file: source code (.go), prompts (.md), config, tools — anything.\n\n")

	// Score context
	if history.BestScore != nil {
		sb.WriteString(fmt.Sprintf("## Baseline Score (gen %d)\n", history.BestGeneration))
		sb.WriteString(fmt.Sprintf("Weighted Average: %.1f\n\n", history.BestScore.WeightedAverage))

		sb.WriteString("### Per-Case Scores\n")
		for _, cs := range history.BestScore.CaseScores {
			sb.WriteString(fmt.Sprintf("- %s: total=%d (retain=%d, exec=%d, decision=%d, accuracy=%d, efficiency=%d)\n",
				cs.SnapshotID, cs.WeightedTotal,
				cs.Scores.InfoRetention, cs.Scores.TaskExecutability,
				cs.Scores.DecisionCorrectness, cs.Scores.ContextAccuracy,
				cs.Scores.TokenEfficiency))
		}
		sb.WriteString("\n")

		// Worst cases
		if len(history.BestScore.WorstCaseIDs) > 0 {
			sb.WriteString("### Worst Cases (focus here)\n")
			for _, id := range history.BestScore.WorstCaseIDs {
				for _, cs := range history.BestScore.CaseScores {
					if cs.SnapshotID == id {
						sb.WriteString(fmt.Sprintf("- %s: %s\n", id, cs.OverallAssessment))
					}
				}
			}
			sb.WriteString("\n")
		}
	}

	// Key files
	sb.WriteString("## Key Source Files\n")
	sb.WriteString("- pkg/compact/llm_mini_compact.go\n")
	sb.WriteString("- pkg/prompt/llm_mini_compact_system.md\n")
	sb.WriteString("- pkg/tools/context_mgmt/truncate_messages.go\n")
	sb.WriteString("- pkg/prompt/mini_compact.md\n\n")

	// Instructions
	sb.WriteString("## Instructions\n")
	sb.WriteString("1. Read the source code and understand what's failing.\n")
	sb.WriteString("2. Make minimal, targeted fixes to improve scores.\n")
	sb.WriteString("3. Focus on the biggest problems first.\n")
	sb.WriteString("4. **After making changes, run `go build ./...` to verify compilation succeeds.** Fix any errors.\n")

	return sb.String()
}

// EvolutionHistory wraps evolvemini types with helpers.
type EvolutionHistory struct {
	CurrentGeneration int                          `json:"currentGeneration"`
	BestGeneration    int                          `json:"bestGeneration"`
	BestScore        *evolvemini.SuiteScore        `json:"bestScore"`
	AllGenerations  []evolvemini.GenerationRecord  `json:"allGenerations"`
}

// GetWorstCases returns N lowest-scoring cases from best generation.
func (eh *EvolutionHistory) GetWorstCases(n int) []evolvemini.CaseScore {
	if eh.BestScore == nil || len(eh.BestScore.CaseScores) == 0 {
		return nil
	}
	scores := make([]evolvemini.CaseScore, len(eh.BestScore.CaseScores))
	copy(scores, eh.BestScore.CaseScores)
	if n > len(scores) {
		n = len(scores)
	}
	return scores[:n]
}