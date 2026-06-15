package agent

import (
	"regexp"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
)

// Delta compaction is an incremental context-compression mechanism. Instead of
// compressing the whole conversation at once, it asks the model to summarize
// only the "delta" — the task messages accumulated since the last delta
// compaction boundary (a delta_summary message). This file implements the
// decision engine: token estimation, trigger thresholds, protected-boundary
// calculation, and the LLM response parsing.
//
// Design reference: docs/context-mgmt-redesign/final-v3.md (D8, D9, D13, D14).

// Delta tier thresholds (absolute token values, not percentages).
const (
	DeltaTierAsk       = 30000  // Start asking if tool call interval met
	DeltaTier50K       = 50000  // Ask more frequently
	DeltaTier80K       = 80000  // Ask even more frequently
	DeltaTierHardLimit = 120000 // Forced compaction, no decision
)

// Tool call intervals per tier: how many tool calls to wait after a previous
// "no" decision before asking again at that tier.
const (
	ToolCallInterval30K = 10
	ToolCallInterval50K = 7
	ToolCallInterval80K = 3
)

// ProtectedTokenBudget is the number of recent tokens always kept verbatim
// (never compressed) when calculating the protected boundary. Expressed in
// tokens, not characters.
const ProtectedTokenBudget = 10000

// metadata.Kind values that delta compaction cares about.
const (
	kindDeltaSummary       = "delta_summary"
	kindRuntimeState       = "runtime_state"
	kindCompactionDecision = "context_compaction_decision"
)

// DeltaCompactionTrigger describes what (if anything) the loop should inject.
type DeltaCompactionTrigger int

const (
	TriggerNone     DeltaCompactionTrigger = iota // Don't trigger
	TriggerDecision                               // Inject <agent:context_compaction_decision>
	TriggerForced                                 // Inject <agent:context_compaction> (no decision)
)

// ProtectedRange describes the slice of messages [StartIndex, EndIndex) that
// must be retained verbatim during delta compaction.
type ProtectedRange struct {
	StartIndex int // Index into messages slice where protected messages begin
	EndIndex   int // End index (exclusive)
}

// CompactionDecision is the parsed result of an LLM compaction response.
type CompactionDecision struct {
	ShouldCompact bool
	Summary       string
	Parsed        bool // false if response was unparseable (treated as "no" per D7)
}

// EstimateDeltaTokens counts tokens of agent-visible task messages since the
// last delta_compact entry (a delta_summary message). It iterates backwards
// from the end of messages, skipping summary/runtime/decision messages, and
// stops at the delta_summary boundary.
//
// Token heuristic matches the rest of the codebase: ceil(chars/4), images ~1200
// tokens.
func EstimateDeltaTokens(messages []agentctx.AgentMessage) int {
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		kind := messageKind(messages[i])
		if kind == kindDeltaSummary {
			// Boundary reached: everything before this was already summarized.
			break
		}
		// Skip non-task messages that shouldn't count toward the delta.
		if kind == kindRuntimeState || kind == kindCompactionDecision {
			continue
		}
		total += agentctx.EstimateMessageTokens(messages[i])
	}
	return total
}

// CheckDeltaCompactionTrigger determines whether to trigger delta compaction
// based on the current delta token count and the number of tool calls since the
// last time we asked. Once delta reaches the hard limit, compaction is forced
// regardless of the tool-call interval (see D9).
func CheckDeltaCompactionTrigger(deltaTokens, toolCallsSinceLastCheck int) DeltaCompactionTrigger {
	switch {
	case deltaTokens >= DeltaTierHardLimit:
		return TriggerForced
	case deltaTokens >= DeltaTier80K:
		if toolCallsSinceLastCheck >= ToolCallInterval80K {
			return TriggerDecision
		}
		return TriggerNone
	case deltaTokens >= DeltaTier50K:
		if toolCallsSinceLastCheck >= ToolCallInterval50K {
			return TriggerDecision
		}
		return TriggerNone
	case deltaTokens >= DeltaTierAsk:
		if toolCallsSinceLastCheck >= ToolCallInterval30K {
			return TriggerDecision
		}
		return TriggerNone
	default:
		return TriggerNone
	}
}

// DeltaTier returns a human-readable label for the current delta token tier,
// for telemetry.
func DeltaTier(deltaTokens int) string {
	switch {
	case deltaTokens >= DeltaTierHardLimit:
		return "critical"
	case deltaTokens >= DeltaTier80K:
		return "high"
	case deltaTokens >= DeltaTier50K:
		return "medium"
	case deltaTokens >= DeltaTierAsk:
		return "ask"
	default:
		return "low"
	}
}

// CalculateProtectedBoundary determines which recent messages to keep verbatim
// (not compress). It reverse-scans the delta range [deltaStartIndex, len) and
// accumulates tokens until the ProtectedTokenBudget is reached. The resulting
// cut-point is then adjusted so it never splits a tool-call/tool-result pair:
// if the boundary lands on a tool result whose issuing assistant message is
// below the cut, the cut moves back to include that assistant message.
func CalculateProtectedBoundary(messages []agentctx.AgentMessage, deltaStartIndex int) ProtectedRange {
	n := len(messages)
	if deltaStartIndex < 0 {
		deltaStartIndex = 0
	}
	if deltaStartIndex >= n {
		return ProtectedRange{StartIndex: n, EndIndex: n}
	}

	// Default: protect the entire delta range (used when the budget is never
	// reached, i.e. the delta is smaller than the protected budget).
	cut := deltaStartIndex
	acc := 0
	for i := n - 1; i >= deltaStartIndex; i-- {
		acc += agentctx.EstimateMessageTokens(messages[i])
		if acc >= ProtectedTokenBudget {
			cut = i
			break
		}
	}

	cut = adjustCutForToolCallPairs(messages, deltaStartIndex, cut)
	return ProtectedRange{StartIndex: cut, EndIndex: n}
}

// adjustCutForToolCallPairs moves the cut-point back (to a smaller index) so
// that no tool result in the protected region is separated from the assistant
// message that issued it. Because tool-call sequences are contiguous, the only
// split case is when messages[cut] itself is a toolResult.
func adjustCutForToolCallPairs(messages []agentctx.AgentMessage, deltaStartIndex, cut int) int {
	n := len(messages)
	for cut < n && messages[cut].Role == "toolResult" {
		id := messages[cut].ToolCallID
		found := -1
		for j := cut - 1; j >= deltaStartIndex; j-- {
			if messages[j].Role != "assistant" {
				continue
			}
			if id == "" {
				found = j
				break
			}
			for _, tc := range messages[j].ExtractToolCalls() {
				if tc.ID == id {
					found = j
					break
				}
			}
			if found >= 0 {
				break
			}
		}
		if found < 0 {
			// No issuing assistant in range; nothing more we can do.
			break
		}
		cut = found
		// messages[cut] is now an assistant message, so the loop exits.
	}
	return cut
}

// ParseCompactionResponse extracts <decision> and <summary> tags from an LLM
// compaction response. An unrecognized format yields Parsed:false, which the
// caller treats as a "no" decision (D7: no silent fallback to compaction).
func ParseCompactionResponse(responseText string) CompactionDecision {
	decisionMatch := compactionDecisionTagRe.FindStringSubmatch(responseText)
	if len(decisionMatch) < 2 {
		return CompactionDecision{Parsed: false}
	}
	val := strings.ToLower(strings.TrimSpace(decisionMatch[1]))
	if val != "yes" && val != "no" {
		return CompactionDecision{Parsed: false}
	}
	result := CompactionDecision{Parsed: true}
	if val == "yes" {
		result.ShouldCompact = true
		if sm := compactionSummaryTagRe.FindStringSubmatch(responseText); len(sm) >= 2 {
			result.Summary = strings.TrimSpace(sm[1])
		}
	}
	return result
}

// ParseForcedCompactionResponse extracts a <summary> tag from a forced
// compaction response (which has no <decision> tag). Returns the summary and
// whether a valid summary was found. An unrecognized format yields ok=false,
// which the caller treats as declined (D7).
func ParseForcedCompactionResponse(responseText string) (summary string, ok bool) {
	sm := compactionSummaryTagRe.FindStringSubmatch(responseText)
	if len(sm) < 2 {
		return "", false
	}
	summary = strings.TrimSpace(sm[1])
	if summary == "" {
		return "", false
	}
	return summary, true
}

// compactionDecisionTagRe matches <decision>...</decision> (case-insensitive,
// dotall so trailing content is tolerated).
var compactionDecisionTagRe = regexp.MustCompile(`(?is)<decision>\s*(.*?)\s*</decision>`)

// compactionSummaryTagRe matches <summary>...</summary> (case-insensitive,
// dotall so multi-line summaries are captured).
var compactionSummaryTagRe = regexp.MustCompile(`(?is)<summary>\s*(.*?)\s*</summary>`)

// BuildDecisionMessage creates the <agent:context_compaction_decision> user
// message. This is a short instruction (~200 tokens) that does NOT repeat the
// delta content — the delta messages are already in RecentMessages (cache hit).
func BuildDecisionMessage() agentctx.AgentMessage {
	return newCompactionPromptMessage(prompt.DeltaCompactionDecisionPrompt())
}

// BuildForcedCompactionMessage creates the <agent:context_compaction> user
// message, used when delta >= DeltaTierHardLimit. No decision is requested,
// only a summary.
func BuildForcedCompactionMessage() agentctx.AgentMessage {
	return newCompactionPromptMessage(prompt.DeltaCompactionForcedPrompt())
}

func newCompactionPromptMessage(body string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "user",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: body},
		},
		Timestamp: time.Now().UnixMilli(),
		Metadata:  &agentctx.MessageMetadata{Kind: kindCompactionDecision},
	}
}

// messageKind returns the metadata.Kind of a message, or "" when absent.
func messageKind(m agentctx.AgentMessage) string {
	if m.Metadata == nil {
		return ""
	}
	return m.Metadata.Kind
}

// findDeltaStartIndex returns the index immediately after the most recent
// delta_summary message, or 0 when no boundary exists. Messages at or before
// this index are already summarized and must not be re-compressed.
func findDeltaStartIndex(messages []agentctx.AgentMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messageKind(messages[i]) == kindDeltaSummary {
			return i + 1
		}
	}
	return 0
}

// newDeltaSummaryMessage builds the in-memory delta_summary message that
// replaces a compressed delta interval in RecentMessages. It mirrors the
// session package's deltaSummaryMessage shape (metadata.Kind = "delta_summary").
func newDeltaSummaryMessage(summary string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "user",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: summary},
		},
		Timestamp: time.Now().UnixMilli(),
		Metadata:  &agentctx.MessageMetadata{Kind: kindDeltaSummary},
	}
}
