// Package run provides a unified interface for interacting with ai run artifacts
// stored under ~/.ai/runs/<runID>/. All path construction for events.jsonl,
// run.json, and other run metadata goes through this package.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RunsDir returns the base directory for all ai runs (~/.ai/runs/).
func RunsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(homeDir, ".ai", "runs"), nil
}

// Dir returns the directory for a specific run (~/.ai/runs/<runID>/).
func Dir(runID string) (string, error) {
	base, err := RunsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runID), nil
}

// EventsPath returns the path to events.jsonl for a run.
func EventsPath(runID string) (string, error) {
	dir, err := Dir(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "events.jsonl"), nil
}

// MetaPath returns the path to run.json for a run.
func MetaPath(runID string) (string, error) {
	dir, err := Dir(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run.json"), nil
}

// RunMeta represents the metadata stored in run.json.
type RunMeta struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	CWD        string `json:"cwd"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Name       string `json:"name"`
	ParentRun  string `json:"parent_run"`
}

// ReadMeta reads and parses run.json for a given run ID.
func ReadMeta(runID string) (*RunMeta, error) {
	path, err := MetaPath(runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read run meta %s: %w", runID, err)
	}
	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse run meta %s: %w", runID, err)
	}
	return &meta, nil
}