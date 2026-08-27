package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"github.com/tiancaiamao/ai/pkg/truncate"
)

// helper: create a temp dir with a workspace and edit tool
func newEditToolInTempDir(t *testing.T) (*EditTool, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return NewEditTool(ws), dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// EditTool interface
// ---------------------------------------------------------------------------

func TestEditTool_Name(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)
	if tool.Name() != "edit" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "edit")
	}
}

func TestEditTool_Description(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestEditTool_Parameters(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// ---------------------------------------------------------------------------
// Exact match replacement
// ---------------------------------------------------------------------------

func TestEditTool_ExactMatch(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "hello world\nfoo bar\n")

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "hello world",
		"newText": "hello universe",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected result content blocks")
	}

	got := readFile(t, dir, "test.txt")
	want := "hello universe\nfoo bar\n"
	if got != want {
		t.Errorf("file content = %q, want %q", got, want)
	}
}

func TestEditTool_ExactMatchMultiline(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	content := "package main\n\nfunc main() {\n\treturn\n}\n"
	writeFile(t, dir, "main.go", content)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "main.go",
		"oldText": "func main() {\n\treturn\n}",
		"newText": "func main() {\n\tfmt.Println(\"hello\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected result")
	}

	got := readFile(t, dir, "main.go")
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("expected replacement to contain fmt.Println, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Fuzzy match replacement
// ---------------------------------------------------------------------------

func TestEditTool_NormalizedMatchTrailingWhitespace(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "  hello world  \nfoo bar\n")

	// Should match because we normalize trailing whitespace
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "  hello world", // no trailing spaces
		"newText": "  hello universe",
	})
	if err != nil {
		t.Fatalf("Execute (normalized): %v", err)
	}

	got := readFile(t, dir, "test.txt")
	if !strings.Contains(got, "hello universe") {
		t.Errorf("normalized match should have replaced text, got %q", got)
	}
	// Verify leading whitespace is preserved
	if !strings.Contains(got, "  hello universe") {
		t.Errorf("leading whitespace not preserved, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestEditTool_FileNotFound(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "nonexistent.txt",
		"oldText": "foo",
		"newText": "bar",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "failed to read file") {
		t.Errorf("error = %v, want read file error", err)
	}
}

func TestEditTool_NoMatch(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "hello world\n")

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "this text does not exist",
		"newText": "bar",
	})
	if err == nil {
		t.Fatal("expected error when no match found")
	}
}

func TestEditTool_MissingPathArg(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"oldText": "foo",
		"newText": "bar",
	})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestEditTool_MissingOldTextArg(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"newText": "bar",
	})
	if err == nil {
		t.Fatal("expected error for missing oldText")
	}
}

func TestEditTool_MissingNewTextArg(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "foo",
	})
	if err == nil {
		t.Fatal("expected error for missing newText")
	}
}

// ---------------------------------------------------------------------------
// Diff output verification
// ---------------------------------------------------------------------------

func TestEditTool_DiffInResult(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "line1\nline2\nline3\n")

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "line2",
		"newText": "LINE_TWO",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "-line2") {
		t.Errorf("diff should contain removed line, got: %s", text)
	}
	if !strings.Contains(text, "+LINE_TWO") {
		t.Errorf("diff should contain added line, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// findMatch (unit tests for internal logic)
// ---------------------------------------------------------------------------

func TestFindMatch_ExactMatch(t *testing.T) {
	content := "foo bar baz\nhello world\n"
	match, err := findMatch(content, "hello world")
	if err != nil {
		t.Fatalf("findMatch: %v", err)
	}
	if match.strategy != "exact" {
		t.Errorf("exact match strategy = %s, want 'exact'", match.strategy)
	}
	if content[match.start:match.end] != "hello world" {
		t.Errorf("match text = %q", content[match.start:match.end])
	}
}

func TestFindMatch_EmptyOldText(t *testing.T) {
	// Empty oldText should return error
	_, err := findMatch("some content", "")
	if err == nil {
		t.Fatal("expected error when oldText is empty")
	}
}

func TestFindMatch_NoMatchInContent(t *testing.T) {
	_, err := findMatch("short content", "a completely different text that is very long and unique")
	if err == nil {
		t.Fatal("expected error when no match possible")
	}
}

func TestFindMatch_NormalizedMatch(t *testing.T) {
	content := "  hello world  \nsecond line\n"
	// oldText with trailing space that differs from content should still match via normalization
	match, err := findMatch(content, "  hello world  ") // same trailing spaces
	if err != nil {
		t.Fatalf("findMatch normalized: %v", err)
	}
	// This will be exact match because the oldText exactly matches content's first line (including trailing spaces)
	if match.strategy != "exact" {
		t.Errorf("expected exact strategy (oldText matches exactly including trailing spaces), got %s", match.strategy)
	}
	if match.start < 0 || match.end > len(content) {
		t.Errorf("match bounds [%d,%d] out of range [0,%d]", match.start, match.end, len(content))
	}
}

func TestFindMatch_TrailingWhitespaceNormalized(t *testing.T) {
	content := "  hello world  \nsecond line\n"
	// oldText without trailing whitespace should still match via normalization
	// but exact match should fail
	_, err := findMatch(content, "  hello world\n")
	if err != nil {
		t.Fatalf("findMatch with newline: %v", err)
	}
	// This should succeed via normalization (content's trailing spaces are stripped)
}

func TestFindMatch_UnicodeNormalization(t *testing.T) {
	content := "“Smart quotes” and text" // Actually using smart quotes
	// oldText with ASCII quotes should match smart quotes
	match, err := findMatch(content, "\"Smart quotes\"")
	if err != nil {
		t.Fatalf("findMatch unicode: %v", err)
	}
	// Should match via Unicode normalization
	if match.strategy != "normalized" {
		t.Errorf("expected normalized strategy for Unicode, got %s", match.strategy)
	}
}

// ---------------------------------------------------------------------------
// normalizeForMatch (unit tests)
// ---------------------------------------------------------------------------

func TestNormalizeForMatch_TrailingWhitespace(t *testing.T) {
	input := "  hello world  \n  second line  \n"
	expected := "  hello world\n  second line\n"
	result := normalizeForMatch(input)
	if result != expected {
		t.Errorf("normalizeForMatch:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestNormalizeForMatch_LeadingWhitespacePreserved(t *testing.T) {
	input := "    hello\n  world\n"
	result := normalizeForMatch(input)
	// Check that leading whitespace is preserved
	lines := strings.Split(result, "\n")
	if !strings.HasPrefix(lines[0], "    ") {
		t.Errorf("leading whitespace not preserved in line 0: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  ") {
		t.Errorf("leading whitespace not preserved in line 1: %q", lines[1])
	}
}

func TestNormalizeUnicode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"smart 'quotes'", "smart 'quotes'"},
		{`smart "quotes"`, `smart "quotes"`},
		{"em—dash", "em-dash"},
		{"en–dash", "en-dash"},
		{"ellipsis…", "ellipsis..."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeUnicode(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeUnicode(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// generateDiff (unit tests)
// ---------------------------------------------------------------------------

func TestGenerateDiff_Basic(t *testing.T) {
	content := "line1\nline2\nline3\nline4\n"
	diff := generateDiff(content, 6, 12, "LINE_TWO")
	if !strings.Contains(diff, "-line2") {
		t.Errorf("diff should contain -line2, got: %s", diff)
	}
	if !strings.Contains(diff, "+LINE_TWO") {
		t.Errorf("diff should contain +LINE_TWO, got: %s", diff)
	}
}

func TestGenerateDiff_Header(t *testing.T) {
	content := "a\nb\nc\n"
	diff := generateDiff(content, 2, 3, "B")
	if !strings.Contains(diff, "@@") {
		t.Errorf("diff should contain @@ header, got: %s", diff)
	}
}

// ---------------------------------------------------------------------------
// truncate.TruncateString (unit tests)
// ---------------------------------------------------------------------------

func TestTruncateString_Short(t *testing.T) {
	if s := truncate.TruncateString("hi", 10); s != "hi" {
		t.Errorf("TruncateString(hi,10) = %q, want %q", s, "hi")
	}
}

func TestTruncateString_Long(t *testing.T) {
	if s := truncate.TruncateString("hello world", 8); s != "hello..." {
		t.Errorf("TruncateString(hello world,8) = %q, want %q", s, "hello...")
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// resolvePath (unit tests via tool execution)
// ---------------------------------------------------------------------------

func TestEditTool_ResolveAbsolutePath(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "content here")

	absPath := filepath.Join(dir, "test.txt")
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    absPath,
		"oldText": "content here",
		"newText": "replaced",
	})
	if err != nil {
		t.Fatalf("Execute with absolute path: %v", err)
	}
	got := readFile(t, dir, "test.txt")
	if got != "replaced" {
		t.Errorf("content = %q, want %q", got, "replaced")
	}
}

// TestEdit_AmbiguousMatchListsLocations verifies goose-style error reporting:
// when oldText matches multiple locations, every location is listed with its
// line number instead of a bare "not found" failure.
func TestEdit_AmbiguousMatchListsLocations(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "dup here\nmid\ndup here\n")

	_, err := tool.Execute(t.Context(), map[string]any{
		"path": "a.txt", "oldText": "dup here", "newText": "X",
	})
	if err == nil || !strings.Contains(err.Error(), "matches 2 locations") {
		t.Fatalf("err = %v, want multiple-match error", err)
	}
	if !strings.Contains(err.Error(), "L1") || !strings.Contains(err.Error(), "L3") {
		t.Fatalf("error should list L1 and L3, got: %v", err)
	}
}

// TestEdit_NoMatchErrorSuggestsSimilarContext verifies the not-found error
// surfaces nearby lines that resemble the requested text.
func TestEdit_NoMatchErrorSuggestsSimilarContext(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "func handleRequest(w http) {\n\treturn\n}\n")

	_, err := tool.Execute(t.Context(), map[string]any{
		"path": "a.txt", "oldText": "func handelRequest(w http) {", "newText": "X",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "handleRequest") || !strings.Contains(err.Error(), "L1") {
		t.Fatalf("no-match error lacks suggestion, got: %v", err)
	}
}
