package agent

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func makeTextMessage(role string, texts ...string) *agentctx.AgentMessage {
	msg := &agentctx.AgentMessage{
		Role:    role,
		Content: []agentctx.ContentBlock{},
	}
	for _, t := range texts {
		msg.Content = append(msg.Content, agentctx.TextContent{Type: "text", Text: t})
	}
	return msg
}

func TestHasHandoffMarker(t *testing.T) {
	tests := []struct {
		name string
		msg  *agentctx.AgentMessage
		want bool
	}{
		{
			name: "marker present at end",
			msg:  makeTextMessage("assistant", "Here is the handoff doc.\n<handoff_complete>"),
			want: true,
		},
		{
			name: "marker in middle of text",
			msg:  makeTextMessage("assistant", "Doc content <handoff_complete> trailing text"),
			want: true,
		},
		{
			name: "marker in second block",
			msg: makeTextMessage("assistant",
				"First block without marker",
				"Second block <handoff_complete>"),
			want: true,
		},
		{
			name: "marker absent — plain text",
			msg:  makeTextMessage("assistant", "Just a regular response"),
			want: false,
		},
		{
			name: "marker absent — multiple blocks",
			msg: makeTextMessage("assistant",
				"Block one",
				"Block two"),
			want: false,
		},
		{
			name: "nil message",
			msg:  nil,
			want: false,
		},
		{
			name: "empty content",
			msg:  &agentctx.AgentMessage{Role: "assistant", Content: []agentctx.ContentBlock{}},
			want: false,
		},
		{
			name: "marker as user message",
			msg:  makeTextMessage("user", "<handoff_complete>"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasHandoffMarker(tt.msg)
			if got != tt.want {
				t.Errorf("hasHandoffMarker() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractHandoffDoc(t *testing.T) {
	tests := []struct {
		name string
		msg  *agentctx.AgentMessage
		want string
	}{
		{
			name: "marker at end of single block",
			msg:  makeTextMessage("assistant", "Task: implement X\nStatus: done\n<handoff_complete>"),
			want: "Task: implement X\nStatus: done\n",
		},
		{
			name: "marker in middle of text",
			msg:  makeTextMessage("assistant", "Doc content <handoff_complete> trailing text"),
			want: "Doc content ",
		},
		{
			name: "marker in second block — first block preserved",
			msg: makeTextMessage("assistant",
				"First block content",
				"Second <handoff_complete> rest"),
			want: "First block content\nSecond ",
		},
		{
			name: "marker in first of three blocks",
			msg: makeTextMessage("assistant",
				"Alpha <handoff_complete>",
				"Beta",
				"Gamma"),
			want: "Alpha ",
		},
		{
			name: "nil message returns empty",
			msg:  nil,
			want: "",
		},
		{
			name: "no marker returns all text joined",
			msg: makeTextMessage("assistant",
				"Block A",
				"Block B"),
			want: "Block A\nBlock B",
		},
		{
			name: "marker at very start",
			msg:  makeTextMessage("assistant", "<handoff_complete>rest"),
			want: "",
		},
		{
			name: "empty blocks before marker",
			msg: makeTextMessage("assistant",
				"",
				"<handoff_complete>"),
			want: "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHandoffDoc(tt.msg)
			if got != tt.want {
				t.Errorf("extractHandoffDoc() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractHandoffDoc_RoundTripWithHasHandoffMarker(t *testing.T) {
	// A message that has the marker should produce a non-nil doc when extracted,
	// and the extracted doc should not contain the marker.
	docText := "## Handoff\n\nTask complete.\nNext: run tests."
	msg := makeTextMessage("assistant", docText+"<handoff_complete>")

	if !hasHandoffMarker(msg) {
		t.Fatal("expected hasHandoffMarker to return true")
	}

	extracted := extractHandoffDoc(msg)
	if extracted != docText {
		t.Errorf("extracted doc mismatch:\n got=%q\nwant=%q", extracted, docText)
	}
	if strings.Contains(extracted, handoffCompleteMarker) {
		t.Error("extracted doc should not contain the marker")
	}
}
