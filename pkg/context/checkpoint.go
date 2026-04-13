package context

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	CheckpointDirPattern = "checkpoint_%05d"
	CurrentLinkName      = "current"
)

// CheckpointInfo represents metadata about a checkpoint.
type CheckpointInfo struct {
	Turn                int    `json:"turn"`
	MessageIndex        int    `json:"message_index"`
	Path                string `json:"path"`
	CreatedAt           string `json:"created_at"`
	LLMContextChars     int    `json:"llm_context_chars,omitempty"`
	RecentMessagesCount int    `json:"recent_messages_count,omitempty"`
}

// CreateCheckpointDir creates a new checkpoint directory.
// Uses a sequential counter instead of turn number to avoid overwriting
// when multiple operations happen at the same turn.
func CreateCheckpointDir(sessionDir string, turn int) (*CheckpointInfo, error) {
	slog.Info("[CreateCheckpointDir] Creating checkpoint", "turn", turn)

	checkpointsDir := filepath.Join(sessionDir, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoints directory: %w", err)
	}

	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			idx = &CheckpointIndex{Checkpoints: []CheckpointInfo{}}
		} else {
			return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
		}
	}

	nextID := len(idx.Checkpoints)
	checkpointName := fmt.Sprintf(CheckpointDirPattern, nextID)
	checkpointPath := filepath.Join(checkpointsDir, checkpointName)
	slog.Info("[CreateCheckpointDir] Creating checkpoint directory", "path", checkpointPath)

	if err := os.MkdirAll(checkpointPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	info := &CheckpointInfo{
		Turn:        turn,
		MessageIndex: 0,
		Path:        filepath.Join("checkpoints", checkpointName),
		CreatedAt:   time.Now().Format(time.RFC3339),
	}

	slog.Info("[CreateCheckpointDir] Checkpoint directory created", "path", checkpointPath, "turn", turn)
	return info, nil
}

// UpdateCurrentLink updates the current/ symlink to point to the latest checkpoint.
func UpdateCurrentLink(sessionDir string, checkpointPath string) error {
	currentLinkPath := filepath.Join(sessionDir, CurrentLinkName)

	if _, err := os.Lstat(currentLinkPath); err == nil {
		if err := os.Remove(currentLinkPath); err != nil {
			return fmt.Errorf("failed to remove existing current link: %w", err)
		}
	}

	if err := os.Symlink(checkpointPath, currentLinkPath); err != nil {
		if runtime.GOOS == "windows" {
			return fmt.Errorf("failed to create symlink: %w (note: symlinks require Developer Mode on Windows)", err)
		}
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}

// LoadLatestCheckpoint loads the most recent checkpoint info.
func LoadLatestCheckpoint(sessionDir string) (*CheckpointInfo, error) {
	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
	}

	if len(idx.Checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints found")
	}

	return &idx.Checkpoints[len(idx.Checkpoints)-1], nil
}

// LoadCheckpointAtTurn loads a checkpoint at a specific turn.
func LoadCheckpointAtTurn(sessionDir string, turn int) (*CheckpointInfo, error) {
	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
	}

	info, err := idx.GetCheckpointAtTurn(turn)
	if err != nil {
		return nil, fmt.Errorf("failed to get checkpoint at turn %d: %w", turn, err)
	}

	return info, nil
}

// GetCurrentCheckpointPath returns the absolute path the current/ symlink points to.
func GetCurrentCheckpointPath(sessionDir string) (string, error) {
	currentLinkPath := filepath.Join(sessionDir, CurrentLinkName)

	target, err := os.Readlink(currentLinkPath)
	if err != nil {
		return "", fmt.Errorf("failed to read current link: %w", err)
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(sessionDir, target)
	}

	return target, nil
}