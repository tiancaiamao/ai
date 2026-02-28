package session

import (
	"encoding/json"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

const CurrentSessionVersion = 1

const (
	EntryTypeSession       = "session"
	EntryTypeMessage       = "message"
	EntryTypeCompaction    = "compaction"
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
	ResumeOffset     int64  `json:"resumeOffset,omitempty"`     // File offset for fast resume
}

type SessionEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`

	Message *agent.AgentMessage `json:"message,omitempty"`

	Summary          string  `json:"summary,omitempty"`
	SummaryFile      *string `json:"summaryFile,omitempty"` // Reference to detail/ file for compacted summaries
	FirstKeptEntryID string  `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`

	FromID string `json:"fromId,omitempty"`

	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
}

func newSessionHeader(id, cwd, parentSession string) SessionHeader {
	return SessionHeader{
		Type:          EntryTypeSession,
		Version:       CurrentSessionVersion,
		ID:            id,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Cwd:           cwd,
		ParentSession: parentSession,
	}
}

func compactionSummaryMessage(summary, timestamp string) agent.AgentMessage {
	return summaryMessage(CompactionSummaryPrefix+summary+CompactionSummarySuffix, timestamp)
}

func branchSummaryMessage(summary, timestamp string) agent.AgentMessage {
	return summaryMessage(BranchSummaryPrefix+summary+BranchSummarySuffix, timestamp)
}

func summaryMessage(text, timestamp string) agent.AgentMessage {
	return agent.AgentMessage{
		Role: "user",
		Content: []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: text},
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

func buildSessionContext(entries []*SessionEntry, leafID *string, byID map[string]*SessionEntry) []agent.AgentMessage {
	if len(entries) == 0 {
		return []agent.AgentMessage{}
	}

	var leaf *SessionEntry
	if leafID != nil {
		leaf = byID[*leafID]
	} else {
		leaf = entries[len(entries)-1]
	}

	if leaf == nil {
		return []agent.AgentMessage{}
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

	messages := make([]agent.AgentMessage, 0)
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
		messages = append(messages, compactionSummaryMessage(compaction.Summary, compaction.Timestamp))

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
		return messages
	}

	for _, entry := range path {
		appendMessage(entry)
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
