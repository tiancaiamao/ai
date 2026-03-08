package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TaskResult represents the result of running a single task
type TaskResult struct {
	TaskID      string        `json:"task_id"`
	Name        string        `json:"name"`
	Passed      bool          `json:"passed"`
	Duration    time.Duration `json:"duration"`
	Error       string        `json:"error,omitempty"`
	Output      string        `json:"output,omitempty"`
	AgentOutput string        `json:"agent_output,omitempty"` // Agent's response/trajectory
}

// BenchmarkReport represents the full benchmark report
type BenchmarkReport struct {
	Timestamp   string       `json:"timestamp"`
	AgentName   string       `json:"agent_name"`
	GitCommit   string       `json:"git_commit"`
	TotalTasks  int          `json:"total_tasks"`
	PassedTasks int          `json:"passed_tasks"`
	FailedTasks int          `json:"failed_tasks"`
	Duration    time.Duration `json:"duration"`
	Results     []TaskResult `json:"results"`
	PassRate    float64      `json:"pass_rate"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "run":
		runBenchmark()
	case "compare":
		compareResults()
	case "list":
		listTasks()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Agent Benchmark Runner

Usage:
  runner run              Run all benchmark tasks
  runner compare          Compare baseline vs current results
  runner list             List all available tasks

Examples:
  # Run benchmark and save as current
  go run runner.go run

  # Save current as baseline
  cp results/current.json results/baseline.json

  # Compare results
  go run runner.go compare
`)
}

func listTasks() {
	tasksDir := "tasks"
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		fmt.Printf("Error reading tasks directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Available Tasks:")
	fmt.Println("================")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		taskDir := filepath.Join(tasksDir, entry.Name())
		taskFile := filepath.Join(taskDir, "task.md")

		content, err := os.ReadFile(taskFile)
		if err != nil {
			continue
		}

		// Extract title from markdown
		lines := strings.Split(string(content), "\n")
		title := entry.Name()
		for _, line := range lines {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimPrefix(line, "# ")
				break
			}
		}

		fmt.Printf("  - %s: %s\n", entry.Name(), title)
	}
}

func runBenchmark() {
	tasksDir := "tasks"
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		fmt.Printf("Error reading tasks directory: %v\n", err)
		os.Exit(1)
	}

	report := BenchmarkReport{
		Timestamp: time.Now().Format(time.RFC3339),
		AgentName: getEnvOrDefault("AGENT_NAME", "local-agent"),
		GitCommit: getGitCommit(),
		Results:   make([]TaskResult, 0),
	}

	startTime := time.Now()

	fmt.Println("Running Benchmark Tasks")
	fmt.Println("=======================")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		taskID := entry.Name()
		taskDir := filepath.Join(tasksDir, taskID)

		fmt.Printf("\n[%s] Running...\n", taskID)
		result := runTask(taskID, taskDir)
		report.Results = append(report.Results, result)

		if result.Passed {
			fmt.Printf("[%s] PASSED (%.2fs)\n", taskID, result.Duration.Seconds())
			report.PassedTasks++
		} else {
			fmt.Printf("[%s] FAILED: %s\n", taskID, result.Error)
			report.FailedTasks++
		}

		report.TotalTasks++
	}

	report.Duration = time.Since(startTime)
	report.PassRate = float64(report.PassedTasks) / float64(report.TotalTasks) * 100

	// Print summary
	fmt.Println("\n=======================")
	fmt.Println("Summary")
	fmt.Println("=======================")
	fmt.Printf("Total:   %d\n", report.TotalTasks)
	fmt.Printf("Passed:  %d\n", report.PassedTasks)
	fmt.Printf("Failed:  %d\n", report.FailedTasks)
	fmt.Printf("Rate:    %.1f%%\n", report.PassRate)
	fmt.Printf("Duration: %s\n", report.Duration.Round(time.Second))

	// Save results
	saveReport(&report, "results/current.json")
	fmt.Println("\nResults saved to results/current.json")
}

func runTask(taskID, taskDir string) TaskResult {
	result := TaskResult{
		TaskID: taskID,
	}

	// Get absolute path
	absTaskDir, err := filepath.Abs(taskDir)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("Failed to get absolute path: %v", err)
		return result
	}

	// Read task name from task.md
	taskFile := filepath.Join(absTaskDir, "task.md")
	if content, err := os.ReadFile(taskFile); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "# ") {
				result.Name = strings.TrimPrefix(line, "# ")
				break
			}
		}
	}

	startTime := time.Now()

	// Check if this is an interactive task (needs agent)
	agentScript := filepath.Join(absTaskDir, "run_agent.sh")
	if _, err := os.Stat(agentScript); err == nil {
		// Run agent first
		fmt.Printf("[%s] Running agent...\n", taskID)
		agentCmd := exec.Command("/bin/bash", agentScript)
		agentCmd.Dir = absTaskDir
		agentOutput, err := agentCmd.CombinedOutput()
		result.AgentOutput = string(agentOutput)
		if err != nil {
			result.Passed = false
			result.Error = fmt.Sprintf("Agent failed: %v", err)
			result.Duration = time.Since(startTime)
			return result
		}
	}

	// Run verification script
	verifyScript := filepath.Join(absTaskDir, "verify.sh")
	if _, err := os.Stat(verifyScript); os.IsNotExist(err) {
		result.Passed = false
		result.Error = "No verify.sh found"
		result.Duration = time.Since(startTime)
		return result
	}

	// Make verify script executable
	os.Chmod(verifyScript, 0755)

	cmd := exec.Command("/bin/bash", verifyScript)
	cmd.Dir = absTaskDir
	output, err := cmd.CombinedOutput()
	result.Output = string(output)
	result.Duration = time.Since(startTime)

	if err != nil {
		result.Passed = false
		result.Error = "Verification failed"
	} else {
		result.Passed = true
	}

	return result
}

func compareResults() {
	baselineFile := "results/baseline.json"
	currentFile := "results/current.json"

	baseline, err := loadReport(baselineFile)
	if err != nil {
		fmt.Printf("Error loading baseline: %v\n", err)
		fmt.Println("Run 'go run runner.go run' first to create current results")
		os.Exit(1)
	}

	current, err := loadReport(currentFile)
	if err != nil {
		fmt.Printf("Error loading current: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Benchmark Comparison")
	fmt.Println("====================")
	fmt.Printf("\nBaseline: %s (commit: %s)\n", baseline.Timestamp, baseline.GitCommit)
	fmt.Printf("Current:  %s (commit: %s)\n", current.Timestamp, current.GitCommit)

	fmt.Printf("\n         Baseline    Current     Diff\n")
	fmt.Printf("         --------    -------     ----\n")
	fmt.Printf("Pass Rate: %5.1f%%    %5.1f%%    %+.1f%%\n",
		baseline.PassRate, current.PassRate, current.PassRate-baseline.PassRate)
	fmt.Printf("Passed:    %5d      %5d      %+d\n",
		baseline.PassedTasks, current.PassedTasks, current.PassedTasks-baseline.PassedTasks)
	fmt.Printf("Failed:    %5d      %5d      %+d\n",
		baseline.FailedTasks, current.FailedTasks, current.FailedTasks-baseline.FailedTasks)

	// Detailed comparison
	fmt.Println("\nTask-by-Task Comparison:")
	fmt.Println("------------------------")

	baselineMap := make(map[string]TaskResult)
	for _, r := range baseline.Results {
		baselineMap[r.TaskID] = r
	}

	regressions := 0
	improvements := 0

	for _, currentResult := range current.Results {
		baselineResult, exists := baselineMap[currentResult.TaskID]

		if !exists {
			fmt.Printf("[%s] NEW - %s\n", currentResult.TaskID, passStr(currentResult.Passed))
			continue
		}

		if baselineResult.Passed != currentResult.Passed {
			if currentResult.Passed {
				fmt.Printf("[%s] IMPROVED ✅\n", currentResult.TaskID)
				improvements++
			} else {
				fmt.Printf("[%s] REGRESSED ❌\n", currentResult.TaskID)
				regressions++
			}
		} else {
			fmt.Printf("[%s] %s\n", currentResult.TaskID, passStr(currentResult.Passed))
		}
	}

	fmt.Println("\n========================")
	if regressions > 0 {
		fmt.Printf("⚠️  REGRESSION DETECTED: %d task(s) failed that previously passed\n", regressions)
	} else if improvements > 0 {
		fmt.Printf("✅ IMPROVEMENT: %d task(s) fixed\n", improvements)
	} else {
		fmt.Println("✅ No regressions detected")
	}

	if regressions > 0 {
		os.Exit(1)
	}
}

func passStr(passed bool) string {
	if passed {
		return "PASS ✓"
	}
	return "FAIL ✗"
}

func loadReport(path string) (*BenchmarkReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var report BenchmarkReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	return &report, nil
}

func saveReport(report *BenchmarkReport, path string) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling report: %v\n", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Printf("Error saving report: %v\n", err)
	}
}

func getGitCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
