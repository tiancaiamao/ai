package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvolutionConfig controls the self-evolution loop.
type EvolutionConfig struct {
	// PromptFile is the path to the system prompt being optimized.
	PromptFile string `json:"prompt_file"`
	// TestSuiteFile is the path to the test suite (questions + scenarios).
	TestSuiteFile string `json:"test_suite_file"`
	// WorkDir is the directory for all evolution artifacts.
	WorkDir string `json:"work_dir"`
	// MaxIterations is the maximum number of optimization rounds.
	MaxIterations int `json:"max_iterations"`
	// PromptLengthWeight is the penalty weight for prompt length (0-1).
	// Higher = more penalty for longer prompts.
	PromptLengthWeight float64 `json:"prompt_length_weight"`
}

// TestCase represents a single test case for evaluating prompt understanding.
type TestCase struct {
	ID       string `json:"id"`
	Category string `json:"category"` // "knowledge", "behavior", "edge_case", "negative"
	Type     string `json:"type"`     // "llm_judge" or "structural"
	Question string `json:"question"`
	// ExpectedBehavior describes what the agent should do (for scoring).
	ExpectedBehavior string `json:"expected_behavior"`
	// StructuralChecks defines what to look for in session output (for structural tests).
	StructuralChecks []StructuralCheck `json:"structural_checks,omitempty"`
}

// StructuralCheck defines a pattern to look for in subagent output.
type StructuralCheck struct {
	ID       string `json:"id"`
	Pattern  string `json:"pattern"`  // regex or text to find in output
	Negate   bool   `json:"negate"`   // if true, pattern must NOT appear
	Weight   float64 `json:"weight"`  // weight for this check (0-1)
}

// TestSuite is a collection of test cases.
type TestSuite struct {
	Name      string     `json:"name"`
	TargetPrompt string  `json:"target_prompt"`
	TestCases []TestCase `json:"test_cases"`
}

// TestResult is the result of running a single test case.
type TestResult struct {
	TestCaseID string  `json:"test_case_id"`
	Score      float64 `json:"score"` // 0-1
	Feedback   string  `json:"feedback"`
}

// EvolutionResult captures the result of one evolution iteration.
type EvolutionResult struct {
	Iteration    int          `json:"iteration"`
	Timestamp    string       `json:"timestamp"`
	PromptFile   string       `json:"prompt_file"`
	PromptLength int          `json:"prompt_length"`
	TestResults  []TestResult `json:"test_results"`
	TotalScore   float64      `json:"total_score"`
	// AdjustedScore accounts for prompt length penalty.
	AdjustedScore float64 `json:"adjusted_score"`
	// OptimizationSuggestions from the judge for next iteration.
	OptimizationSuggestions string `json:"optimization_suggestions"`
	// PromptDiff is the diff from previous prompt version.
	PromptDiff string `json:"prompt_diff,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: evolve <command> [args]")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  init <work_dir>           Initialize evolution workspace")
		fmt.Fprintln(os.Stderr, "  run <config.json>         Run evolution loop")
		fmt.Fprintln(os.Stderr, "  judge <results.json>      Run LLM judge on test results")
		fmt.Fprintln(os.Stderr, "  report <work_dir>         Print evolution report")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: evolve init <work_dir>")
			os.Exit(1)
		}
		if err := initWorkspace(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: evolve run <config.json>")
			os.Exit(1)
		}
		if err := runEvolution(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "report":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: evolve report <work_dir>")
			os.Exit(1)
		}
		if err := printReport(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func initWorkspace(workDir string) error {
	dirs := []string{
		filepath.Join(workDir, "prompts"),
		filepath.Join(workDir, "results"),
		filepath.Join(workDir, "sessions"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}

	// Create default test suite for context_management.md
	suite := TestSuite{
		Name:         "context_management",
		TargetPrompt: "context_management.md",
		TestCases: []TestCase{
			// --- Knowledge Tests ---
			{
				ID:               "k1",
				Category:         "knowledge",
				Type:             "llm_judge",
				Question:         "What is the trigger condition for truncate? Do you need to wait for context usage to reach a threshold before truncating?",
				ExpectedBehavior: "Should say truncate can be done ANYTIME, no threshold needed. The criterion is whether the output is still useful for the current task.",
			},
			{
				ID:               "k2",
				Category:         "knowledge",
				Type:             "llm_judge",
				Question:         "What does the stale tag mean? Is it the criterion for deciding whether to truncate?",
				ExpectedBehavior: "Should say stale is only a HINT about recency, NOT the truncation criterion. The real criterion is usefulness for current task.",
			},
			{
				ID:               "k3",
				Category:         "knowledge",
				Type:             "llm_judge",
				Question:         "How frequently should you truncate? Should you do it every turn or batch?",
				ExpectedBehavior: "Should say CHECK every turn, but TRUNCATE IN BATCH. Each truncate call adds context, so batching is more efficient.",
			},
			{
				ID:               "k4",
				Category:         "knowledge",
				Type:             "llm_judge",
				Question:         "What is the trigger condition for compact? How does it differ from truncate?",
				ExpectedBehavior: "Should say compact triggers at >=30% context usage, is for topic shift or phase completed. Truncate is per-output, compact is global compression.",
			},
			// --- Edge Case Tests ---
			{
				ID:               "e1",
				Category:         "edge_case",
				Type:             "llm_judge",
				Question:         "You see a tool output with stale=\"3\" that contains critical information needed for your current task. Should you truncate it? Why?",
				ExpectedBehavior: "Should NOT truncate. Stale is only a hint. The real criterion is usefulness — if it's still needed, keep it regardless of stale value.",
			},
			{
				ID:               "e2",
				Category:         "edge_case",
				Type:             "llm_judge",
				Question:         "Your context usage is at 5%. There are 3 stale tool outputs from a previous completed task. Should you truncate them?",
				ExpectedBehavior: "Should say YES — truncate whenever outputs are no longer useful, regardless of context usage percentage. Low usage doesn't mean you should skip truncation.",
			},
			// --- Negative Behavior Tests ---
			{
				ID:               "n1",
				Category:         "negative",
				Type:             "llm_judge",
				Question:         "Should you truncate all outputs that have a stale tag? Explain your reasoning.",
				ExpectedBehavior: "Should say NO. Stale is a hint, not a criterion. Truncate based on usefulness to current task, not just stale status.",
			},
			{
				ID:               "n2",
				Category:         "negative",
				Type:             "llm_judge",
				Question:         "When you receive a context management reminder, can you use skip to defer handling it?",
				ExpectedBehavior: "Should say NO. When reminded, skip is NOT allowed. Must use truncate or compact immediately.",
			},
			// --- Behavioral / Structural Tests ---
			{
				ID:       "s1",
				Category: "behavior",
				Type:     "structural",
				Question: "You are an AI agent. You just read a file at /tmp/test.txt and got its contents. The file content is no longer needed. Your current task is to list directory contents. Read /tmp/test.txt then list /tmp directory.",
				ExpectedBehavior: "Agent should truncate the file read output after using it, before or after doing the next task.",
				StructuralChecks: []StructuralCheck{
					{ID: "s1_truncate", Pattern: "truncate", Weight: 0.8},
					{ID: "s1_no_skip", Pattern: "skip", Negate: true, Weight: 0.2},
				},
			},
		},
	}

	suitePath := filepath.Join(workDir, "test_suite.json")
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal test suite: %w", err)
	}
	if err := os.WriteFile(suitePath, data, 0644); err != nil {
		return fmt.Errorf("write test suite: %w", err)
	}

	// Create default config
	config := EvolutionConfig{
		PromptFile:        "pkg/prompt/context_management.md",
		TestSuiteFile:     "test_suite.json",
		WorkDir:           workDir,
		MaxIterations:     5,
		PromptLengthWeight: 0.1,
	}
	configPath := filepath.Join(workDir, "config.json")
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("Initialized evolution workspace at %s\n", workDir)
	fmt.Printf("  Test suite: %s\n", suitePath)
	fmt.Printf("  Config:     %s\n", configPath)
	return nil
}

func runEvolution(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var config EvolutionConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Load test suite
	suiteData, err := os.ReadFile(filepath.Join(config.WorkDir, config.TestSuiteFile))
	if err != nil {
		return fmt.Errorf("read test suite: %w", err)
	}
	var suite TestSuite
	if err := json.Unmarshal(suiteData, &suite); err != nil {
		return fmt.Errorf("parse test suite: %w", err)
	}

	// Load current prompt
	promptData, err := os.ReadFile(config.PromptFile)
	if err != nil {
		return fmt.Errorf("read prompt: %w", err)
	}
	currentPrompt := string(promptData)

	for i := 0; i < config.MaxIterations; i++ {
		fmt.Printf("\n=== Iteration %d ===\n", i+1)

		// Save current prompt version
		promptVersionFile := filepath.Join(config.WorkDir, "prompts", fmt.Sprintf("v%d.md", i))
		if err := os.WriteFile(promptVersionFile, []byte(currentPrompt), 0644); err != nil {
			return fmt.Errorf("save prompt v%d: %w", i, err)
		}

		result := EvolutionResult{
			Iteration:    i + 1,
			Timestamp:    time.Now().Format(time.RFC3339),
			PromptFile:   promptVersionFile,
			PromptLength: len(currentPrompt),
		}

		// Run test cases via subagents
		llmJudgeCases := filterCases(suite.TestCases, "llm_judge")
		structuralCases := filterCases(suite.TestCases, "structural")

		// Phase 1: LLM Judge tests — generate a single subagent task with all questions
		if len(llmJudgeCases) > 0 {
			fmt.Printf("Running %d LLM judge test cases...\n", len(llmJudgeCases))
			results, err := runLLMJudgeTests(config, llmJudgeCases, currentPrompt)
			if err != nil {
				fmt.Fprintf(os.Stderr, "LLM judge error: %v\n", err)
			} else {
				result.TestResults = append(result.TestResults, results...)
			}
		}

		// Phase 2: Structural tests
		if len(structuralCases) > 0 {
			fmt.Printf("Running %d structural test cases...\n", len(structuralCases))
			results, err := runStructuralTests(config, structuralCases, currentPrompt)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Structural test error: %v\n", err)
			} else {
				result.TestResults = append(result.TestResults, results...)
			}
		}

		// Calculate scores
		result.TotalScore = calculateTotalScore(result.TestResults)
		result.AdjustedScore = result.TotalScore - config.PromptLengthWeight*float64(len(currentPrompt))/1000.0

		// Save result
		resultData, _ := json.MarshalIndent(result, "", "  ")
		resultFile := filepath.Join(config.WorkDir, "results", fmt.Sprintf("iteration_%d.json", i+1))
		if err := os.WriteFile(resultFile, resultData, 0644); err != nil {
			return fmt.Errorf("save result: %w", err)
		}

		fmt.Printf("Score: %.2f (adjusted: %.2f, prompt: %d bytes)\n",
			result.TotalScore, result.AdjustedScore, len(currentPrompt))

		// Check convergence
		if result.TotalScore >= 0.95 {
			fmt.Println("Converged! Score >= 0.95")
			break
		}

		// Generate optimization suggestions (placeholder — in full version, use LLM)
		fmt.Println("In full version, an LLM optimizer would adjust the prompt here.")
		fmt.Printf("Review results at: %s\n", resultFile)
		break // For PoC, run only one iteration
	}

	return nil
}

func filterCases(cases []TestCase, typ string) []TestCase {
	var filtered []TestCase
	for _, c := range cases {
		if c.Type == typ {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func runLLMJudgeTests(config EvolutionConfig, cases []TestCase, prompt string) ([]TestResult, error) {
	// Build a single task file with all questions for the subagent
	var sb strings.Builder
	sb.WriteString("You are being tested on your understanding of context management rules.\n")
	sb.WriteString("Answer each question concisely. Use the format: Q<id>: <answer>\n\n")
	for _, tc := range cases {
		sb.WriteString(fmt.Sprintf("Q%s: %s\n\n", tc.ID, tc.Question))
	}
	sb.WriteString("\n---\nExpected behaviors (for your reference, do NOT repeat these verbatim):\n")
	for _, tc := range cases {
		sb.WriteString(fmt.Sprintf("Q%s expected: %s\n", tc.ID, tc.ExpectedBehavior))
	}

	taskFile := filepath.Join(config.WorkDir, "sessions", "llm_judge_task.txt")
	if err := os.WriteFile(taskFile, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write task file: %w", err)
	}

	outputFile := filepath.Join(config.WorkDir, "sessions", "llm_judge_output.txt")
	_ = outputFile // Used by subagent in full implementation

	// For PoC, print the command that would be run
	fmt.Printf("  Task file: %s\n", taskFile)
	fmt.Printf("  Subagent command would be: start_subagent_tmux.sh %s 5m - 'Read task from %s'\n", outputFile, taskFile)

	// Return placeholder results — in full implementation, parse subagent output
	var results []TestResult
	for _, tc := range cases {
		results = append(results, TestResult{
			TestCaseID: tc.ID,
			Score:      0, // To be filled by judge
			Feedback:   "Pending — subagent output needs to be parsed and judged",
		})
	}
	return results, nil
}

func runStructuralTests(config EvolutionConfig, cases []TestCase, prompt string) ([]TestResult, error) {
	var results []TestResult
	for _, tc := range cases {
		fmt.Printf("  Structural test: %s (%s)\n", tc.ID, tc.Category)

		// For PoC, print what would be checked
		for _, check := range tc.StructuralChecks {
			negateStr := ""
			if check.Negate {
				negateStr = " (must NOT appear)"
			}
			fmt.Printf("    Check: pattern=%q%s weight=%.1f\n", check.Pattern, negateStr, check.Weight)
		}

		results = append(results, TestResult{
			TestCaseID: tc.ID,
			Score:      0,
			Feedback:   "Pending — subagent session needs to be analyzed",
		})
	}
	return results, nil
}

func calculateTotalScore(results []TestResult) float64 {
	if len(results) == 0 {
		return 0
	}
	var total float64
	for _, r := range results {
		total += r.Score
	}
	return total / float64(len(results))
}

func printReport(workDir string) error {
	resultsDir := filepath.Join(workDir, "results")
	entries, err := os.ReadDir(resultsDir)
	if os.IsNotExist(err) {
		return fmt.Errorf("no results found in %s", workDir)
	}
	if err != nil {
		return fmt.Errorf("read results dir: %w", err)
	}

	fmt.Println("=== Evolution Report ===")
	fmt.Printf("Workspace: %s\n\n", workDir)

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(resultsDir, e.Name()))
		if err != nil {
			continue
		}
		var result EvolutionResult
		if err := json.Unmarshal(data, &result); err != nil {
			continue
		}
		fmt.Printf("Iteration %d (%s)\n", result.Iteration, result.Timestamp)
		fmt.Printf("  Prompt: %s (%d bytes)\n", result.PromptFile, result.PromptLength)
		fmt.Printf("  Score: %.2f (adjusted: %.2f)\n", result.TotalScore, result.AdjustedScore)
		for _, tr := range result.TestResults {
			fmt.Printf("    %s: %.2f — %s\n", tr.TestCaseID, tr.Score, tr.Feedback)
		}
		fmt.Println()
	}
	return nil
}