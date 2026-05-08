package conv

import (
	"bufio"
	"io"
	"os"
	"time"
)

// WatchFile watches an events file for new lines and calls hooks for each
// parsed event. It polls the file at the given interval. Stops when stopCh
// is closed or the file is deleted.
//
// This is designed for monitoring agent runs in real-time (like tail -f but
// with event parsing and hooks).
func WatchFile(filePath string, pollInterval time.Duration, stopCh <-chan struct{}, hooks ...HookFunc) error {
	var offset int64

	for {
		select {
		case <-stopCh:
			return nil
		default:
		}

				f, err := os.Open(filePath)
		if err != nil {
			// If we've read before, the file was deleted — stop watching.
			// Otherwise, the file hasn't been created yet — wait and retry.
			if offset > 0 {
				return nil
			}
			time.Sleep(pollInterval)
			continue
		}

		// Seek to where we left off
		if offset > 0 {
			f.Seek(offset, io.SeekStart)
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 4096), 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			evt := ParseEvent(line)
			if evt == nil {
				continue
			}
			for _, hook := range hooks {
				if !hook(evt) {
					f.Close()
					return nil
				}
			}
		}

		// Remember where we read up to
		offset, _ = f.Seek(0, io.SeekCurrent)
		f.Close()

		time.Sleep(pollInterval)
	}
}

// WatchUntilDone watches an events file until an agent_end event is detected.
// Returns the formatted summary lines collected during the watch.
// This is the convenience API for scheduler's checkAIServeRun.
func WatchUntilDone(filePath string, pollInterval time.Duration, stopCh <-chan struct{}) (done bool, summary string) {
	lastNHook, result := CollectLastN(20, KindTool, KindMeta)

	doneHook := func(evt *FormattedEvent) bool {
		return !IsAgentDone(evt)
	}

	_ = WatchFile(filePath, pollInterval, stopCh, lastNHook, doneHook)

	// Check if we stopped because of agent_end or because stopCh closed
	// Re-scan the file to check final state
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, ""
	}

	agentDone := false
			_, _ = StreamEventsFromString(string(data), func(evt *FormattedEvent) bool {
		if IsAgentDone(evt) {
			agentDone = true
			return false
		}
		return true
	})

	return agentDone, joinLines(*result)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := ""
	for _, l := range lines {
		if l != "" {
			result += l + "\n"
		}
	}
	return result
}