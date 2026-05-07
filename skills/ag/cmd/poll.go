package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/spf13/cobra"
)

var (
	pollTimeout int
	pollFromOff int64
	pollEvent   string
	pollQuiesce int
)

var agentPollCmd = &cobra.Command{
	Use:   "poll <id> [text]",
	Short: "Block until agent output meets a condition",
	Long: `Poll agent output until a condition is met.

Modes of operation:
1. Text search:     ag agent poll <id> "some text" --timeout 120
2. Event wait:      ag agent poll <id> "" --event turn_end --timeout 300
3. Quiesce detect:  ag agent poll <id> "" --quiesce 15 --timeout 300

Text search scans all assistant text in events.jsonl (AI agents) or stream.log (legacy).
Event wait blocks until a specific event type appears in events.jsonl.
Quiesce detect waits for events.jsonl to stop growing for N seconds — the most reliable
way to detect that an agent has finished a turn.

Use --from-offset to skip already-scanned content.

Examples:
  ag agent poll worker-1 "DONE" --timeout 120
  ag agent poll worker-1 "" --event turn_end --timeout 300
  ag agent poll worker-1 "" --quiesce 15 --timeout 300
  ag agent poll worker-1 "COMPLETE" --from-offset 4096 --timeout 60`,
	Args: cobra.MaximumNArgs(2),
	RunE: runAgentPoll,
}

func init() {
	agentPollCmd.Flags().IntVar(&pollTimeout, "timeout", 120, "Timeout in seconds (0 = no timeout)")
	agentPollCmd.Flags().Int64Var(&pollFromOff, "from-offset", 0, "Byte offset cursor to start scanning from")
	agentPollCmd.Flags().StringVar(&pollEvent, "event", "", "Wait for this event type (e.g. turn_end, agent_end)")
	agentPollCmd.Flags().IntVar(&pollQuiesce, "quiesce", 0, "Wait for events.jsonl to stop growing for N seconds")
}

func runAgentPoll(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("requires agent id")
	}
	id := args[0]
	needle := ""
	if len(args) > 1 {
		needle = args[1]
	}

	if err := agent.EnsureExists(id); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(pollTimeout) * time.Second)
	if pollTimeout == 0 {
		deadline = time.Now().Add(365 * 24 * time.Hour)
	}

	// Mode 1: Quiesce detection
	if pollQuiesce > 0 {
		return pollForQuiesce(id, time.Duration(pollQuiesce)*time.Second, deadline)
	}

	// Mode 2: Event-based polling (AI backend only)
	if pollEvent != "" {
		return pollForEvent(id, pollEvent, deadline)
	}

	// Mode 3: Text-based polling
	if needle == "" {
		return fmt.Errorf("must specify text, --event, or --quiesce")
	}

	offset := pollFromOff

	for {
		found, newOffset, err := scanForText(id, needle, offset)
		if err != nil {
			return err
		}
		if found {
			fmt.Printf("found at offset %d\n", newOffset)
			return nil
		}
		offset = newOffset

		// Check agent terminal state
		act, _ := GetAgentStatus(id)
		if act != nil && agent.IsTerminal(act.Status) {
			found, newOffset, _ = scanForText(id, needle, offset)
			if found {
				fmt.Printf("found at offset %d\n", newOffset)
				return nil
			}
			return fmt.Errorf("agent ended (%s) without producing \"%s\"", act.Status, needle)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %ds waiting for \"%s\"", pollTimeout, needle)
		}

		time.Sleep(2 * time.Second)
	}
}

// pollForQuiesce waits for events.jsonl to stop growing for quiesce duration.
// This is the most reliable way to detect agent turn completion — regardless of
// what the agent does internally (thinking, tool calls, multiple messages).
func pollForQuiesce(id string, quiesce time.Duration, deadline time.Time) error {
	if !useAIAdapterForCommand(id) {
		return fmt.Errorf("--quiesce requires AI-backed agent")
	}

	runID, err := aiAdapter.getRunIDForAgent(id)
	if err != nil {
		return fmt.Errorf("get run ID: %w", err)
	}

	homeDir, _ := os.UserHomeDir()
	eventsFile := filepath.Join(homeDir, ".ai", "runs", runID, "events.jsonl")

	var lastSize int64
	var stableSince time.Time
	initialized := false

	for {
		f, err := os.Open(eventsFile)
		if err != nil {
			if os.IsNotExist(err) {
				time.Sleep(2 * time.Second)
				continue
			}
			return err
		}
		fi, _ := f.Stat()
		size := fi.Size()
		f.Close()

		if !initialized {
			// First observation — set baseline and start quiesce timer
			lastSize = size
			stableSince = time.Now()
			initialized = true
		} else if size > lastSize {
			// File grew — reset quiesce timer
			lastSize = size
			stableSince = time.Now()
		}
		// else: size unchanged, timer continues

		if initialized && time.Since(stableSince) >= quiesce {
			fmt.Printf("quiesced for %v (offset %d)\n", quiesce, size)
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %ds (last stable %.0fs ago, need %v)",
				pollTimeout, time.Since(stableSince).Seconds(), quiesce)
		}

		time.Sleep(2 * time.Second)
	}
}

// pollForEvent waits for a specific event type in events.jsonl.
func pollForEvent(id, eventType string, deadline time.Time) error {
	if !useAIAdapterForCommand(id) {
		return fmt.Errorf("--event flag requires AI-backed agent")
	}

	runID, err := aiAdapter.getRunIDForAgent(id)
	if err != nil {
		return fmt.Errorf("get run ID: %w", err)
	}

	homeDir, _ := os.UserHomeDir()
	eventsFile := filepath.Join(homeDir, ".ai", "runs", runID, "events.jsonl")

	lastSize := pollFromOff

	for {
		f, err := os.Open(eventsFile)
		if err != nil {
			if os.IsNotExist(err) {
				time.Sleep(2 * time.Second)
				continue
			}
			return err
		}

		fi, _ := f.Stat()
		size := fi.Size()

		if size > lastSize {
			if _, err := f.Seek(lastSize, 0); err != nil {
				f.Close()
				return err
			}

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}

				var event map[string]any
				if err := json.Unmarshal(line, &event); err != nil {
					continue
				}

				if t, ok := event["type"].(string); ok && t == eventType {
					f.Close()
					fmt.Printf("event %s found at offset %d\n", eventType, size)
					return nil
				}
			}
			lastSize = size
		}
		f.Close()

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %ds waiting for event \"%s\"", pollTimeout, eventType)
		}

		time.Sleep(1 * time.Second)
	}
}

// scanForText checks if needle appears in agent output starting from offset.
func scanForText(id, needle string, offset int64) (bool, int64, error) {
	if useAIAdapterForCommand(id) {
		return scanAIEvents(id, needle, offset)
	}
	return scanStreamLog(id, needle, offset)
}

// scanAIEvents scans assistant text in events.jsonl for AI-backed agents.
func scanAIEvents(id, needle string, offset int64) (bool, int64, error) {
	runID, err := aiAdapter.getRunIDForAgent(id)
	if err != nil {
		return false, offset, fmt.Errorf("get run ID: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, offset, err
	}

	eventsFile := filepath.Join(homeDir, ".ai", "runs", runID, "events.jsonl")
	f, err := os.Open(eventsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, offset, nil
		}
		return false, offset, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, offset, err
	}
	newOffset := fi.Size()

	if offset >= newOffset {
		return false, offset, nil
	}

	if _, err := f.Seek(offset, 0); err != nil {
		return false, newOffset, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var textBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		etype, _ := event["type"].(string)

		// Extract text from message_update events
		if etype == "message_update" {
			if msg, ok := event["message"].(map[string]any); ok {
				if content, ok := msg["content"].([]any); ok {
					for _, item := range content {
						if ci, ok := item.(map[string]any); ok {
							if t, ok := ci["type"].(string); ok && t == "text" {
								if s, ok := ci["text"].(string); ok {
									textBuf.WriteString(s)
								}
							}
						}
					}
				}
			}
		}

		// Also check tool_execution_end for text output
		if etype == "tool_execution_end" {
			if result, ok := event["result"]; ok {
				if resultMap, ok := result.(map[string]any); ok {
					if content, ok := resultMap["content"].([]any); ok {
						for _, item := range content {
							if ci, ok := item.(map[string]any); ok {
								if t, ok := ci["type"].(string); ok && t == "text" {
									if s, ok := ci["text"].(string); ok {
										textBuf.WriteString(s)
									}
								}
							}
						}
					}
				} else if s, ok := result.(string); ok {
					textBuf.WriteString(s)
				}
			}
		}
	}

	allText := textBuf.String()
	return strings.Contains(allText, needle), newOffset, nil
}

// scanStreamLog scans stream.log for legacy bridge agents.
func scanStreamLog(id, needle string, offset int64) (bool, int64, error) {
	agentDir := agent.AgentDir(id)
	streamPath := filepath.Join(agentDir, "stream.log")

	f, err := os.Open(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, offset, nil
		}
		return false, offset, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, offset, err
	}
	newOffset := fi.Size()

	if offset >= newOffset {
		return false, offset, nil
	}

	if _, err := f.Seek(offset, 0); err != nil {
		return false, newOffset, err
	}

	data := make([]byte, newOffset-offset)
	if _, err := f.Read(data); err != nil {
		return false, newOffset, err
	}

	return strings.Contains(string(data), needle), newOffset, nil
}