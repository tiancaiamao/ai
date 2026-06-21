package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AgentStateFile is the filename for persisted AgentState in a session directory.
const AgentStateFile = "agent_state.json"

// SaveAgentState writes AgentState to the session directory atomically.
func SaveAgentState(sessionDir string, state *AgentState) error {
	path := filepath.Join(sessionDir, AgentStateFile)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal agent state: %w", err)
	}
	return saveAtomic(path, data)
}

// LoadAgentState reads AgentState from the session directory.
// Returns (nil, nil) when no agent_state.json exists yet.
func LoadAgentState(sessionDir string) (*AgentState, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, AgentStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read agent_state.json: %w", err)
	}

	var state AgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent state: %w", err)
	}
	return &state, nil
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
