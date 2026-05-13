package task

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/genius/ag/internal/storage"
	"gopkg.in/yaml.v3"
)

// Status constants
const (
	StatusPending   = "pending"
	StatusClaimed   = "claimed"
	StatusRunning   = "running"
	StatusReview    = "review"
	StatusRevision  = "revision"
	StatusDone      = "done"
	StatusFailed    = "failed"
)

// validTransitions defines allowed state transitions.
// Any transition not in this map is rejected.
var validTransitions = map[string][]string{
	StatusPending:  {StatusClaimed},
	StatusClaimed:  {StatusRunning, StatusDone, StatusFailed},       // claimed→done: manual override when work is complete
	StatusRunning:  {StatusDone, StatusFailed, StatusReview},
	StatusReview:   {StatusDone, StatusRevision, StatusFailed},
	StatusRevision: {StatusReview, StatusFailed},
	StatusDone:     {},                    // Terminal
	StatusFailed:   {StatusPending, StatusDone}, // failed→done: manual override after human verification
}

// isWorkComplete returns true if the task's code work is finished,
// meaning downstream tasks can safely build on top of it.
// Both "done" (fully complete) and "review" (code done, awaiting review)
// count as work complete.
func isWorkComplete(status string) bool {
	return status == StatusDone || status == StatusReview
}

// CanTransition checks if a state transition is valid.
func CanTransition(from, to string) bool {
	allowed, exists := validTransitions[from]
	if !exists {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// IsTerminal returns true if the state is terminal (no further transitions).
func IsTerminal(state string) bool {
	allowed, exists := validTransitions[state]
	return exists && len(allowed) == 0
}

// Task represents a work unit.
type Task struct {
	ID               string   `json:"id"`
	Title            string   `json:"title,omitempty"`
	Status           string   `json:"status"`
	Claimant         string   `json:"claimant,omitempty"`
	Description      string   `json:"description"`
	SpecFile         string   `json:"specFile,omitempty"`
	OutputFile       string   `json:"outputFile,omitempty"`
	FileScope        string   `json:"fileScope,omitempty"` // Comma-separated path prefixes this task should modify
	Dependencies     []string `json:"dependencies,omitempty"`
	Group            string   `json:"group,omitempty"`
	EstimatedMinutes int      `json:"estimatedMinutes,omitempty"` // Per-task timeout hint: scheduler uses 2× this value
	CreatedAt        int64    `json:"createdAt"`
	ClaimedAt        int64    `json:"claimedAt,omitempty"`
	FinishedAt       int64    `json:"finishedAt,omitempty"`
	Error            string   `json:"error,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Retryable        bool     `json:"retryable,omitempty"`
	RetryCount       int      `json:"retryCount,omitempty"`
}

var (
	taskMu sync.Mutex
)

// Create creates a new task with pending status.
// Uses O_EXCL directory creation as an atomic primitive to prevent ID collisions
// across concurrent processes.
func Create(description string, specFile string) (*Task, error) {
	return createWithID("", description, specFile)
}

// CreateWithID creates a new task with a caller-provided ID.
func CreateWithID(id string, description string, specFile string) (*Task, error) {
	return createWithID(id, description, specFile)
}

func createWithID(id string, description string, specFile string) (*Task, error) {
	// Initialize storage
	_, _, tasksDir := storage.Paths()
	os.MkdirAll(tasksDir, 0755)

	// If ID is provided, attempt exactly that ID.
	if strings.TrimSpace(id) != "" {
		id = strings.TrimSpace(id)
		taskDir := storage.TaskDir(id)
		if err := os.Mkdir(taskDir, 0755); err != nil {
			if os.IsExist(err) {
				return nil, fmt.Errorf("task already exists: %s", id)
			}
			return nil, err
		}
		task := &Task{
			ID:           id,
			Status:       StatusPending,
			Description:  description,
			SpecFile:     specFile,
			Dependencies: []string{},
			CreatedAt:    time.Now().Unix(),
		}
		if err := storage.AtomicWriteJSON(filepath.Join(taskDir, "task.json"), task); err != nil {
			os.RemoveAll(taskDir)
			return nil, err
		}
		return task, nil
	}

	// Find next available ID using O_EXCL directory creation.
	for i := 1; ; i++ {
		id := fmt.Sprintf("t%03d", i)
		taskDir := storage.TaskDir(id)
		// O_EXCL ensures only one process successfully creates the directory
		if err := os.Mkdir(taskDir, 0755); err != nil {
			if os.IsExist(err) {
				continue // already taken, try next
			}
			return nil, err
		}
		task := &Task{
			ID:           id,
			Status:       StatusPending,
			Description:  description,
			SpecFile:     specFile,
			Dependencies: []string{},
			CreatedAt:    time.Now().Unix(),
		}
		if err := storage.AtomicWriteJSON(filepath.Join(taskDir, "task.json"), task); err != nil {
			os.RemoveAll(taskDir)
			return nil, err
		}
		return task, nil
	}
}

// Claim claims a pending task for an agent.
// Cross-process safety: flock is the primary guard.
// We acquire the lock FIRST, then check status — this prevents TOCTOU races.
// flock is automatically released when the process exits, so no stale locks.
func Claim(taskID, agentID string) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	taskDir := storage.TaskDir(taskID)
	if !storage.Exists(taskDir) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Acquire exclusive lock using flock — auto-released on process exit.
	// No stale lock files possible, unlike O_EXCL.
	lockPath := filepath.Join(taskDir, ".claim-lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("task %s: open lock file: %w", taskID, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("task %s already claimed by another process", taskID)
	}
	// Note: we intentionally keep f open until Claim returns,
	// holding the lock for the duration of the status update.
	defer func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}()

	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(agentID)

	// NOW check status (safe because we hold the lock)
	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if task.Status != StatusPending {
		return nil, fmt.Errorf("task %s is %s (not pending)", taskID, task.Status)
	}

	task.Status = StatusClaimed
	task.Claimant = agentID
	task.ClaimedAt = time.Now().Unix()

	unmet, err := unmetDependencies(task)
	if err != nil {
		return nil, err
	}
	if len(unmet) > 0 {
		return nil, fmt.Errorf("task %s is blocked by: %s", taskID, strings.Join(unmet, ", "))
	}

	if err := storage.AtomicWriteJSON(filepath.Join(taskDir, "task.json"), task); err != nil {
		return nil, err
	}

	return task, nil
}

// Done marks a task as completed with optional summary.
// Transition changes task state after validating the transition is allowed.
// Returns error if the transition is invalid.
func Transition(taskID, to string) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

		if !CanTransition(task.Status, to) {
		return nil, fmt.Errorf("invalid transition: %s → %s for task %s", task.Status, to, taskID)
	}

		task.Status = to
	if to == StatusClaimed && task.ClaimedAt == 0 {
		task.ClaimedAt = time.Now().Unix()
	}
	if to == StatusDone || to == StatusFailed {
		task.FinishedAt = time.Now().Unix()
	}
	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}
	return task, nil
}

func Done(taskID string, summary string) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if !CanTransition(task.Status, StatusDone) {
		return nil, fmt.Errorf("task %s is %s (cannot transition to done)", taskID, task.Status)
	}

	task.Status = StatusDone
	task.Summary = summary
	task.FinishedAt = time.Now().Unix()

	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}

	return task, nil
}

// Fail marks a task as failed with error message and retryable flag.
func Fail(taskID string, errMsg string, retryable bool) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if !CanTransition(task.Status, StatusFailed) {
		return nil, fmt.Errorf("task %s is %s (cannot transition to failed)", taskID, task.Status)
	}

	task.Status = StatusFailed
	task.Error = errMsg
	task.Retryable = retryable
	task.FinishedAt = time.Now().Unix()

	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}

	return task, nil
}

// Retry resets a failed task back to pending for re-execution.
// Increments RetryCount. Returns error if task is not failed or max retries exceeded.
func Retry(taskID string, maxRetries int) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if task.Status != StatusFailed {
		return nil, fmt.Errorf("task %s is %s (not failed, cannot retry)", taskID, task.Status)
	}

	if task.RetryCount >= maxRetries {
		return nil, fmt.Errorf("task %s exceeded max retries (%d)", taskID, maxRetries)
	}

	task.Status = StatusPending
	task.Claimant = ""
	task.ClaimedAt = 0
	task.FinishedAt = 0
	task.Error = ""
	task.RetryCount++
	task.Retryable = false

	// Remove claim lock so Claim can succeed
	os.Remove(filepath.Join(storage.TaskDir(taskID), ".claim-lock"))

	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}
	return task, nil
}

// Groups returns all unique group names across all tasks.
// Tasks without a group are assigned to the "default" group.
func Groups() ([]string, error) {
	tasks, err := List("")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var groups []string
	for _, t := range tasks {
		g := t.Group
		if g == "" {
			g = "default"
		}
		if !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// GroupTasks returns all tasks in a given group.
func GroupTasks(group string) ([]*Task, error) {
	tasks, err := List("")
	if err != nil {
		return nil, err
	}
	var result []*Task
	for _, t := range tasks {
		g := t.Group
		if g == "" {
			g = "default"
		}
		if g == group {
			result = append(result, t)
		}
	}
	return result, nil
}

// AllDone returns true if all tasks are in a terminal state (done or failed).
func AllDone() (bool, error) {
	tasks, err := List("")
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if !IsTerminal(t.Status) {
			return false, nil
		}
	}
	return true, nil
}

// Cleanup removes done/failed tasks and fixes dangling dependencies in remaining tasks.
// Returns the number of tasks cleaned up and the number of dangling deps fixed.
func Cleanup() (cleaned int, depsFixed int, err error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	_, _, tasksDir := storage.Paths()
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	// Phase 1: Identify terminal tasks to remove
	toRemove := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, loadErr := loadTask(entry.Name())
		if loadErr != nil {
			continue
		}
		if IsTerminal(t.Status) {
			toRemove[t.ID] = true
		}
	}

	// Phase 2: Remove terminal task directories
	for id := range toRemove {
		taskDir := storage.TaskDir(id)
		if rmErr := os.RemoveAll(taskDir); rmErr != nil {
			log.Printf("[task] cleanup: failed to remove %s: %v", id, rmErr)
			continue
		}
		cleaned++
		log.Printf("[task] cleanup: removed %s", id)
	}

	if cleaned == 0 {
		return 0, 0, nil
	}

	// Phase 3: Fix dangling dependencies in remaining tasks
	remaining, _ := os.ReadDir(tasksDir)
	for _, entry := range remaining {
		if !entry.IsDir() {
			continue
		}
		t, loadErr := loadTask(entry.Name())
		if loadErr != nil {
			continue
		}
		if len(t.Dependencies) == 0 {
			continue
		}

		fixed := false
		newDeps := make([]string, 0, len(t.Dependencies))
		for _, depID := range t.Dependencies {
			if toRemove[depID] {
				log.Printf("[task] cleanup: removing dangling dep %s from %s (deleted task)", depID, t.ID)
				fixed = true
				continue
			}
			// Also check if dep task directory actually exists
			depDir := storage.TaskDir(depID)
			if !storage.Exists(depDir) {
				log.Printf("[task] cleanup: removing dangling dep %s from %s (dir not found)", depID, t.ID)
				fixed = true
				continue
			}
			newDeps = append(newDeps, depID)
		}

		if fixed {
			t.Dependencies = newDeps
			taskPath := filepath.Join(storage.TaskDir(t.ID), "task.json")
			if writeErr := storage.AtomicWriteJSON(taskPath, t); writeErr != nil {
				log.Printf("[task] cleanup: failed to update %s deps: %v", t.ID, writeErr)
			}
			depsFixed++
		}
	}

	return cleaned, depsFixed, nil
}

// Load loads a task by ID (alias for reading task.json).
func Load(taskID string) (*Task, error) {
	return loadTask(taskID)
}

// ClaimNext claims the first pending and unblocked task for agentID.
// Returns the claimed task ID on success.
func ClaimNext(claimant string) (string, error) {
	t, err := Next(claimant)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

// ImportPlan imports tasks from a PLAN.yml file.
// Returns the number of tasks imported.
func ImportPlan(filePath string) (int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("read plan file: %w", err)
	}

		// Parse YAML plan
	var plan struct {
		Tasks []struct {
			ID               string   `yaml:"id"`
			Title            string   `yaml:"title"`
			Description      string   `yaml:"description"`
			Dependencies     []string `yaml:"dependencies"`
			Group            string   `yaml:"group"`
			EstimatedMinutes int      `yaml:"estimated_minutes"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &plan); err != nil {
		return 0, fmt.Errorf("parse plan: %w", err)
	}

	count := 0
	var depErrors []string

	// Phase 1: Create all tasks first (ensures forward dependencies resolve)
	for _, pt := range plan.Tasks {
		desc := pt.Title
		if pt.Description != "" {
			desc = pt.Description
		}

		var t *Task
		if pt.ID != "" {
			t, err = CreateWithID(pt.ID, desc, "")
			if err != nil {
				continue // skip duplicates
			}
		} else {
			t, err = Create(desc, "")
			if err != nil {
				continue
			}
		}
				// Set title and group
		t.Title = pt.Title
		t.Group = pt.Group
		if t.Group == "" {
			t.Group = "default"
		}
		t.EstimatedMinutes = pt.EstimatedMinutes
		taskMu.Lock()
		storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(t.ID), "task.json"), t)
		taskMu.Unlock()
		count++
	}

	// Phase 2: Add dependencies (all tasks now exist, forward refs resolve)
	for _, pt := range plan.Tasks {
		if pt.ID == "" {
			continue
		}
		for _, dep := range pt.Dependencies {
			if _, err := AddDependency(pt.ID, dep); err != nil {
				depErrors = append(depErrors, fmt.Sprintf("%s -> %s: %v", pt.ID, dep, err))
			}
		}
	}

	if len(depErrors) > 0 {
		return count, fmt.Errorf("dependency errors: %s", strings.Join(depErrors, "; "))
	}
	return count, nil
}

// Show returns task details.
func Show(taskID string) (*Task, error) {
	return loadTask(taskID)
}

// Next claims the first pending and unblocked task for agentID.
func Next(agentID string) (*Task, error) {
	tasks, err := List(StatusPending)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		claimed, err := Claim(t.ID, agentID)
		if err == nil {
			return claimed, nil
		}
	}
	return nil, fmt.Errorf("no claimable pending tasks")
}

// AddDependency adds depID as a prerequisite for taskID.
func AddDependency(taskID, depID string) (*Task, error) {
	if taskID == depID {
		return nil, fmt.Errorf("a task cannot depend on itself")
	}
	taskMu.Lock()
	defer taskMu.Unlock()

	taskObj, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}
	if _, err := loadTask(depID); err != nil {
		return nil, fmt.Errorf("dependency task not found: %s", depID)
	}
	for _, dep := range taskObj.Dependencies {
		if dep == depID {
			return taskObj, nil
		}
	}
	cycle, err := createsCycle(taskID, depID)
	if err != nil {
		return nil, err
	}
	if cycle {
		return nil, fmt.Errorf("dependency would create a cycle: %s -> %s", taskID, depID)
	}
	taskObj.Dependencies = append(taskObj.Dependencies, depID)
	sort.Strings(taskObj.Dependencies)
	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), taskObj); err != nil {
		return nil, err
	}
	return taskObj, nil
}

// RemoveDependency removes depID from taskID prerequisites.
func RemoveDependency(taskID, depID string) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	taskObj, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(taskObj.Dependencies))
	for _, dep := range taskObj.Dependencies {
		if dep != depID {
			filtered = append(filtered, dep)
		}
	}
	taskObj.Dependencies = filtered
	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), taskObj); err != nil {
		return nil, err
	}
	return taskObj, nil
}

// Dependencies returns dependency IDs for a task.
func Dependencies(taskID string) ([]string, error) {
	taskObj, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(taskObj.Dependencies))
	copy(out, taskObj.Dependencies)
	return out, nil
}

// UnmetDependencies returns dependency IDs that are not yet done.
func UnmetDependencies(taskID string) ([]string, error) {
	taskObj, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}
	return unmetDependencies(taskObj)
}

// List returns tasks, optionally filtered by status.
func List(statusFilter string) ([]*Task, error) {
	_, _, tasksDir := storage.Paths()
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tasks []*Task
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		task, err := loadTask(entry.Name())
		if err != nil {
			continue
		}
		if statusFilter != "" && task.Status != statusFilter {
			continue
		}
		tasks = append(tasks, task)
	}

	// Sort by ID numerically
	sort.Slice(tasks, func(i, j int) bool {
		ni, errI := parseNumericID(tasks[i].ID)
		nj, errJ := parseNumericID(tasks[j].ID)
		if errI == nil && errJ == nil && ni != nj {
			return ni < nj
		}
		if errI == nil && errJ != nil {
			return true
		}
		if errI != nil && errJ == nil {
			return false
		}
		return tasks[i].ID < tasks[j].ID
	})

	return tasks, nil
}

func loadTask(id string) (*Task, error) {
	taskPath := filepath.Join(storage.TaskDir(id), "task.json")
	if !storage.Exists(taskPath) {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	task := &Task{}
	if err := storage.ReadJSON(taskPath, task); err != nil {
		return nil, err
	}
	return task, nil
}

func unmetDependencies(taskObj *Task) ([]string, error) {
	if len(taskObj.Dependencies) == 0 {
		return nil, nil
	}
	unmet := make([]string, 0)
	for _, depID := range taskObj.Dependencies {
		depTask, err := loadTask(depID)
		if err != nil {
			log.Printf("[task] unmetDependencies: dependency %s not found for %s", depID, taskObj.ID)
			unmet = append(unmet, depID)
			continue
		}
		if !isWorkComplete(depTask.Status) {
			unmet = append(unmet, depID)
		}
	}
	sort.Strings(unmet)
	return unmet, nil
}

func createsCycle(taskID, depID string) (bool, error) {
	seen := map[string]bool{}
	var dfs func(cur string) (bool, error)
	dfs = func(cur string) (bool, error) {
		if cur == taskID {
			return true, nil
		}
		if seen[cur] {
			return false, nil
		}
		seen[cur] = true
		taskObj, err := loadTask(cur)
		if err != nil {
			return false, err
		}
		for _, dep := range taskObj.Dependencies {
			cycle, err := dfs(dep)
			if err != nil {
				return false, err
			}
			if cycle {
				return true, nil
			}
		}
		return false, nil
	}
	return dfs(depID)
}

func parseNumericID(id string) (int, error) {
	var digits strings.Builder
	for _, r := range id {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0, errors.New("no digits in id")
	}
	return strconv.Atoi(digits.String())
}
