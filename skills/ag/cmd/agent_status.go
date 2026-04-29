package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/genius/ag/internal/agent"
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

	return agent.ReadActivity(agentID)
}

// getAgentStatusWithAI reads status from ai run metadata.
func getAgentStatusWithAI(agentID string) (*agent.Activity, error) {
	runMeta, err := aiAdapter.GetStatus(agentID)
	if err != nil {
		return nil, err
	}

	activity := &agent.Activity{
		Status:    convertAIStatus(runMeta.Status),
		Pid:       runMeta.PID,
		StartedAt: runMeta.StartedAt,
		Backend:   "ai",
	}

	if runMeta.FinishedAt > 0 {
		activity.FinishedAt = runMeta.FinishedAt
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return activity, nil
	}

	runID, err := aiAdapter.getRunIDForAgent(agentID)
	if err != nil {
		return activity, nil
	}

	baseDir := filepath.Join(homeDir, ".ai", "runs")
	eventsFile := filepath.Join(baseDir, runID, "events.jsonl")

	if data, err := os.ReadFile(eventsFile); err == nil {
		activity = enrichActivityFromEvents(activity, data)
	}

	return activity, nil
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
		homeDir, _ := os.UserHomeDir()
		aiDir := filepath.Join(homeDir, ".ai", "runs")
		if _, err := os.Stat(aiDir); err == nil {
			fmt.Printf("AI runs directory: %s\n", aiDir)
		}
	}
}

// Helper functions for time formatting
func formatTime(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

func formatDuration(seconds int64) string {
	return time.Duration(seconds).String()
}

func timeNow() int64 {
	return time.Now().Unix()
}
