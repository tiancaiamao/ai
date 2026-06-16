package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"

	"github.com/google/uuid"
)

// handoffGenerateSystemPrompt instructs the LLM to produce a handoff document
// from the current conversation. Used by auto-execute.
const handoffGenerateSystemPrompt = `Summarize the current task, key decisions, and pending work. Include file paths, error context, and next steps. Be thorough but concise.`

// performHandoff executes the complete handoff process:
//  1. Run Q&A verification on the handoff document.
//  2. Create a new checkpoint.
//  3. Write checkpoint messages + handoff document.
//  4. Atomically switch to the new checkpoint.
//  5. Reload agent context from the new checkpoint.
//  6. Reset hard floor and compaction recovery state.
//
// On failure the old checkpoint is left untouched, so the loop can safely
// continue with the existing context.
func (s *loopState) performHandoff(ctx context.Context, handoffDoc string) error {
	model := getEffectiveModel(s.config)
	apiKey := getEffectiveAPIKey(s.config)
	contextWindow := s.config.ContextWindow

	// Save old messages before Q&A (Q&A does not mutate RecentMessages, but
	// we capture a snapshot for clarity).
	oldMessages := s.agentCtx.RecentMessages

	// Run Q&A verification. Failures here are non-fatal — we proceed with the
	// checkpoint using whatever Q&A turns were collected.
	qaTurns, err := runHandoffQA(ctx, model, apiKey, contextWindow, handoffDoc, oldMessages, 3)
	if err != nil {
		slog.Warn("[Handoff] Q&A verification failed, proceeding with checkpoint",
			"error", err,
			"qa_turns", len(qaTurns),
		)
	}

	return s.finalizeHandoff(handoffDoc, qaTurns)
}

// finalizeHandoff creates the checkpoint, writes messages, switches the active
// checkpoint, and reloads context. It is separated from performHandoff so it
// can be unit-tested without making LLM calls.
func (s *loopState) finalizeHandoff(handoffDoc string, qaTurns []qaTurn) error {
	sessionDir := s.config.GetSessionDir()
	if sessionDir == "" {
		return fmt.Errorf("handoff requires a session directory")
	}

	parentCheckpoint, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		return fmt.Errorf("get current checkpoint: %w", err)
	}

	// Create new checkpoint directory.
	checkpointName, err := session.CreateHandoffCheckpoint(sessionDir, parentCheckpoint)
	if err != nil {
		return fmt.Errorf("create handoff checkpoint: %w", err)
	}

	// Build checkpoint messages from handoff doc + Q&A turns.
	entries := buildHandoffEntries(handoffDoc, qaTurns)

	// Write messages to the new checkpoint.
	if err := session.WriteHandoffMessages(sessionDir, checkpointName, entries); err != nil {
		return fmt.Errorf("write handoff messages: %w", err)
	}

	// Write the handoff document as handoff.md.
	if err := session.WriteHandoffDocument(sessionDir, checkpointName, handoffDoc); err != nil {
		return fmt.Errorf("write handoff document: %w", err)
	}

	// Write the current AgentState so the resume path can restore CWD,
	// token counts, etc. (P1-3). Failures are non-fatal — the checkpoint is
	// still usable, just without restored agent state.
	if s.agentCtx.AgentState != nil {
		if err := session.WriteHandoffAgentState(sessionDir, checkpointName, s.agentCtx.AgentState); err != nil {
			slog.Warn("[Handoff] Failed to write agent state to checkpoint", "error", err)
		}
	}

	// Atomically switch current.txt to the new checkpoint.
	if err := session.SwitchCheckpoint(sessionDir, checkpointName); err != nil {
		return fmt.Errorf("switch checkpoint: %w", err)
	}

	// Reload agent context from the new checkpoint.
	messages, err := session.LoadHandoffCheckpointMessages(sessionDir, checkpointName)
	if err != nil {
		return fmt.Errorf("load checkpoint messages: %w", err)
	}
	s.agentCtx.RecentMessages = messages

	// Reset state counters — the new checkpoint starts fresh.
	s.hardFloorCrossed = false
	s.hardFloorTurns = 0
	s.compactionRecs = 0
	s.emptyRetries = 0
	s.guardAbortRecovery = false

	slog.Info("[Handoff] Handoff complete",
		"checkpoint", checkpointName,
		"parent", parentCheckpoint,
		"messages", len(messages),
		"qa_turns", len(qaTurns),
	)

	return nil
}

// buildHandoffEntries converts the handoff document and Q&A turns into session
// entries for the new checkpoint's messages.jsonl.
//
// The structure is:
//   - Handoff doc as a user message.
//   - Each Q&A turn as: question (user) + answer (assistant).
func buildHandoffEntries(handoffDoc string, qaTurns []qaTurn) []session.SessionEntry {
	entries := make([]session.SessionEntry, 0, 1+len(qaTurns)*2)

	// Handoff doc as a user message.
	now := time.Now().UnixMilli()
	entries = append(entries, session.SessionEntry{
		Type:      session.EntryTypeMessage,
		ID:        generateHandoffEntryID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Message: &agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: handoffDoc},
			},
			Timestamp: now,
		},
	})

	// Each Q&A turn as: question (user) + answer (assistant).
	for _, turn := range qaTurns {
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		ms := time.Now().UnixMilli()

		entries = append(entries, session.SessionEntry{
			Type:      session.EntryTypeMessage,
			ID:        generateHandoffEntryID(),
			Timestamp: ts,
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: turn.Question},
				},
				Timestamp: ms,
			},
		})

		entries = append(entries, session.SessionEntry{
			Type:      session.EntryTypeMessage,
			ID:        generateHandoffEntryID(),
			Timestamp: ts,
			Message: &agentctx.AgentMessage{
				Role: "assistant",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: turn.Answer},
				},
				Timestamp: ms,
			},
		})
	}

	return entries
}

// generateHandoffEntryID returns a short unique ID for a session entry.
func generateHandoffEntryID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}

// autoGenerateHandoffDoc generates a handoff document via a minimal LLM call
// when auto-execute is triggered by the hard floor threshold.
//
// It serializes the current conversation and asks the LLM to produce a
// handoff summary. The resulting document does NOT include the
// <handoff_complete> marker — performHandoff handles checkpoint creation
// directly.
func (s *loopState) autoGenerateHandoffDoc(ctx context.Context) (string, error) {
	model := getEffectiveModel(s.config)
	apiKey := getEffectiveAPIKey(s.config)

	oldMessages := s.agentCtx.RecentMessages

	// Serialize old messages into conversation text.
	var conversationText strings.Builder
	for _, msg := range oldMessages {
		if !msg.IsAgentVisible() {
			continue
		}
		text := msg.ExtractText()
		if strings.TrimSpace(text) == "" {
			continue
		}
		conversationText.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, text))
	}

	if conversationText.Len() == 0 {
		return "", fmt.Errorf("no conversation content to generate handoff doc from")
	}

	llmCtx := llm.LLMContext{
		SystemPrompt: handoffGenerateSystemPrompt,
		Messages: []llm.LLMMessage{
			{Role: "user", Content: conversationText.String()},
		},
	}

	doc, err := streamLLMText(ctx, model, llmCtx, apiKey)
	if err != nil {
		return "", fmt.Errorf("auto-generate handoff doc: %w", err)
	}

	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", fmt.Errorf("auto-generated handoff doc is empty")
	}

	return doc, nil
}
