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
			name: "content inside tags",
			msg:  makeTextMessage("assistant", "<handoff_complete>\nContent here\n</handoff_complete>"),
			want: "Content here",
		},
		{
			name: "content after open tag, no closing tag",
			msg:  makeTextMessage("assistant", "<handoff_complete>\nContent here"),
			want: "Content here",
		},
		{
			name: "text before marker is not the doc",
			msg:  makeTextMessage("assistant", "Some text\n<handoff_complete>\nReal content\n</handoff_complete>"),
			want: "Real content",
		},
		{
			name: "no marker at all",
			msg:  makeTextMessage("assistant", "No marker at all"),
			want: "",
		},
		{
			name: "content inside tags with surrounding text",
			msg:  makeTextMessage("assistant", "Intro text\n<handoff_complete>\n## Handoff\nTask: X\n</handoff_complete>\nTrailing"),
			want: "## Handoff\nTask: X",
		},
		{
			name: "open tag only with nothing after",
			msg:  makeTextMessage("assistant", "Task: implement X\n<handoff_complete>"),
			want: "",
		},
		{
			name: "multi-line doc between tags",
			msg:  makeTextMessage("assistant", "<handoff_complete>\n## Status\n- Done: A\n- Pending: B\n</handoff_complete>"),
			want: "## Status\n- Done: A\n- Pending: B",
		},
		{
			name: "nil message returns empty",
			msg:  nil,
			want: "",
		},
		{
			name: "empty message returns empty",
			msg:  &agentctx.AgentMessage{Role: "assistant", Content: []agentctx.ContentBlock{}},
			want: "",
		},
		{
			name: "content spans multiple blocks inside tags",
			msg: makeTextMessage("assistant",
				"<handoff_complete>",
				"Line one",
				"Line two",
				"</handoff_complete>"),
			want: "Line one\nLine two",
		},
		{
			name: "open and close in different blocks",
			msg: makeTextMessage("assistant",
				"<handoff_complete>Start content",
				"more content</handoff_complete>"),
			want: "Start content\nmore content",
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
	msg := makeTextMessage("assistant", "<handoff_complete>\n"+docText+"\n</handoff_complete>")

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
