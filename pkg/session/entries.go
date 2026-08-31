package session

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/version"
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

	// SnapshotRef is the relative path (within session dir) to a file containing
	// the post-compaction in-memory messages. This is the Proposal B approach:
	// messages.jsonl is append-only; compaction records reference an external
	// snapshot file rather than rewriting history.
	SnapshotRef string `json:"snapshotRef,omitempty"`

	Summary          string `json:"summary,omitempty"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
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

func buildSessionContext(entries []*SessionEntry, leafID *string, byID map[string]*SessionEntry, sessionDir string) []agentctx.AgentMessage {
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
				msg := *entry.Message
				msg.EntryID = entry.ID
				messages = append(messages, msg)
			}
		case EntryTypeBranchSummary:
			if entry.Summary != "" {
				msg := branchSummaryMessage(entry.Summary, entry.Timestamp)
				msg.EntryID = entry.ID
				messages = append(messages, msg)
			}
		}
	}

	if compaction != nil {
		// Proposal B: if SnapshotRef is set, load post-compaction messages from
		// the external snapshot file. This avoids rewriting messages.jsonl and
		// makes compaction entries simple pointers.
		if compaction.SnapshotRef != "" && sessionDir != "" {
			snapshotPath := filepath.Join(sessionDir, compaction.SnapshotRef)
			if loaded, err := loadSnapshotMessages(snapshotPath); err == nil {
				messages = append(messages, loaded...)
			} else {
				slog.Warn("[session] Failed to load compaction snapshot, falling back to summary only",
					"path", snapshotPath, "error", err)
				msg := compactionSummaryMessage(compaction)
				if msg.Role != "" {
					messages = append(messages, msg)
				}
			}
		} else if compaction.FirstKeptEntryID != "" {
			// Legacy path: reconstruct from FirstKeptEntryID (old sessions)
			msg := compactionSummaryMessage(compaction)
			if msg.Role != "" {
				messages = append(messages, msg)
			}
			foundFirstKept := false
			for i := 0; i < compactionIndex; i++ {
				entry := path[i]
				if entry.ID == compaction.FirstKeptEntryID {
					foundFirstKept = true
				}
				if foundFirstKept {
					appendMessage(entry)
				}
			}
		} else {
			msg := compactionSummaryMessage(compaction)
			if msg.Role != "" {
				messages = append(messages, msg)
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

// loadSnapshotMessages reads a compaction snapshot file (JSONL of AgentMessage).
func loadSnapshotMessages(path string) ([]agentctx.AgentMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var messages []agentctx.AgentMessage
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var msg agentctx.AgentMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode snapshot message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// saveSnapshotMessages writes messages to a compaction snapshot file (JSONL).
func saveSnapshotMessages(path string, messages []agentctx.AgentMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(buf.String()), 0644)
}
