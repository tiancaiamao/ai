package agent

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// --- helpers --------------------------------------------------------------

func textMsg(role, text string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role:      role,
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: text}},
		Timestamp: 1,
	}
}

func textMsgKind(role, text, kind string) agentctx.AgentMessage {
	m := textMsg(role, text)
	m.Metadata = &agentctx.MessageMetadata{Kind: kind}
	return m
}

func assistantToolCall(id string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.ToolCallContent{ID: id, Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "x"}},
		},
		Timestamp: 1,
	}
}

func toolResult(id string) agentctx.AgentMessage {
	m := agentctx.NewToolResultMessage(id, "read",
		[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "ok"}}, false)
	return m
}

func imageMsg() agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "user",
		Content: []agentctx.ContentBlock{
			agentctx.ImageContent{Type: "image", Data: "abc", MimeType: "png"},
		},
	}
}

// tokensFor returns the expected ceil(len/4) token count for a text string.
func tokensFor(s string) int {
	return (len(s) + 3) / 4
}

// --- EstimateDeltaTokens --------------------------------------------------

func TestEstimateDeltaTokensEmpty(t *testing.T) {
	if got := EstimateDeltaTokens(nil); got != 0 {
		t.Fatalf("nil messages: got %d, want 0", got)
	}
}

func TestEstimateDeltaTokensCountsText(t *testing.T) {
	body := strings.Repeat("a", 4000) // 1000 tokens
	msgs := []agentctx.AgentMessage{
		textMsg("user", body),
		textMsg("assistant", body),
	}
	want := 2000
	if got := EstimateDeltaTokens(msgs); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestEstimateDeltaTokensStopsAtDeltaSummary(t *testing.T) {
	// delta_summary is the boundary; messages before it must not be counted.
	old := strings.Repeat("b", 4000) // 1000 tokens, should NOT be counted
	recent := strings.Repeat("a", 4000)
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", old, "user"),
		textMsgKind("user", "summary text", "delta_summary"),
		textMsg("user", recent),
		textMsg("assistant", recent),
	}
	want := 2000 // only the two recent messages
	if got := EstimateDeltaTokens(msgs); got != want {
		t.Fatalf("got %d, want %d (delta_summary should stop scan)", got, want)
	}
}

func TestEstimateDeltaTokensSkipsExcludedKinds(t *testing.T) {
	recent := strings.Repeat("a", 4000) // 1000 tokens
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", strings.Repeat("x", 4000), "runtime_state"),
		textMsgKind("user", strings.Repeat("y", 4000), "context_compaction_decision"),
		textMsgKind("user", strings.Repeat("z", 4000), "delta_summary"),
		textMsg("user", recent),
	}
	// Only the final recent message counts (1000 tokens). The runtime_state and
	// compaction_decision are skipped, and delta_summary stops the scan.
	want := 1000
	if got := EstimateDeltaTokens(msgs); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestEstimateDeltaTokensImages(t *testing.T) {
	msgs := []agentctx.AgentMessage{imageMsg()}
	// 4800 chars / 4 = 1200 tokens.
	if got := EstimateDeltaTokens(msgs); got != 1200 {
		t.Fatalf("image tokens: got %d, want 1200", got)
	}
}

// --- CheckDeltaCompactionTrigger -----------------------------------------

func TestCheckDeltaCompactionTrigger(t *testing.T) {
	cases := []struct {
		name      string
		delta     int
		toolCalls int
		want      DeltaCompactionTrigger
	}{
		{"below 30K never triggers", 0, 100, TriggerNone},
		{"below 30K never triggers 2", 29999, 100, TriggerNone},
		{"30K tier no interval", 30000, 9, TriggerNone},
		{"30K tier interval met", 30000, 10, TriggerDecision},
		{"30K tier interval exceeded", 49999, 50, TriggerDecision},
		{"50K tier no interval", 50000, 6, TriggerNone},
		{"50K tier interval met", 50000, 7, TriggerDecision},
		{"50K tier interval exceeded", 79999, 20, TriggerDecision},
		{"80K tier no interval", 80000, 2, TriggerNone},
		{"80K tier interval met", 80000, 3, TriggerDecision},
		{"80K tier interval exceeded", 119999, 10, TriggerDecision},
		{"hard limit forces regardless of interval 0", 120000, 0, TriggerForced},
		{"hard limit forces regardless of interval big", 200000, 100, TriggerForced},
		{"exactly 120K forces", 120000, 0, TriggerForced},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckDeltaCompactionTrigger(c.delta, c.toolCalls)
			if got != c.want {
				t.Fatalf("delta=%d toolCalls=%d: got %d, want %d", c.delta, c.toolCalls, got, c.want)
			}
		})
	}
}

// --- DeltaTier -----------------------------------------------------------

func TestDeltaTier(t *testing.T) {
	cases := []struct {
		delta int
		want  string
	}{
		{0, "low"},
		{29999, "low"},
		{30000, "ask"},
		{49999, "ask"},
		{50000, "medium"},
		{79999, "medium"},
		{80000, "high"},
		{119999, "high"},
		{120000, "critical"},
		{500000, "critical"},
	}
	for _, c := range cases {
		if got := DeltaTier(c.delta); got != c.want {
			t.Errorf("delta=%d: got %q, want %q", c.delta, got, c.want)
		}
	}
}

// --- ParseCompactionResponse ---------------------------------------------

func TestParseCompactionResponse(t *testing.T) {
	t.Run("yes with summary", func(t *testing.T) {
		text := "Let me decide.\n<decision>yes</decision>\n<summary>Did X and Y.</summary>"
		d := ParseCompactionResponse(text)
		if !d.Parsed || !d.ShouldCompact {
			t.Fatalf("expected parsed+compact, got %+v", d)
		}
		if d.Summary != "Did X and Y." {
			t.Fatalf("summary = %q", d.Summary)
		}
	})

	t.Run("no", func(t *testing.T) {
		d := ParseCompactionResponse("<decision>no</decision>")
		if !d.Parsed || d.ShouldCompact {
			t.Fatalf("expected parsed+no-compact, got %+v", d)
		}
		if d.Summary != "" {
			t.Fatalf("summary should be empty, got %q", d.Summary)
		}
	})

	t.Run("yes without summary", func(t *testing.T) {
		d := ParseCompactionResponse("<decision>yes</decision>")
		if !d.Parsed || !d.ShouldCompact {
			t.Fatalf("expected parsed+compact, got %+v", d)
		}
		if d.Summary != "" {
			t.Fatalf("summary should be empty, got %q", d.Summary)
		}
	})

	t.Run("case insensitive yes", func(t *testing.T) {
		d := ParseCompactionResponse("<decision>  YES  </decision><summary>s</summary>")
		if !d.Parsed || !d.ShouldCompact {
			t.Fatalf("expected parsed+compact, got %+v", d)
		}
		if d.Summary != "s" {
			t.Fatalf("summary = %q", d.Summary)
		}
	})

	t.Run("multiline summary", func(t *testing.T) {
		text := "<decision>yes</decision><summary>line1\nline2\nline3</summary>"
		d := ParseCompactionResponse(text)
		if !d.Parsed || !d.ShouldCompact {
			t.Fatalf("expected parsed+compact, got %+v", d)
		}
		if d.Summary != "line1\nline2\nline3" {
			t.Fatalf("summary = %q", d.Summary)
		}
	})

	t.Run("unparseable no tags", func(t *testing.T) {
		d := ParseCompactionResponse("I will keep working on the task.")
		if d.Parsed || d.ShouldCompact {
			t.Fatalf("expected unparsed, got %+v", d)
		}
	})

	t.Run("unparseable invalid value", func(t *testing.T) {
		d := ParseCompactionResponse("<decision>maybe</decision>")
		if d.Parsed || d.ShouldCompact {
			t.Fatalf("expected unparsed for invalid value, got %+v", d)
		}
	})

	t.Run("uppercase tags", func(t *testing.T) {
		d := ParseCompactionResponse("<DECISION>no</DECISION>")
		if !d.Parsed || d.ShouldCompact {
			t.Fatalf("expected parsed+no-compact for uppercase tag, got %+v", d)
		}
	})
}

// --- BuildDecisionMessage / BuildForcedCompactionMessage -----------------

func TestBuildDecisionMessage(t *testing.T) {
	m := BuildDecisionMessage()
	if m.Role != "user" {
		t.Fatalf("role = %q, want user", m.Role)
	}
	if messageKind(m) != "context_compaction_decision" {
		t.Fatalf("kind = %q, want context_compaction_decision", messageKind(m))
	}
	body := m.ExtractText()
	if !strings.Contains(body, "<agent:context_compaction_decision>") {
		t.Fatalf("missing opening tag in %q", body)
	}
	if !strings.Contains(body, "</agent:context_compaction_decision>") {
		t.Fatalf("missing closing tag in %q", body)
	}
	if !strings.Contains(body, "<decision>yes or no</decision>") {
		t.Fatalf("missing decision format hint in %q", body)
	}
	if !strings.Contains(body, "<summary>summary content (if decision=yes)</summary>") {
		t.Fatalf("missing summary format hint in %q", body)
	}
}

func TestBuildForcedCompactionMessage(t *testing.T) {
	m := BuildForcedCompactionMessage()
	if m.Role != "user" {
		t.Fatalf("role = %q, want user", m.Role)
	}
	if messageKind(m) != "context_compaction_decision" {
		t.Fatalf("kind = %q, want context_compaction_decision", messageKind(m))
	}
	body := m.ExtractText()
	if !strings.Contains(body, "<agent:context_compaction>") {
		t.Fatalf("missing opening tag in %q", body)
	}
	if !strings.Contains(body, "</agent:context_compaction>") {
		t.Fatalf("missing closing tag in %q", body)
	}
	if strings.Contains(body, "<decision>") {
		t.Fatalf("forced message must not request a decision: %q", body)
	}
	if !strings.Contains(body, "<summary>summary content</summary>") {
		t.Fatalf("missing summary format hint in %q", body)
	}
}

// --- CalculateProtectedBoundary ------------------------------------------

func TestProtectedBoundaryEmptyDelta(t *testing.T) {
	// deltaStartIndex beyond the slice: empty delta range.
	msgs := []agentctx.AgentMessage{textMsg("user", "x")}
	r := CalculateProtectedBoundary(msgs, 5)
	if r.StartIndex != 1 || r.EndIndex != 1 {
		t.Fatalf("empty delta: got %+v, want {1 1}", r)
	}
}

func TestProtectedBoundarySmallDeltaProtectsAll(t *testing.T) {
	// Total delta < budget: protect everything in the delta range.
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", "summary", "delta_summary"),
		textMsg("user", strings.Repeat("a", 1000)), // 250 tokens
		textMsg("assistant", strings.Repeat("b", 1000)),
	}
	r := CalculateProtectedBoundary(msgs, 1)
	if r.StartIndex != 1 || r.EndIndex != 3 {
		t.Fatalf("small delta: got %+v, want {1 3}", r)
	}
}

func TestProtectedBoundaryLargeDeltaCutsAtBudget(t *testing.T) {
	// Three 10K-token messages; reverse scan hits budget on the last one.
	big := strings.Repeat("a", 40000) // 10000 tokens
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", "summary", "delta_summary"),
		textMsg("user", big),
		textMsg("assistant", big),
		textMsg("user", big),
	}
	r := CalculateProtectedBoundary(msgs, 1)
	// Reverse scan: msg[3]=10000 >= budget -> cut=3.
	if r.StartIndex != 3 || r.EndIndex != 4 {
		t.Fatalf("got %+v, want {3 4}", r)
	}
}

func TestProtectedBoundaryDoesNotSplitToolCallPair(t *testing.T) {
	// The large message is a tool result whose assistant is just below the
	// budget boundary. The cut must move back to include the assistant.
	largeResult := strings.Repeat("z", 40000) // 10000 tokens -> exactly budget
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", "summary", "delta_summary"), // idx 0
		assistantToolCall("call-1"),                     // idx 1
		agentctx.NewToolResultMessage("call-1", "read",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: largeResult}}, false), // idx 2
		textMsg("user", "small tail"), // idx 3
	}
	r := CalculateProtectedBoundary(msgs, 1)
	// Reverse scan: idx3 small, idx2 = 10000 >= budget -> cut=2 (a toolResult).
	// Adjustment must move cut back to idx1 (the assistant issuing call-1).
	if r.StartIndex != 1 {
		t.Fatalf("expected cut moved to assistant at idx 1, got %+v", r)
	}
	if r.EndIndex != 4 {
		t.Fatalf("end = %d, want 4", r.EndIndex)
	}
}

func TestProtectedBoundaryKeepsCompleteToolGroupBelow(t *testing.T) {
	// A complete tool-call group entirely below the cut is not a split and
	// must remain below (compressed). The cut lands on a plain text message.
	big := strings.Repeat("a", 40000) // 10000 tokens
	msgs := []agentctx.AgentMessage{
		textMsgKind("user", "summary", "delta_summary"), // idx 0
		assistantToolCall("call-1"),                     // idx 1
		toolResult("call-1"),                            // idx 2 (small)
		textMsg("user", big),                            // idx 3 (10000 tokens)
	}
	r := CalculateProtectedBoundary(msgs, 1)
	// Reverse scan: idx3=10000>=budget -> cut=3. messages[3] is text, no adjust.
	if r.StartIndex != 3 || r.EndIndex != 4 {
		t.Fatalf("got %+v, want {3 4}", r)
	}
}

func TestProtectedBoundaryNegativeStartClamped(t *testing.T) {
	msgs := []agentctx.AgentMessage{textMsg("user", "x")}
	r := CalculateProtectedBoundary(msgs, -5)
	if r.StartIndex != 0 || r.EndIndex != 1 {
		t.Fatalf("got %+v, want {0 1}", r)
	}
}

// Sanity check that the local token estimator agrees with ceil(len/4).
func TestEstimateDeltaMessageTokens(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"hello", 2},       // 5 -> ceil(5/4)=2
		{"hello world", 3}, // 11 -> ceil(11/4)=3
		{"1234", 1},        // 4 -> 1
		{"12345678", 2},    // 8 -> 2
	}
	for _, c := range cases {
		m := textMsg("user", c.text)
		if got := estimateDeltaMessageTokens(m); got != c.want {
			t.Errorf("text=%q: got %d, want %d", c.text, got, c.want)
		}
	}
	// Image is ~1200 tokens.
	if got := estimateDeltaMessageTokens(imageMsg()); got != 1200 {
		t.Errorf("image: got %d, want 1200", got)
	}
}

// Ensure tokensFor helper matches estimator for non-empty strings.
func TestTokensForHelper(t *testing.T) {
	for _, s := range []string{"a", "abcd", "abcde", strings.Repeat("x", 3999)} {
		if tokensFor(s) != estimateDeltaMessageTokens(textMsg("user", s)) {
			t.Errorf("mismatch for %q: tokensFor=%d estimator=%d", s, tokensFor(s), estimateDeltaMessageTokens(textMsg("user", s)))
		}
	}
}
