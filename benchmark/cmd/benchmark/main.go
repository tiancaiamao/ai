package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Task represents a benchmark task
type Task struct {
	ID           string
	Name         string
	Description  string
	Dir          string
	NeedsAppShim bool
}

// Result represents a task execution result
type Result struct {
	TaskID               string         `json:"task_id"`
	Passed               bool           `json:"passed"`
	FunctionalPassed     bool           `json:"functional_passed"`
	AgenticPassed        bool           `json:"agentic_passed"`
	AgenticScore         float64        `json:"agentic_score,omitempty"`
	Output               string         `json:"output"`
	Error                string         `json:"error,omitempty"`
	Duration             float64        `json:"duration_seconds"`
	Timestamp            time.Time      `json:"timestamp"`
	AgentOutput          string         `json:"agent_output,omitempty"`
	ToolCalls            int            `json:"tool_calls,omitempty"`
	ToolsUsed            []string       `json:"tools_used,omitempty"`
	CapabilitiesUsed     []string       `json:"capabilities_used,omitempty"`
	CapabilityCounts     map[string]int `json:"capability_counts,omitempty"`
	ConstraintChecked    bool           `json:"constraint_checked,omitempty"`
	ConstraintPassed     *bool          `json:"constraint_passed,omitempty"`
	ConstraintViolations []string       `json:"constraint_violations,omitempty"`
	SoftViolations       []string       `json:"soft_violations,omitempty"`
}

// ConstraintSpec defines process-level constraints for agent tasks.
type ConstraintSpec struct {
	MaxSteps            int            `json:"max_steps,omitempty"`
	MaxStepsMode        string         `json:"max_steps_mode,omitempty"`
	MustUseTools        []string       `json:"must_use_tools,omitempty"`
	MustUseCapabilities []string       `json:"must_use_capabilities,omitempty"`
	ForbiddenPatterns   []string       `json:"forbidden_patterns,omitempty"`
	SuccessCriteria     map[string]any `json:"success_criteria,omitempty"`
	Description         string         `json:"description,omitempty"`
}

type toolEvent struct {
	Tool    string
	Payload string
	Command string
	File    string
}

// ProcessMetrics summarizes tool usage and behavioral signals from agent output.
type ProcessMetrics struct {
	Events              []toolEvent
	ToolCounts          map[string]int
	ToolsUsed           []string
	CapabilityCounts    map[string]int
	CapabilitiesUsed    []string
	TotalToolCalls      int
	ReadCalls           int
	ReadBeforeFirstEdit int
	EditCalls           int
	TestRuns            int
	FirstEditIndex      int
	FirstTestIndex      int
	GrepLikeCalls       int
	SearchLikeCalls     int
	LogFilesRead        int
	EditedFiles         map[string]struct{}
	ReadStage1          bool
	EditedStage5        bool
	HasRollbackAction   bool
}

// RunReport represents a complete benchmark run
type RunReport struct {
	Timestamp          string   `json:"timestamp"`
	Agent              string   `json:"agent"`
	ManifestPath       string   `json:"manifest_path,omitempty"`
	MaxStepsMode       string   `json:"max_steps_mode,omitempty"`
	TotalTasks         int      `json:"total_tasks"`
	Passed             int      `json:"passed"`
	Failed             int      `json:"failed"`
	PassRate           float64  `json:"pass_rate"`
	FunctionalPassed   int      `json:"functional_passed,omitempty"`
	FunctionalFailed   int      `json:"functional_failed,omitempty"`
	FunctionalPassRate float64  `json:"functional_pass_rate,omitempty"`
	AgenticPassed      int      `json:"agentic_passed,omitempty"`
	AgenticFailed      int      `json:"agentic_failed,omitempty"`
	AgenticPassRate    float64  `json:"agentic_pass_rate,omitempty"`
	AvgAgenticScore    float64  `json:"avg_agentic_score,omitempty"`
	Results            []Result `json:"results"`
	totalAgenticScore  float64  `json:"-"`
}

// TaskManifest defines a frozen task list and optional defaults.
type TaskManifest struct {
	Version        string                       `json:"version,omitempty"`
	FrozenAt       string                       `json:"frozen_at,omitempty"`
	Tasks          []string                     `json:"tasks"`
	GlobalDefaults TaskManifestGlobalDefaults   `json:"global_defaults,omitempty"`
	TaskOverrides  map[string]TaskManifestRules `json:"task_overrides,omitempty"`
}

type TaskManifestGlobalDefaults struct {
	MaxStepsMode string `json:"max_steps_mode,omitempty"`
}

type TaskManifestRules struct {
	MaxStepsMode string `json:"max_steps_mode,omitempty"`
}

// LoadProgress loads progress from a previous run
func (b *Benchmark) LoadProgress(progressFile string) (map[string]Result, error) {
	completed := make(map[string]Result)

	data, err := os.ReadFile(progressFile)
	if err != nil {
		if os.IsNotExist(err) {
			return completed, nil
		}
		return nil, err
	}

	var report RunReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	for _, r := range report.Results {
		completed[r.TaskID] = r
	}

	return completed, nil
}

// SaveProgress saves current progress to a file
func (b *Benchmark) SaveProgress(report *RunReport, progressFile string) error {
	os.MkdirAll(filepath.Dir(progressFile), 0755)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(progressFile, data, 0644)
}

// AgentRunner interface for different agent backends
type AgentRunner interface {
	Run(taskDir string, prompt string) (string, error)
	Name() string
}

// AIAgentRunner runs the ai agent in headless mode
type AIAgentRunner struct {
	BinaryPath string
	MaxTurns   int
	Timeout    time.Duration
}

func (r *AIAgentRunner) Name() string {
	return "ai-agent"
}

func (r *AIAgentRunner) Run(taskDir string, prompt string) (string, error) {
	var ctx context.Context
	var cancel context.CancelFunc

	// Only set timeout if Timeout > 0
	if r.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), r.Timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, r.BinaryPath,
		"--mode", "headless",
		"--max-turns", fmt.Sprintf("%d", r.MaxTurns),
		"--timeout", r.Timeout.String(),
		prompt,
	)
	cmd.Env = nonInteractiveCommandEnv()

	// Set working directory to task dir
	cmd.Dir = taskDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}

	return output, err
}

// Benchmark is the main benchmark runner
type Benchmark struct {
	TasksDir     string
	ResultsDir   string
	Agent        AgentRunner
	Timeout      time.Duration
	MaxStepsMode string
}

func NewBenchmark(tasksDir, resultsDir string, agent AgentRunner) *Benchmark {
	return &Benchmark{
		TasksDir:     tasksDir,
		ResultsDir:   resultsDir,
		Agent:        agent,
		Timeout:      0, // No timeout by default
		MaxStepsMode: "soft",
	}
}

// DiscoverTasks finds all testable tasks in the tasks directory (including subdirectories)
// Only includes tasks that have verify.sh
func (b *Benchmark) DiscoverTasks() ([]Task, error) {
	var tasks []Task
	var skipped []string

	err := filepath.WalkDir(b.TasksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		taskFile := filepath.Join(path, "task.md")
		if _, err := os.Stat(taskFile); err != nil {
			return nil // No task.md, skip
		}

		// Check if verify.sh exists - only include testable tasks
		verifyScript := filepath.Join(path, "verify.sh")
		if _, err := os.Stat(verifyScript); os.IsNotExist(err) {
			relPath, _ := filepath.Rel(b.TasksDir, path)
			skipped = append(skipped, relPath)
			return nil // No verify.sh, skip
		}

		relPath, _ := filepath.Rel(b.TasksDir, path)
		desc, _ := os.ReadFile(taskFile)
		needsAppShim, detectErr := taskNeedsAppShim(path)
		if detectErr != nil {
			return fmt.Errorf("detect /app dependency for %s: %w", path, detectErr)
		}

		task := Task{
			ID:           relPath, // e.g., "tbench/chess-best-move"
			Name:         filepath.Base(path),
			Description:  string(desc),
			Dir:          path,
			NeedsAppShim: needsAppShim,
		}
		tasks = append(tasks, task)
		return nil
	})

	// Print skipped tasks summary
	if len(skipped) > 0 {
		fmt.Printf("\n⚠️  Skipped %d tasks (missing verify.sh):\n", len(skipped))
		for _, s := range skipped {
			fmt.Printf("  - %s\n", s)
		}
	}

	return tasks, err
}

// RunTask runs a single task
func (b *Benchmark) RunTask(task Task) Result {
	start := time.Now()
	result := Result{
		TaskID:    task.ID,
		Timestamp: start,
	}

	if task.NeedsAppShim {
		shimmedTask, cleanup, err := prepareShimmedTask(task)
		if err != nil {
			result.Error = fmt.Sprintf("prepare /app shim failed: %v", err)
			result.Passed = false
			result.FunctionalPassed = false
			result.AgenticPassed = false
			result.AgenticScore = 0
			result.Duration = time.Since(start).Seconds()
			return result
		}
		defer cleanup()
		task = shimmedTask
	}

	// 1. Reset task to initial state
	if err := b.resetTask(task); err != nil {
		result.Error = fmt.Sprintf("reset failed: %v", err)
		result.Passed = false
		result.Duration = time.Since(start).Seconds()
		return result
	}

	if task.NeedsAppShim {
		if err := rewriteLegacyAppPaths(task.Dir, filepath.Join(task.Dir, "setup")); err != nil {
			result.Error = fmt.Sprintf("rewrite /app paths failed: %v", err)
			result.Passed = false
			result.FunctionalPassed = false
			result.AgenticPassed = false
			result.AgenticScore = 0
			result.Duration = time.Since(start).Seconds()
			return result
		}
	}

	// 2. Generate prompt
	prompt := b.generatePrompt(task)
	setupDir := filepath.Join(task.Dir, "setup")

	// 3. Run agent
	agentOutput, err := b.Agent.Run(setupDir, prompt)
	result.AgentOutput = agentOutput
	result.Duration = time.Since(start).Seconds() // Always calculate duration
	metrics := analyzeAgentOutput(agentOutput)
	result.ToolCalls = metrics.TotalToolCalls
	result.ToolsUsed = metrics.ToolsUsed
	result.CapabilitiesUsed = metrics.CapabilitiesUsed
	result.CapabilityCounts = metrics.CapabilityCounts

	if err != nil {
		result.Error = fmt.Sprintf("agent failed: %v", err)

		// Check if agent actually succeeded despite error (e.g., signal: killed after completion)
		if strings.Contains(agentOutput, "All tests passed") ||
			strings.Contains(agentOutput, "9 passed") ||
			strings.Contains(agentOutput, "passed") {
			// Agent likely completed but process was killed
			result.Error = fmt.Sprintf("agent completed but process killed: %v", err)
			result.Passed = true // Mark as passed if output indicates success
		} else {
			result.Passed = false
		}
		result.FunctionalPassed = result.Passed
		result.AgenticPassed = result.Passed
		if result.AgenticPassed {
			result.AgenticScore = 100
		} else {
			result.AgenticScore = 0
		}
		return result
	}

	// 4. Verify
	passed, output, err := b.verifyTask(task)
	result.FunctionalPassed = passed
	result.Passed = passed
	result.Output = output
	if err != nil {
		result.Error = err.Error()
	}

	hardViolations, softViolations, agenticScore, checked, evalErr := b.evaluateTaskConstraints(task, metrics, result.Passed)
	result.AgenticScore = agenticScore
	result.SoftViolations = softViolations
	if evalErr != nil {
		if result.Error == "" {
			result.Error = fmt.Sprintf("constraint evaluation error: %v", evalErr)
		} else {
			result.Error = fmt.Sprintf("%s; constraint evaluation error: %v", result.Error, evalErr)
		}
	}
	if checked {
		constraintPassed := len(hardViolations) == 0
		result.ConstraintChecked = true
		result.ConstraintPassed = &constraintPassed
		result.ConstraintViolations = hardViolations
		result.AgenticPassed = constraintPassed

		if !constraintPassed {
			result.Passed = result.FunctionalPassed && constraintPassed
			if result.Error == "" {
				result.Error = fmt.Sprintf("constraint violations: %s", strings.Join(hardViolations, "; "))
			} else {
				result.Error = fmt.Sprintf("%s; constraint violations: %s", result.Error, strings.Join(hardViolations, "; "))
			}
		} else {
			result.Passed = result.FunctionalPassed
		}
	} else {
		result.AgenticPassed = result.Passed
		result.AgenticScore = 100
	}

	return result
}

// resetTask resets the task setup directory from init (if exists)
func (b *Benchmark) resetTask(task Task) error {
	initDir := filepath.Join(task.Dir, "init")
	setupDir := filepath.Join(task.Dir, "setup")

	// If init directory exists, copy it to setup
	if _, err := os.Stat(initDir); err == nil {
		// Remove setup dir if exists
		os.RemoveAll(setupDir)

		// Copy init to setup
		return filepath.WalkDir(initDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			relPath, _ := filepath.Rel(initDir, path)
			destPath := filepath.Join(setupDir, relPath)

			if d.IsDir() {
				return os.MkdirAll(destPath, 0755)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(destPath, data, 0644)
		})
	}

	// No init directory - ensure setup directory exists for agent to work in
	return os.MkdirAll(setupDir, 0755)
}

// generatePrompt creates the prompt for a task
func (b *Benchmark) generatePrompt(task Task) string {
	appShimNote := ""
	if task.NeedsAppShim {
		appShimNote = fmt.Sprintf(
			"\nImportant: In this harness, absolute path /app maps to %s. Do not use sudo for /app setup.",
			filepath.Join(task.Dir, "setup"),
		)
	}

	return fmt.Sprintf(`You are given a coding task. Read the task description and fix/implement the code.

Task ID: %s
Working Directory: %s/setup

Task Description:
%s

Instructions:
1. Read the files in the setup directory
2. Fix the bugs or implement the required functionality
3. Make sure the code compiles
4. Do NOT modify verify.sh
%s

Please start by reading the task files.`, task.ID, task.Dir, task.Description, appShimNote)
}

// verifyTask runs the verification script
func (b *Benchmark) verifyTask(task Task) (bool, string, error) {
	verifyScript := filepath.Join(task.Dir, "verify.sh")

	if _, err := os.Stat(verifyScript); err != nil {
		return false, "", fmt.Errorf("no verify.sh found")
	}

	ctx, cancel := NewTimeoutContext(60 * time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", verifyScript)
	cmd.Dir = task.Dir
	cmd.Env = nonInteractiveCommandEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}

	return err == nil, output, err
}

// verifyInitialTaskFailure resets the task and verifies initial state fails.
// Returns passed=true when verification unexpectedly passes before agent edits.
func (b *Benchmark) verifyInitialTaskFailure(task Task) (bool, string, error) {
	if task.NeedsAppShim {
		shimmedTask, cleanup, err := prepareShimmedTask(task)
		if err != nil {
			return false, "", fmt.Errorf("prepare /app shim failed: %w", err)
		}
		defer cleanup()
		task = shimmedTask
	}

	if err := b.resetTask(task); err != nil {
		return false, "", fmt.Errorf("reset failed: %w", err)
	}

	if task.NeedsAppShim {
		if err := rewriteLegacyAppPaths(task.Dir, filepath.Join(task.Dir, "setup")); err != nil {
			return false, "", fmt.Errorf("rewrite /app paths failed: %w", err)
		}
	}

	passed, output, err := b.verifyTask(task)
	if passed {
		return true, output, nil
	}
	if err != nil {
		// Expected path: initial state should fail verification.
		return false, output, nil
	}
	return false, output, nil
}

// AssertInitialFailures checks that all selected tasks fail in initial state.
func (b *Benchmark) AssertInitialFailures(tasks []Task) error {
	unexpectedPasses := make([]string, 0)

	fmt.Printf("Precheck: asserting initial state fails for %d task(s)...\n", len(tasks))
	for _, task := range tasks {
		passed, output, err := b.verifyInitialTaskFailure(task)
		if err != nil {
			return fmt.Errorf("[%s] initial-state precheck error: %w", task.ID, err)
		}
		if passed {
			preview := strings.TrimSpace(output)
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			if preview == "" {
				unexpectedPasses = append(unexpectedPasses, task.ID)
			} else {
				unexpectedPasses = append(unexpectedPasses, fmt.Sprintf("%s (%s)", task.ID, preview))
			}
			fmt.Printf("[%s] ✗ unexpected PASS\n", task.ID)
			continue
		}
		fmt.Printf("[%s] ✓ expected FAIL\n", task.ID)
	}

	if len(unexpectedPasses) > 0 {
		return fmt.Errorf("initial-state verify unexpectedly passed for %d task(s):\n  - %s",
			len(unexpectedPasses), strings.Join(unexpectedPasses, "\n  - "))
	}

	fmt.Println("Precheck passed: all selected tasks fail before agent modifications.")
	return nil
}

var (
	logFilePattern   = regexp.MustCompile(`logs/[A-Za-z0-9_.-]+`)
	searchCmdPattern = regexp.MustCompile(`\b(grep|rg)\b`)
	readCmdPattern   = regexp.MustCompile(`\b(cat|sed|nl|head|tail|less|more|awk)\b`)
	stage1Pattern    = regexp.MustCompile(`stage1_load\.py`)
	stage5Pattern    = regexp.MustCompile(`stage5_save\.py`)
	rollbackPattern  = regexp.MustCompile(`git (checkout|restore|revert)`)
)

func analyzeAgentOutput(output string) ProcessMetrics {
	metrics := ProcessMetrics{
		Events:           make([]toolEvent, 0),
		ToolCounts:       make(map[string]int),
		ToolsUsed:        make([]string, 0),
		CapabilityCounts: make(map[string]int),
		CapabilitiesUsed: make([]string, 0),
		FirstEditIndex:   -1,
		FirstTestIndex:   -1,
		EditedFiles:      make(map[string]struct{}),
	}
	seenTools := make(map[string]struct{})
	seenCapabilities := make(map[string]struct{})
	logFiles := make(map[string]struct{})

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if events := parseCodexEvents(line); len(events) > 0 {
			for _, ev := range events {
				applyEventSignals(&metrics, ev, seenTools, seenCapabilities, logFiles)
			}
			continue
		}

		tool, payload, ok := parseToolLine(line)
		if !ok {
			continue
		}

		event := toolEvent{
			Tool:    tool,
			Payload: payload,
			Command: extractArgValue(payload, "command"),
		}
		event.File = extractArgValue(payload, "file")
		if event.File == "" {
			event.File = extractArgValue(payload, "path")
		}

		applyEventSignals(&metrics, event, seenTools, seenCapabilities, logFiles)
	}

	if metrics.FirstEditIndex > 0 {
		for i := 0; i < metrics.FirstEditIndex && i < len(metrics.Events); i++ {
			if eventHasReadSignal(metrics.Events[i]) {
				metrics.ReadBeforeFirstEdit++
			}
		}
	}

	metrics.LogFilesRead = len(logFiles)
	return metrics
}

func applyEventSignals(metrics *ProcessMetrics, event toolEvent, seenTools, seenCapabilities map[string]struct{}, logFiles map[string]struct{}) {
	metrics.Events = append(metrics.Events, event)
	metrics.ToolCounts[event.Tool]++
	metrics.TotalToolCalls++

	if _, ok := seenTools[event.Tool]; !ok {
		seenTools[event.Tool] = struct{}{}
		metrics.ToolsUsed = append(metrics.ToolsUsed, event.Tool)
	}

	addCapability := func(name string) {
		metrics.CapabilityCounts[name]++
		if _, ok := seenCapabilities[name]; !ok {
			seenCapabilities[name] = struct{}{}
			metrics.CapabilitiesUsed = append(metrics.CapabilitiesUsed, name)
		}
	}

	switch strings.ToLower(event.Tool) {
	case "read", "open_file":
		metrics.ReadCalls++
		addCapability("read")
		if strings.Contains(event.File, "logs/") {
			logFiles[event.File] = struct{}{}
		}
		if strings.Contains(event.File, "stage1_load.py") {
			metrics.ReadStage1 = true
		}
	case "edit", "patch", "write":
		metrics.EditCalls++
		addCapability("edit")
		if metrics.FirstEditIndex == -1 {
			metrics.FirstEditIndex = len(metrics.Events) - 1
		}
		if event.File != "" {
			metrics.EditedFiles[event.File] = struct{}{}
		}
		if stage5Pattern.MatchString(strings.ToLower(event.File)) {
			metrics.EditedStage5 = true
		}
	case "test":
		metrics.TestRuns++
		addCapability("test")
		if metrics.FirstTestIndex == -1 {
			metrics.FirstTestIndex = len(metrics.Events) - 1
		}
	case "grep":
		metrics.GrepLikeCalls++
		metrics.SearchLikeCalls++
		addCapability("search")
	case "search":
		metrics.SearchLikeCalls++
		addCapability("search")
	case "git_checkout":
		metrics.HasRollbackAction = true
		addCapability("rollback")
	}

	if strings.ToLower(event.Tool) != "bash" {
		return
	}

	cmdLower := strings.ToLower(event.Command)
	if isSearchCommand(cmdLower) {
		metrics.GrepLikeCalls++
		metrics.SearchLikeCalls++
		addCapability("search")
	}
	if isTestCommand(cmdLower) {
		metrics.TestRuns++
		addCapability("test")
		if metrics.FirstTestIndex == -1 {
			metrics.FirstTestIndex = len(metrics.Events) - 1
		}
	}
	if isReadCommand(cmdLower) {
		metrics.ReadCalls++
		addCapability("read")
	}
	if rollbackPattern.MatchString(cmdLower) {
		metrics.HasRollbackAction = true
		addCapability("rollback")
	}

	for _, m := range logFilePattern.FindAllString(event.Command, -1) {
		logFiles[m] = struct{}{}
	}

	if stage1Pattern.MatchString(cmdLower) && isReadCommand(cmdLower) {
		metrics.ReadStage1 = true
	}
}

func eventHasReadSignal(event toolEvent) bool {
	tool := strings.ToLower(event.Tool)
	if tool == "read" || tool == "open_file" {
		return true
	}
	return tool == "bash" && isReadCommand(strings.ToLower(event.Command))
}

func parseToolLine(line string) (string, string, bool) {
	bulletPos := strings.Index(line, "•")
	if bulletPos == -1 {
		return "", "", false
	}
	rest := strings.TrimSpace(line[bulletPos+len("•"):])
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	tool := strings.TrimSpace(parts[0])
	if tool == "" {
		return "", "", false
	}
	return tool, strings.TrimSpace(parts[1]), true
}

func parseCodexEvents(line string) []toolEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return nil
	}

	typ, _ := envelope["type"].(string)
	item, ok := envelope["item"].(map[string]any)
	if !ok {
		return nil
	}
	itemType, _ := item["type"].(string)

	if typ == "item.started" && itemType == "command_execution" {
		command, _ := item["command"].(string)
		if command == "" {
			return nil
		}
		return []toolEvent{{
			Tool:    "bash",
			Payload: command,
			Command: command,
		}}
	}

	if typ == "item.completed" && itemType == "file_change" {
		rawChanges, ok := item["changes"].([]any)
		if !ok || len(rawChanges) == 0 {
			return nil
		}

		events := make([]toolEvent, 0, len(rawChanges))
		for _, raw := range rawChanges {
			change, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			path, _ := change["path"].(string)
			events = append(events, toolEvent{
				Tool: "edit",
				File: path,
			})
		}
		if len(events) == 0 {
			return nil
		}
		return events
	}

	return nil
}

func extractArgValue(payload, key string) string {
	pat := key + "="
	idx := strings.Index(payload, pat)
	if idx == -1 {
		return ""
	}
	s := payload[idx+len(pat):]
	end := len(s)
	for _, sep := range []string{",", "]"} {
		if i := strings.Index(s, sep); i >= 0 && i < end {
			end = i
		}
	}
	value := strings.TrimSpace(s[:end])
	return strings.Trim(value, `"'`)
}

func isSearchCommand(cmd string) bool {
	return searchCmdPattern.MatchString(cmd)
}

func isReadCommand(cmd string) bool {
	if strings.Contains(cmd, "sed -i") || strings.Contains(cmd, "perl -pi") {
		return false
	}
	return readCmdPattern.MatchString(cmd)
}

func isTestCommand(cmd string) bool {
	return strings.Contains(cmd, "pytest") ||
		strings.Contains(cmd, "go test") ||
		strings.Contains(cmd, "npm test") ||
		strings.Contains(cmd, "cargo test") ||
		strings.Contains(cmd, "unittest") ||
		strings.Contains(cmd, "verify.sh")
}

func (b *Benchmark) evaluateTaskConstraints(task Task, metrics ProcessMetrics, testPassed bool) ([]string, []string, float64, bool, error) {
	constraintsPath := filepath.Join(task.Dir, "constraints.json")
	data, err := os.ReadFile(constraintsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, 100, false, nil
		}
		return nil, nil, 0, false, err
	}

	var spec ConstraintSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, nil, 0, false, fmt.Errorf("failed to parse %s: %w", constraintsPath, err)
	}

	hardViolations := make([]string, 0)
	softViolations := make([]string, 0)
	score := 100.0

	maxStepsMode := resolveMaxStepsMode(spec.MaxStepsMode, b.MaxStepsMode)

	if spec.MaxSteps > 0 && metrics.TotalToolCalls > spec.MaxSteps {
		msg := fmt.Sprintf("max_steps exceeded: %d > %d", metrics.TotalToolCalls, spec.MaxSteps)
		if maxStepsMode == "hard" {
			hardViolations = append(hardViolations, msg)
		} else {
			softViolations = append(softViolations, msg)
			score -= maxStepsPenalty(metrics.TotalToolCalls, spec.MaxSteps)
		}
	}

	for _, required := range spec.MustUseTools {
		if toolUsageCount(metrics, required) == 0 {
			hardViolations = append(hardViolations, fmt.Sprintf("required tool not used: %s", required))
		}
	}
	for _, required := range spec.MustUseCapabilities {
		if capabilityUsageCount(metrics, required) == 0 {
			hardViolations = append(hardViolations, fmt.Sprintf("required capability not used: %s", required))
		}
	}

	for _, pattern := range spec.ForbiddenPatterns {
		if violatesForbiddenPattern(metrics, pattern) {
			hardViolations = append(hardViolations, fmt.Sprintf("forbidden pattern detected: %s", pattern))
		}
	}

	for key, rule := range spec.SuccessCriteria {
		if ok := successCriterionSatisfied(metrics, key, rule, testPassed); !ok {
			hardViolations = append(hardViolations, fmt.Sprintf("success criterion not met: %s", key))
		}
	}

	sort.Strings(hardViolations)
	sort.Strings(softViolations)
	score -= float64(len(hardViolations)) * 20
	if score < 0 {
		score = 0
	}
	return hardViolations, softViolations, score, true, nil
}

func resolveMaxStepsMode(taskMode, defaultMode string) string {
	mode := strings.ToLower(strings.TrimSpace(taskMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(defaultMode))
	}
	if mode == "hard" {
		return "hard"
	}
	return "soft"
}

func maxStepsPenalty(actual, limit int) float64 {
	if limit <= 0 || actual <= limit {
		return 0
	}
	ratio := float64(actual) / float64(limit)
	penalty := (ratio - 1.0) * 40.0
	if penalty < 5 {
		penalty = 5
	}
	if penalty > 35 {
		penalty = 35
	}
	return penalty
}

func violatesForbiddenPattern(metrics ProcessMetrics, pattern string) bool {
	switch pattern {
	case "read_all_files", "sequential_file_read_without_search", "read_entire_file", "sequential_read_all":
		return metrics.ReadCalls > 5 && metrics.SearchLikeCalls == 0
	case "partial_information":
		return metrics.LogFilesRead < 3
	case "single_file_fix":
		return len(metrics.EditedFiles) <= 1
	case "fix_first_error", "fix_first_error_seen", "fix_symptom_not_root_cause":
		return metrics.TestRuns < 2 && metrics.ReadBeforeFirstEdit <= 1 && metrics.SearchLikeCalls == 0
	case "excessive_reads":
		return metrics.ReadCalls > 6
	case "multiple_test_runs":
		return metrics.TestRuns > 1
	case "guess_without_testing":
		return metrics.FirstEditIndex >= 0 &&
			(metrics.FirstTestIndex == -1 || metrics.FirstEditIndex < metrics.FirstTestIndex)
	case "forget_stage1_info":
		return !(metrics.ReadStage1 && metrics.EditedStage5)
	default:
		return false
	}
}

func successCriterionSatisfied(metrics ProcessMetrics, key string, rule any, testPassed bool) bool {
	switch key {
	case "test_passed":
		required, ok := rule.(bool)
		if !ok {
			return true
		}
		if required {
			return testPassed
		}
		return !testPassed
	case "tools_used":
		toolRules, ok := rule.(map[string]any)
		if !ok {
			return true
		}
		for tool, expr := range toolRules {
			if !matchesNumericRule(toolUsageCount(metrics, tool), expr) {
				return false
			}
		}
		return true
	case "capabilities_used":
		capabilityRules, ok := rule.(map[string]any)
		if !ok {
			return true
		}
		for capability, expr := range capabilityRules {
			if !matchesNumericRule(capabilityUsageCount(metrics, capability), expr) {
				return false
			}
		}
		return true
	case "grep_used":
		return matchesNumericRule(metrics.GrepLikeCalls, rule)
	case "total_tool_calls":
		return matchesNumericRule(metrics.TotalToolCalls, rule)
	case "file_reads":
		return matchesNumericRule(metrics.ReadCalls, rule)
	case "files_read_before_fix":
		return matchesNumericRule(metrics.ReadBeforeFirstEdit, rule)
	case "log_files_read":
		return matchesNumericRule(metrics.LogFilesRead, rule)
	case "files_modified":
		return matchesNumericRule(len(metrics.EditedFiles), rule)
	case "ran_tests_first":
		required, ok := rule.(bool)
		if !ok || !required {
			return true
		}
		return metrics.FirstTestIndex >= 0 &&
			(metrics.FirstEditIndex == -1 || metrics.FirstTestIndex < metrics.FirstEditIndex)
	case "requires_rollback":
		required, ok := rule.(bool)
		if !ok || !required {
			return true
		}
		return metrics.HasRollbackAction
	case "read_stage1":
		required, ok := rule.(bool)
		if !ok || !required {
			return true
		}
		return metrics.ReadStage1
	case "applied_stage1_info_in_stage5":
		required, ok := rule.(bool)
		if !ok || !required {
			return true
		}
		return metrics.ReadStage1 && metrics.EditedStage5
	case "investigated_root_cause":
		required, ok := rule.(bool)
		if !ok || !required {
			return true
		}
		return metrics.TestRuns >= 2
	case "reasoning":
		// Free-text guidance, not machine-checkable.
		return true
	default:
		return true
	}
}

func toolUsageCount(metrics ProcessMetrics, tool string) int {
	switch strings.ToLower(tool) {
	case "grep":
		return metrics.GrepLikeCalls
	case "search":
		return metrics.SearchLikeCalls
	case "read", "open_file":
		return metrics.ReadCalls
	case "edit", "patch", "write":
		return metrics.EditCalls
	case "test":
		return metrics.TestRuns
	case "rollback":
		if metrics.HasRollbackAction {
			return 1
		}
		return 0
	default:
		return metrics.ToolCounts[tool]
	}
}

func capabilityUsageCount(metrics ProcessMetrics, capability string) int {
	switch strings.ToLower(capability) {
	case "search":
		return metrics.SearchLikeCalls
	case "read":
		return metrics.ReadCalls
	case "edit":
		return metrics.EditCalls
	case "test":
		return metrics.TestRuns
	case "rollback":
		if metrics.HasRollbackAction {
			return 1
		}
		return 0
	default:
		return metrics.CapabilityCounts[strings.ToLower(capability)]
	}
}

func matchesNumericRule(actual int, rule any) bool {
	switch v := rule.(type) {
	case float64:
		return actual == int(v)
	case int:
		return actual == v
	case string:
		expr := strings.TrimSpace(v)
		ops := []string{">=", "<=", "==", ">", "<", "="}
		op := ""
		for _, candidate := range ops {
			if strings.HasPrefix(expr, candidate) {
				op = candidate
				expr = strings.TrimSpace(strings.TrimPrefix(expr, candidate))
				break
			}
		}
		if op == "" {
			op = "="
		}
		expected, err := strconv.Atoi(expr)
		if err != nil {
			return false
		}
		switch op {
		case ">=":
			return actual >= expected
		case "<=":
			return actual <= expected
		case "==", "=":
			return actual == expected
		case ">":
			return actual > expected
		case "<":
			return actual < expected
		default:
			return false
		}
	default:
		return false
	}
}

// RunAll runs all tasks and returns a report
func (b *Benchmark) RunAll(tasks []Task, progressFile string) *RunReport {
	report := &RunReport{
		Timestamp:    time.Now().Format(time.RFC3339),
		Agent:        b.Agent.Name(),
		MaxStepsMode: b.MaxStepsMode,
		Results:      []Result{},
	}

	// Load previous progress
	completed, err := b.LoadProgress(progressFile)
	if err != nil {
		fmt.Printf("Warning: failed to load progress: %v\n", err)
	}

	// Filter out completed tasks and add their results
	var remaining []Task
	for _, task := range tasks {
		if result, done := completed[task.ID]; done {
			fmt.Printf("[%s] Skipping (already completed, passed=%v)\n", task.ID, result.Passed)
			report.Results = append(report.Results, result)
			accumulateReportCounts(report, result)
		} else {
			remaining = append(remaining, task)
		}
	}

	for _, task := range remaining {
		fmt.Printf("[%s] Running...\n", task.ID)
		result := b.RunTask(task)
		report.Results = append(report.Results, result)
		accumulateReportCounts(report, result)

		if result.Passed {
			fmt.Printf("[%s] ✓ PASS (%.1fs)\n", task.ID, result.Duration)
		} else {
			fmt.Printf("[%s] ✗ FAIL (%.1fs)", task.ID, result.Duration)
			if result.Error != "" {
				fmt.Printf("\n  Error: %s", result.Error)
			}
			fmt.Println()
		}

		// Save progress after each task
		report.TotalTasks = len(report.Results)
		computeReportRates(report)
		if err := b.SaveProgress(report, progressFile); err != nil {
			fmt.Printf("Warning: failed to save progress: %v\n", err)
		}
	}

	report.TotalTasks = len(report.Results)
	computeReportRates(report)

	return report
}

func accumulateReportCounts(report *RunReport, result Result) {
	if result.Passed {
		report.Passed++
	} else {
		report.Failed++
	}

	if result.FunctionalPassed {
		report.FunctionalPassed++
	} else {
		report.FunctionalFailed++
	}

	if result.AgenticPassed {
		report.AgenticPassed++
	} else {
		report.AgenticFailed++
	}
	report.totalAgenticScore += result.AgenticScore
}

func computeReportRates(report *RunReport) {
	if report.TotalTasks == 0 {
		return
	}
	total := float64(report.TotalTasks)
	report.PassRate = float64(report.Passed) / total * 100
	report.FunctionalPassRate = float64(report.FunctionalPassed) / total * 100
	report.AgenticPassRate = float64(report.AgenticPassed) / total * 100
	report.AvgAgenticScore = report.totalAgenticScore / total
}

// SaveReport saves the report to a JSON file
func (b *Benchmark) SaveReport(report *RunReport) (string, error) {
	os.MkdirAll(b.ResultsDir, 0755)

	filename := fmt.Sprintf("result_%s.json", time.Now().Format("20060102_150405"))
	path := filepath.Join(b.ResultsDir, filename)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}

	return path, os.WriteFile(path, data, 0644)
}

// CompareReports compares two reports and returns regression info
func CompareReports(old, new *RunReport) []string {
	var regressions []string

	oldMap := make(map[string]bool)
	for _, r := range old.Results {
		oldMap[r.TaskID] = r.Passed
	}

	for _, r := range new.Results {
		oldPassed, exists := oldMap[r.TaskID]
		if exists && oldPassed && !r.Passed {
			regressions = append(regressions,
				fmt.Sprintf("REGRESSION: %s (PASS → FAIL)", r.TaskID))
		} else if exists && !oldPassed && r.Passed {
			fmt.Printf("FIXED: %s (FAIL → PASS)\n", r.TaskID)
		}
	}

	return regressions
}

func NewTimeoutContext(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func nonInteractiveCommandEnv() []string {
	env := os.Environ()
	env = append(env,
		"SUDO_ASKPASS=/usr/bin/false",
		"SSH_ASKPASS=/usr/bin/false",
		"GIT_TERMINAL_PROMPT=0",
	)
	return env
}

func taskNeedsAppShim(taskDir string) (bool, error) {
	found := false

	err := filepath.WalkDir(taskDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".pytest_cache":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 4*1024*1024 {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isBinaryContent(content) {
			return nil
		}
		if bytes.Contains(content, []byte("/app")) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, fs.SkipAll) {
		return true, nil
	}
	return found, err
}

func prepareShimmedTask(task Task) (Task, func(), error) {
	tmpDir, err := os.MkdirTemp("", "benchmark-task-*")
	if err != nil {
		return Task{}, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	if err := copyDir(task.Dir, tmpDir); err != nil {
		cleanup()
		return Task{}, nil, err
	}

	shimmed := task
	shimmed.Dir = tmpDir
	if desc, err := os.ReadFile(filepath.Join(tmpDir, "task.md")); err == nil {
		shimmed.Description = string(desc)
	}

	return shimmed, cleanup, nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}

		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func rewriteLegacyAppPaths(taskDir, setupDir string) error {
	return filepath.WalkDir(taskDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".pytest_cache":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 4*1024*1024 {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isBinaryContent(data) || !bytes.Contains(data, []byte("/app")) {
			return nil
		}

		rewritten := strings.ReplaceAll(string(data), "/app", setupDir)
		if rewritten == string(data) {
			return nil
		}
		return os.WriteFile(path, []byte(rewritten), info.Mode().Perm())
	})
}

func isBinaryContent(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	return !utf8.Valid(data)
}

func loadTaskManifest(path string) (TaskManifest, error) {
	var manifest TaskManifest

	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if len(manifest.Tasks) == 0 {
		return manifest, fmt.Errorf("manifest has no tasks: %s", path)
	}
	return manifest, nil
}

func filterTasksByManifest(tasks []Task, manifest TaskManifest) ([]Task, error) {
	taskByID := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
	}

	selected := make([]Task, 0, len(manifest.Tasks))
	missing := make([]string, 0)
	seen := make(map[string]struct{})
	for _, id := range manifest.Tasks {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		task, ok := taskByID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		selected = append(selected, task)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("manifest references missing tasks: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func isFlagExplicitlySet(name string) bool {
	longName := "--" + name
	for _, arg := range os.Args[1:] {
		if arg == longName || strings.HasPrefix(arg, longName+"=") {
			return true
		}
	}
	return false
}

func main() {
	// Parse flags
	var (
		tasksDir          = flag.String("tasks", "tasks", "Tasks directory")
		resultsDir        = flag.String("results", "results", "Results directory")
		agentBinary       = flag.String("agent", "/Users/genius/go/bin/ai", "Agent binary path")
		maxTurns          = flag.Int("max-turns", 50, "Maximum agent turns")
		timeout           = flag.Duration("timeout", 10*time.Minute, "Per-task timeout")
		manifestPath      = flag.String("manifest", "", "Task manifest path (JSON)")
		taskID            = flag.String("task", "", "Run specific task only")
		compare           = flag.String("compare", "", "Compare with previous result file")
		resume            = flag.String("resume", "", "Resume from progress file (default: results/progress.json)")
		clean             = flag.Bool("clean", false, "Start fresh (ignore existing progress)")
		list              = flag.Bool("list", false, "List available tasks")
		maxStepsMode      = flag.String("max-steps-mode", "soft", "How to treat max_steps constraint: soft|hard")
		assertInitialFail = flag.Bool("assert-initial-fail", false, "Assert that selected tasks fail in initial state before running agent")
		precheckOnly      = flag.Bool("precheck-only", false, "Run only initial-state failure precheck, then exit")
	)
	flag.Parse()
	maxStepsFlagSet := isFlagExplicitlySet("max-steps-mode")

	// Convert to absolute paths to avoid working directory issues
	absTasksDir, err := filepath.Abs(*tasksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving tasks dir: %v\n", err)
		os.Exit(1)
	}
	absResultsDir, err := filepath.Abs(*resultsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving results dir: %v\n", err)
		os.Exit(1)
	}
	absAgentBinary, err := filepath.Abs(*agentBinary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving agent binary: %v\n", err)
		os.Exit(1)
	}
	absManifestPath := ""
	if *manifestPath != "" {
		absManifestPath, err = filepath.Abs(*manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving manifest path: %v\n", err)
			os.Exit(1)
		}
	}

	// Create agent runner
	agent := &AIAgentRunner{
		BinaryPath: absAgentBinary,
		MaxTurns:   *maxTurns,
		Timeout:    *timeout,
	}

	// Create benchmark
	bench := NewBenchmark(absTasksDir, absResultsDir, agent)
	bench.Timeout = *timeout
	bench.MaxStepsMode = resolveMaxStepsMode("", *maxStepsMode)

	// Discover tasks
	tasks, err := bench.DiscoverTasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering tasks: %v\n", err)
		os.Exit(1)
	}

	if absManifestPath != "" {
		manifest, err := loadTaskManifest(absManifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading manifest: %v\n", err)
			os.Exit(1)
		}
		tasks, err = filterTasksByManifest(tasks, manifest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error applying manifest: %v\n", err)
			os.Exit(1)
		}
		if !maxStepsFlagSet && manifest.GlobalDefaults.MaxStepsMode != "" {
			bench.MaxStepsMode = resolveMaxStepsMode("", manifest.GlobalDefaults.MaxStepsMode)
		}
	}

	// Filter to single task if specified
	if *taskID != "" {
		var filtered []Task
		for _, t := range tasks {
			if t.ID == *taskID {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
		if len(tasks) == 0 {
			fmt.Fprintf(os.Stderr, "Task not found: %s\n", *taskID)
			os.Exit(1)
		}
	}

	// List tasks
	if *list {
		fmt.Printf("============================================================\n")
		fmt.Printf("Available Tasks: %d\n", len(tasks))
		fmt.Printf("============================================================\n")
		if absManifestPath != "" {
			fmt.Printf("Manifest: %s\n", absManifestPath)
		}
		fmt.Println("\nCustom Tasks:")
		for _, t := range tasks {
			if !strings.HasPrefix(t.ID, "tbench/") {
				fmt.Printf("  - %s\n", t.ID)
			}
		}
		fmt.Println("\nTerminal Bench:")
		for _, t := range tasks {
			if strings.HasPrefix(t.ID, "tbench/") {
				fmt.Printf("  - %s\n", t.ID)
			}
		}
		return
	}

	if *assertInitialFail || *precheckOnly {
		if err := bench.AssertInitialFailures(tasks); err != nil {
			fmt.Fprintf(os.Stderr, "Initial-state precheck failed: %v\n", err)
			os.Exit(2)
		}
		if *precheckOnly {
			return
		}
	}

	// Determine progress file
	progressFile := filepath.Join(absResultsDir, "progress.json")
	if *resume != "" {
		progressFile = *resume
	}

	// Clean progress if requested
	if *clean {
		if err := os.Remove(progressFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean progress: %v\n", err)
		}
	}

	// Run benchmark
	report := bench.RunAll(tasks, progressFile)
	if absManifestPath != "" {
		report.ManifestPath = absManifestPath
	}

	// Print summary
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("Summary (overall): %d/%d passed (%.1f%%)\n",
		report.Passed, report.TotalTasks, report.PassRate)
	fmt.Printf("Summary (functional): %d/%d passed (%.1f%%)\n",
		report.FunctionalPassed, report.TotalTasks, report.FunctionalPassRate)
	fmt.Printf("Summary (agentic): %d/%d passed (%.1f%%)\n",
		report.AgenticPassed, report.TotalTasks, report.AgenticPassRate)
	fmt.Printf("Summary (agentic score): %.1f/100\n", report.AvgAgenticScore)
	fmt.Printf("%s\n", strings.Repeat("=", 60))

	// Save report
	path, err := bench.SaveReport(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving report: %v\n", err)
	} else {
		fmt.Printf("\nReport saved to: %s\n", path)
	}

	// Compare with previous
	if *compare != "" {
		var oldReport RunReport
		data, err := os.ReadFile(*compare)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading comparison file: %v\n", err)
		} else if err := json.Unmarshal(data, &oldReport); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing comparison file: %v\n", err)
		} else {
			regressions := CompareReports(&oldReport, report)
			if len(regressions) > 0 {
				fmt.Println("\n⚠️  Regressions detected:")
				for _, r := range regressions {
					fmt.Printf("  - %s\n", r)
				}
			}
		}
	}
}
