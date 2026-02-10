package session

import (
	"errors"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
)

// CompactionResult describes a session compaction operation.
type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	TokensAfter      int
}

type messageRef struct {
	EntryID  string
	Message  agent.AgentMessage
	Cuttable bool
}

// Compact compacts the current session branch and appends a compaction entry.
func (s *Session) Compact(compactor *compact.Compactor) (*CompactionResult, error) {
	if compactor == nil {
		return nil, errors.New("compactor is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getBranchLocked("")
	if len(path) == 0 {
		return nil, errors.New("nothing to compact")
	}
	if path[len(path)-1].Type == EntryTypeCompaction {
		return nil, errors.New("already compacted")
	}

	prevCompactionIndex := -1
	previousSummary := ""
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			prevCompactionIndex = i
			previousSummary = path[i].Summary
			break
		}
	}

	boundaryStart := prevCompactionIndex + 1
	refs := buildMessageRefs(path[boundaryStart:])
	if len(refs) == 0 {
		return nil, errors.New("nothing to compact")
	}

	messages := refsToMessages(refs)
	tokensBefore := compactor.EstimateContextTokens(messages)

	firstKeptIndex := findFirstKeptIndex(refs, compactor)
	if firstKeptIndex <= 0 {
		return nil, errors.New("nothing to compact")
	}

	messagesToSummarize := refsToMessages(refs[:firstKeptIndex])
	summary, err := compactor.GenerateSummaryWithPrevious(messagesToSummarize, previousSummary)
	if err != nil {
		return nil, err
	}

	firstKeptEntryID := refs[firstKeptIndex].EntryID
	entry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(s.byID),
		ParentID:         s.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
	}

	s.addEntry(entry)
	if err := s.persistEntry(entry); err != nil {
		return nil, err
	}

	updatedContext := buildSessionContext(s.entries, s.leafID, s.byID)
	tokensAfter := compactor.EstimateContextTokens(updatedContext)

	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		TokensAfter:      tokensAfter,
	}, nil
}

func buildMessageRefs(entries []SessionEntry) []messageRef {
	refs := make([]messageRef, 0, len(entries))
	for _, entry := range entries {
		switch entry.Type {
		case EntryTypeMessage:
			if entry.Message == nil {
				continue
			}
			cuttable := entry.Message.Role == "user"
			refs = append(refs, messageRef{
				EntryID:  entry.ID,
				Message:  *entry.Message,
				Cuttable: cuttable,
			})
		case EntryTypeBranchSummary:
			if entry.Summary == "" {
				continue
			}
			msg := branchSummaryMessage(entry.Summary, entry.Timestamp)
			refs = append(refs, messageRef{
				EntryID:  entry.ID,
				Message:  msg,
				Cuttable: true,
			})
		case EntryTypeCompaction:
			if entry.Summary == "" {
				continue
			}
			msg := compactionSummaryMessage(entry.Summary, entry.Timestamp)
			refs = append(refs, messageRef{
				EntryID:  entry.ID,
				Message:  msg,
				Cuttable: true,
			})
		}
	}
	return refs
}

func refsToMessages(refs []messageRef) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, 0, len(refs))
	for _, ref := range refs {
		messages = append(messages, ref.Message)
	}
	return messages
}

func findFirstKeptIndex(refs []messageRef, compactor *compact.Compactor) int {
	keepTokens := compactor.KeepRecentTokens()
	keepMessages := compactor.KeepRecentMessages()

	if keepTokens <= 0 {
		if keepMessages <= 0 {
			return 0
		}
		if len(refs) <= keepMessages {
			return 0
		}
		idx := len(refs) - keepMessages
		return adjustToCuttable(refs, idx)
	}

	used := 0
	cutIndex := len(refs)
	for i := len(refs) - 1; i >= 0; i-- {
		used += compact.EstimateMessageTokens(refs[i].Message)
		cutIndex = i
		if used >= keepTokens {
			break
		}
	}

	if cutIndex <= 0 || cutIndex >= len(refs) {
		return 0
	}

	return adjustToCuttable(refs, cutIndex)
}

func adjustToCuttable(refs []messageRef, idx int) int {
	if idx <= 0 {
		return 0
	}
	for i := idx; i >= 0; i-- {
		if refs[i].Cuttable {
			return i
		}
	}
	return 0
}
