package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// BaseDir is the root directory for all ag state.
// Can be overridden for testing.
var BaseDir = ".ag"

// Paths returns common directory paths.
func Paths() (agentsDir, channelsDir, tasksDir string) {
	return filepath.Join(BaseDir, "agents"),
		filepath.Join(BaseDir, "channels"),
		filepath.Join(BaseDir, "tasks")
}

var mu sync.Mutex

// Init creates the base directory structure.
func Init() error {
	agentsDir, channelsDir, tasksDir := Paths()
	for _, dir := range []string{agentsDir, channelsDir, tasksDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// AtomicWrite writes data to a file atomically via temp file + rename.
func AtomicWrite(path string, data []byte) error {
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// AtomicWriteJSON marshals and writes JSON atomically.
func AtomicWriteJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, data)
}

// ReadJSON reads and unmarshals a JSON file.
func ReadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// ReadStatus reads a status file (single line text).
func ReadStatus(agentDir string) string {
	data, err := os.ReadFile(filepath.Join(agentDir, "status"))
	if err != nil {
		return "unknown"
	}
	// Trim whitespace
	s := string(data)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return s
}

// WriteStatus writes a status file atomically.
func WriteStatus(agentDir, status string) error {
	return AtomicWrite(filepath.Join(agentDir, "status"), []byte(status))
}

// WriteFile writes data to a file, creating parent dirs.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AppendFile appends data to a file, creating if needed.
func AppendFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// Exists checks if a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// AgentDir returns the directory path for an agent.
func AgentDir(id string) string {
	return filepath.Join(BaseDir, "agents", id)
}

// ChannelDir returns the directory path for a channel.
func ChannelDir(name string) string {
	return filepath.Join(BaseDir, "channels", name)
}

// TaskDir returns the directory path for a task.
func TaskDir(id string) string {
	return filepath.Join(BaseDir, "tasks", id)
}
