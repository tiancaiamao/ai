package context

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	CheckpointDirPattern = "checkpoint_%05d"
	CurrentLinkName      = "current"
)

// CheckpointInfo represents metadata about a checkpoint
type CheckpointInfo struct {
	Turn               int    `json:"turn"`
	MessageIndex       int    `json:"message_index"`
	Path               string `json:"path"`
	CreatedAt          string `json:"created_at"`
	LLMContextChars    int    `json:"llm_context_chars,omitempty"`
	RecentMessagesCount int   `json:"recent_messages_count,omitempty"`
}

// CreateCheckpointDir creates a new checkpoint directory
func CreateCheckpointDir(sessionDir string, turn int) (*CheckpointInfo, error) {
	// Create checkpoints directory if it doesn't exist
	checkpointsDir := filepath.Join(sessionDir, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoints directory: %w", err)
	}

	// Create checkpoint directory with turn number
	checkpointName := fmt.Sprintf(CheckpointDirPattern, turn)
	checkpointPath := filepath.Join(checkpointsDir, checkpointName)

	if err := os.MkdirAll(checkpointPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	info := &CheckpointInfo{
		Turn:        turn,
		MessageIndex: 0, // Will be set by caller
		Path:        filepath.Join("checkpoints", checkpointName),
		CreatedAt:   time.Now().Format(time.RFC3339),
	}

	return info, nil
}

// UpdateCurrentLink updates the current/ symlink to point to the latest checkpoint
func UpdateCurrentLink(sessionDir string, checkpointPath string) error {
	currentLinkPath := filepath.Join(sessionDir, CurrentLinkName)
	targetPath := checkpointPath

	// Remove existing symlink if it exists
	if _, err := os.Lstat(currentLinkPath); err == nil {
		if err := os.Remove(currentLinkPath); err != nil {
			return fmt.Errorf("failed to remove existing current link: %w", err)
		}
	}

	// Create new symlink
	// On Windows, we need to handle junctions differently, but Windows 10+ supports symlinks
	if err := os.Symlink(targetPath, currentLinkPath); err != nil {
		// If symlink fails on Windows, try creating a junction
		if runtime.GOOS == "windows" {
			// Fallback to directory junction logic could go here
			// For now, return error
			return fmt.Errorf("failed to create symlink: %w (note: symlinks require Developer Mode on Windows)", err)
		}
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}

// LoadLatestCheckpoint loads the most recent checkpoint
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

// LoadCheckpointAtTurn loads a checkpoint at a specific turn
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

// GetCurrentCheckpointPath returns the absolute path that the current/ symlink points to
func GetCurrentCheckpointPath(sessionDir string) (string, error) {
	currentLinkPath := filepath.Join(sessionDir, CurrentLinkName)

	// Read the symlink
	target, err := os.Readlink(currentLinkPath)
	if err != nil {
		return "", fmt.Errorf("failed to read current link: %w", err)
	}

	// Convert to absolute path if relative
	if !filepath.IsAbs(target) {
		target = filepath.Join(sessionDir, target)
	}

	return target, nil
}
