package agent

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestTruncateToolContentTruncatesToMaxChars(t *testing.T) {
	longText := strings.Repeat("a", 10001)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: longText},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash")
	if len(result) != 1 {
		t.Fatalf("expected one content block, got %d", len(result))
	}

	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result[0])
	}
	if len(text.Text) > 10000 {
		t.Fatalf("truncated text exceeds limit: got %d > 10000", len(text.Text))
	}
	if !strings.Contains(text.Text, "tokens truncated") {
		t.Fatalf("expected truncation marker in output")
	}
}

func TestTruncateToolContentUsesDefaultLimitWhenUnset(t *testing.T) {
	longText := strings.Repeat("b", 12000)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: longText},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{}, "read")
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result[0])
	}
	if len(text.Text) > 10000 {
		t.Fatalf("default truncation limit not applied: got %d", len(text.Text))
	}
}

func TestTruncateToolContentClampsOversizedLimit(t *testing.T) {
	longText := strings.Repeat("c", 15000)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: longText},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 204800}, "bash")
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result[0])
	}
	if len(text.Text) > 10000 {
		t.Fatalf("oversized tool output limit should be clamped to 10000, got %d", len(text.Text))
	}
}

func TestTruncateToolContentPreservesImageBlocks(t *testing.T) {
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "ok"},
		agentctx.ImageContent{Type: "image", Data: "base64", MimeType: "image/png"},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10}, "read")
	if len(result) != 2 {
		t.Fatalf("expected two content blocks, got %d", len(result))
	}
	if _, ok := result[1].(agentctx.ImageContent); !ok {
		t.Fatalf("expected image content to be preserved, got %T", result[1])
	}
}

func TestSetToolOutputLimitsNormalizesLimit(t *testing.T) {
	a := &Agent{}
	a.SetToolOutputLimits(ToolOutputLimits{MaxChars: 204800})

	if a.LoopConfig.ToolOutput.MaxChars != 10000 {
		t.Fatalf("expected SetToolOutputLimits to clamp maxChars to 10000, got %d", a.LoopConfig.ToolOutput.MaxChars)
	}
}
