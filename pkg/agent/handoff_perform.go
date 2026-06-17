package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"

	"github.com/google/uuid"
)

// handoffGenerateSystemPrompt is a focused system prompt for handoff document
// generation. It replaces the main agent system prompt to avoid confusing the
// model — a coding assistant prompt and a summarization task are incompatible.
const handoffGenerateSystemPrompt = `You are a context handoff document generator. Your job is to read the conversation transcript and produce a comprehensive handoff document that allows a fresh agent to continue the work without losing context.

The document MUST include these sections:

## Current Task
What is being worked on right now. What was the original goal.

## Key Decisions
Important decisions made, alternatives considered, and why.

## Completed Work
What has been done so far. File paths modified, commands run, results obtained.

## Pending Work
What remains to be done. Specific next steps.

## Important Context
File paths, error messages, API constraints, environment details, or anything a fresh agent would need to know.

## Open Questions
Unresolved issues or questions that need clarification.

Be specific. Include exact file paths, error messages, and code snippets. A fresh agent reading this document should be able to continue the work immediately.`

// handoffGenerateInstruction instructs the LLM to produce a handoff document
// from the current conversation transcript.
const handoffGenerateInstruction = `Below is a conversation transcript. Read it carefully and write a handoff document using the format specified in your instructions. The document must be detailed enough for a fresh agent to continue the work.`

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

	startTime := time.Now()

	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_start",
		traceevent.Field{Key: "session_dir", Value: s.config.GetSessionDir()})

	// Save old messages before Q&A (Q&A does not mutate RecentMessages, but
	// we capture a snapshot for clarity).
	oldMessages := s.agentCtx.RecentMessages

	// Run Q&A verification. Failures here are non-fatal — we proceed with the
	// checkpoint using whatever Q&A turns were collected.
	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_qa_start",
		traceevent.Field{Key: "max_rounds", Value: handoffQADefaultRounds})
	// Build the QA system prompt with the thinking-level instruction appended,
	// matching how the main LLM stream loop builds its system prompt. This keeps
	// the prefix identical so provider prefix caching is not invalidated.
	qaSystemPrompt := s.agentCtx.SystemPrompt
	if instruction := prompt.ThinkingInstruction(s.config.ThinkingLevel); instruction != "" {
		if strings.TrimSpace(qaSystemPrompt) == "" {
			qaSystemPrompt = instruction
		} else {
			qaSystemPrompt = qaSystemPrompt + "\n\n" + instruction
		}
	}

	qaTurns, err := runHandoffQA(ctx, model, apiKey, contextWindow, handoffDoc, oldMessages, handoffQADefaultRounds, qaSystemPrompt)
	if err != nil {
		slog.Warn("[Handoff] Q&A verification failed, proceeding with checkpoint",
			"error", err,
			"qa_turns", len(qaTurns),
		)
	}
	totalQuestions := 0
	totalAnswers := 0
	for _, t := range qaTurns {
		totalQuestions += len(t.Question)
		totalAnswers += len(t.Answer)
	}
	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_qa_complete",
		traceevent.Field{Key: "rounds_completed", Value: len(qaTurns)},
		traceevent.Field{Key: "total_questions", Value: totalQuestions},
		traceevent.Field{Key: "total_answers", Value: totalAnswers})

	if err := s.finalizeHandoff(ctx, handoffDoc, qaTurns); err != nil {
		traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_failed",
			traceevent.Field{Key: "error", Value: err.Error()},
			traceevent.Field{Key: "phase", Value: "checkpoint"})
		s.handoffPending = false
		return err
	}

	durationMs := time.Since(startTime).Milliseconds()
	checkpoint, _ := session.GetCurrentCheckpoint(s.config.GetSessionDir())
	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_complete",
		traceevent.Field{Key: "checkpoint_name", Value: checkpoint},
		traceevent.Field{Key: "duration_ms", Value: durationMs})

	s.handoffPending = false
	return nil
}

// finalizeHandoff creates the checkpoint, writes messages, switches the active
// checkpoint, and reloads context. It is separated from performHandoff so it
// can be unit-tested without making LLM calls.
func (s *loopState) finalizeHandoff(ctx context.Context, handoffDoc string, qaTurns []qaTurn) error {
	sessionDir := s.config.GetSessionDir()
	if sessionDir == "" {
		return fmt.Errorf("handoff requires a session directory")
	}

	parentCheckpoint, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		return fmt.Errorf("get current checkpoint: %w", err)
	}

	// Read checkpoint count from meta, compute next checkpoint number.
	checkpointCount, err := session.ReadCheckpointCount(sessionDir)
	if err != nil {
		return fmt.Errorf("read checkpoint count: %w", err)
	}
	checkpointNum := checkpointCount + 1

	// Create new checkpoint directory.
	checkpointName, err := session.CreateHandoffCheckpoint(sessionDir, checkpointNum, parentCheckpoint)
	if err != nil {
		return fmt.Errorf("create handoff checkpoint: %w", err)
	}

	// Persist the updated checkpoint count.
	if err := session.WriteCheckpointCount(sessionDir, checkpointNum); err != nil {
		return fmt.Errorf("write checkpoint count: %w", err)
	}

	// Build checkpoint messages from handoff doc + Q&A turns.
	entries := buildHandoffEntries(handoffDoc, qaTurns)

	// Write messages to the new checkpoint.
	if err := session.WriteHandoffMessages(sessionDir, checkpointName, entries); err != nil {
		return fmt.Errorf("write handoff messages: %w", err)
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_checkpoint_created",
		traceevent.Field{Key: "checkpoint_name", Value: checkpointName},
		traceevent.Field{Key: "checkpoint_num", Value: checkpointNum},
		traceevent.Field{Key: "messages_written", Value: len(entries)})

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

	// Capture old message count before switching.
	oldMessagesCount := len(s.agentCtx.RecentMessages)

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

	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_context_switched",
		traceevent.Field{Key: "new_checkpoint", Value: checkpointName},
		traceevent.Field{Key: "old_messages_count", Value: oldMessagesCount},
		traceevent.Field{Key: "new_messages_count", Value: len(messages)})

	// Reset state counters — the new checkpoint starts fresh.
	s.hardFloorCrossed = false
	s.hardFloorTurns = 0
	s.handoffPending = false
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

	// Serialize conversation into a text transcript. We use a single user
	// message (not raw LLM messages) to avoid role-formatting issues — the
	// raw conversation may have consecutive same-role messages or tool-only
	// messages that violate API constraints.
	const maxTranscriptBytes = 512 * 1024 // ~128K tokens

	var lines []string
	totalBytes := 0
	for i := len(oldMessages) - 1; i >= 0; i-- {
		msg := oldMessages[i]
		if !msg.IsAgentVisible() {
			continue
		}
		text := msg.ExtractText()
		if strings.TrimSpace(text) == "" {
			continue
		}
		line := fmt.Sprintf("[%s]: %s", msg.Role, text)
		if totalBytes+len(line) > maxTranscriptBytes {
			break
		}
		lines = append(lines, line)
		totalBytes += len(line)
	}

	// Reverse to chronological order (we walked backward).
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no conversation content to generate handoff doc from")
	}

	conversationText := strings.Join(lines, "\n\n")

	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_auto_generate_start",
		traceevent.Field{Key: "message_count", Value: len(lines)},
		traceevent.Field{Key: "transcript_bytes", Value: totalBytes})

	// Use a dedicated system prompt for handoff generation. The main agent
	// system prompt (coding assistant) confuses the model into trying to
	// respond as a coding assistant rather than writing a summary.
	llmCtx := llm.LLMContext{
		SystemPrompt: handoffGenerateSystemPrompt,
		Messages: []llm.LLMMessage{
			{Role: "user", Content: handoffGenerateInstruction},
			{Role: "user", Content: conversationText},
		},
	}

	doc, err := streamLLMText(ctx, model, llmCtx, apiKey)
	if err != nil {
		traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_auto_generate_failed",
			traceevent.Field{Key: "error", Value: err.Error()})
		return "", fmt.Errorf("auto-generate handoff doc: %w", err)
	}

	doc = strings.TrimSpace(doc)
	if doc == "" {
		traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_auto_generate_failed",
			traceevent.Field{Key: "error", Value: "empty doc"})
		return "", fmt.Errorf("auto-generated handoff doc is empty")
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_auto_generate_complete",
		traceevent.Field{Key: "doc_length", Value: len(doc)})

	return doc, nil
}
