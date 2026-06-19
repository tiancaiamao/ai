package context

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// SaveCheckpoint saves a ContextSnapshot to a checkpoint directory.
func SaveCheckpoint(sessionDir string, snapshot *ContextSnapshot, turn int, messageIndex int) (*CheckpointInfo, error) {
	info, err := CreateCheckpointDir(sessionDir, turn)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	info.MessageIndex = messageIndex
	checkpointPath := filepath.Join(sessionDir, info.Path)

	// Save agent_state.json
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	agentStateData, err := json.MarshalIndent(snapshot.AgentState, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal agent state: %w", err)
	}
	if err := saveAtomic(agentStatePath, agentStateData); err != nil {
		return nil, fmt.Errorf("failed to save agent_state.json: %w", err)
	}

	// Save messages.jsonl with RecentMessages
	if len(snapshot.RecentMessages) > 0 {
		messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
		if err := saveMessagesJSONL(messagesPath, snapshot.RecentMessages); err != nil {
			return nil, fmt.Errorf("failed to save messages.jsonl: %w", err)
		}
	}

	info.RecentMessagesCount = len(snapshot.RecentMessages)

	// Update checkpoint_index.json atomically
	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
	}
	if err := idx.AddAndSave(*info, sessionDir); err != nil {
		return nil, fmt.Errorf("failed to add and save checkpoint index: %w", err)
	}

	// Update current/ symlink
	if err := UpdateCurrentLink(sessionDir, info.Path); err != nil {
		// Log but don't fail - checkpoint is already created
		slog.Warn("Failed to update current link", "error", err, "path", info.Path)
	}

	return info, nil
}

// LoadCheckpoint loads a ContextSnapshot from a checkpoint directory.
func LoadCheckpoint(sessionDir string, checkpointInfo *CheckpointInfo) (*ContextSnapshot, error) {
	checkpointPath := filepath.Join(sessionDir, checkpointInfo.Path)

	// Load agent_state.json
	agentStateData, err := os.ReadFile(filepath.Join(checkpointPath, "agent_state.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read agent_state.json: %w", err)
	}

	var agentState AgentState
	if err := json.Unmarshal(agentStateData, &agentState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent state: %w", err)
	}

	// Load messages.jsonl if present (backward compatible)
	recentMessages := []AgentMessage{}
	if data, err := os.ReadFile(filepath.Join(checkpointPath, "messages.jsonl")); err == nil && len(data) > 0 {
		if loaded, err := loadMessagesJSONL(data); err == nil {
			recentMessages = loaded
		}
	}

	return &ContextSnapshot{
		RecentMessages: recentMessages,
		AgentState:     &agentState,
	}, nil
}

// LoadCheckpointAgentState loads only the agent state from a checkpoint.
func LoadCheckpointAgentState(checkpointPath string) (*AgentState, error) {
	data, err := os.ReadFile(filepath.Join(checkpointPath, "agent_state.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read agent_state.json: %w", err)
	}

	var agentState AgentState
	if err := json.Unmarshal(data, &agentState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent state: %w", err)
	}

	return &agentState, nil
}

func saveAtomic(filePath string, data []byte) error {
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// saveMessagesJSONL writes messages as JSONL.
func saveMessagesJSONL(filePath string, messages []AgentMessage) error {
	var buf []byte
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	return saveAtomic(filePath, buf)
}

// loadMessagesJSONL reads messages from raw JSONL bytes.
func loadMessagesJSONL(data []byte) ([]AgentMessage, error) {
	var messages []AgentMessage
	for _, line := range SplitLines(data) {
		if len(line) == 0 {
			continue
		}
		var msg AgentMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// SplitLines splits byte data by newline, skipping empty lines.
func SplitLines(data []byte) [][]byte {
	var lines [][]byte
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if len(data) > 0 {
				lines = append(lines, data)
			}
			break
		}
		if i > 0 {
			lines = append(lines, data[:i])
		}
		data = data[i+1:]
	}
	return lines
}
