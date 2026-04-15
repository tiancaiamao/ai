package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"encoding/json"
	"time"

	"github.com/tiancaiamao/ai/pkg/version"
)

const CurrentSessionVersion = 1

const (
	EntryTypeSession       = "session"
	EntryTypeMessage       = "message"
	EntryTypeCompaction    = "compaction"
	EntryTypeCompactEvent  = "compact_event"
	EntryTypeBranchSummary = "branch_summary"
	EntryTypeSessionInfo   = "session_info"
)

const (
	CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	CompactionSummarySuffix = "\n</summary>"
	BranchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	BranchSummarySuffix     = "\n</summary>"
)

type SessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`

	// Resume optimization fields
	LastCompactionID string `json:"lastCompactionId,omitempty"` // Most recent compaction entry ID

	// Git commit hash of the ai binary that created this session
	GitCommit string `json:"gitCommit,omitempty"`
	// Git version/tag of the ai binary
	GitVersion string `json:"gitVersion,omitempty"`
}

type SessionEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`

	Message *agentctx.AgentMessage `json:"message,omitempty"`

	Summary          string `json:"summary,omitempty"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`

	FromID string `json:"fromId,omitempty"`

	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`

	// CompactEvent records a context management operation (truncate, update_llm_context).
	// This is appended to messages.jsonl as an immutable event.
	// The in-memory snapshot applies the operation; messages.jsonl never mutates.
	CompactEvent *agentctx.CompactEventDetail `json:"compactEvent,omitempty"`
}

func newSessionHeader(id, cwd, parentSession string) SessionHeader {
	return SessionHeader{
		Type:          EntryTypeSession,
		Version:       CurrentSessionVersion,
		ID:            id,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Cwd:           cwd,
		ParentSession: parentSession,
		GitCommit:     version.GitCommit,
		GitVersion:    version.GitVersion,
	}
}

func compactionSummaryMessage(entry *SessionEntry) agentctx.AgentMessage {
	var text string
	if entry.Summary != "" {
		text = CompactionSummaryPrefix + entry.Summary + CompactionSummarySuffix
	} else {
		return agentctx.AgentMessage{}
	}
	return summaryMessage(text, entry.Timestamp)
}

func branchSummaryMessage(summary, timestamp string) agentctx.AgentMessage {
	return summaryMessage(BranchSummaryPrefix+summary+BranchSummarySuffix, timestamp)
}

func summaryMessage(text, timestamp string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "user",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: text},
		},
		Timestamp: timestampToMillis(timestamp),
	}
}

func timestampToMillis(ts string) int64 {
	if ts == "" {
		return time.Now().UnixMilli()
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return parsed.UnixMilli()
}

func buildSessionContext(entries []*SessionEntry, leafID *string, byID map[string]*SessionEntry) []agentctx.AgentMessage {
	if len(entries) == 0 {
		return []agentctx.AgentMessage{}
	}

	var leaf *SessionEntry
	if leafID != nil {
		leaf = byID[*leafID]
	} else {
		leaf = entries[len(entries)-1]
	}

	if leaf == nil {
		return []agentctx.AgentMessage{}
	}

	path := make([]*SessionEntry, 0)
	current := leaf
	for current != nil {
		path = append(path, current)
		if current.ParentID == nil {
			break
		}
		current = byID[*current.ParentID]
	}

	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	var compaction *SessionEntry
	compactionIndex := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			compaction = path[i]
			compactionIndex = i
			break
		}
	}

	messages := make([]agentctx.AgentMessage, 0)
	appendMessage := func(entry *SessionEntry) {
		switch entry.Type {
		case EntryTypeMessage:
			if entry.Message != nil {
				messages = append(messages, *entry.Message)
			}
		case EntryTypeBranchSummary:
			if entry.Summary != "" {
				messages = append(messages, branchSummaryMessage(entry.Summary, entry.Timestamp))
			}
		}
	}

if compaction != nil {
		msg := compactionSummaryMessage(compaction)
		if msg.Role != "" {
			messages = append(messages, msg)
		}

		foundFirstKept := false
		if compaction.FirstKeptEntryID != "" {
			for i := 0; i < compactionIndex; i++ {
				entry := path[i]
				if entry.ID == compaction.FirstKeptEntryID {
					foundFirstKept = true
				}
				if foundFirstKept {
					appendMessage(entry)
				}
			}
		}

		for i := compactionIndex + 1; i < len(path); i++ {
			appendMessage(path[i])
		}
		return applyContextManagementEvents(messages, path)
	}

	for _, entry := range path {
		appendMessage(entry)
	}

	return applyContextManagementEvents(messages, path)
}

// applyCompactEvents replays compact events on the message list.
// Each compact_event entry records an operation (truncate, update_llm_context).
// Apply reconstructs the result deterministically from the original message content.
func applyContextManagementEvents(messages []agentctx.AgentMessage, path []*SessionEntry) []agentctx.AgentMessage {
	for _, entry := range path {
		if entry.Type != EntryTypeCompactEvent || entry.CompactEvent == nil {
			continue
		}
		evt := entry.CompactEvent
		switch evt.Action {
		case agentctx.CompactActionTruncate:
			for _, id := range evt.IDs {
				for i := range messages {
					msg := &messages[i]
					if msg.ToolCallID == id && msg.Role == "toolResult" && !msg.Truncated {
						originalText := msg.ExtractText()
						msg.Truncated = true
						msg.OriginalSize = len(originalText)
						msg.Content = []agentctx.ContentBlock{
							agentctx.TextContent{
								Type: "text",
								Text: agentctx.TruncateWithHeadTail(originalText),
							},
						}
						break
					}
				}
			}
		case agentctx.CompactActionUpdateLLMContext:
			// llm_context is stored in a separate file (llm_context.txt), not in messages.jsonl.
			// This event marks that llm_context was updated at this point in the log.
			// During replay, llm_context.txt should already be loaded from checkpoint or session.
			// No action needed here for the messages array.
		default:
			// Unknown action types are logged but don't cause replay failures.
			// This handles future schema extensions or corrupted data gracefully.
		}
	}
	return messages
}

func decodeSessionHeader(line []byte) (*SessionHeader, error) {
	var header SessionHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return nil, err
	}
	if header.Type != EntryTypeSession || header.ID == "" {
		return nil, nil
	}
	return &header, nil
}
