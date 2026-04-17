package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genius/ag/internal/storage"
)

// Status constants
const (
	StatusPending = "pending"
	StatusClaimed = "claimed"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Task represents a work unit.
type Task struct {
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	Claimant     string   `json:"claimant,omitempty"`
	Description  string   `json:"description"`
	SpecFile     string   `json:"specFile,omitempty"`
	OutputFile   string   `json:"outputFile,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	CreatedAt    int64    `json:"createdAt"`
	ClaimedAt    int64    `json:"claimedAt,omitempty"`
	FinishedAt   int64    `json:"finishedAt,omitempty"`
	Error        string   `json:"error,omitempty"`
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
// Cross-process safety: O_EXCL on .claim-lock is the primary guard.
// We acquire the lock FIRST, then check status — this prevents TOCTOU races.
func Claim(taskID, agentID string) (*Task, error) {
	taskMu.Lock()
	defer taskMu.Unlock()

	taskDir := storage.TaskDir(taskID)
	if !storage.Exists(taskDir) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Acquire exclusive lock FIRST (atomic cross-process guard)
	lockPath := filepath.Join(taskDir, ".claim-lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("task %s already claimed by another process", taskID)
	}
	defer f.Close()
	f.WriteString(agentID)

	// NOW check status (safe because we hold the lock)
	task, err := loadTask(taskID)
	if err != nil {
		os.Remove(lockPath)
		return nil, err
	}

	if task.Status != StatusPending {
		os.Remove(lockPath)
		return nil, fmt.Errorf("task %s is %s (not pending)", taskID, task.Status)
	}

	task.Status = StatusClaimed
	task.Claimant = agentID
	task.ClaimedAt = time.Now().Unix()

	unmet, err := unmetDependencies(task)
	if err != nil {
		os.Remove(lockPath)
		return nil, err
	}
	if len(unmet) > 0 {
		os.Remove(lockPath)
		return nil, fmt.Errorf("task %s is blocked by: %s", taskID, strings.Join(unmet, ", "))
	}

	if err := storage.AtomicWriteJSON(filepath.Join(taskDir, "task.json"), task); err != nil {
		os.Remove(lockPath)
		return nil, err
	}

	return task, nil
}

// Done marks a task as completed.
func Done(taskID string, outputFile string) (*Task, error) {
	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if task.Status != StatusClaimed {
		return nil, fmt.Errorf("task %s is %s (not claimed)", taskID, task.Status)
	}

	task.Status = StatusDone
	task.OutputFile = outputFile
	task.FinishedAt = time.Now().Unix()

	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}

	return task, nil
}

// Fail marks a task as failed.
func Fail(taskID string, errMsg string) (*Task, error) {
	task, err := loadTask(taskID)
	if err != nil {
		return nil, err
	}

	if task.Status != StatusClaimed {
		return nil, fmt.Errorf("task %s is %s (not claimed)", taskID, task.Status)
	}

	task.Status = StatusFailed
	task.Error = errMsg
	task.FinishedAt = time.Now().Unix()

	if err := storage.AtomicWriteJSON(filepath.Join(storage.TaskDir(taskID), "task.json"), task); err != nil {
		return nil, err
	}

	return task, nil
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
			unmet = append(unmet, depID)
			continue
		}
		if depTask.Status != StatusDone {
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
