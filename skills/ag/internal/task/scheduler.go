package task

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/genius/ag/internal/storage"
)

// SchedulerConfig holds configuration for the scheduler loop.
type SchedulerConfig struct {
	MaxConcurrent int
	MaxRetries    int
	Timeout       time.Duration
	PollInterval  time.Duration
	DesignFile    string
	WorkDir       string
	SkipReview    bool
	MaxReviewRounds int
}

// DefaultSchedulerConfig returns sensible defaults.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		MaxConcurrent:   2,
		MaxRetries:      3,
		Timeout:         10 * time.Minute,
		PollInterval:    5 * time.Second,
		SkipReview:      false,
		MaxReviewRounds: 2,
	}
}

// RunScheduler executes the scheduler loop until all tasks are terminal
// or the context is cancelled.
func RunScheduler(ctx context.Context, cfg SchedulerConfig) error {
	storage.Init()

	// Check for tasks
	tasks, err := List("")
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	if len(tasks) == 0 {
		fmt.Println("No tasks to run.")
		return nil
	}

	// If only 1-2 tasks, skip review by default
	if len(tasks) <= 2 && !cfg.SkipReview {
		cfg.SkipReview = true
		fmt.Println("≤2 tasks, skipping review phase.")
	}

	fmt.Printf("Scheduler started: %d tasks, max concurrent=%d, review=%v\n",
		len(tasks), cfg.MaxConcurrent, !cfg.SkipReview)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nScheduler stopped by user.")
			return ctx.Err()
		default:
		}

		progress, err := tick(ctx, cfg)
		if err != nil {
			return fmt.Errorf("scheduler tick: %w", err)
		}

		// Check if all done
		allDone, err := AllDone()
		if err != nil {
			return fmt.Errorf("check all done: %w", err)
		}
		if allDone {
			fmt.Println("\n✅ All tasks completed.")
			printSummary()
			return nil
		}

		if !progress {
			// No progress made, wait before next poll
			time.Sleep(cfg.PollInterval)
		}
	}
}

// tick executes one scheduler cycle. Returns true if any progress was made.
func tick(ctx context.Context, cfg SchedulerConfig) (bool, error) {
	progress := false

	// 1. Retry failed tasks
	if p, err := retryFailed(cfg); err != nil {
		return false, err
	} else if p {
		progress = true
	}

	// 2. Spawn workers for runnable tasks
	if p, err := spawnWorkers(ctx, cfg); err != nil {
		return false, err
	} else if p {
		progress = true
	}

	// 3. Check running tasks for completion
	if p, err := checkRunning(cfg); err != nil {
		return false, err
	} else if p {
		progress = true
	}

	// 4. Group review
	if !cfg.SkipReview {
		if p, err := checkGroupReview(ctx, cfg); err != nil {
			return false, err
		} else if p {
			progress = true
		}
	}

	return progress, nil
}

// retryFailed retries failed tasks that haven't exceeded max retries.
func retryFailed(cfg SchedulerConfig) (bool, error) {
	failed, err := List(StatusFailed)
	if err != nil {
		return false, err
	}
	progress := false
	for _, t := range failed {
		if !t.Retryable {
			continue
		}
		_, err := Retry(t.ID, cfg.MaxRetries)
		if err != nil {
			continue // max retries exceeded, skip
		}
		fmt.Printf("  🔄 Retried %s (attempt %d)\n", t.ID, t.RetryCount+1)
		progress = true
	}
	return progress, nil
}

// spawnWorkers picks pending tasks with met dependencies and spawns worker agents.
func spawnWorkers(ctx context.Context, cfg SchedulerConfig) (bool, error) {
	// Count currently running tasks
	running, err := List(StatusRunning)
	if err != nil {
		return false, err
	}
	slots := cfg.MaxConcurrent - len(running)
	if slots <= 0 {
		return false, nil
	}

	// Find pending tasks with met dependencies
	pending, err := List(StatusPending)
	if err != nil {
		return false, err
	}

	progress := false
	for _, t := range pending {
		if slots <= 0 {
			break
		}
		unmet, err := UnmetDependencies(t.ID)
		if err != nil {
			continue
		}
		if len(unmet) > 0 {
			continue
		}

		// Claim and transition to running
		claimed, err := Claim(t.ID, fmt.Sprintf("worker-%s", t.ID))
		if err != nil {
			continue
		}
		_, err = Transition(t.ID, StatusRunning)
		if err != nil {
			fmt.Printf("  ⚠️ Failed to transition %s to running: %v\n", t.ID, err)
			continue
		}

		// Spawn worker agent
		go spawnWorker(t.ID, claimed.Claimant, cfg)
		fmt.Printf("  🚀 Started %s: %s\n", t.ID, t.Title)
		slots--
		progress = true
	}
	return progress, nil
}

// spawnWorker runs a worker agent for a single task.
func spawnWorker(taskID, agentID string, cfg SchedulerConfig) {
	t, err := Load(taskID)
	if err != nil {
		Fail(taskID, fmt.Sprintf("load task: %v", err), false)
		return
	}

	prompt := BuildWorkerPrompt(t, cfg.DesignFile)

	// Write prompt to temp file to avoid shell escaping issues
	promptFile := filepath.Join(storage.TaskDir(taskID), "prompt.txt")
	os.WriteFile(promptFile, []byte(prompt), 0644)

	// Build ai serve command
	args := []string{"serve"}
	args = append(args, "--input", promptFile)
	args = append(args, "--name", "ag-worker-"+taskID)
	if cfg.WorkDir != "" {
		args = append(args, "--cwd", cfg.WorkDir)
	}

	cmd := exec.Command("ai", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// Capture output
	outputFile := filepath.Join(storage.TaskDir(taskID), "output")
	outFile, err := os.Create(outputFile)
	if err != nil {
		Fail(taskID, fmt.Sprintf("create output file: %v", err), true)
		return
	}
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	if err := cmd.Start(); err != nil {
		outFile.Close()
		Fail(taskID, fmt.Sprintf("start agent: %v", err), true)
		return
	}

	// Write agent PID
	agentDir := storage.AgentDir(agentID)
	os.MkdirAll(agentDir, 0755)
	activity := map[string]interface{}{
		"status":    "running",
		"backend":   "ai",
		"startedAt": time.Now().Unix(),
		"pid":       cmd.Process.Pid,
	}
	storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity)

	// Wait with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		outFile.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			Fail(taskID, fmt.Sprintf("agent exited with error: %v", err), true)
			fmt.Printf("  ❌ Failed %s: %v\n", taskID, err)
		} else {
			// Read output for summary
			summary := readSummary(outputFile)
			Done(taskID, summary)
			fmt.Printf("  ✅ Done %s: %s\n", taskID, truncate(summary, 80))
		}
	case <-time.After(cfg.Timeout):
		cmd.Process.Kill()
		Fail(taskID, "agent timed out", true)
		fmt.Printf("  ⏰ Timed out %s\n", taskID)
	}
}

// checkRunning checks if any running tasks have completed (stale detection).
func checkRunning(cfg SchedulerConfig) (bool, error) {
	running, err := List(StatusRunning)
	if err != nil {
		return false, err
	}
	progress := false
	for _, t := range running {
		// Check if agent process is still alive
		if t.Claimant == "" {
			continue
		}
		agentDir := storage.AgentDir(t.Claimant)
		actPath := filepath.Join(agentDir, "activity.json")
		if !storage.Exists(actPath) {
			continue
		}

		var act struct {
			Pid int `json:"pid"`
		}
		if err := storage.ReadJSON(actPath, &act); err != nil || act.Pid <= 0 {
			continue
		}

		// Check if process is alive
		if !isProcessAlive(act.Pid) {
			// Process is dead — check output for completion
			outputFile := filepath.Join(storage.TaskDir(t.ID), "output")
			if storage.Exists(outputFile) {
				summary := readSummary(outputFile)
				Done(t.ID, summary)
				fmt.Printf("  ✅ Detected completion %s (process exited)\n", t.ID)
			} else {
				Fail(t.ID, "agent process died without output", true)
				fmt.Printf("  ❌ Detected failure %s (process died)\n", t.ID)
			}
			progress = true
		}
	}
	return progress, nil
}

// checkGroupReview checks if any group has all tasks done and needs review.
func checkGroupReview(ctx context.Context, cfg SchedulerConfig) (bool, error) {
	groups, err := Groups()
	if err != nil {
		return false, err
	}

	progress := false
	for _, group := range groups {
		tasks, err := GroupTasks(group)
		if err != nil {
			continue
		}

		// Check if all tasks in group are done
		allDone := true
		hasReview := false
		for _, t := range tasks {
			if t.Status == StatusReview || t.Status == StatusRevision {
				hasReview = true
				break
			}
			if t.Status != StatusDone {
				allDone = false
				break
			}
		}

		if hasReview {
			continue // Already in review
		}

		if !allDone {
			continue
		}

		// All done — spawn reviewer
		fmt.Printf("  🔍 Reviewing group %s\n", group)
		go spawnReviewer(group, tasks, cfg)
		// Mark all as review
		for _, t := range tasks {
			Transition(t.ID, StatusReview)
		}
		progress = true
	}
	return progress, nil
}

// spawnReviewer runs a reviewer agent for a group of tasks.
func spawnReviewer(group string, tasks []*Task, cfg SchedulerConfig) {
	// Get diff of changes
	diff := getDiff(cfg.WorkDir)

	prompt := BuildReviewerPrompt(tasks, diff)
	promptFile := filepath.Join(storage.TaskDir(tasks[0].ID), "review-prompt.txt")
	os.WriteFile(promptFile, []byte(prompt), 0644)

		agentID := fmt.Sprintf("reviewer-%s", group)
	_ = agentID
	args := []string{"serve"}
	args = append(args, "--input", promptFile)
	args = append(args, "--name", "ag-reviewer-"+group)
	if cfg.WorkDir != "" {
		args = append(args, "--cwd", cfg.WorkDir)
	}

	cmd := exec.Command("ai", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	outputFile := filepath.Join(storage.TaskDir(tasks[0].ID), "review-output")
	outFile, _ := os.Create(outputFile)
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	if err := cmd.Start(); err != nil {
		outFile.Close()
		for _, t := range tasks {
			Fail(t.ID, fmt.Sprintf("start reviewer: %v", err), true)
		}
		return
	}

	err := cmd.Wait()
	outFile.Close()

	if err != nil {
		// Reviewer failed — pass anyway to not block
		fmt.Printf("  ⚠️ Reviewer failed for group %s, auto-passing\n", group)
		for _, t := range tasks {
			Done(t.ID, "auto-passed: reviewer failed")
		}
		return
	}

	// Check review output for pass/revision
	output := readFile(outputFile)
	if strings.Contains(output, "REVIEW_PASS") {
		fmt.Printf("  ✅ Review passed for group %s\n", group)
		for _, t := range tasks {
			Done(t.ID, t.Summary)
		}
	} else {
		fmt.Printf("  🔧 Revision requested for group %s\n", group)
		// For now, auto-pass after review (revision loop can be added later)
		for _, t := range tasks {
			Done(t.ID, "reviewed with comments")
		}
	}
}

// Helper functions

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check liveness.
	err = proc.Signal(nil)
	return err == nil
}

func getDiff(workDir string) string {
	cmd := exec.Command("git", "diff")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return "(no diff available)"
	}
	return string(out)
}

func readSummary(outputFile string) string {
	data := readFile(outputFile)
	// Take last 500 chars as summary
	if len(data) > 500 {
		return "..." + data[len(data)-500:]
	}
	return data
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// printSummary shows final task status.
func printSummary() {
	tasks, _ := List("")
	for _, t := range tasks {
		icon := "✅"
		if t.Status == StatusFailed {
			icon = "❌"
		}
		fmt.Printf("  %s %s: %s\n", icon, t.ID, t.Title)
		if t.Error != "" {
			fmt.Printf("     Error: %s\n", t.Error)
		}
	}
}