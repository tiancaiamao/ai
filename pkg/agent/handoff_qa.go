package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// qaTurn represents a single question-answer exchange during handoff
// verification.
type qaTurn struct {
	Question string
	Answer   string
}

const (
	// handoffQATimeout is the per-call timeout for each Q&A LLM call.
	handoffQATimeout = 5 * time.Minute
	// handoffQAChunkTimeout is the inter-chunk timeout passed to StreamLLM.
	handoffQAChunkTimeout = 2 * time.Minute
	// handoffQADefaultRounds is the default number of Q&A rounds.
	handoffQADefaultRounds = 3
)

// qaAskUserMessage instructs the LLM to identify gaps in the handoff document.
// Sent as a role:"user" message so the system prompt prefix is preserved for
// provider prefix caching.
const qaAskUserMessage = `You are being asked to review a handoff document for completeness. Identify any critical information that is missing or unclear. Ask specific questions. If the document is complete, respond with 'COMPLETE'. Focus on: current task state, key decisions, pending work, file paths, error context.`

// qaAnswerSystemPrompt instructs the LLM to answer questions from conversation
// history.
// qaAnswerUserMessage is the template for requesting answers about the handoff
// document from conversation history. Sent as a role:"user" message.
const qaAnswerUserMessageTemplate = `Answer these questions about the handoff document based on the conversation history: %s`

// noGapsIndicators are substrings that signal the LLM found no gaps. The check
// is case-insensitive.
var noGapsIndicators = []string{
	"no gaps",
	"no questions",
	"no missing",
	"looks complete",
	"document is complete",
	"nothing missing",
	"no further questions",
	"no gaps found",
}

// hasNoGaps returns true if the text indicates no gaps were found (either
// empty or containing a no-gaps indicator phrase).
func hasNoGaps(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	for _, indicator := range noGapsIndicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}

// streamLLMText is a helper that calls llm.StreamLLM and collects the full text
// response. It applies a bounded timeout via context.WithTimeout.
func streamLLMText(ctx context.Context, model llm.Model, llmCtx llm.LLMContext, apiKey string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, handoffQATimeout)
	defer cancel()

	llmStream := llm.StreamLLM(callCtx, model, llmCtx, apiKey, handoffQAChunkTimeout)

	var text strings.Builder
	for event := range llmStream.Iterator(callCtx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			text.WriteString(e.Delta)
		case llm.LLMErrorEvent:
			return "", e.Error
		}
	}

	return text.String(), nil
}

// runHandoffQA runs a bounded Q&A verification loop.
//
// For each round (up to maxRounds):
//  1. Ask questions: send the handoff doc to the LLM (without old context) to
//     identify gaps.
//  2. If no gaps found → break early.
//  3. Answer questions: send old context messages plus the questions to the
//     LLM and collect the answer.
//  4. Store the question-answer pair as a qaTurn and augment the handoff doc
//     with the Q&A for the next round.
//
// Returns the collected Q&A turns and any error.
func runHandoffQA(
	ctx context.Context,
	model llm.Model,
	apiKey string,
	contextWindow int,
	handoffDoc string,
	oldMessages []agentctx.AgentMessage,
	maxRounds int,
	systemPrompt string,
) ([]qaTurn, error) {
	if maxRounds <= 0 {
		maxRounds = handoffQADefaultRounds
	}

	var turns []qaTurn

	for round := 0; round < maxRounds; round++ {
		// --- Ask questions: send handoff doc + QA instructions as user messages.
		// The system prompt is reused from the main agent loop to preserve
		// provider prefix caching.
		span := traceevent.StartSpan(ctx, "handoff_qa_round", traceevent.CategoryEvent,
			traceevent.Field{Key: "round", Value: round})

		askCtx := llm.LLMContext{
			SystemPrompt: systemPrompt,
			Messages: []llm.LLMMessage{
				{Role: "user", Content: qaAskUserMessage},
				{Role: "user", Content: handoffDoc},
			},
		}
		questions, err := streamLLMText(ctx, model, askCtx, apiKey)
		if err != nil {
			return turns, fmt.Errorf("handoff QA round %d ask: %w", round, err)
		}

		if hasNoGaps(questions) {
			slog.Info("[Handoff] QA verification complete — no gaps found",
				"round", round)
			break
		}

		// --- Answer questions: send old context + questions to LLM.
		answerMessages := make([]llm.LLMMessage, 0, len(oldMessages)+1)
		for _, msg := range oldMessages {
			if !msg.IsAgentVisible() {
				continue
			}
			text := msg.ExtractText()
			if strings.TrimSpace(text) == "" {
				continue
			}
			answerMessages = append(answerMessages, llm.LLMMessage{
				Role:    msg.Role,
				Content: text,
			})
		}
		answerMessages = append(answerMessages, llm.LLMMessage{
			Role: "user",
			Content: fmt.Sprintf(
				qaAnswerUserMessageTemplate,
				questions,
			),
		})

		answerCtx := llm.LLMContext{
			SystemPrompt: systemPrompt,
			Messages:     answerMessages,
		}
		answer, err := streamLLMText(ctx, model, answerCtx, apiKey)
		if err != nil {
			return turns, fmt.Errorf("handoff QA round %d answer: %w", round, err)
		}

		turns = append(turns, qaTurn{
			Question: questions,
			Answer:   answer,
		})

		slog.Info("[Handoff] QA round completed",
			"round", round,
			"questions_len", len(questions),
			"answer_len", len(answer),
		)

		// Augment handoff doc with Q&A for the next round so the LLM can
		// verify the gaps are now addressed.
		handoffDoc = fmt.Sprintf(
			"%s\n\n## Q&A Round %d\n\n**Q:** %s\n\n**A:** %s",
			handoffDoc, round+1, questions, answer,
		)

		span.End()
	}

	return turns, nil
}
