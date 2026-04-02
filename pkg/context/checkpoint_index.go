package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CheckpointIndex maintains the list of all checkpoints
type CheckpointIndex struct {
	LatestCheckpointTurn  int             `json:"latest_checkpoint_turn"`
	LatestCheckpointPath  string          `json:"latest_checkpoint_path"`
	Checkpoints          []CheckpointInfo `json:"checkpoints"`
	mu                   sync.RWMutex    `json:"-"` // Protects concurrent access
}

// LoadCheckpointIndex loads the checkpoint index from disk
func LoadCheckpointIndex(sessionDir string) (*CheckpointIndex, error) {
	indexPath := filepath.Join(sessionDir, "checkpoint_index.json")

	// If file doesn't exist, return empty index
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return &CheckpointIndex{
			LatestCheckpointTurn: 0,
			LatestCheckpointPath: "",
			Checkpoints:         []CheckpointInfo{},
		}, nil
	}

	// Read existing index
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

// SaveCheckpointIndex saves the checkpoint index to disk
func (idx *CheckpointIndex) Save(sessionDir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	indexPath := filepath.Join(sessionDir, "checkpoint_index.json")

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint index: %w", err)
	}

	// Atomic write: write to temporary file then rename
	tmpPath := indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary index file: %w", err)
	}

	// Rename to actual path
	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath) // Clean up temp file
		return fmt.Errorf("failed to rename index file: %w", err)
	}

	return nil
}

// AddCheckpoint adds a new checkpoint to the index
func (idx *CheckpointIndex) AddCheckpoint(info CheckpointInfo) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Add checkpoint to list
	idx.Checkpoints = append(idx.Checkpoints, info)

	// Update latest pointers
	idx.LatestCheckpointTurn = info.Turn
	idx.LatestCheckpointPath = info.Path
}

// AddAndSave atomically adds a checkpoint and saves the index to disk.
// This prevents race conditions when multiple goroutines try to create checkpoints concurrently.
func (idx *CheckpointIndex) AddAndSave(info CheckpointInfo, sessionDir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Add checkpoint to list
	idx.Checkpoints = append(idx.Checkpoints, info)

	// Update latest pointers
	idx.LatestCheckpointTurn = info.Turn
	idx.LatestCheckpointPath = info.Path

	// Save to disk (still holding lock to ensure atomicity)
	indexPath := filepath.Join(sessionDir, "checkpoint_index.json")

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint index: %w", err)
	}

	// Atomic write: write to temporary file then rename
	tmpPath := indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary index file: %w", err)
	}

	// Rename to actual path
	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath) // Clean up temp file
		return fmt.Errorf("failed to rename index file: %w", err)
	}

	return nil
}

// GetCheckpointAtTurn retrieves checkpoint info for a specific turn
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

// GetLatestCheckpoint returns the most recent checkpoint
func (idx *CheckpointIndex) GetLatestCheckpoint() (*CheckpointInfo, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.Checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints available")
	}

	return &idx.Checkpoints[len(idx.Checkpoints)-1], nil
}

// GetCheckpointCount returns the number of checkpoints
func (idx *CheckpointIndex) GetCheckpointCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return len(idx.Checkpoints)
}
