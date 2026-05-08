package task

import (
	"bufio"
	"context"
			"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/conv"
	"github.com/genius/ag/internal/run"
	"github.com/genius/ag/internal/storage"
)

// SchedulerConfig holds configuration for the scheduler loop.
type SchedulerConfig struct {
	MaxConcurrent   int
	MaxRetries      int
	Timeout         time.Duration
	PollInterval    time.Duration
	DesignFile      string
	WorkDir         string
	SkipReview      bool
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

	// Spawn agent using "ai serve" — same pattern as aiAdapter.SpawnWithAIServe.
	// Key: --input receives the prompt TEXT, not a file path.
	// We read stdout to get the run ID, then Release the process.
	cmd := exec.Command("ai", "serve")
	cmd.Args = append(cmd.Args, "--input", prompt)
	cmd.Args = append(cmd.Args, "--name", "ag-worker-"+taskID)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture run ID from stdout (ai serve prints run ID as first line)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		Fail(taskID, fmt.Sprintf("create stdout pipe: %v", err), true)
		return
	}

	// Redirect stderr to output file for debugging
	outputFile := filepath.Join(storage.TaskDir(taskID), "output")
	outFile, err := os.Create(outputFile)
	if err != nil {
		Fail(taskID, fmt.Sprintf("create output file: %v", err), true)
		return
	}
	cmd.Stderr = outFile

		if err := cmd.Start(); err != nil {
		outFile.Close()
		Fail(taskID, fmt.Sprintf("start agent: %v", err), true)
		return
	}

	fmt.Fprintf(os.Stderr, "  [debug] spawnWorker %s: pid=%d, waiting for run ID...\n", taskID, cmd.Process.Pid)

	// Read run ID from stdout
	reader := bufio.NewReader(stdout)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [debug] spawnWorker %s: readString error: %v\n", taskID, err)
	}
	stdout.Close()
	runID := strings.TrimSpace(firstLine)
	fmt.Fprintf(os.Stderr, "  [debug] spawnWorker %s: runID=%q\n", taskID, runID)

	// Save PID before Release
	pid := cmd.Process.Pid

	// Release process so it runs independently
	if err := cmd.Process.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not release worker process: %v\n", err)
	}

	// Write agent activity with PID and run ID
	agentDir := storage.AgentDir(agentID)
	os.MkdirAll(agentDir, 0755)
	activity := map[string]interface{}{
		"status":    "running",
		"backend":   "ai",
		"startedAt": time.Now().Unix(),
		"pid":       pid,
		"runID":     runID,
	}
	storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity)

	// Write run ID to output file for traceability
	outFile.WriteString(fmt.Sprintf("\nRun ID: %s\n", runID))
	outFile.Close()

	fmt.Printf("  🚀 Started %s: %s (run %s)\n", taskID, t.Title, runID)
}

// checkRunning checks if any running tasks have completed (stale detection).
// For ai-serve workers, checks the run.json status via runID.
// For legacy workers, checks if PID is still alive.
func checkRunning(cfg SchedulerConfig) (bool, error) {
	running, err := List(StatusRunning)
	if err != nil {
		return false, err
	}
	progress := false
	for _, t := range running {
		if t.Claimant == "" {
			continue
		}
		agentDir := storage.AgentDir(t.Claimant)
		actPath := filepath.Join(agentDir, "activity.json")
		if !storage.Exists(actPath) {
			continue
		}

				var act struct {
			Pid       int    `json:"pid"`
			RunID     string `json:"runID"`
			StartedAt int64  `json:"startedAt"`
		}
		if err := storage.ReadJSON(actPath, &act); err != nil {
			continue
		}

		// Check for scheduler timeout — if the worker has been running too long,
		// treat it as failed to prevent hung runs from occupying slots forever.
		if act.StartedAt > 0 && cfg.Timeout > 0 {
			elapsed := time.Since(time.Unix(act.StartedAt, 0))
			if elapsed > cfg.Timeout {
				Fail(t.ID, fmt.Sprintf("task timed out after %v", elapsed.Round(time.Minute)), true)
				fmt.Printf("  ⏰ Detected timeout %s (ran for %v)\n", t.ID, elapsed.Round(time.Minute))
				progress = true
				continue
			}
		}

				completed := false
		summary := ""

		if act.RunID != "" {
			// ai-serve worker: check events.jsonl for completion
			completed, summary = checkAIServeRun(act.RunID, t.ID)
			// Fall back to PID liveness if events are unavailable
			if !completed && act.Pid > 0 && !agent.IsProcessAlive(act.Pid) {
				// Process died but events never showed agent_end — treat as failure
				Fail(t.ID, "ai-serve process died without completing", true)
				fmt.Printf("  ❌ Detected failure %s (ai-serve process died)\n", t.ID)
				progress = true
				continue
			}
		} else if act.Pid > 0 {
			// Legacy worker: check if process is alive
			if !agent.IsProcessAlive(act.Pid) {
				outputFile := filepath.Join(storage.TaskDir(t.ID), "output")
				if storage.Exists(outputFile) {
					summary = readSummary(outputFile)
					completed = true
				} else {
					Fail(t.ID, "agent process died without output", true)
					fmt.Printf("  ❌ Detected failure %s (process died)\n", t.ID)
					progress = true
					continue
				}
			}
		}

		if completed {
			if cfg.SkipReview {
				Done(t.ID, summary)
				fmt.Printf("  ✅ Detected completion %s (run done)\n", t.ID)
			} else {
				Transition(t.ID, StatusReview)
				outPath := filepath.Join(storage.TaskDir(t.ID), "output")
				if summary != "" {
					os.WriteFile(outPath, []byte(summary), 0644)
				}
				fmt.Printf("  📋 Detected completion %s → review\n", t.ID)
			}
			progress = true
		}
	}
	return progress, nil
}

// checkAIServeRun checks if an ai-serve run has completed by reading its events.jsonl.
// It does NOT rely on run.json status because ai serve blocks on cmd.Wait() and
// only updates run.json after the RPC subprocess exits — which may never happen
// when stdin is not closed.
// Returns (completed, summary).
func checkAIServeRun(runID, taskID string) (bool, string) {
	eventsPath, err := run.EventsPath(runID)
	if err != nil {
		return false, ""
	}

	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return false, "" // events file not found yet, still starting
	}

	// Use conv streaming API to scan for agent_end and collect summary.
	lastNHook, result := conv.CollectLastN(20, conv.KindTool, conv.KindMeta)

	agentDone := false
	doneHook := func(evt *conv.FormattedEvent) bool {
		if conv.IsAgentDone(evt) {
			agentDone = true
			return false // stop scanning
		}
		return true
	}

		conv.StreamEventsFromString(string(data), lastNHook, doneHook) //nolint:errcheck // best-effort event scanning

	if !agentDone {
		return false, "" // still running
	}

	summary := strings.Join(*result, "\n")

	// Kill the RPC subprocess so ai serve can exit and clean up
	meta, err := run.ReadMeta(runID)
	if err == nil && meta.PID > 0 {
		if proc, err := os.FindProcess(meta.PID); err == nil {
			proc.Signal(syscall.SIGTERM)
		}
	}

	return true, summary
}

// checkGroupReview checks if any group has all tasks in review and needs reviewer.
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

		// Check if all tasks in group are in review state
		allReview := true
		for _, t := range tasks {
			if t.Status != StatusReview {
				allReview = false
				break
			}
		}

		if !allReview {
			continue
		}

		// All in review — spawn reviewer (after transitions are committed)
		fmt.Printf("  🔍 Reviewing group %s\n", group)
		go spawnReviewer(group, tasks, cfg)
		progress = true
	}
	return progress, nil
}

// spawnReviewer runs a reviewer agent for a group of tasks.
func spawnReviewer(group string, tasks []*Task, cfg SchedulerConfig) {
	// Get diff of changes
	diff := getDiff(cfg.WorkDir)

	prompt := BuildReviewerPrompt(tasks, diff)
	agentID := fmt.Sprintf("reviewer-%s", group)

	// Spawn reviewer using "ai serve" — same pattern as spawnWorker.
	cmd := exec.Command("ai", "serve")
	cmd.Args = append(cmd.Args, "--input", prompt)
	cmd.Args = append(cmd.Args, "--name", agentID)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture run ID from stdout
		stdout, err := cmd.StdoutPipe()
	if err != nil {
		for _, t := range tasks {
			Fail(t.ID, fmt.Sprintf("reviewer stdout pipe: %v", err), true)
		}
		return
	}

	// Redirect stderr to review output file for debugging
	outputFile := filepath.Join(storage.TaskDir(tasks[0].ID), "review-output")
	outFile, _ := os.Create(outputFile)
	cmd.Stderr = outFile

		if err := cmd.Start(); err != nil {
		outFile.Close()
		for _, t := range tasks {
			Fail(t.ID, fmt.Sprintf("start reviewer: %v", err), true)
		}
		return
	}

	// Read run ID from stdout
	reader := bufio.NewReader(stdout)
	firstLine, _ := reader.ReadString('\n')
	stdout.Close()
	runID := strings.TrimSpace(firstLine)

	// Wait for ai serve to complete (it exits when the RPC subprocess finishes)
	waitErr := cmd.Wait()
	outFile.Close()

	if waitErr != nil {
		// Reviewer failed — pass anyway to not block
		fmt.Printf("  ⚠️ Reviewer failed for group %s (run %s), auto-passing: %v\n", group, runID, waitErr)
		for _, t := range tasks {
			Done(t.ID, "auto-passed: reviewer failed")
		}
		return
	}

		// Read events.jsonl for REVIEW_PASS
	eventsPath, _ := run.EventsPath(runID)
	eventsData := readFile(eventsPath)

	if strings.Contains(eventsData, "REVIEW_PASS") {
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
