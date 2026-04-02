// Package agent provides session replay functionality for regression testing.
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ============================================================================
// Session Recording
// ============================================================================

// RecordedSession represents a recorded session extracted from messages.jsonl and trace.
type RecordedSession struct {
	SessionID string
	CWD       string
	Messages  []RecordedMessage
	Events    []RecordedEvent
}

// RecordedMessage represents a single message in the session.
type RecordedMessage struct {
	ID        string
	ParentID  string
	Role      string // "user", "assistant", "toolResult"
	Content   []agentctx.ContentBlock
	Timestamp int64

	// Assistant-specific fields
	ToolCalls []ToolCallInfo

	// ToolResult-specific fields
	ToolCallID string
	ToolName   string
	Truncated  bool
}

// ToolCallInfo represents a tool call from assistant message.
type ToolCallInfo struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// RecordedEvent represents a trace event (for verification).
type RecordedEvent struct {
	Name string
	Args map[string]any
	Ts   int64
}

// journalEntryRaw represents the raw structure of a journal entry with all fields.
type journalEntryRaw struct {
	Type     string                  `json:"type"`
	ID       string                  `json:"id,omitempty"`
	ParentID string                  `json:"parentId,omitempty"`
	Message  *agentctx.AgentMessage  `json:"message,omitempty"`
	Truncate *agentctx.TruncateEvent `json:"truncate,omitempty"`
	Compact  *agentctx.CompactEvent  `json:"compact,omitempty"`
}

// RecordFromSession extracts a recording from a session directory.
// It reads messages.jsonl and optionally trace files.
func RecordFromSession(sessionDir string) (*RecordedSession, error) {
	// Read meta.json
	var sessionID string
	if data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json")); err == nil {
		var meta struct {
			ID string `json:"id"`
		}
		json.Unmarshal(data, &meta)
		sessionID = meta.ID
	}

	// Read messages.jsonl
	journalPath := filepath.Join(sessionDir, "messages.jsonl")
	file, err := os.Open(journalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open messages.jsonl: %w", err)
	}
	defer file.Close()

	recording := &RecordedSession{
		SessionID: sessionID,
		CWD:       "",
		Messages:  []RecordedMessage{},
		Events:    []RecordedEvent{},
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry journalEntryRaw
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		if entry.Type == "message" && entry.Message != nil {
			msg := entry.Message

			// Extract tool calls from assistant messages
			var toolCalls []ToolCallInfo
			if msg.Role == "assistant" {
				for _, block := range msg.Content {
					if tc, ok := block.(agentctx.ToolCallContent); ok {
						toolCalls = append(toolCalls, ToolCallInfo{
							ID:        tc.ID,
							Name:      tc.Name,
							Arguments: tc.Arguments,
						})
					}
				}
			}

			recording.Messages = append(recording.Messages, RecordedMessage{
				ID:        entry.ID,
				ParentID:  entry.ParentID,
				Role:      msg.Role,
				Content:   msg.Content,
				Timestamp: msg.Timestamp,
				ToolCalls: toolCalls,
				ToolCallID: msg.ToolCallID,
				ToolName:   msg.ToolName,
				Truncated:  msg.Truncated,
			})
		}
	}

	return recording, nil
}

// RecordFromSessionWithTrace extracts a recording including trace events.
func RecordFromSessionWithTrace(sessionDir, traceFile string) (*RecordedSession, error) {
	recording, err := RecordFromSession(sessionDir)
	if err != nil {
		return nil, err
	}

	// Read trace file
	data, err := os.ReadFile(traceFile)
	if err != nil {
		return recording, nil // Return recording without trace events
	}

	var traceData struct {
		TraceEvents []RecordedEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &traceData); err != nil {
		return recording, nil // Return recording without trace events
	}

	// Filter relevant events
	for _, event := range traceData.TraceEvents {
		name := event.Name
		// Only include events relevant to regression testing
		if isRelevantEvent(name) {
			recording.Events = append(recording.Events, event)
		}
	}

	return recording, nil
}

// isRelevantEvent checks if an event is relevant for regression testing.
func isRelevantEvent(name string) bool {
	relevantEvents := []string{
		"context_checkpoint_created",
		"context_management",
		"context_compaction_started",
		"context_compaction_completed",
		"context_trigger_checked",
		"context_management_decision",
	}
	for _, r := range relevantEvents {
		if name == r {
			return true
		}
	}
	return false
}

// ============================================================================
// Session Replayer
// ============================================================================

// Replayer replays a session from a given position.
type Replayer struct {
	recording    *RecordedSession
	startIndex   int
	currentIndex  int
	sessionDir    string

	// Tracking for verification
	checkpointsCreated     int
	contextMgmtTriggered   bool
	compactExecuted        bool
	llmCallsMade           int
	toolExecutions         []string
	lastCompactTurn        int
	lastTriggerTurn        int
	// Scan results
	scanLog []string
}

// NewReplayer creates a new replayer.
func NewReplayer(recording *RecordedSession, sessionDir string) *Replayer {
	return &Replayer{
		recording:   recording,
		startIndex:  0,
		currentIndex: 0,
		sessionDir:  sessionDir,
		toolExecutions: []string{},
		scanLog:     []string{},
	}
}

// StartFrom sets the position to start replaying from.
func (r *Replayer) StartFrom(index int) *Replayer {
	r.startIndex = index
	r.currentIndex = index
	return r
}

// Scan scans through the recording and extracts key information.
// This doesn't actually replay, just analyzes what happened.
func (r *Replayer) Scan() {
	r.scanLog = append(r.scanLog, fmt.Sprintf("Scanning session: %s", r.recording.SessionID))
	r.scanLog = append(r.scanLog, fmt.Sprintf("  Total messages: %d", len(r.recording.Messages)))
	r.scanLog = append(r.scanLog, fmt.Sprintf("  Total events: %d", len(r.recording.Events)))

	// Scan for checkpoints
	for _, event := range r.recording.Events {
		switch event.Name {
		case "context_checkpoint_created":
			r.checkpointsCreated++
			if path, ok := event.Args["checkpoint_path"].(string); ok {
				r.scanLog = append(r.scanLog, fmt.Sprintf("  Checkpoint created: %s", path))
			}
		case "context_compaction_completed":
			r.compactExecuted = true
			r.scanLog = append(r.scanLog, "  Compaction completed")
		case "context_management":
			r.contextMgmtTriggered = true
			if turn, ok := event.Args["turn"].(float64); ok {
				r.lastCompactTurn = int(turn)
				r.scanLog = append(r.scanLog, fmt.Sprintf("  Context management at turn %d", int(turn)))
			}
		}
	}

	// Scan for tool executions
	for _, msg := range r.recording.Messages {
		if msg.Role == "toolResult" && msg.ToolName != "" {
			r.toolExecutions = append(r.toolExecutions, msg.ToolName)
		}
	}

	r.scanLog = append(r.scanLog, fmt.Sprintf("  Tool executions: %d", len(r.toolExecutions)))
	r.scanLog = append(r.scanLog, fmt.Sprintf("  Checkpoints: %d", r.checkpointsCreated))
}

// GetRecording returns the recording.
func (r *Replayer) GetRecording() *RecordedSession {
	return r.recording
}

// GetScanLog returns the scan log.
func (r *Replayer) GetScanLog() []string {
	return r.scanLog
}

// CheckpointsCreated returns the number of checkpoints found during scan.
func (r *Replayer) CheckpointsCreated() int {
	return r.checkpointsCreated
}

// ContextMgmtTriggered returns whether context management was triggered.
func (r *Replayer) ContextMgmtTriggered() bool {
	return r.contextMgmtTriggered
}

// CompactExecuted returns whether compaction was executed.
func (r *Replayer) CompactExecuted() bool {
	return r.compactExecuted
}

// ToolExecutions returns the list of tool executions found.
func (r *Replayer) ToolExecutions() []string {
	return r.toolExecutions
}

// ============================================================================
// Helper Functions
// ============================================================================

// FindCheckpointByTurn finds the checkpoint created at a specific turn.
func FindCheckpointByTurn(sessionDir string, turn int) (*agentctx.CheckpointInfo, error) {
	idx, err := agentctx.LoadCheckpointIndex(sessionDir)
	if err != nil {
		return nil, err
	}

	return idx.GetCheckpointAtTurn(turn)
}

// CountCheckpoints counts checkpoints in a session.
func CountCheckpoints(sessionDir string) (int, error) {
	idx, err := agentctx.LoadCheckpointIndex(sessionDir)
	if err != nil {
		return 0, err
	}

	return len(idx.Checkpoints), nil
}



