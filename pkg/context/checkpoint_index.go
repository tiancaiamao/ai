package context

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// CheckpointIndex maintains the list of all checkpoints.
type CheckpointIndex struct {
	LatestCheckpointTurn int             `json:"latest_checkpoint_turn"`
	LatestCheckpointPath string          `json:"latest_checkpoint_path"`
	Checkpoints          []CheckpointInfo `json:"checkpoints"`
	mu                   sync.RWMutex    `json:"-"`
}

// LoadCheckpointIndex loads the checkpoint index from disk.
func LoadCheckpointIndex(sessionDir string) (*CheckpointIndex, error) {
	indexPath := filepath.Join(sessionDir, "checkpoint_index.json")

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return &CheckpointIndex{
			LatestCheckpointTurn: 0,
			LatestCheckpointPath: "",
			Checkpoints:         []CheckpointInfo{},
		}, nil
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint index: %w", err)
	}

	var idx CheckpointIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint index: %w", err)
	}

	return &idx, nil
}

// Save persists the checkpoint index to disk.
func (idx *CheckpointIndex) Save(sessionDir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.saveLocked(sessionDir)
}

// saveLocked saves to disk while already holding the lock.
func (idx *CheckpointIndex) saveLocked(sessionDir string) error {
	indexPath := filepath.Join(sessionDir, "checkpoint_index.json")

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint index: %w", err)
	}

	tmpPath := indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary index file: %w", err)
	}

	slog.Info("[saveLocked] Renaming checkpoint index", "from", tmpPath, "to", indexPath)

	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename index file: %w", err)
	}

	slog.Info("[saveLocked] Checkpoint index saved", "count", len(idx.Checkpoints))
	return nil
}

// AddCheckpoint adds a new checkpoint to the index.
func (idx *CheckpointIndex) AddCheckpoint(info CheckpointInfo) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.Checkpoints = append(idx.Checkpoints, info)
	idx.LatestCheckpointTurn = info.Turn
	idx.LatestCheckpointPath = info.Path
}

// AddAndSave atomically adds a checkpoint and saves the index to disk.
func (idx *CheckpointIndex) AddAndSave(info CheckpointInfo, sessionDir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.Checkpoints = append(idx.Checkpoints, info)
	idx.LatestCheckpointTurn = info.Turn
	idx.LatestCheckpointPath = info.Path

	slog.Info("[AddAndSave] Adding checkpoint to index", "turn", info.Turn, "path", info.Path, "total", len(idx.Checkpoints))

	return idx.saveLocked(sessionDir)
}

// GetCheckpointAtTurn retrieves checkpoint info for a specific turn.
func (idx *CheckpointIndex) GetCheckpointAtTurn(turn int) (*CheckpointInfo, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for i := range idx.Checkpoints {
		if idx.Checkpoints[i].Turn == turn {
			return &idx.Checkpoints[i], nil
		}
	}

	return nil, fmt.Errorf("checkpoint not found for turn %d", turn)
}

// GetLatestCheckpoint returns the most recent checkpoint.
func (idx *CheckpointIndex) GetLatestCheckpoint() (*CheckpointInfo, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.Checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints available")
	}

	return &idx.Checkpoints[len(idx.Checkpoints)-1], nil
}

// GetCheckpointCount returns the number of checkpoints.
func (idx *CheckpointIndex) GetCheckpointCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.Checkpoints)
}