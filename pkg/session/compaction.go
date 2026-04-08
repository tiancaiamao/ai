package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	refs := buildMessageRefs(s.sessionDir, path[boundaryStart:])
	if len(refs) == 0 {
		return false
	}

	firstKeptIndex := findFirstKeptIndex(refs, compactor)
	return firstKeptIndex > 0
}

// readSummaryFromFile reads a compaction summary from a detail file
func readSummaryFromFile(sessionDir, relativePath string) (string, error) {
	fullPath := filepath.Join(sessionDir, relativePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	
	// Extract the actual summary content (after the metadata section)
	// The metadata section is between <!-- and -->
	contentStr := string(content)
	if idx := strings.Index(contentStr, "-->"); idx != -1 {
		// Find the end of the metadata comment and skip the newline
		idx += 3
		if idx < len(contentStr) && contentStr[idx] == '\n' {
			idx++
		}
		// Trim leading/trailing whitespace
		return strings.TrimSpace(contentStr[idx:]), nil
	}
	
	// If no metadata found, return the whole content
	return strings.TrimSpace(contentStr), nil
}

// GetSummaryFromEntry retrieves the summary content from an entry
// either from the referenced file or from the inline Summary field
func GetSummaryFromEntry(sessionDir string, entry *SessionEntry) string {
	if entry == nil {
		return ""
	}
	
	// Try to read from file first (new format)
	if entry.SummaryFile != nil {
		summary, err := readSummaryFromFile(sessionDir, *entry.SummaryFile)
		if err == nil && summary != "" {
			return summary
		}
		// Fall through to inline summary if file read fails
	}
	
	// Use inline summary (old format or fallback)
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
	previousSummary := ""
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			prevCompactionIndex = i
			previousSummary = GetSummaryFromEntry(s.sessionDir, &path[i])
			break
		}
	}

	boundaryStart := prevCompactionIndex + 1
	refs := buildMessageRefs(s.sessionDir, path[boundaryStart:])
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
	summary, err := compactor.GenerateSummaryWithPrevious(messagesToSummarize, previousSummary)
	if err != nil {
		return nil, err
	}

	firstKeptEntryID := refs[firstKeptIndex].EntryID
	
	// Save compaction summary to llm context detail directory before creating entry
	// This ensures all compactions (auto and manual) save summaries
	summaryFile := ""
	if s.llmContext != nil && summary != "" {
		summaryFile, err = s.llmContext.SaveCompactionSummary(summary)
		if err != nil {
			// Log warning but don't fail the compaction
			// We'll store the summary inline as fallback
			summaryFile = ""
		}
	}
	
	entry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(s.byID),
		ParentID:         s.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
	}
	
	// Store summary file reference if saved successfully, otherwise inline summary
	if summaryFile != "" {
		entry.SummaryFile = &summaryFile
		// Keep Summary empty to avoid duplication
	} else {
		entry.Summary = summary
	}

	s.addEntry(entry)

	// Update header with compaction info for fast resume
	s.header.LastCompactionID = entry.ID
	s.header.ResumeOffset = 0 // File will be rewritten, so offset resets

	// Backup messages.jsonl before rewrite so pre-compact data is preserved.
	// This allows post-hoc analysis of full session history (e.g., evolve loop
	// structural checks that need to inspect messages before compaction).
	if backupErr := s.backupPreCompact(); backupErr != nil {
		slog.Warn("Failed to backup pre-compact messages", "error", backupErr)
	}

	// Rewrite the entire file to persist the updated header
	if err := s.rewriteFile(); err != nil {
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

// backupPreCompact copies the current messages.jsonl to llm-context/detail/
// before compaction rewrites it. This preserves the full pre-compact history
// for post-hoc analysis (e.g., behavioral checks in evolve loop).
func (s *Session) backupPreCompact() error {
	src := s.filePath()
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to backup
		}
		return err
	}

	dir := GetLLMContextDetailDir(s.sessionDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(dir, "pre-compact-"+ts+".jsonl")

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func buildMessageRefs(sessionDir string, entries []SessionEntry) []messageRef {
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
			// Get summary content from inline or file reference
			summary := GetSummaryFromEntry(sessionDir, &entry)
			msg := compactionSummaryMessageWithContent(summary, entry.Timestamp)
			if msg.Role == "" {
				continue
			}
			refs = append(refs, messageRef{
				EntryID:  entry.ID,
				Message:  msg,
				Cuttable: true,
			})
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