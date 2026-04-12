package task

import (
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
	ID          string `json:"id"`
	Status      string `json:"status"`
	Claimant    string `json:"claimant,omitempty"`
	Description string `json:"description"`
	SpecFile    string `json:"specFile,omitempty"`
	OutputFile  string `json:"outputFile,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	ClaimedAt   int64  `json:"claimedAt,omitempty"`
	FinishedAt  int64  `json:"finishedAt,omitempty"`
	Error       string `json:"error,omitempty"`
}

var (
	taskMu sync.Mutex
)

// Create creates a new task with pending status.
// Uses O_EXCL directory creation as an atomic primitive to prevent ID collisions
// across concurrent processes.
func Create(description string, specFile string) (*Task, error) {
	// Initialize storage
	_, _, tasksDir := storage.Paths()
	os.MkdirAll(tasksDir, 0755)

	// Find next available ID using O_EXCL for atomic creation
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
			ID:          id,
			Status:      StatusPending,
			Description: description,
			SpecFile:    specFile,
			CreatedAt:   time.Now().Unix(),
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
		ni, _ := strconv.Atoi(strings.TrimPrefix(tasks[i].ID, "t"))
		nj, _ := strconv.Atoi(strings.TrimPrefix(tasks[j].ID, "t"))
		return ni < nj
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
