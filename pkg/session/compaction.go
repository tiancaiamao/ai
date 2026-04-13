package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"errors"
	"time"

	"github.com/tiancaiamao/ai/pkg/compact"
)

var (
	ErrNothingToCompact = errors.New("nothing to compact")
	ErrAlreadyCompacted = errors.New("already compacted")
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
	Message  agentctx.AgentMessage
	Cuttable bool
}

// IsNonActionableCompactionError reports whether a compaction error means
// there is simply no current compaction work to perform.
func IsNonActionableCompactionError(err error) bool {
	return errors.Is(err, ErrNothingToCompact) || errors.Is(err, ErrAlreadyCompacted)
}

// CanCompact checks whether the current branch has enough cuttable context
// to produce a compaction entry.
func (s *Session) CanCompact(compactor *compact.Compactor) bool {
	if compactor == nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return canCompactLocked(s, compactor)
}

func canCompactLocked(s *Session, compactor *compact.Compactor) bool {
	path := s.getBranchLocked("")
	if len(path) == 0 {
		return false
	}
	if path[len(path)-1].Type == EntryTypeCompaction {
		return false
	}

	prevCompactionIndex := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			prevCompactionIndex = i
			break
		}
	}

	boundaryStart := prevCompactionIndex + 1
	refs := buildMessageRefs(path[boundaryStart:])
	if len(refs) == 0 {
		return false
	}

	firstKeptIndex := findFirstKeptIndex(refs, compactor)
	return firstKeptIndex > 0
}

// GetSummaryFromEntry retrieves the summary content from an entry.
func GetSummaryFromEntry(_ string, entry *SessionEntry) string {
	if entry == nil {
		return ""
	}
	return entry.Summary
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
		return nil, ErrNothingToCompact
	}
	if path[len(path)-1].Type == EntryTypeCompaction {
		return nil, ErrAlreadyCompacted
	}

	prevCompactionIndex := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			prevCompactionIndex = i
			break
		}
	}

	boundaryStart := prevCompactionIndex + 1
	refs := buildMessageRefs(path[boundaryStart:])
	if len(refs) == 0 {
		return nil, ErrNothingToCompact
	}

	messages := refsToMessages(refs)
	tokensBefore := compactor.EstimateContextTokensOld(messages)

	firstKeptIndex := findFirstKeptIndex(refs, compactor)
	if firstKeptIndex <= 0 {
		return nil, ErrNothingToCompact
	}

	messagesToSummarize := refsToMessages(refs[:firstKeptIndex])
	summary, err := compactor.GenerateSummary(messagesToSummarize)
	if err != nil {
		return nil, err
	}

	firstKeptEntryID := refs[firstKeptIndex].EntryID

	entry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(s.byID),
		ParentID:         s.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Summary:          summary,
	}

	s.addEntry(entry)

	// Update header with compaction info for fast resume
	s.header.LastCompactionID = entry.ID

	// Persist the compaction entry to messages.jsonl (append-only, no rewrite)
	if err := s.persistEntry(entry); err != nil {
		return nil, err
	}

	updatedContext := buildSessionContext(s.entries, s.leafID, s.byID)
	tokensAfter := compactor.EstimateContextTokensOld(updatedContext)

	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		TokensAfter:      tokensAfter,
	}, nil
}

func buildMessageRefs(entries []SessionEntry) []messageRef {
	refs := make([]messageRef, 0, len(entries))
	for i := range entries {
		entry := &entries[i]
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
			// Use inline summary directly
			if entry.Summary != "" {
				msg := compactionSummaryMessage(entry)
				if msg.Role != "" {
					refs = append(refs, messageRef{
						EntryID:  entry.ID,
						Message:  msg,
						Cuttable: true,
					})
				}
			}
		}
	}
	return refs
}

func refsToMessages(refs []messageRef) []agentctx.AgentMessage {
	messages := make([]agentctx.AgentMessage, 0, len(refs))
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
	if len(refs) == 0 || idx <= 0 {
		return 0
	}

	if idx >= len(refs) {
		idx = len(refs) - 1
	}

	for i := idx; i >= 0; i-- {
		if refs[i].Cuttable {
			return i
		}
	}

	// If there is no cuttable message at or before idx, keep searching
	// forward so long assistant/tool-heavy tails can still compact.
	for i := idx + 1; i < len(refs); i++ {
		if refs[i].Cuttable {
			return i
		}
	}
	return 0
}