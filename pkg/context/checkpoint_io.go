package context

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveCheckpoint saves a ContextSnapshot to a checkpoint directory
func SaveCheckpoint(sessionDir string, snapshot *ContextSnapshot, turn int, messageIndex int) (*CheckpointInfo, error) {
	// 1. Create checkpoint directory
	info, err := CreateCheckpointDir(sessionDir, turn)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Update message index
	info.MessageIndex = messageIndex

	checkpointPath := filepath.Join(sessionDir, info.Path)

	// 2. Save llm_context.txt
	llmContextPath := filepath.Join(checkpointPath, "llm_context.txt")
	if err := saveAtomic(llmContextPath, []byte(snapshot.LLMContext)); err != nil {
		return nil, fmt.Errorf("failed to save llm_context.txt: %w", err)
	}

	// 3. Save agent_state.json
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	agentStateData, err := json.MarshalIndent(snapshot.AgentState, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal agent state: %w", err)
	}
	if err := saveAtomic(agentStatePath, agentStateData); err != nil {
		return nil, fmt.Errorf("failed to save agent_state.json: %w", err)
	}

	// 4. Save messages.jsonl with RecentMessages
	// RecentMessages is persisted to the checkpoint so that resume can load them
	// directly instead of replaying the entire journal from the beginning.
	if len(snapshot.RecentMessages) > 0 {
		messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
		if err := saveMessagesJSONL(messagesPath, snapshot.RecentMessages); err != nil {
			return nil, fmt.Errorf("failed to save messages.jsonl: %w", err)
		}
	}

	// Update metadata in info (for debugging/monitoring only)
	info.LLMContextChars = len(snapshot.LLMContext)
	info.RecentMessagesCount = len(snapshot.RecentMessages)

	// 5. Update checkpoint_index.json
	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
	}
	// Use AddAndSave for atomic operation to prevent race conditions
	if err := idx.AddAndSave(*info, sessionDir); err != nil {
		return nil, fmt.Errorf("failed to add and save checkpoint index: %w", err)
	}

	// 6. Update current/ symlink
	if err := UpdateCurrentLink(sessionDir, info.Path); err != nil {
		return nil, fmt.Errorf("failed to update current link: %w", err)
	}

	return info, nil
}

// LoadCheckpoint loads a ContextSnapshot from a checkpoint directory.
// Loads LLMContext and AgentState. RecentMessages will be empty.
// Per design, RecentMessages is rebuilt by replaying the journal from message_index.
func LoadCheckpoint(sessionDir string, checkpointInfo *CheckpointInfo) (*ContextSnapshot, error) {
	checkpointPath := filepath.Join(sessionDir, checkpointInfo.Path)

	// 1. Load agent_state.json
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	agentStateData, err := os.ReadFile(agentStatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent_state.json: %w", err)
	}

	var agentState AgentState
	if err := json.Unmarshal(agentStateData, &agentState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent state: %w", err)
	}

	// 2. Load llm_context.txt
	llmContextPath := filepath.Join(checkpointPath, "llm_context.txt")
	llmContextData, err := os.ReadFile(llmContextPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read llm_context.txt: %w", err)
	}

	// 3. Load messages.jsonl if present (backward compatible)
	// Newer checkpoints include RecentMessages for faster resume.
	// Older checkpoints without messages.jsonl will return empty RecentMessages
	// and the caller will replay the entire journal to rebuild them.
	recentMessages := []AgentMessage{}
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	if data, err := os.ReadFile(messagesPath); err == nil && len(data) > 0 {
		if loaded, err := loadMessagesJSONL(data); err == nil {
			recentMessages = loaded
		}
	}

	snapshot := &ContextSnapshot{
		LLMContext:     string(llmContextData),
		RecentMessages: recentMessages,
		AgentState:     agentState,
	}

	return snapshot, nil
}

// LoadCheckpointLLMContext loads only the LLM context from a checkpoint
func LoadCheckpointLLMContext(checkpointPath string) (string, error) {
	llmContextPath := filepath.Join(checkpointPath, "llm_context.txt")
	data, err := os.ReadFile(llmContextPath)
	if err != nil {
		return "", fmt.Errorf("failed to read llm_context.txt: %w", err)
	}

	return string(data), nil
}

// LoadCheckpointAgentState loads only the agent state from a checkpoint
func LoadCheckpointAgentState(checkpointPath string) (*AgentState, error) {
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	data, err := os.ReadFile(agentStatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent_state.json: %w", err)
	}

	var agentState AgentState
	if err := json.Unmarshal(data, &agentState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent state: %w", err)
	}

	return &agentState, nil
}

// saveAtomic performs an atomic write using temporary file + rename
func saveAtomic(filePath string, data []byte) error {
	// Write to temporary file
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename to actual path (atomic on most filesystems)
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath) // Clean up temp file
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// saveMessagesJSONL writes RecentMessages as JSONL to a file.
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
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var msg AgentMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // skip malformed lines
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// splitLines splits byte data into lines (without trailing newlines).
func splitLines(data []byte) [][]byte {
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
