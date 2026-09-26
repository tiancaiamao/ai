package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestTruncateToolContentTruncatesToMaxChars(t *testing.T) {
	longText := strings.Repeat("a", 10001)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: longText},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash", "call_1", t.TempDir(), "run-1")
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

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{}, "read", "call_2", t.TempDir(), "run-1")
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

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 204800}, "bash", "call_3", t.TempDir(), "run-1")
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result[0])
	}
	if len(text.Text) > maxToolOutputMaxChars {
		t.Fatalf("oversized tool output limit should be clamped to %d, got %d", maxToolOutputMaxChars, len(text.Text))
	}
}

func TestTruncateToolContentPreservesImageBlocks(t *testing.T) {
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "ok"},
		agentctx.ImageContent{Type: "image", Data: "base64", MimeType: "image/png"},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10}, "read", "call_4", t.TempDir(), "run-1")
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

	if a.LoopConfig.ToolOutput.MaxChars != maxToolOutputMaxChars {
		t.Fatalf("expected SetToolOutputLimits to clamp maxChars to %d, got %d", maxToolOutputMaxChars, a.LoopConfig.ToolOutput.MaxChars)
	}
}

func TestTruncateToolContentOffloadsFullOutput(t *testing.T) {
	sessionDir := t.TempDir()
	longText := strings.Repeat("x", 12000)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: longText},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash", "callu_offload", sessionDir, "")
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result[0])
	}

	// Final output still respects maxChars and contains the marker with path.
	if len(text.Text) > 10000 {
		t.Fatalf("truncated text exceeds limit: got %d > 10000", len(text.Text))
	}
	path := filepath.Join(sessionDir, "toolout", "callu_offload.txt")
	if !strings.Contains(text.Text, "Full output: 1 lines / "+humanByteSize(len(longText))+" at "+path) {
		t.Fatalf("marker should include size metadata and path %s, got: %.200s", path, text.Text)
	}
	if !strings.Contains(text.Text, "Use read (offset/limit) or grep") {
		t.Fatalf("marker should suggest read/grep, got: %.200s", text.Text)
	}

	// File must contain the full original output.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected offload file: %v", err)
	}
	if string(data) != longText {
		t.Fatalf("offloaded content differs from original output")
	}
}

func TestOffloadNamingIdempotentAndFallsBackToHash(t *testing.T) {
	sessionDir := t.TempDir()

	// Same tool call id + same content reuses the same file without rewrite.
	if p1 := offloadTruncatedToolOutput("hello", "call_a", sessionDir, "run-1"); p1 != filepath.Join(sessionDir, "toolout", "call_a.txt") {
		t.Fatalf("unexpected path: %q", p1)
	}
	info, err := os.Stat(filepath.Join(sessionDir, "toolout", "call_a.txt"))
	if err != nil {
		t.Fatalf("expected file: %v", err)
	}
	if p2 := offloadTruncatedToolOutput("hello", "call_a", sessionDir, "run-1"); p2 == "" {
		t.Fatal("second call should succeed")
	}
	info2, err := os.Stat(filepath.Join(sessionDir, "toolout", "call_a.txt"))
	if err != nil || !info2.ModTime().Equal(info.ModTime()) {
		t.Fatal("identical content should not be rewritten")
	}

	// Unsafe/empty tool call id falls back to content hash naming.
	unsafe := offloadTruncatedToolOutput("data", "../../evil", sessionDir, "")
	sum := sha256.Sum256([]byte("data"))
	want := filepath.Join(sessionDir, "toolout", hex.EncodeToString(sum[:8])+".txt")
	if unsafe != want {
		t.Fatalf("expected hash-named fallback file %q, got %q", want, unsafe)
	}

	// No session dir: falls back to /tmp with run id.
	tmpPath := offloadTruncatedToolOutput("data2", "", "", "myrun")
	if !filepath.IsAbs(tmpPath) || !strings.HasPrefix(tmpPath, "/tmp/ai-toolout-myrun-") {
		t.Fatalf("expected /tmp fallback with run id, got %q", tmpPath)
	}
	t.Cleanup(func() { os.Remove(tmpPath) })
}

func TestOffloadSkipsOversizedOutput(t *testing.T) {
	sessionDir := t.TempDir()
	huge := strings.Repeat("z", toolOutputOffloadCap+1)

	if path := offloadTruncatedToolOutput(huge, "call_big", sessionDir, ""); path != "" {
		t.Fatalf("expected skip for oversized output, got %q", path)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "toolout")); !os.IsNotExist(err) {
		t.Fatal("no toolout dir should be created when offload is skipped")
	}

	// truncateToolContent still truncates, without an offload path in the marker.
	blocks := []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: huge}}
	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash", "call_big", sessionDir, "")
	text := result[0].(agentctx.TextContent)
	if len(text.Text) > 10000 {
		t.Fatalf("truncated text exceeds limit: %d", len(text.Text))
	}
	if strings.Contains(text.Text, "use read tool") {
		t.Fatal("marker should not point at an offload file when offload is skipped")
	}
}

func TestTruncateToolContentNoFileWhenNotTruncated(t *testing.T) {
	sessionDir := t.TempDir()
	blocks := []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "short"}}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash", "call_small", sessionDir, "")
	text := result[0].(agentctx.TextContent)
	if text.Text != "short" {
		t.Fatalf("short output should be preserved, got %q", text.Text)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "toolout")); !os.IsNotExist(err) {
		t.Fatal("no toolout dir should be created when output is not truncated")
	}
}

func TestTruncateToolContentMultipleTruncatedBlocksUseDistinctFiles(t *testing.T) {
	sessionDir := t.TempDir()
	first := strings.Repeat("a", 12000)
	second := strings.Repeat("b", 11000)
	blocks := []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: first},
		agentctx.TextContent{Type: "text", Text: second},
	}

	result := truncateToolContent(context.Background(), blocks, ToolOutputLimits{MaxChars: 10000}, "bash", "callu_multi", sessionDir, "")
	for i, want := range []string{first, second} {
		text, ok := result[i].(agentctx.TextContent)
		if !ok {
			t.Fatalf("block %d: expected text content, got %T", i, result[i])
		}
		name := "callu_multi.txt"
		if i == 1 {
			name = "callu_multi-2.txt"
		}
		path := filepath.Join(sessionDir, "toolout", name)
		if !strings.Contains(text.Text, "Full output: 1 lines / "+humanByteSize(len(want))+" at "+path) {
			t.Fatalf("block %d: marker should include metadata and point at %s, got: %.200s", i, path, text.Text)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("block %d: expected offload file %s: %v", i, path, err)
		}
		if string(data) != want {
			t.Fatalf("block %d: offloaded content differs from original output", i)
		}
	}
}

func TestOffloadDuplicateCallIDDisambiguatesByHash(t *testing.T) {
	sessionDir := t.TempDir()

	// Same tool call id, different content (duplicate ids from the provider):
	// the second offload must not clobber the first file.
	p1 := offloadTruncatedToolOutput("content one", "call_dup", sessionDir, "")
	p2 := offloadTruncatedToolOutput("content two", "call_dup", sessionDir, "")
	if p1 != filepath.Join(sessionDir, "toolout", "call_dup.txt") {
		t.Fatalf("unexpected first path: %q", p1)
	}
	if p2 == p1 {
		t.Fatal("second offload with different content should get a distinct path")
	}
	data, err := os.ReadFile(p1)
	if err != nil || string(data) != "content one" {
		t.Fatalf("first offload file was clobbered: %q (%v)", string(data), err)
	}

	// Repeating the second offload is idempotent.
	if again := offloadTruncatedToolOutput("content two", "call_dup", sessionDir, ""); again != p2 {
		t.Fatalf("expected idempotent reuse of %q, got %q", p2, again)
	}
}
