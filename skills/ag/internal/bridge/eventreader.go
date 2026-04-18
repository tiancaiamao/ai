package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// EventReader reads newline-delimited JSON RPC responses from an io.Reader
// (typically ai's stdout pipe) and feeds events to an ActivityWriter.
type EventReader struct {
	scanner  *bufio.Scanner
	writer   *ActivityWriter
	output   strings.Builder
	outputDir string
}

// NewEventReader creates a new EventReader that reads from r and updates w.
// outputDir is the agent directory where final output will be written.
func NewEventReader(r io.Reader, writer *ActivityWriter, outputDir string) *EventReader {
	s := bufio.NewScanner(r)
	// bufio.Scanner defaults to 64KB max token, which is too small for
	// large message_update events. We set the max to 10MB but the scanner
	// starts with a small buffer and grows on demand — no upfront 10MB.
	const maxTokenSize = 10 * 1024 * 1024
	s.Buffer(make([]byte, 0, 4096), maxTokenSize)
	return &EventReader{
		scanner:   s,
		writer:    writer,
		outputDir: outputDir,
	}
}

// Run blocks until EOF or a fatal error. It reads each line from the input,
// parses it as a JSON RPC response, and dispatches events to the ActivityWriter.
// On EOF (ai process exited normally), it returns nil.
func (er *EventReader) Run() error {
	for er.scanner.Scan() {
		line := er.scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse as generic map for flexibility
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			// JSON parse error: log warning, skip line, continue
			log.Printf("eventreader: skipping unparseable line: %v", err)
			continue
		}

		er.handleEvent(evt)
	}

	// Scanner stopped; check if it was due to an error
	if err := er.scanner.Err(); err != nil {
		return fmt.Errorf("eventreader: scanner error: %w", err)
	}

	// EOF is normal (ai process exited). Write final output file.
	er.writeOutputFile()

	return nil
}

// Output returns the accumulated output text from all message_update events.
func (er *EventReader) Output() string {
	return er.output.String()
}

// handleEvent dispatches a single parsed event to the appropriate handler.
func (er *EventReader) handleEvent(evt map[string]any) {
	eventType, _ := evt["type"].(string)

	switch eventType {
	case "agent_start":
		er.writer.UpdateStatus(StatusRunning)

	case "agent_end":
		er.handleAgentEnd(evt)

	case "turn_start":
		er.writer.UpdateActivity(func(a *AgentActivity) {
			a.Turns++
		})

	case "turn_end":
		er.handleTurnEnd(evt)

	case "tool_execution_start":
		er.handleToolExecutionStart(evt)

	case "tool_execution_end":
		// Write immediately to reflect tool completion
		er.writer.UpdateActivity(func(a *AgentActivity) {
			// No specific field to update; just flush state
		})

	case "message_update":
		er.handleMessageUpdate(evt)

	case "error":
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error from RPC event stream"
		}
		er.writer.SetError(errMsg)

	default:
		// Unknown event type; ignore silently
	}
}

// handleAgentEnd processes an agent_end event, setting final status.
func (er *EventReader) handleAgentEnd(evt map[string]any) {
	success, _ := evt["success"].(bool)

	if success {
		er.writer.UpdateStatus(StatusDone)
	} else {
		// Check for error field
		errMsg, _ := evt["error"].(string)
		if errMsg != "" {
			er.writer.SetError(errMsg)
		} else {
			er.writer.UpdateStatus(StatusFailed)
		}
	}
}

// handleTurnEnd processes a turn_end event. May contain token count data.
// Writes immediately.
func (er *EventReader) handleTurnEnd(evt map[string]any) {
	data, _ := evt["data"].(map[string]any)

	er.writer.UpdateActivity(func(a *AgentActivity) {
		if data != nil {
			before, hasBefore := toInt64(data["tokensBefore"])
			after, hasAfter := toInt64(data["tokensAfter"])
			if hasBefore && hasAfter && after >= before {
				// Accumulate turn tokens into totals
				a.TokensIn += before
				a.TokensOut += after - before
				a.TokensTotal = a.TokensIn + a.TokensOut
			}
		}
	})
}

// handleToolExecutionStart processes a tool_execution_start event.
// Sets LastTool from Data.tool and writes immediately.
func (er *EventReader) handleToolExecutionStart(evt map[string]any) {
	data, _ := evt["data"].(map[string]any)
	toolName, _ := data["tool"].(string)

	er.writer.UpdateActivity(func(a *AgentActivity) {
		a.LastTool = toolName
	})
}

// handleMessageUpdate processes a message_update event.
// Appends text_delta to accumulated output and updates LastText.
// Writes are rate-limited by ActivityWriter.
func (er *EventReader) handleMessageUpdate(evt map[string]any) {
	data, _ := evt["data"].(map[string]any)
	textDelta, _ := data["text_delta"].(string)

	if textDelta == "" {
		return
	}

	er.output.WriteString(textDelta)

	er.writer.UpdateActivity(func(a *AgentActivity) {
		a.LastText = er.output.String()
	})
}

// writeOutputFile writes the accumulated output to the "output" file in the agent directory.
func (er *EventReader) writeOutputFile() {
	text := er.output.String()
	if text == "" {
		return
	}

	outputPath := filepath.Join(er.outputDir, "output")
	if err := os.MkdirAll(er.outputDir, 0755); err != nil {
		log.Printf("eventreader: failed to create output dir: %v", err)
		return
	}

	if err := os.WriteFile(outputPath, []byte(text), 0644); err != nil {
		log.Printf("eventreader: failed to write output file: %v", err)
	}
}

// toInt64 converts a JSON number (float64) or int to int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		return 0, false
	default:
		return 0, false
	}
}