package task

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	Timeout         time.Duration // Base timeout per task
	PollInterval    time.Duration
	DesignFile      string
	WorkDir         string
	SkipReview      bool
	MaxReviewRounds int
	Callback        string // Shell command to execute after all tasks complete
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

const (
	// maxTimeout is the absolute ceiling for task timeout.
	// A task's timeout starts at cfg.Timeout and extends up to this limit
	// while the worker is actively streaming events (dynamic timeout).
	maxTimeout = 60 * time.Minute

	// timeoutExtendPerActivity adds this much to the deadline each time
	// we detect the worker is still actively writing events.
	timeoutExtendPerActivity = 5 * time.Minute
)

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

	// Heartbeat: write timestamp every poll interval so `ag task stop`
	// can detect stale scheduler vs. dead scheduler.
	heartbeatPath := filepath.Join(storage.BaseDir, "scheduler.heartbeat")
	go func() {
		for {
			select {
			case <-ctx.Done():
				os.Remove(heartbeatPath)
				return
			case <-time.After(cfg.PollInterval):
				os.WriteFile(heartbeatPath, []byte(fmt.Sprintf("%d", time.Now().Unix())), 0644)
			}
		}
	}()

	// Circuit breaker: stop after N consecutive failures
	consecutiveFailures := 0
	const maxConsecutiveFailures = 3

	// Print blocking status for each pending task
	pendingTasks, _ := List(StatusPending)
	for _, t := range pendingTasks {
		unmet, depErr := UnmetDependencies(t.ID)
		if depErr != nil {
			log.Printf("[scheduler] %s: dependency check error: %v", t.ID, depErr)
			continue
		}
		if len(unmet) > 0 {
			// Check which deps are missing (deleted) vs just incomplete
			var missing []string
			var blocked []string
			for _, depID := range unmet {
				depDir := storage.TaskDir(depID)
				if !storage.Exists(depDir) {
					missing = append(missing, depID+" (deleted)")
				} else {
					depTask, loadErr := loadTask(depID)
					if loadErr != nil {
						missing = append(missing, depID+" (load error)")
					} else {
						blocked = append(blocked, depID+" ("+depTask.Status+")")
					}
				}
			}
			parts := append(missing, blocked...)
			log.Printf("[scheduler] %s: blocked by [%s]", t.ID, strings.Join(parts, ", "))
		} else {
			fmt.Printf("  🟢 %s: ready to claim\n", t.ID)
		}
	}

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

						// Circuit breaker: count consecutive failures or truly stuck reviews
		// "Truly stuck" means: reviews in a fully-ready group (all tasks in review)
		// with no reviewer process alive — indicating reviewer crashed.
		failed, _ := List(StatusFailed)
		trulyStuck := false
		if len(failed) == 0 {
			// Check if any review group is fully in review but has no reviewer alive
			reviewTasks, _ := List(StatusReview)
			if len(reviewTasks) > 0 {
				groups, _ := Groups()
				for _, g := range groups {
					gtasks, _ := GroupTasks(g)
					allReview := len(gtasks) > 0
					for _, gt := range gtasks {
						if gt.Status != StatusReview {
							allReview = false
							break
						}
					}
					if allReview {
						// Group fully in review — check if reviewer is alive
						reviewerKey := fmt.Sprintf("reviewer-%s", g)
						reviewerAgentDir := storage.AgentDir(reviewerKey)
						metaPath := filepath.Join(reviewerAgentDir, "worker-meta.json")
						if storage.Exists(metaPath) {
							var meta struct {
								Pid int `json:"pid"`
							}
							if err := storage.ReadJSON(metaPath, &meta); err == nil && meta.Pid > 0 && agent.IsProcessAlive(meta.Pid) {
								continue // reviewer alive, not stuck
							}
						}
						// No reviewer alive for a fully-in-review group — stuck!
						trulyStuck = true
						break
					}
				}
			}
		}
		if (len(failed) > 0 || trulyStuck) && !progress {
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveFailures {
				fmt.Printf("\n⛔ Circuit breaker: %d consecutive ticks without progress\n", consecutiveFailures)
				if trulyStuck {
					fmt.Println("  Reason: review group fully in review but no reviewer alive")
				}
				fmt.Println("Stopping scheduler to prevent wasting resources.")
				printSummary()
				return fmt.Errorf("circuit breaker triggered: %d consecutive failures", consecutiveFailures)
			}
			log.Printf("[scheduler] circuit breaker: %d/%d (failed=%d, trulyStuck=%v)", consecutiveFailures, maxConsecutiveFailures, len(failed), trulyStuck)
		} else if progress {
			consecutiveFailures = 0
		}

		// Check if all done
		allDone, err := AllDone()
		if err != nil {
			return fmt.Errorf("check all done: %w", err)
		}
				if allDone {
			fmt.Println("\n✅ All tasks completed.")
			printSummary()
			executeCallback(cfg)
			return nil
		}

				if !progress {
			// No progress made, wait before next poll
			running, _ := List(StatusRunning)
			if len(running) == 0 {
				// Nothing running and nothing claimed — likely all blocked
				pending, _ := List(StatusPending)
				if len(pending) > 0 {
					log.Printf("[scheduler] no running tasks, %d pending — all blocked?", len(pending))
				}
			}
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
// Before retrying, kills any leftover worker processes from the failed attempt
// and waits for them to actually die before resetting the task to pending.
func retryFailed(cfg SchedulerConfig) (bool, error) {
	failed, err := List(StatusFailed)
	if err != nil {
		return false, err
	}
	progress := false
	for _, t := range failed {
		if !t.Retryable {
			log.Printf("[scheduler] skip retry %s: not retryable (error: %s)", t.ID, t.Error)
			continue
		}

		// Kill any leftover worker from the failed attempt
		killWorker(t.ID, t.Claimant)

		// Wait for the worker process to actually die.
		// This prevents spawning a new worker while the old one is still
		// writing files or holding resources.
		waitForWorkerDeath(t.Claimant, 5*time.Second)

		_, err := Retry(t.ID, cfg.MaxRetries)
		if err != nil {
			log.Printf("[scheduler] skip retry %s: %v", t.ID, err)
			continue // max retries exceeded, skip
		}
		fmt.Printf("  🔄 Retried %s (attempt %d)\n", t.ID, t.RetryCount+1)
		progress = true
	}
	return progress, nil
}

// waitForWorkerDeath polls until the worker process is confirmed dead
// or the timeout elapses.
func waitForWorkerDeath(claimant string, timeout time.Duration) {
	if claimant == "" {
		return
	}
	agentDir := storage.AgentDir(claimant)
	metaPath := filepath.Join(agentDir, "worker-meta.json")
	if !storage.Exists(metaPath) {
		return
	}
	var meta struct {
		Pid int `json:"pid"`
	}
	if err := storage.ReadJSON(metaPath, &meta); err != nil || meta.Pid <= 0 {
		return
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !agent.IsProcessAlive(meta.Pid) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("[scheduler] waitForWorkerDeath: pid %d still alive after %v", meta.Pid, timeout)
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
			log.Printf("[scheduler] skip %s: dependency check error: %v", t.ID, err)
			continue
		}
		if len(unmet) > 0 {
			log.Printf("[scheduler] skip %s: blocked by %v", t.ID, unmet)
			continue
		}

		// Claim and transition to running
		claimed, err := Claim(t.ID, fmt.Sprintf("worker-%s", t.ID))
		if err != nil {
			log.Printf("[scheduler] skip %s: claim failed: %v", t.ID, err)
			continue
		}
		_, err = Transition(t.ID, StatusRunning)
		if err != nil {
			log.Printf("[scheduler] skip %s: transition to running failed: %v", t.ID, err)
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

	// Read run ID from stdout
	reader := bufio.NewReader(stdout)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("[scheduler] worker %s: read runID error: %v", taskID, err)
	}
	stdout.Close()
	runID := strings.TrimSpace(firstLine)

		// Save PID before releasing to background goroutine.
	pid := cmd.Process.Pid

	// Reap the child process in a background goroutine.
	// Without Wait(), the child becomes a zombie after exit.
	// We can't block here (need to return to scheduler loop),
	// so Wait() runs in a goroutine that cleans up the zombie.
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("[scheduler] worker %s: process reaped with error: %v", taskID, err)
		}
	}()

	// Write worker metadata to a separate file that ai serve won't overwrite.
		// This solves the race where ai serve's activity.json overwrites our runID.
	agentDir := storage.AgentDir(agentID)
	os.MkdirAll(agentDir, 0755)
	now := time.Now().Unix()
	workerMeta := map[string]interface{}{
		"pid":          pid,
		"runID":        runID,
		"startedAt":    now,
		"lastActivity": now, // Heartbeat: updated by checkRunning while streaming
		"taskID":       taskID,
	}
	metaPath := filepath.Join(agentDir, "worker-meta.json")
	if err := storage.AtomicWriteJSON(metaPath, workerMeta); err != nil {
		log.Printf("[scheduler] worker %s: write worker-meta: %v", taskID, err)
	}

	// Also write activity.json for compatibility with agent ls etc.
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
			log.Printf("[scheduler] checkRunning: %s has no claimant, skipping", t.ID)
			continue
		}
		agentDir := storage.AgentDir(t.Claimant)

		// Read worker metadata — our separate file that ai serve won't touch.
		workerMetaPath := filepath.Join(agentDir, "worker-meta.json")
		var pid int
		var runID string
		var startedAt int64

		if storage.Exists(workerMetaPath) {
			var meta struct {
				Pid       int    `json:"pid"`
				RunID     string `json:"runID"`
				StartedAt int64  `json:"startedAt"`
			}
			if err := storage.ReadJSON(workerMetaPath, &meta); err != nil {
				log.Printf("[scheduler] checkRunning: %s read worker-meta error: %v", t.ID, err)
			} else {
				pid = meta.Pid
				runID = meta.RunID
				startedAt = meta.StartedAt
			}
		} else {
			// Fallback: try activity.json (legacy / compatibility)
			actPath := filepath.Join(agentDir, "activity.json")
			if !storage.Exists(actPath) {
				log.Printf("[scheduler] checkRunning: %s no worker-meta.json or activity.json", t.ID)
				continue
			}
			var act struct {
				Pid       int    `json:"pid"`
				RunID     string `json:"runID"`
				StartedAt int64  `json:"startedAt"`
			}
			if err := storage.ReadJSON(actPath, &act); err != nil {
				log.Printf("[scheduler] checkRunning: %s read activity error: %v", t.ID, err)
				continue
			}
			pid = act.Pid
			runID = act.RunID
			startedAt = act.StartedAt
		}

											// Check for scheduler timeout — if the worker has been running too long,
		// treat it as failed to prevent hung runs from occupying slots forever.
		// Dynamic timeout: extend while the worker is actively streaming events.
		if startedAt > 0 && cfg.Timeout > 0 {
			elapsed := time.Since(time.Unix(startedAt, 0))

			// Per-task timeout: if estimated_minutes is set, use 2× that value.
			// Otherwise fall back to the global cfg.Timeout.
			effectiveTimeout := cfg.Timeout
			if t.EstimatedMinutes > 0 {
				taskTimeout := time.Duration(t.EstimatedMinutes) * 2 * time.Minute
				if taskTimeout < 5*time.Minute {
					taskTimeout = 5 * time.Minute
				}
				effectiveTimeout = taskTimeout
			}

						// Dynamic timeout: if the worker is actively streaming events,
			// extend the timeout. Check events.jsonl modification time as heartbeat.
			if runID != "" {
				eventsPath, _ := run.EventsPath(runID)
				if fi, err := os.Stat(eventsPath); err == nil {
					eventsAge := time.Since(fi.ModTime())
					if eventsAge < cfg.PollInterval*3 {
						// Events are fresh — worker is actively streaming.
						// Extend timeout up to maxTimeout.
						effectiveTimeout = maxTimeout
						if elapsed > cfg.Timeout {
							log.Printf("[scheduler] %s: extending timeout (events fresh, age=%v, elapsed=%v)",
								t.ID, eventsAge.Round(time.Second), elapsed.Round(time.Minute))
						}
						// Heartbeat: update lastActivity timestamp in worker-meta.json
						// so external tools can detect "still alive" workers.
						updateWorkerHeartbeat(agentDir)
					} else if eventsAge < cfg.PollInterval*6 {
						// Events somewhat recent — partial extension
						effectiveTimeout = cfg.Timeout + timeoutExtendPerActivity
					}
				}
			}

			if elapsed > effectiveTimeout {
				// Before failing, check if the run actually completed
				if runID != "" {
					completed, summary := checkAIServeRun(runID, t.ID)
					if completed {
						log.Printf("[scheduler] %s timed out but run completed, accepting result", t.ID)
						if cfg.SkipReview {
							Done(t.ID, summary)
						} else {
							Transition(t.ID, StatusReview)
						}
						fmt.Printf("  ⏰ Late completion %s (ran %v, over timeout)\n", t.ID, elapsed.Round(time.Minute))
						progress = true
						continue
					}
				}
								// Kill the worker BEFORE marking as failed, so retryFailed
				// won't spawn a new worker while the old one is still alive.
				killWorker(t.ID, t.Claimant)
				Fail(t.ID, fmt.Sprintf("task timed out after %v", elapsed.Round(time.Minute)), true)
				fmt.Printf("  ⏰ Detected timeout %s (ran for %v)\n", t.ID, elapsed.Round(time.Minute))
				progress = true
				continue
			}
		}

		completed := false
		summary := ""

		if runID != "" {
			// ai-serve worker: check events.jsonl for completion
			completed, summary = checkAIServeRun(runID, t.ID)
			// Fall back to PID liveness if events are unavailable
			if !completed && pid > 0 && !agent.IsProcessAlive(pid) {
							// Process died but events never showed agent_end — treat as failure
				outputFile := filepath.Join(storage.TaskDir(t.ID), "output")
				lastOutput := ""
				if storage.Exists(outputFile) {
					lastOutput = truncate(readFile(outputFile), 300)
				}
				errMsg := "ai-serve process died without completing"
				if lastOutput != "" {
					errMsg += "\nLast output:\n" + lastOutput
				}
				Fail(t.ID, errMsg, true)
				fmt.Printf("  ❌ Detected failure %s (ai-serve process died)\n", t.ID)
				progress = true
				continue
			}
		} else if pid > 0 {
			// Legacy worker: check if process is alive
						if !agent.IsProcessAlive(pid) {
				outputFile := filepath.Join(storage.TaskDir(t.ID), "output")
				if storage.Exists(outputFile) {
					summary = readSummary(outputFile)
					completed = true
				} else {
					lastOutput := ""
					if storage.Exists(outputFile) {
						lastOutput = truncate(readFile(outputFile), 300)
					}
					errMsg := "agent process died without output"
					if lastOutput != "" {
						errMsg += "\nLast output:\n" + lastOutput
					}
					Fail(t.ID, errMsg, true)
					fmt.Printf("  ❌ Detected failure %s (process died)\n", t.ID)
					progress = true
					continue
				}
			}
		}

				if completed {
			// Cross-task file modification detection (Problem 4):
			// Check if the worker modified files outside its declared scope.
			if warning := checkCrossTaskModifications(cfg.WorkDir, t); warning != "" {
				log.Printf("[scheduler] ⚠️ %s: %s", t.ID, warning)
				fmt.Printf("  ⚠️ %s\n", warning)
				// Append warning to task output for review visibility
				outputFile := filepath.Join(storage.TaskDir(t.ID), "output")
				existing := readFile(outputFile)
				os.WriteFile(outputFile, []byte(existing+"\n\n⚠️ "+warning+"\n"), 0644)
			}

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

// checkAIServeRun checks events.jsonl for a specific run to see if the agent has finished.
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
	agentFailed := false

	doneHook := func(evt *conv.FormattedEvent) bool {
		if conv.IsAgentDone(evt) {
			agentDone = true
			agentFailed = strings.Contains(evt.Text, "agent failed")
			return false
		}
		return true
	}

	conv.StreamEventsFromString(string(data), lastNHook, doneHook) //nolint:errcheck // best-effort event scanning

	if !agentDone {
		return false, "" // still running
	}

	// If the agent ended with an explicit failure marker (e.g. 429 rate limit
	// causing a fatal exit), treat as not-completed so the scheduler can retry.
	if agentFailed {
		return false, ""
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
	// Guard: only spawn if no reviewer is already running for this group.
	reviewerKey := fmt.Sprintf("reviewer-%s", group)
	reviewerAgentDir := storage.AgentDir(reviewerKey)
	if storage.Exists(filepath.Join(reviewerAgentDir, "worker-meta.json")) {
		// Check if the reviewer process is still alive
		var meta struct {
			Pid int `json:"pid"`
		}
		if err := storage.ReadJSON(filepath.Join(reviewerAgentDir, "worker-meta.json"), &meta); err == nil && meta.Pid > 0 {
			if agent.IsProcessAlive(meta.Pid) {
				continue // reviewer already running, skip
			}
		}
	}
	fmt.Printf("  🔍 Reviewing group %s\n", group)
	go spawnReviewer(group, tasks, cfg)
	progress = true
	}
	return progress, nil
}

// spawnReviewer runs a reviewer agent for a group of tasks.
// It writes the prompt to a temp file (--input-file) to avoid OS argument
// length limits, and writes activity.json so agent commands can find it.
// The reviewer runs synchronously within this goroutine (blocks until cmd.Wait).
func spawnReviewer(group string, tasks []*Task, cfg SchedulerConfig) {
	// Get diff of changes
	diff := getDiff(cfg.WorkDir)

	prompt := BuildReviewerPrompt(tasks, diff)
	agentID := fmt.Sprintf("reviewer-%s", group)

	// Write prompt to a temp file to avoid OS ARG_MAX limits.
	// Reviewer prompts include full git diffs which can be very large.
	promptFile := filepath.Join(storage.TaskDir(tasks[0].ID), "review-prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0644); err != nil {
		for _, t := range tasks {
			Fail(t.ID, fmt.Sprintf("write reviewer prompt: %v", err), true)
		}
		return
	}

	// Spawn reviewer using "ai serve" — same pattern as spawnWorker.
	// Use --input-file to pass the prompt via file instead of CLI arg.
	cmd := exec.Command("ai", "serve")
	cmd.Args = append(cmd.Args, "--input-file", promptFile)
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

	pid := cmd.Process.Pid

	// Write worker-meta.json so checkGroupReview can deduplicate reviewers.
	agentDir := storage.AgentDir(agentID)
	os.MkdirAll(agentDir, 0755)
	workerMeta := map[string]interface{}{
		"pid":       pid,
		"runID":     runID,
		"startedAt": time.Now().Unix(),
		"group":     group,
	}
	metaPath := filepath.Join(agentDir, "worker-meta.json")
	if err := storage.AtomicWriteJSON(metaPath, workerMeta); err != nil {
		log.Printf("[scheduler] reviewer %s: write worker-meta: %v", group, err)
	}

	// Write activity.json for compatibility with agent ls, ag task status, etc.
	// This mirrors spawnWorker's activity.json write.
	activity := map[string]interface{}{
		"status":    "running",
		"backend":   "ai",
		"startedAt": time.Now().Unix(),
		"pid":       pid,
		"runID":     runID,
	}
	storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity)

	// Wait for ai serve to complete (it exits when the RPC subprocess finishes)
	// Apply reviewer timeout (2x base timeout) — reviewers shouldn't run forever.
	reviewTimeout := cfg.Timeout * 2
	if reviewTimeout < 20*time.Minute {
		reviewTimeout = 20 * time.Minute
	}
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-doneCh:
		// Normal completion
	case <-time.After(reviewTimeout):
		// Reviewer timed out — kill it
		log.Printf("[scheduler] reviewer %s timed out after %v, killing", group, reviewTimeout)
		if pid > 0 {
			syscall.Kill(-pid, syscall.SIGKILL)
			syscall.Kill(pid, syscall.SIGKILL)
		}
		waitErr = fmt.Errorf("reviewer timed out after %v", reviewTimeout)
		<-doneCh // Wait for cmd.Wait to return after kill
	}
	outFile.Close()

	// Update activity.json to reflect completion
	activity["status"] = "done"
	if waitErr != nil {
		activity["status"] = "failed"
	}
	activity["finishedAt"] = time.Now().Unix()
	storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity)

	// Clean up worker-meta.json after review completes to avoid stale PID reuse.
	defer os.Remove(metaPath)

	if waitErr != nil {
		// Reviewer failed — mark tasks as failed (retryable)
		// so the scheduler can retry them instead of silently passing.
		fmt.Printf("  ❌ Reviewer failed for group %s (run %s): %v\n", group, runID, waitErr)
		for _, t := range tasks {
			Fail(t.ID, fmt.Sprintf("reviewer process error: %v", waitErr), true)
		}
		return
	}

	if runID == "" {
		fmt.Printf("  ⚠️ Reviewer for group %s returned empty run ID, treating as pass\n", group)
		for _, t := range tasks {
			if _, err := Done(t.ID, t.Summary); err != nil {
				fmt.Printf("  ❌ Done(%s) failed: %v\n", t.ID, err)
			}
		}
		return
	}

	// Read events.jsonl for REVIEW_PASS
	eventsPath, _ := run.EventsPath(runID)
	eventsData := readFile(eventsPath)

	if strings.Contains(eventsData, "REVIEW_PASS") {
		fmt.Printf("  ✅ Review passed for group %s\n", group)
	} else {
		fmt.Printf("  🔧 Revision requested for group %s\n", group)
	}

				// Transition tasks to done with error handling.
	for _, t := range tasks {
		summary := t.Summary
		if !strings.Contains(eventsData, "REVIEW_PASS") {
			summary = "reviewed with comments"
		}
		if _, err := Done(t.ID, summary); err != nil {
			fmt.Printf("  ❌ Done(%s) failed after review: %v\n", t.ID, err)
			// Task state may have been changed externally (e.g. retryFailed reset to pending).
			// Try to recover: re-read current state and transition appropriately.
			current, loadErr := loadTask(t.ID)
			if loadErr != nil {
				fmt.Printf("  ❌ Cannot load task %s: %v\n", t.ID, loadErr)
				continue
			}
			switch current.Status {
			case StatusDone:
				// Already done — nothing to do
				fmt.Printf("  ℹ️ %s already done, skipping\n", t.ID)
			case StatusPending:
				// Was reset to pending — the work is done but state was lost.
				// Force-transition: pending→claimed→done
				if _, te := Transition(t.ID, StatusClaimed); te != nil {
					fmt.Printf("  ❌ Cannot claim %s: %v\n", t.ID, te)
				} else if _, de := Done(t.ID, summary); de != nil {
					fmt.Printf("  ❌ Cannot done %s: %v\n", t.ID, de)
				} else {
					fmt.Printf("  ✅ Recovered %s (pending→claimed→done)\n", t.ID)
				}
			case StatusFailed:
				// Was failed — retryFailed will handle
				fmt.Printf("  ℹ️ %s already failed, retryFailed will handle\n", t.ID)
			default:
				fmt.Printf("  ⚠️ %s in unexpected state %s after review\n", t.ID, current.Status)
			}
		}
	}
}

// Helper functions

// killWorker terminates any leftover worker process for a task.
// Reads worker-meta.json to find the PID and runID,
// then kills both the ai-serve process and the RPC subprocess.
// Uses SIGKILL to ensure prompt termination (SIGTERM may be ignored by runaway workers).
// Kills the entire process group since spawnWorker sets Setpgid=true.
func killWorker(taskID, claimant string) {
	if claimant == "" {
		return
	}
	agentDir := storage.AgentDir(claimant)

	// Try worker-meta.json first (new format)
	metaPath := filepath.Join(agentDir, "worker-meta.json")
	if storage.Exists(metaPath) {
		var meta struct {
			Pid   int    `json:"pid"`
			RunID string `json:"runID"`
		}
		if err := storage.ReadJSON(metaPath, &meta); err == nil {
			if meta.Pid > 0 && agent.IsProcessAlive(meta.Pid) {
				log.Printf("[scheduler] killing leftover worker pid %d for %s", meta.Pid, taskID)
				// Kill entire process group (negative PID = process group)
				syscall.Kill(-meta.Pid, syscall.SIGKILL)
				// Also kill the process directly as fallback
				syscall.Kill(meta.Pid, syscall.SIGKILL)
			}
			if meta.RunID != "" {
				runMeta, err := run.ReadMeta(meta.RunID)
				if err == nil && runMeta.PID > 0 && agent.IsProcessAlive(runMeta.PID) {
					log.Printf("[scheduler] killing leftover RPC subprocess pid %d for %s", runMeta.PID, taskID)
					syscall.Kill(-runMeta.PID, syscall.SIGKILL)
					syscall.Kill(runMeta.PID, syscall.SIGKILL)
				}
			}
		}
	}
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

// updateWorkerHeartbeat updates the lastActivity timestamp in worker-meta.json.
// This provides a "still alive" signal that external tools can check
// without needing to read events.jsonl.
func updateWorkerHeartbeat(agentDir string) {
	metaPath := filepath.Join(agentDir, "worker-meta.json")
	if !storage.Exists(metaPath) {
		return
	}
	// Read existing meta, update lastActivity, write back
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		return
	}
	meta["lastActivity"] = time.Now().Unix()
	storage.AtomicWriteJSON(metaPath, meta)
}

// checkCrossTaskModifications scans git diff to detect files modified by a task
// that fall outside its declared file scope. Returns a warning string if
// violations are found, or empty string if all clean.
func checkCrossTaskModifications(workDir string, task *Task) string {
	if workDir == "" {
		return ""
	}

	// Get the list of files changed in working tree
	cmd := exec.Command("git", "diff", "--name-only")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "" // Can't check, skip silently
	}
	changedFiles := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(changedFiles) == 0 || (len(changedFiles) == 1 && changedFiles[0] == "") {
		return ""
	}

	// Build allowed file set from task's FileScope
	allowed := parseFileScope(task.FileScope)
	if len(allowed) == 0 {
		return "" // No file scope declared, can't check
	}

	var violations []string
	for _, f := range changedFiles {
		if f == "" {
			continue
		}
		if !isFileInScope(f, allowed) {
			violations = append(violations, f)
		}
	}

	if len(violations) > 0 {
		return fmt.Sprintf("cross-task file modification: %s modified files outside scope: %s",
			task.ID, strings.Join(violations, ", "))
	}
	return ""
}

// parseFileScope parses the FileScope field into a list of path prefixes.
// Supports comma-separated paths: "pkg/agent/,pkg/rpc/".
func parseFileScope(fileScope string) []string {
	if fileScope == "" {
		return nil
	}
	parts := strings.Split(fileScope, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// isFileInScope checks if a file path matches any of the allowed prefixes.
func isFileInScope(file string, allowed []string) bool {
	for _, prefix := range allowed {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
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

// executeCallback runs the callback shell command if configured.
// This is the "prompt-as-callback" mechanism: typically
//   ag agent prompt <main-id> "scheduler done"
// Errors are logged but not fatal — the scheduler has already completed its work.
func executeCallback(cfg SchedulerConfig) {
	if cfg.Callback == "" {
		return
	}
	fmt.Printf("\n📞 Executing callback: %s\n", cfg.Callback)
	cmd := exec.Command("sh", "-c", cfg.Callback)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[scheduler] callback failed: %v\noutput: %s", err, string(output))
		fmt.Printf("  ⚠️ Callback failed: %v\n", err)
		return
	}
	fmt.Printf("  ✅ Callback executed successfully\n")
}