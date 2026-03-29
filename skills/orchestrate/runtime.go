package orchestrate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Runtime manages team execution
type Runtime struct {
	config      *TeamConfig
	storage     *Storage
	api         *API
	workers     map[string]*Worker
	workerMu    sync.RWMutex
	stopCh      chan struct{}
	cwd         string
	tmux        *TmuxManager
	heartbeatCh chan string // worker name sent on heartbeat
}

// RuntimeConfig holds runtime configuration
type RuntimeConfig struct {
	TaskTimeout  time.Duration // default: 30m
	HeartbeatTTL time.Duration // default: 5m
	MonitorTick  time.Duration // default: 5s
	UseTmux      bool          // default: true if available
}

// DefaultRuntimeConfig returns default runtime config
func DefaultRuntimeConfig() *RuntimeConfig {
	return &RuntimeConfig{
		TaskTimeout:  30 * time.Minute,
		HeartbeatTTL: 5 * time.Minute,
		MonitorTick:  5 * time.Second,
		UseTmux:      true,
	}
}

// NewRuntime creates a new runtime
func NewRuntime(cwd string) *Runtime {
	storage := NewStorage(cwd)
	return &Runtime{
		storage:     storage,
		api:         NewAPI(storage),
		workers:     make(map[string]*Worker),
		stopCh:      make(chan struct{}),
		heartbeatCh: make(chan string, 100),
		cwd:         cwd,
	}
}

// Start starts the team with a workflow
func (r *Runtime) Start(config *TeamConfig, workflow *Workflow) error {
	return r.StartWithConfig(config, workflow, DefaultRuntimeConfig())
}

// StartWithConfig starts the team with custom runtime config
func (r *Runtime) StartWithConfig(config *TeamConfig, workflow *Workflow, rc *RuntimeConfig) error {
	r.config = config

	// Initialize storage
	if err := r.storage.Init(); err != nil {
		return err
	}
	// Clear stale stop request from previous runs
	if err := r.storage.ClearStopRequest(); err != nil {
		return err
	}

	// Write config
	config.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := r.storage.WriteConfig(config); err != nil {
		return err
	}

	// Create tasks from workflow phases
	for _, phase := range workflow.Phases {
		task := &Task{
			ID:          phase.ID,
			Subject:     phase.Subject,
			Description: phase.Description,
			Status:      StatePending,
			BlockedBy:   phase.BlockedBy,
			CreatedAt:   time.Now().UTC(),
		}
		if err := r.storage.WriteTask(task); err != nil {
			return err
		}
	}

	// Write initial state
	state := &TeamState{
		Phase:       "initializing",
		ActiveCount: 0,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := r.storage.WriteState(state); err != nil {
		return err
	}

	// Initialize tmux if available
	if rc.UseTmux && IsTmuxAvailable() {
		r.tmux = NewTmuxManager(config.Name, r.cwd)
		if err := r.tmux.CreateSession(); err != nil {
			r.storage.AppendLog("runtime", fmt.Sprintf("tmux session created: %s", config.Name))
		}
	}

	// Start monitor loop
	go r.monitorLoop(rc)

	// Start heartbeat monitor
	go r.heartbeatMonitor(rc)

	return nil
}

// monitorLoop monitors tasks and dispatches workers
func (r *Runtime) monitorLoop(rc *RuntimeConfig) {
	ticker := time.NewTicker(rc.MonitorTick)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.reconcile(rc)
		}
	}
}

// reconcile checks tasks and dispatches workers
func (r *Runtime) reconcile(rc *RuntimeConfig) {
	if r.storage.IsStopRequested() {
		r.storage.AppendLog("runtime", "received stop request")
		r.Stop()
		return
	}

	tasks, err := r.storage.ListTasks()
	if err != nil {
		r.storage.AppendLog("runtime", fmt.Sprintf("reconcile error: %v", err))
		return
	}

	// Update state
	state := r.inferState(tasks)
	r.storage.WriteState(state)

	// Check if all completed
	if state.Phase == "completed" || state.Phase == "failed" {
		r.Stop()
		return
	}

	// Check for pending reviews - block dispatching if reviews pending
	reviews, _ := r.storage.ListReviewRequests()
	if len(reviews) > 0 {
		r.storage.AppendLog("runtime", fmt.Sprintf("waiting for %d pending reviews", len(reviews)))
		// Continue monitoring but don't dispatch new workers for review-dependent tasks
	}

	// Check for timed out tasks
	for _, task := range tasks {
		if task.Status == StateInProgress || task.Status == StateClaimed {
			if r.isTaskTimedOut(task, rc.TaskTimeout) {
				r.storage.AppendLog(task.ID, fmt.Sprintf("task timed out after %v", rc.TaskTimeout))
				r.api.FailTask(task.ID, task.ClaimToken, "task timed out")

				// Kill worker if running
				r.workerMu.RLock()
				if worker, ok := r.workers[task.ID]; ok {
					worker.Stop()
				}
				r.workerMu.RUnlock()
			}
		}
	}

	// Find ready tasks and dispatch
	for _, task := range tasks {
		// Skip tasks that are awaiting review
		if strings.HasPrefix(task.Result, "[AWAITING REVIEW]") {
			continue
		}
		if r.api.IsReady(task) && r.hasCapacity() {
			r.dispatchWorker(task)
		}
	}

	// Check for failed tasks that need retry
	for _, task := range tasks {
		if task.Status == StateFailed && task.RetryCount < r.config.MaxRetries {
			r.api.RetryTask(task.ID, r.config.MaxRetries)
			r.storage.AppendLog(task.ID, fmt.Sprintf("retrying task (attempt %d/%d)", task.RetryCount+1, r.config.MaxRetries))
		}
	}
}

// heartbeatMonitor monitors worker heartbeats
func (r *Runtime) heartbeatMonitor(rc *RuntimeConfig) {
	// Map of worker name -> last heartbeat time
	heartbeats := make(map[string]time.Time)

	ticker := time.NewTicker(rc.MonitorTick)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case workerName := <-r.heartbeatCh:
			heartbeats[workerName] = time.Now()
		case <-ticker.C:
			// Check for stale workers
			now := time.Now()
			for workerName, lastBeat := range heartbeats {
				if now.Sub(lastBeat) > rc.HeartbeatTTL {
					r.storage.AppendLog("runtime", fmt.Sprintf("worker %s heartbeat timeout", workerName))
					// Worker will be handled by timeout check in reconcile
				}
			}
		}
	}
}

// isTaskTimedOut checks if a task has exceeded timeout
func (r *Runtime) isTaskTimedOut(task *Task, timeout time.Duration) bool {
	if task.StartedAt == nil {
		return false
	}
	return time.Since(*task.StartedAt) > timeout
}

// inferState infers team state from tasks
func (r *Runtime) inferState(tasks []*Task) *TeamState {
	if len(tasks) == 0 {
		return &TeamState{Phase: "initializing", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}

	hasInProgress := false
	allCompleted := true
	hasPending := false
	hasFailed := false

	activeCount := 0
	for _, t := range tasks {
		switch t.Status {
		case StateInProgress, StateClaimed:
			hasInProgress = true
			activeCount++
		case StatePending:
			hasPending = true
			allCompleted = false
		case StateFailed:
			hasFailed = true
			allCompleted = false
		}
	}

	phase := "executing"
	if hasInProgress {
		phase = "executing"
	} else if hasPending {
		phase = "planning"
	} else if allCompleted {
		phase = "completed"
	} else if hasFailed {
		phase = "failed"
	}

	return &TeamState{
		Phase:       phase,
		ActiveCount: activeCount,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

// hasCapacity checks if we can start more workers
func (r *Runtime) hasCapacity() bool {
	tasks, err := r.storage.ListTasks()
	if err != nil {
		r.storage.AppendLog("runtime", fmt.Sprintf("failed to check capacity: %v", err))
		return false
	}

	active := 0
	for _, task := range tasks {
		if task.Status == StateClaimed || task.Status == StateInProgress {
			active++
		}
	}
	return active < r.config.WorkerCount
}

// dispatchWorker starts a worker for a task
func (r *Runtime) dispatchWorker(task *Task) {
	workerName := fmt.Sprintf("worker-%s", task.ID)

	// Claim task
	_, claimToken, err := r.api.ClaimTask(task.ID, workerName)
	if err != nil {
		return
	}

	// Create worker
	worker := &Worker{
		Name:        workerName,
		TaskID:      task.ID,
		ClaimToken:  claimToken,
		Storage:     r.storage,
		Cwd:         r.cwd,
		heartbeatCh: r.heartbeatCh,
		tmux:        r.tmux,
	}

	r.workerMu.Lock()
	r.workers[task.ID] = worker
	r.workerMu.Unlock()

	// Start worker in background
	taskID := task.ID
	go func() {
		worker.Start()
		r.workerMu.Lock()
		if existing, ok := r.workers[taskID]; ok && existing == worker {
			delete(r.workers, taskID)
		}
		r.workerMu.Unlock()
	}()
}

// Stop stops the runtime
func (r *Runtime) Stop() {
	select {
	case <-r.stopCh:
		// Already stopped
	default:
		close(r.stopCh)

		r.workerMu.Lock()
		for _, w := range r.workers {
			w.Stop()
		}
		r.workerMu.Unlock()

		// Kill tmux session
		if r.tmux != nil {
			r.tmux.KillSession()
		}
	}
}

// Wait blocks until the runtime is stopped
func (r *Runtime) Wait() {
	<-r.stopCh
}

// Status returns current team status
func (r *Runtime) Status() (*TeamState, []*Task, error) {
	state, err := r.storage.ReadState()
	if err != nil {
		return nil, nil, err
	}
	tasks, err := r.storage.ListTasks()
	if err != nil {
		return nil, nil, err
	}
	return state, tasks, nil
}

// AggregateLogs returns aggregated logs from all tasks
func (r *Runtime) AggregateLogs() (map[string]string, error) {
	logs := make(map[string]string)

	tasks, err := r.storage.ListTasks()
	if err != nil {
		return nil, err
	}

	for _, task := range tasks {
		logPath := filepath.Join(r.storage.root, "logs", task.ID+".log")
		if data, err := os.ReadFile(logPath); err == nil {
			logs[task.ID] = string(data)
		}
	}

	return logs, nil
}

// CaptureWorkerOutput captures output from a worker's tmux pane
func (r *Runtime) CaptureWorkerOutput(workerName string) (string, error) {
	if r.tmux == nil {
		return "", fmt.Errorf("tmux not available")
	}
	return r.tmux.CapturePane(workerName)
}

// Worker represents a worker process
type Worker struct {
	Name        string
	TaskID      string
	ClaimToken  string
	Storage     *Storage
	Cwd         string
	cmd         *exec.Cmd
	heartbeatCh chan string
	tmux        *TmuxManager
	stopCh      chan struct{}
	stopMu      sync.Mutex
}

// Start starts the worker
func (w *Worker) Start() {
	// Read task
	task, err := w.Storage.ReadTask(w.TaskID)
	if err != nil {
		w.Storage.AppendLog(w.TaskID, fmt.Sprintf("failed to read task: %v", err))
		return
	}

	// Generate worker overlay prompt
	overlay := w.GenerateOverlay(task)

	// Write inbox
	w.Storage.WriteWorkerInbox(w.Name, overlay)

	// Write worker status
	now := time.Now().UTC()
	w.Storage.WriteWorkerStatus(&WorkerStatus{
		Name:      w.Name,
		TaskID:    w.TaskID,
		Status:    "running",
		StartedAt: &now,
		UpdatedAt: now,
	})

	// Start heartbeat
	w.stopCh = make(chan struct{})
	go w.heartbeatLoop()

	// Start ai in tmux or direct
	if w.tmux != nil {
		w.startInTmux(task)
	} else {
		w.startDirect(task)
	}
}

// heartbeatLoop sends periodic heartbeats
func (w *Worker) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			// Update worker status
			now := time.Now().UTC()
			status, _ := w.Storage.ReadWorkerStatus(w.Name)
			if status != nil {
				status.UpdatedAt = now
				w.Storage.WriteWorkerStatus(status)
			}

			// Send heartbeat to runtime
			if w.heartbeatCh != nil {
				select {
				case w.heartbeatCh <- w.Name:
				default:
					// Channel full, skip
				}
			}

			// Append to log
			w.Storage.AppendLog(w.TaskID, "heartbeat")
		}
	}
}

// startInTmux starts the worker in a tmux window
func (w *Worker) startInTmux(task *Task) {
	if err := w.tmux.StartWorker(w.Name, w.TaskID, w.ClaimToken); err != nil {
		w.Storage.AppendLog(w.TaskID, fmt.Sprintf("failed to start in tmux: %v", err))
		// Fallback to direct
		w.startDirect(task)
		return
	}

	w.Storage.AppendLog(w.TaskID, fmt.Sprintf("started in tmux window: %s", w.Name))

	// Poll task state so this worker exits when task reaches terminal state.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			currentTask, err := w.Storage.ReadTask(w.TaskID)
			if err != nil {
				continue
			}
			if currentTask.Status != StateClaimed && currentTask.Status != StateInProgress {
				w.updateStatus(string(currentTask.Status))
				w.closeStopCh()
				return
			}
		}
	}
}

// startDirect starts the worker directly using headless mode
func (w *Worker) startDirect(task *Task) {
	// Read inbox content
	inboxPath := filepath.Join(w.Storage.Root(), "workers", w.Name, "inbox.md")
	inboxContent, err := os.ReadFile(inboxPath)
	if err != nil {
		w.Storage.AppendLog(w.TaskID, fmt.Sprintf("failed to read inbox: %v", err))
		return
	}

	// Start ai in headless mode with inbox content as prompt
	cmd := exec.Command("ai", "--mode", "headless", "--timeout", "60m", string(inboxContent))
	cmd.Dir = w.Cwd
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("AI_TEAM_WORKER=%s", w.Name),
		fmt.Sprintf("AI_TEAM_TASK_ID=%s", w.TaskID),
		fmt.Sprintf("AI_TEAM_CLAIM_TOKEN=%s", w.ClaimToken),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	w.cmd = cmd

	w.Storage.AppendLog(w.TaskID, "starting worker process in headless mode")

	if err := cmd.Run(); err != nil {
		w.Storage.AppendLog(w.TaskID, fmt.Sprintf("worker error: %v", err))
	}

	w.updateStatusFromTask()
	w.closeStopCh()
}

// Stop stops the worker
func (w *Worker) Stop() {
	w.closeStopCh()

	if w.tmux != nil {
		_ = w.tmux.KillWindow(w.Name)
	}

	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}

	// Update status
	now := time.Now().UTC()
	status, _ := w.Storage.ReadWorkerStatus(w.Name)
	if status != nil {
		status.Status = "stopped"
		status.UpdatedAt = now
		w.Storage.WriteWorkerStatus(status)
	}

	w.Storage.AppendLog(w.TaskID, "worker stopped")
}

func (w *Worker) closeStopCh() {
	w.stopMu.Lock()
	defer w.stopMu.Unlock()

	if w.stopCh == nil {
		return
	}
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

func (w *Worker) updateStatus(statusValue string) {
	now := time.Now().UTC()
	status, err := w.Storage.ReadWorkerStatus(w.Name)
	if err != nil || status == nil {
		status = &WorkerStatus{
			Name:      w.Name,
			TaskID:    w.TaskID,
			StartedAt: &now,
		}
	}
	status.Status = statusValue
	status.UpdatedAt = now
	_ = w.Storage.WriteWorkerStatus(status)
}

func (w *Worker) updateStatusFromTask() {
	task, err := w.Storage.ReadTask(w.TaskID)
	if err != nil {
		w.updateStatus("stopped")
		return
	}
	w.updateStatus(string(task.Status))
}

// GenerateOverlay generates worker prompt overlay
func (w *Worker) GenerateOverlay(task *Task) string {
	return fmt.Sprintf(`# Worker: %s

## Your Task
- ID: %s
- Subject: %s

## Description
%s

## Task Lifecycle (CLI API)
You MUST use these commands to manage task lifecycle:

### Claim and Start
orchestrate api claim-task --input '{"task_id":"%s","worker":"%s"}'
orchestrate api start-task --input '{"task_id":"%s","claim_token":"YOUR_TOKEN"}'

### Complete (when done)
orchestrate api complete-task --input '{"task_id":"%s","claim_token":"YOUR_TOKEN","summary":"What you did"}'

### Fail (if stuck)
orchestrate api fail-task --input '{"task_id":"%s","claim_token":"YOUR_TOKEN","error":"What went wrong"}'

### Create Sub-tasks (for dynamic decomposition)
orchestrate api create-task --input '{"subject":"...","description":"...","blocked_by":[]}'

### Update Task Dependencies
orchestrate api update-task --input '{"task_id":"TARGET_ID","blocked_by":["%s"]}'

## Communication
- Your status: .ai/team/workers/%s/status.json
- Task logs: .ai/team/logs/%s.log

## Rules
- Do NOT edit task files directly
- Do NOT spawn sub-agents
- Do NOT run tmux commands
- Use CLI API for all task operations
- You MUST call complete-task or fail-task before exiting

## CRITICAL
Before you exit, you MUST either:
1. complete-task with a summary, OR
2. fail-task with an error message
`, w.Name, task.ID, task.Subject, task.Description,
		task.ID, w.Name, task.ID, task.ID, task.ID, task.ID,
		w.Name, task.ID)
}

// LoadWorkflow loads a workflow from file
func LoadWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var workflow Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, err
	}
	return &workflow, nil
}

// LoadWorkflowFromTemplate loads a workflow from template directory
func LoadWorkflowFromTemplate(name string) (*Workflow, error) {
	var candidates []string

	// Project-local templates
	candidates = append(candidates, filepath.Join(".ai", "workflows", name+".yaml"))

	// User-level templates
	home, _ := os.UserHomeDir()
	candidates = append(candidates, filepath.Join(home, ".ai", "templates", "workflows", name+".yaml"))

	// Templates bundled next to the installed orchestrate binary
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "..", "templates", name+".yaml"),
			filepath.Join(exeDir, "templates", name+".yaml"),
		)
	}

	// Development-time fallbacks
	candidates = append(candidates,
		filepath.Join("templates", name+".yaml"),
		filepath.Join("skills", "orchestrate", "templates", name+".yaml"),
	)

	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(candidate); err == nil {
			return LoadWorkflow(candidate)
		}
	}

	return nil, fmt.Errorf("workflow template not found: %s", name)
}

// LoadState loads the current team state
func (r *Runtime) LoadState() (*TeamState, []*Task, error) {
	state, err := r.storage.ReadState()
	if err != nil {
		return nil, nil, err
	}

	tasks, err := r.storage.ListTasks()
	if err != nil {
		return nil, nil, err
	}

	return state, tasks, nil
}

// GetLogs retrieves all logs
func (r *Runtime) GetLogs() ([]*LogEntry, error) {
	return r.storage.ReadLogs()
}

// ApproveTask marks a task as approved
func (r *Runtime) ApproveTask(taskID, comment string) error {
	if comment == "" {
		comment = "Approved"
	}
	return r.api.SubmitReview(taskID, true, comment, "human")
}
