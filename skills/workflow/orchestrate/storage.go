package orchestrate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Storage handles file system operations for team state
type Storage struct {
	root string // .ai/team/
	mu   sync.Mutex
}

// NewStorage creates a new storage instance
func NewStorage(cwd string) *Storage {
	return &Storage{
		root: filepath.Join(cwd, ".ai", "team"),
	}
}

// Root returns the root path
func (s *Storage) Root() string {
	return s.root
}

// Init initializes the team directory structure
func (s *Storage) Init() error {
	dirs := []string{
		s.root,
		filepath.Join(s.root, "tasks"),
		filepath.Join(s.root, "workers"),
		filepath.Join(s.root, "logs"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create dir %s: %w", dir, err)
		}
	}
	return nil
}

// LockHandle represents a file lock
type LockHandle struct {
	fd   *os.File
	path string
}

// AcquireTaskLock acquires a lock for a task
func (s *Storage) AcquireTaskLock(taskID string) (*LockHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lockPath := filepath.Join(s.root, "tasks", taskID+".lock")

	// O_CREAT | O_EXCL - atomic create, fails if exists
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("task %s is locked by another process", taskID)
		}
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Write lock metadata
	lockData := map[string]interface{}{
		"pid":       os.Getpid(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(lockData)
	fd.Write(data)
	fd.Sync()

	return &LockHandle{fd: fd, path: lockPath}, nil
}

// ReleaseTaskLock releases a task lock
func (s *Storage) ReleaseTaskLock(handle *LockHandle) error {
	if handle == nil {
		return nil
	}
	handle.fd.Close()
	return os.Remove(handle.path)
}

// ReadTask reads a task from file
func (s *Storage) ReadTask(taskID string) (*Task, error) {
	path := filepath.Join(s.root, "tasks", taskID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read task %s: %w", taskID, err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to parse task %s: %w", taskID, err)
	}
	return &task, nil
}

// WriteTask writes a task to file
func (s *Storage) WriteTask(task *Task) error {
	path := filepath.Join(s.root, "tasks", task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// DeleteTask deletes a task file
func (s *Storage) DeleteTask(taskID string) error {
	path := filepath.Join(s.root, "tasks", taskID+".json")
	return os.Remove(path)
}

// ListTasks lists all tasks
func (s *Storage) ListTasks() ([]*Task, error) {
	dir := filepath.Join(s.root, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read tasks dir: %w", err)
	}

	var tasks []*Task
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		taskID := entry.Name()[:len(entry.Name())-5] // remove .json
		task, err := s.ReadTask(taskID)
		if err != nil {
			continue // skip invalid tasks
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// AtomicUpdate atomically updates a task with lock
func (s *Storage) AtomicUpdate(taskID string, fn func(*Task) error) error {
	lock, err := s.AcquireTaskLock(taskID)
	if err != nil {
		return err
	}
	defer s.ReleaseTaskLock(lock)

	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}

	if err := fn(task); err != nil {
		return err
	}

	return s.WriteTask(task)
}

// WriteConfig writes team config
func (s *Storage) WriteConfig(config *TeamConfig) error {
	path := filepath.Join(s.root, "config.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// ReadConfig reads team config
func (s *Storage) ReadConfig() (*TeamConfig, error) {
	path := filepath.Join(s.root, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config TeamConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// WriteState writes team state
func (s *Storage) WriteState(state *TeamState) error {
	path := filepath.Join(s.root, "state.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// ReadState reads team state
func (s *Storage) ReadState() (*TeamState, error) {
	path := filepath.Join(s.root, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state TeamState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// WriteWorkerInbox writes worker inbox message
func (s *Storage) WriteWorkerInbox(workerName, content string) error {
	dir := filepath.Join(s.root, "workers", workerName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "inbox.md")
	return os.WriteFile(path, []byte(content), 0644)
}

// WriteWorkerStatus writes worker status
func (s *Storage) WriteWorkerStatus(status *WorkerStatus) error {
	dir := filepath.Join(s.root, "workers", status.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "status.json")
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// ReadWorkerStatus reads worker status
func (s *Storage) ReadWorkerStatus(workerName string) (*WorkerStatus, error) {
	path := filepath.Join(s.root, "workers", workerName, "status.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var status WorkerStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// AppendLog appends to task log
func (s *Storage) AppendLog(taskID, line string) error {
	path := filepath.Join(s.root, "logs", taskID+".log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), line)
	return err
}

// WriteReviewRequest writes a review request
func (s *Storage) WriteReviewRequest(req *ReviewRequest) error {
	dir := filepath.Join(s.root, "reviews")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, req.TaskID+".json")
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// ReadReviewRequest reads a review request
func (s *Storage) ReadReviewRequest(taskID string) (*ReviewRequest, error) {
	path := filepath.Join(s.root, "reviews", taskID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var req ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// ListReviewRequests lists all pending review requests
func (s *Storage) ListReviewRequests() ([]*ReviewRequest, error) {
	dir := filepath.Join(s.root, "reviews")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var requests []*ReviewRequest
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		taskID := entry.Name()[:len(entry.Name())-5]
		req, err := s.ReadReviewRequest(taskID)
		if err != nil {
			continue
		}
		// Check if already reviewed
		if _, err := s.ReadReviewResult(taskID); err == nil {
			continue // already reviewed
		}
		requests = append(requests, req)
	}
	return requests, nil
}

// DeleteReviewRequest removes a review request
func (s *Storage) DeleteReviewRequest(taskID string) error {
	path := filepath.Join(s.root, "reviews", taskID+".json")
	return os.Remove(path)
}

// WriteReviewResult writes a review result
func (s *Storage) WriteReviewResult(result ReviewResult) error {
	dir := filepath.Join(s.root, "reviews", "results")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, result.TaskID+".json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// ReadReviewResult reads a review result
func (s *Storage) ReadReviewResult(taskID string) (*ReviewResult, error) {
	path := filepath.Join(s.root, "reviews", "results", taskID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result ReviewResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReadLogs reads all log entries
func (s *Storage) ReadLogs() ([]*LogEntry, error) {
	var logs []*LogEntry

	logDir := filepath.Join(s.root, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		taskID := strings.TrimSuffix(entry.Name(), ".log")
		path := filepath.Join(logDir, entry.Name())

		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			timestamp, message := parseLogLine(line)
			logs = append(logs, &LogEntry{
				Timestamp: timestamp,
				TaskID:    taskID,
				Message:   message,
			})
		}
		_ = f.Close()
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp < logs[j].Timestamp
	})

	return logs, nil
}

func parseLogLine(line string) (timestamp, message string) {
	if strings.HasPrefix(line, "[") {
		if idx := strings.Index(line, "] "); idx > 1 {
			return line[1:idx], line[idx+2:]
		}
	}
	return "", line
}

// RequestStop writes a stop request marker for a running runtime process.
func (s *Storage) RequestStop() error {
	path := filepath.Join(s.root, "stop")
	content := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	return os.WriteFile(path, content, 0644)
}

// IsStopRequested checks whether a stop request marker exists.
func (s *Storage) IsStopRequested() bool {
	path := filepath.Join(s.root, "stop")
	_, err := os.Stat(path)
	return err == nil
}

// ClearStopRequest removes a stale stop request marker if it exists.
func (s *Storage) ClearStopRequest() error {
	path := filepath.Join(s.root, "stop")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
