package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AgentEndInfo holds summary information extracted from an agent_end event.
type AgentEndInfo struct {
	Found   bool   `json:"found"`
	Success bool   `json:"success"`
	Turns   int    `json:"turns,omitempty"`
	Error   string `json:"error,omitempty"`
}

// FindLastAgentEnd scans events.jsonl from the end to find the last agent_end event.
// It reads the file backwards in chunks to avoid loading the entire file into memory.
// Returns nil if no agent_end event is found.
func FindLastAgentEnd(eventsPath string) *AgentEndInfo {
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil
	}
	size := stat.Size()
	if size == 0 {
		return nil
	}

	// Read backwards from the end of the file, looking for lines containing "agent_end".
	// We read in chunks of up to 64KB, scanning each chunk for complete lines.
	const chunkSize = 64 * 1024
	buf := make([]byte, chunkSize)

	// Collect candidate lines (from newest to oldest).
	var candidates []string

	offset := size
	for offset > 0 && len(candidates) < 10 {
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		_, err := f.ReadAt(buf[:readSize], offset)
		if err != nil {
			break
		}

		// Split into lines. Since we're reading from the middle of the file,
		// the first line may be incomplete (unless offset == 0).
		data := string(buf[:readSize])
		lines := strings.Split(data, "\n")

		// Determine start index: skip first line if it's not at the beginning of file
		startIdx := 0
		if offset > 0 {
			startIdx = 1 // first line is a fragment
		}

		for i := len(lines) - 1; i >= startIdx; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			if strings.Contains(line, `"agent_end"`) {
				candidates = append(candidates, line)
			}
		}
	}

	// Parse candidates to find the last valid agent_end.
	for _, line := range candidates {
		info := parseAgentEndLine(line)
		if info != nil {
			return info
		}
	}
	return nil
}

// parseAgentEndLine parses a single JSONL line looking for agent_end event.
func parseAgentEndLine(line string) *AgentEndInfo {
	var evt map[string]any
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return nil
	}
	eventType, _ := evt["type"].(string)
	if eventType != "agent_end" {
		return nil
	}

	info := &AgentEndInfo{
		Found:   true,
		Success: true,
	}

	if success, ok := evt["success"].(bool); ok {
		info.Success = success
	}

	if errMsg, ok := evt["error"].(string); ok && errMsg != "" {
		info.Error = truncateString(errMsg, 100)
	}

	// Count turns by scanning events for turn_start — but we don't have
	// access to the full file here. Instead, extract turns if available.
	if turns, ok := evt["turns"].(float64); ok {
		info.Turns = int(turns)
	}

	return info
}

// FindLastAgentEndFast is a faster version that only checks if an agent_end
// event exists in the last N bytes of events.jsonl, using bufio.Scanner
// from a seeked position. Suitable for ai ls where speed matters.
func FindLastAgentEndFast(eventsPath string) *AgentEndInfo {
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil
	}
	size := stat.Size()
	if size == 0 {
		return nil
	}

	// Only scan the last 1MB — agent_end is always at the end of a session.
	// If the last 1MB doesn't contain it, the agent hasn't ended.
	const tailSize = 1024 * 1024
	offset := size - tailSize
	if offset < 0 {
		offset = 0
	}

	_, err = f.Seek(offset, 0)
	if err != nil {
		return nil
	}

	var lastAgentEndLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"agent_end"`) {
			lastAgentEndLine = line
		}
	}

	if lastAgentEndLine == "" {
		return nil
	}
	return parseAgentEndLine(lastAgentEndLine)
}

// truncateString truncates s to maxLen, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// FormatAgentStatus returns a human-readable status string for ls output.
func FormatAgentStatus(meta *RunMeta, endInfo *AgentEndInfo) string {
	if !IsRunning(meta) {
		return meta.Status // already terminal
	}

	// Running process — check if it has completed at least one prompt cycle.
	if endInfo != nil && endInfo.Found {
		if endInfo.Success {
			return "idle" // finished a prompt, waiting for next
		}
		return fmt.Sprintf("ended:%s", truncateString(endInfo.Error, 30))
	}
	return "running"
}