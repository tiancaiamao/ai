package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/run"
)

// useAIAdapterForCommand returns true when the agent has a run mapping.
func useAIAdapterForCommand(agentID string) bool {
	_, err := aiAdapter.getRunIDForAgent(agentID)
	return err == nil
}

// GetAgentStatus returns status for both AI-backed and legacy agents.
func GetAgentStatus(agentID string) (*agent.Activity, error) {
	if useAIAdapterForCommand(agentID) {
		return getAgentStatusWithAI(agentID)
	}

	activity, err := agent.ReadActivity(agentID)
	if err != nil {
		return nil, err
	}

	// For raw backends (codex, etc.), check process liveness and output.
	if activity.Pid > 0 && activity.Status == "running" {
		alive := agent.IsProcessAlive(activity.Pid, activity.PidStartTime)
		if !alive {
			// Process exited. Scan output for terminal events.
			agentDir := agent.AgentDir(agentID)
			status := detectRawBackendStatus(agentDir)
			activity.Status = status
			if activity.FinishedAt == 0 {
				activity.FinishedAt = time.Now().Unix()
			}
		}
	}

	return activity, nil
}

// getAgentStatusWithAI reads status from ai run metadata.
func getAgentStatusWithAI(agentID string) (*agent.Activity, error) {
	runMeta, err := aiAdapter.GetStatus(agentID)
	if err != nil {
		return nil, err
	}

		activity := &agent.Activity{
		Status:       convertAIStatus(runMeta.Status),
		Pid:          runMeta.PID,
		StartedAt:    runMeta.StartedAt,
		Backend:      "ai",
		PidStartTime: runMeta.PidStartTime,
	}

	if runMeta.FinishedAt > 0 {
		activity.FinishedAt = runMeta.FinishedAt
	}

	// ai serve detached via Process.Release may not update run.json on exit.
	// If run.json says running but PID is dead, mark as done.
	if activity.Status == "running" && activity.Pid > 0 {
		if !agent.IsProcessAlive(activity.Pid, activity.PidStartTime) {
			activity.Status = "done"
			if activity.FinishedAt == 0 {
				activity.FinishedAt = time.Now().Unix()
			}
		}
	}

	runID, err := aiAdapter.getRunIDForAgent(agentID)
	if err != nil {
		return activity, nil
	}

	eventsPath, err := run.EventsPath(runID)
	if err != nil {
		return activity, nil
	}

	// Read only the tail of events.jsonl — the file can be gigabytes.
	// Use bytes.Contains to detect agent_end without parsing JSON,
	// which also handles partial reads from large single-line events.
		if tailData, err := readFileTail(eventsPath, 256*1024); err == nil {
		activity = enrichActivityFromEvents(activity, tailData)
	}

		return activity, nil
}

// detectRawBackendStatus scans the output file for terminal events to determine
// the final status of a raw backend (e.g. codex) agent.
func detectRawBackendStatus(agentDir string) string {
	outputPath := filepath.Join(agentDir, "output")
	data, err := readFileTail(outputPath, 64*1024)
	if err != nil {
		return "done" // No output, assume completed
	}

	// Codex emits {"type":"turn.failed",...} on failure.
	if bytes.Contains(data, []byte(`"turn.failed"`)) {
		return "failed"
	}
	// Codex emits {"type":"turn.completed",...} on success.
	if bytes.Contains(data, []byte(`"turn.completed"`)) {
		return "done"
	}
	// Fallback: if process exited with output, assume done.
	return "done"
}

// convertAIStatus maps ai run status to ag status.
func convertAIStatus(aiStatus string) string {
	switch aiStatus {
	case "running":
		return "running"
	case "done":
		return "done"
	case "failed":
		return "failed"
	case "killed":
		return "killed"
	default:
		return "unknown"
	}
}

// enrichActivityFromEvents fills lightweight details from events data.
func enrichActivityFromEvents(activity *agent.Activity, eventsData []byte) *agent.Activity {
	if activity.Status == "" {
		activity.Status = "running"
	}

	// Scan for agent_end event — if present, the agent has finished.
	// Use string search for robustness with partial reads / large single-line events.
	if bytes.Contains(eventsData, []byte(`"type":"agent_end"`)) {
		activity.Status = "done"
	}

	return activity
}

// FormatAgentStatus renders status in text or JSON format.
func FormatAgentStatus(activity *agent.Activity, format string, agentID string) {
	if format == "json" {
		data, _ := json.MarshalIndent(activity, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Agent: %s\n", agentID)
	fmt.Printf("Status: %s\n", activity.Status)
	if activity.Backend != "" {
		fmt.Printf("Backend: %s\n", activity.Backend)
	}
	if activity.Pid > 0 {
		fmt.Printf("PID: %d\n", activity.Pid)
	}
	if activity.StartedAt > 0 {
		fmt.Printf("Started: %s\n", formatTime(activity.StartedAt))
		if activity.Status == "running" {
			fmt.Printf("Uptime: %s\n", formatDuration(timeNow()-activity.StartedAt))
		}
	}
	if activity.FinishedAt > 0 {
		fmt.Printf("Finished: %s\n", formatTime(activity.FinishedAt))
	}

	if activity.LastText != "" {
		text := activity.LastText
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		fmt.Printf("Last text: %s\n", text)
	}
	if activity.Error != "" {
		fmt.Printf("Error: %s\n", activity.Error)
	}

			if activity.Backend == "ai" {
		aiRunsDir, _ := run.RunsDir()
		if _, err := os.Stat(aiRunsDir); err == nil {
			fmt.Printf("AI runs directory: %s\n", aiRunsDir)
		}

		// Show run ID for ai watch convenience
		runID, err := aiAdapter.getRunIDForAgent(agentID)
		if err == nil && runID != "" {
			fmt.Printf("Run ID: %s\n", runID)
			fmt.Printf("Watch: ai watch %s\n", runID)
		}
	}
}

// Helper functions for time formatting
func formatTime(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

func formatDuration(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}

func timeNow() int64 {
	return time.Now().Unix()
}


// readFileTail reads up to maxBytes from the end of a file.
// Efficient for large files — seeks to offset rather than reading entire file.
func readFileTail(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := fi.Size()
	if size <= maxBytes {
		return os.ReadFile(path)
	}

	// Seek to (size - maxBytes) to read the tail.
	if _, err := f.Seek(size-maxBytes, io.SeekStart); err != nil {
		return nil, err
	}

	return io.ReadAll(f)
}
