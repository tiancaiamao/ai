// Package agent manages agent lifecycle: spawn, status, steer, abort, prompt,
// kill, shutdown, rm, output, wait. All operations target .ag/agents/<id>/.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/genius/ag/internal/storage"
)

// Valid agent ID: alphanumeric, underscore, hyphen, max 64 chars
var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidateID checks that an agent ID is valid.
func ValidateID(id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid agent ID %q: must be 1-64 chars, alphanumeric/underscore/hyphen only", id)
	}
	return nil
}

// AgentDir returns the storage directory for the given agent.
func AgentDir(id string) string {
	return storage.AgentDir(id)
}

// Exists returns true if the agent directory exists.
func Exists(id string) bool {
	return storage.Exists(AgentDir(id))
}

// EnsureExists validates agent ID and checks the agent directory exists.
func EnsureExists(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if !Exists(id) {
		return fmt.Errorf("agent not found: %s", id)
	}
	return nil
}

// AgentEntry is used by List to return agent summary info.
type AgentEntry struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	StartedAt int64 `json:"startedAt,omitempty"`
}

// List returns all agents with their current status.
// Reads from activity.json (no socket calls, fast).
func List() ([]AgentEntry, error) {
	agentsDir := filepath.Join(storage.BaseDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []AgentEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		activity, err := ReadActivity(id)
		if err != nil {
			// Agent dir exists but no activity.json yet — show as unknown
			result = append(result, AgentEntry{ID: id, Status: "unknown"})
			continue
		}
		result = append(result, AgentEntry{
			ID:        id,
			Status:    string(activity.Status),
			StartedAt: activity.StartedAt,
		})
	}
	return result, nil
}

// ReadActivity reads activity.json for an agent.
func ReadActivity(id string) (*Activity, error) {
	path := filepath.Join(AgentDir(id), "activity.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Activity
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Activity mirrors bridge.AgentActivity for use by CLI commands.
// Defined here to avoid circular imports (bridge imports storage, agent imports storage).
type Activity struct {
	Status      string `json:"status"`
	Pid         int    `json:"pid,omitempty"`
	StartedAt   int64  `json:"startedAt,omitempty"`
	FinishedAt  int64  `json:"finishedAt,omitempty"`
	Turns       int    `json:"turns"`
	TokensIn    int64  `json:"tokensIn"`
	TokensOut   int64  `json:"tokensOut"`
	TokensTotal int64  `json:"tokensTotal"`
	LastTool    string `json:"lastTool,omitempty"`
	LastText    string `json:"lastText,omitempty"`
	Error       string `json:"error,omitempty"`
	Backend     string `json:"backend,omitempty"`
}

// IsTerminal returns true if the agent status is terminal (won't change).
func IsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "killed"
}

// timeNow returns current Unix timestamp.
func timeNow() int64 {
	return time.Now().Unix()
}