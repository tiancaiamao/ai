package context

import (
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

	// 2. Save llm_context.txt
	checkpointPath := filepath.Join(sessionDir, info.Path)
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

	// Update metadata in info
	info.LLMContextChars = len(snapshot.LLMContext)
	info.RecentMessagesCount = len(snapshot.RecentMessages)

	// 4. Update checkpoint_index.json
	idx, err := LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint index: %w", err)
	}

	idx.AddCheckpoint(*info)
	if err := idx.Save(sessionDir); err != nil {
		return nil, fmt.Errorf("failed to save checkpoint index: %w", err)
	}

	// 5. Update current/ symlink
	if err := UpdateCurrentLink(sessionDir, info.Path); err != nil {
		return nil, fmt.Errorf("failed to update current link: %w", err)
	}

	// 6. Return CheckpointInfo
	return info, nil
}

// LoadCheckpoint loads a ContextSnapshot from a checkpoint directory
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

	// 3. Return ContextSnapshot with empty RecentMessages
	// RecentMessages will be loaded from journal separately
	snapshot := &ContextSnapshot{
		LLMContext:     string(llmContextData),
		RecentMessages: []AgentMessage{}, // Will be populated from journal
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
