package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/genius/ag/internal/conv"
)

// EventReader reads newline-delimited JSON RPC responses from an io.Reader
// (typically ai's stdout pipe) and feeds events to an ActivityWriter and StreamWriter.
type EventReader struct {
	scanner      *bufio.Scanner
	writer       *ActivityWriter
	streamWriter *StreamWriter
	outputDir    string
}

// NewEventReader creates a new EventReader that reads from r and updates w.
// outputDir is the agent directory. streamWriter may be nil (graceful degradation).
func NewEventReader(r io.Reader, writer *ActivityWriter, streamWriter *StreamWriter, outputDir string) *EventReader {
	s := bufio.NewScanner(r)
	const maxTokenSize = 10 * 1024 * 1024
	s.Buffer(make([]byte, 0, 4096), maxTokenSize)
	return &EventReader{
		scanner:      s,
		writer:       writer,
		streamWriter: streamWriter,
		outputDir:    outputDir,
	}
}

// Run blocks until EOF or a fatal error. It reads each line from the input,
// parses it as a JSON RPC response, and dispatches events.
// On EOF (ai process exited normally), it returns nil.
func (er *EventReader) Run() error {
	var stopFlusher func()
	if er.streamWriter != nil {
		stopFlusher = er.streamWriter.RunFlusher()
	}

	for er.scanner.Scan() {
		line := er.scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			log.Printf("eventreader: skipping unparseable line: %v", err)
			continue
		}

		er.handleEvent(evt)
	}

	if stopFlusher != nil {
		stopFlusher()
	}

	if err := er.scanner.Err(); err != nil {
		return fmt.Errorf("eventreader: scanner error: %w", err)
	}

	return nil
}

// handleEvent dispatches a single parsed event to the appropriate handler.
func (er *EventReader) handleEvent(evt map[string]any) {
	eventType, _ := evt["type"].(string)

	switch eventType {
	case "agent_start":
		er.writer.UpdateStatus(StatusRunning)
		er.writeStreamMeta("--- agent started ---")

	case "agent_end":
		er.handleAgentEnd(evt)

	case "turn_start":
		er.writer.UpdateActivity(func(a *AgentActivity) {
			a.Turns++
		})
		er.writeStreamMeta("--- turn ---")

	case "turn_end":
		er.handleTurnEnd(evt)

	case "tool_execution_start":
		er.handleToolExecutionStart(evt)

	case "tool_execution_end":
		// No action needed

	case "message_update":
		er.handleMessageUpdate(evt)

	case "error":
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error from RPC event stream"
		}
		er.writer.SetError(errMsg)
		er.writeStreamMeta("❌ error: " + errMsg)

	default:
		// Unknown event type; ignore silently
	}
}

func (er *EventReader) handleAgentEnd(evt map[string]any) {
	errMsg, _ := evt["error"].(string)
	if errMsg != "" {
		er.writer.SetError(errMsg)
		er.writeStreamMeta("--- agent failed: " + errMsg + " ---")
		return
	}

	if success, ok := evt["success"].(bool); ok && !success {
		er.writer.UpdateStatus(StatusFailed)
		er.writeStreamMeta("--- agent failed ---")
		return
	}

	er.writer.UpdateStatus(StatusDone)
	er.writeStreamMeta("--- agent done ---")
}

func (er *EventReader) handleTurnEnd(evt map[string]any) {
	er.writer.UpdateActivity(func(a *AgentActivity) {
		data, _ := evt["data"].(map[string]any)
		if data != nil {
			before, hasBefore := toInt64(data["tokensBefore"])
			after, hasAfter := toInt64(data["tokensAfter"])
			if hasBefore && hasAfter && after >= before {
				a.TokensIn += before
				a.TokensOut += after - before
				a.TokensTotal = a.TokensIn + a.TokensOut
				return
			}
		}

		msg, _ := evt["message"].(map[string]any)
		if msg != nil {
			usage, _ := msg["usage"].(map[string]any)
			if usage != nil {
				input, hasInput := toInt64(usage["input"])
				output, hasOutput := toInt64(usage["output"])
				if hasInput && hasOutput {
					a.TokensIn += input
					a.TokensOut += output
					a.TokensTotal = a.TokensIn + a.TokensOut
				}
			}
		}
	})
}

func (er *EventReader) handleToolExecutionStart(evt map[string]any) {
	toolName := conv.ExtractToolName(evt)
	if toolName == "" {
		return
	}

	er.writer.UpdateActivity(func(a *AgentActivity) {
		a.LastTool = toolName
	})

	if er.streamWriter != nil {
		detail := ""
		// Extract detail from the event
		var args map[string]any
		if data, ok := evt["data"].(map[string]any); ok {
			args, _ = data["args"].(map[string]any)
		}
		if args == nil {
			args, _ = evt["args"].(map[string]any)
		}
		if args != nil {
			for _, key := range []string{"path", "file", "command", "pattern", "query", "url"} {
				if v, ok := args[key]; ok {
					detail = fmt.Sprintf(" %s=%v", key, v)
					break
				}
			}
		}
		er.streamWriter.AppendToolCall(toolName, detail)
	}
}

func (er *EventReader) handleMessageUpdate(evt map[string]any) {
	textDelta := conv.ExtractTextDelta(evt)
	if textDelta == "" {
		return
	}

	// Write to stream.log immediately (no memory accumulation)
	if er.streamWriter != nil {
		er.streamWriter.AppendText(textDelta)
	}

	// Update activity.json LastText from stream writer's ring buffer
	er.writer.UpdateActivity(func(a *AgentActivity) {
		if er.streamWriter != nil {
			a.LastText = er.streamWriter.LastText()
		}
	})
}

func (er *EventReader) writeStreamMeta(text string) {
	if er.streamWriter != nil {
		er.streamWriter.AppendMeta(text)
	}
}

// Output returns the accumulated text output.
// It reads from stream.log if available, otherwise returns empty string.
func (er *EventReader) Output() string {
	if er.streamWriter != nil {
		return er.streamWriter.LastText()
	}
	return ""
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