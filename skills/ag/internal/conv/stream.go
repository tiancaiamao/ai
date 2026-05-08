package conv

import (
	"bufio"
	"io"
	"strings"
)

// HookFunc is a callback invoked for each parsed event during streaming.
// Return false to stop streaming early.
type HookFunc func(evt *FormattedEvent) bool

// StreamEvents reads newline-delimited JSON events from reader and calls
// hooks for each parsed event. It stops when the reader reaches EOF or a
// hook returns false. Returns the number of events processed.
func StreamEvents(reader io.Reader, hooks ...HookFunc) int {
	scanner := bufio.NewScanner(reader)
	const maxTokenSize = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 4096), maxTokenSize)

	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		evt := ParseEvent(line)
		if evt == nil {
			continue
		}
		count++
		for _, hook := range hooks {
			if !hook(evt) {
				return count
			}
		}
	}
	return count
}

// StreamEventsFromString parses events from a raw string (e.g. full file content).
func StreamEventsFromString(data string, hooks ...HookFunc) int {
	return StreamEvents(strings.NewReader(data), hooks...)
}

// IsAgentDone checks if a meta event indicates the agent has finished
// (either successfully or with failure). Returns true for agent_end events.
func IsAgentDone(evt *FormattedEvent) bool {
	if evt.Kind != KindMeta {
		return false
	}
	return strings.Contains(evt.Text, "agent done") ||
		strings.Contains(evt.Text, "agent failed")
}

// IsAgentSuccess checks if the agent finished successfully.
func IsAgentSuccess(evt *FormattedEvent) bool {
	if evt.Kind != KindMeta {
		return false
	}
	return strings.Contains(evt.Text, "agent done")
}

// CollectLastN returns a hook that collects the last N formatted lines
// of the specified kinds into a slice.
func CollectLastN(n int, kinds ...EventKind) (hook HookFunc, result *[]string) {
	var lines []string
	result = &lines
	hook = func(evt *FormattedEvent) bool {
		for _, k := range kinds {
			if evt.Kind == k {
				lines = append(lines, evt.Text)
				if len(lines) > n {
					lines = lines[1:]
				}
				break
			}
		}
		return true // continue streaming
	}
	return
}