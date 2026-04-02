package testutil

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ToolCallRecord represents a recorded tool call and its result.
type ToolCallRecord struct {
	ToolCallID string `yaml:"tool_call_id"`
	ToolName   string `yaml:"tool_name"`
	// Arguments is JSON-encoded tool arguments
	Arguments string `yaml:"arguments"`
	// Result is JSON-encoded tool result content blocks
	Result string `yaml:"result"`
	// IsError indicates whether the tool call failed
	IsError bool `yaml:"is_error"`
}

// ToolJournal records and replays tool call results.
// Stored as a YAML file alongside the VCR cassette.
type ToolJournal struct {
	dir      string
	name     string
	records  []ToolCallRecord
}

// NewToolJournal creates a new tool journal.
func NewToolJournal(dir, name string) *ToolJournal {
	return &ToolJournal{
		dir:  dir,
		name: name,
	}
}

// Record adds a tool call record to the journal.
func (j *ToolJournal) Record(toolCallID, toolName, arguments, result string, isError bool) {
	j.records = append(j.records, ToolCallRecord{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Arguments:  arguments,
		Result:     result,
		IsError:    isError,
	})
}

// Save writes the journal to disk.
func (j *ToolJournal) Save() error {
	if len(j.records) == 0 {
		return nil
	}

	data, err := yaml.Marshal(struct {
		ToolCalls []ToolCallRecord `yaml:"tool_calls"`
	}{
		ToolCalls: j.records,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal tool journal: %w", err)
	}

	if err := os.MkdirAll(j.dir, 0755); err != nil {
		return fmt.Errorf("failed to create journal directory: %w", err)
	}

	path := j.path()
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write tool journal: %w", err)
	}

	return nil
}

// Load reads the journal from disk.
func (j *ToolJournal) Load() ([]ToolCallRecord, error) {
	path := j.path()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read tool journal: %w", err)
	}

	var journal struct {
		ToolCalls []ToolCallRecord `yaml:"tool_calls"`
	}
	if err := yaml.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("failed to parse tool journal: %w", err)
	}

	return journal.ToolCalls, nil
}

// path returns the file path for the tool journal.
func (j *ToolJournal) path() string {
	return filepath.Join(j.dir, j.name+"_tools.yaml")
}
