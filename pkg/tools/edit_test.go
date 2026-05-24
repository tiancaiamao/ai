package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
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

func TestEditTool_FuzzyMatchWhitespace(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)
	writeFile(t, dir, "test.txt", "  hello  world  \nfoo bar\n")

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"oldText": "hello world", // missing leading/trailing spaces
		"newText": "hello universe",
	})
	if err != nil {
		t.Fatalf("Execute (fuzzy): %v", err)
	}

	got := readFile(t, dir, "test.txt")
	if !strings.Contains(got, "hello universe") {
		t.Errorf("fuzzy match should have replaced text, got %q", got)
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
// findBestMatch (unit tests for internal logic)
// ---------------------------------------------------------------------------

func TestFindBestMatch_ExactMatch(t *testing.T) {
	content := "foo bar baz\nhello world\n"
	match, err := findBestMatch(content, "hello world")
	if err != nil {
		t.Fatalf("findBestMatch: %v", err)
	}
	if match.score != 0 {
		t.Errorf("exact match score = %d, want 0", match.score)
	}
	if content[match.start:match.end] != "hello world" {
		t.Errorf("match text = %q", content[match.start:match.end])
	}
}

func TestFindBestMatch_EmptyOldText(t *testing.T) {
	// Empty oldText matches at position 0 (strings.Index behavior)
	match, err := findBestMatch("some content", "")
	if err != nil {
		t.Fatalf("findBestMatch with empty oldText: %v", err)
	}
	if match.start != 0 || match.end != 0 {
		t.Errorf("empty match at [%d,%d], want [0,0]", match.start, match.end)
	}
}

func TestFindBestMatch_NoMatchInContent(t *testing.T) {
	_, err := findBestMatch("short content", "a completely different text that is very long and unique")
	if err == nil {
		t.Fatal("expected error when no match possible")
	}
}

func TestFindBestMatch_FuzzyMatch(t *testing.T) {
	content := "  hello world  \nsecond line\n"
	// oldText doesn't match exactly but is close
	match, err := findBestMatch(content, "hello world")
	if err != nil {
		t.Fatalf("findBestMatch fuzzy: %v", err)
	}
	if match.start < 0 || match.end > len(content) {
		t.Errorf("match bounds [%d,%d] out of range [0,%d]", match.start, match.end, len(content))
	}
}

// ---------------------------------------------------------------------------
// editDistance (unit tests)
// ---------------------------------------------------------------------------

func TestEditDistance_Identical(t *testing.T) {
	if d := editDistance("abc", "abc"); d != 0 {
		t.Errorf("editDistance(abc,abc) = %d, want 0", d)
	}
}

func TestEditDistance_EmptyStrings(t *testing.T) {
	if d := editDistance("", ""); d != 0 {
		t.Errorf("editDistance('','') = %d, want 0", d)
	}
	if d := editDistance("abc", ""); d != 3 {
		t.Errorf("editDistance(abc,'') = %d, want 3", d)
	}
	if d := editDistance("", "abc"); d != 3 {
		t.Errorf("editDistance('',abc) = %d, want 3", d)
	}
}

func TestEditDistance_SingleEdits(t *testing.T) {
	// One insertion
	if d := editDistance("ac", "abc"); d != 1 {
		t.Errorf("editDistance(ac,abc) = %d, want 1", d)
	}
	// One deletion
	if d := editDistance("abc", "ac"); d != 1 {
		t.Errorf("editDistance(abc,ac) = %d, want 1", d)
	}
	// One substitution
	if d := editDistance("abc", "axc"); d != 1 {
		t.Errorf("editDistance(abc,axc) = %d, want 1", d)
	}
}

func TestEditDistance_CompleteReplacement(t *testing.T) {
	if d := editDistance("abc", "xyz"); d != 3 {
		t.Errorf("editDistance(abc,xyz) = %d, want 3", d)
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
// truncateString (unit tests)
// ---------------------------------------------------------------------------

func TestTruncateString_Short(t *testing.T) {
	if s := truncateString("hi", 10); s != "hi" {
		t.Errorf("truncateString(hi,10) = %q, want %q", s, "hi")
	}
}

func TestTruncateString_Long(t *testing.T) {
	if s := truncateString("hello world", 5); s != "hello..." {
		t.Errorf("truncateString(hello world,5) = %q, want %q", s, "hello...")
	}
}

// ---------------------------------------------------------------------------
// computeMatchScore (unit tests)
// ---------------------------------------------------------------------------

func TestComputeMatchScore_Identical(t *testing.T) {
	lines := []string{"hello", "world"}
	if s := computeMatchScore(lines, lines); s != 0 {
		t.Errorf("computeMatchScore identical = %d, want 0", s)
	}
}

func TestComputeMatchScore_Different(t *testing.T) {
	a := []string{"hello"}
	b := []string{"world"}
	s := computeMatchScore(a, b)
	if s == 0 {
		t.Error("computeMatchScore for different strings should be > 0")
	}
}

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

// ---------------------------------------------------------------------------
// parseHashlineEditFromMap
// ---------------------------------------------------------------------------

func TestParseHashlineEditFromMap_SetLine(t *testing.T) {
	m := map[string]interface{}{
		"set_line": map[string]interface{}{
			"anchor":   "abc123",
			"new_text": "new content",
		},
	}
	edit, err := parseHashlineEditFromMap(m)
	if err != nil {
		t.Fatalf("parseHashlineEditFromMap: %v", err)
	}
	if edit.Type != HashlineEditSetLine {
		t.Errorf("type = %v, want HashlineEditSetLine", edit.Type)
	}
	if edit.Anchor != "abc123" {
		t.Errorf("Anchor = %q, want %q", edit.Anchor, "abc123")
	}
	if edit.NewText != "new content" {
		t.Errorf("NewText = %q, want %q", edit.NewText, "new content")
	}
}

func TestParseHashlineEditFromMap_ReplaceLines(t *testing.T) {
	m := map[string]interface{}{
		"replace_lines": map[string]interface{}{
			"start_anchor": "abc",
			"end_anchor":   "def",
			"new_text":     "replacement",
		},
	}
	edit, err := parseHashlineEditFromMap(m)
	if err != nil {
		t.Fatalf("parseHashlineEditFromMap: %v", err)
	}
	if edit.Type != HashlineEditReplaceLines {
		t.Errorf("type = %v, want HashlineEditReplaceLines", edit.Type)
	}
}

func TestParseHashlineEditFromMap_InsertAfter(t *testing.T) {
	m := map[string]interface{}{
		"insert_after": map[string]interface{}{
			"anchor": "abc",
			"text":   "inserted line",
		},
	}
	edit, err := parseHashlineEditFromMap(m)
	if err != nil {
		t.Fatalf("parseHashlineEditFromMap: %v", err)
	}
	if edit.Type != HashlineEditInsertAfter {
		t.Errorf("type = %v, want HashlineEditInsertAfter", edit.Type)
	}
}

func TestParseHashlineEditFromMap_Replace(t *testing.T) {
	m := map[string]interface{}{
		"replace": map[string]interface{}{
			"old_text": "foo",
			"new_text": "bar",
		},
	}
	edit, err := parseHashlineEditFromMap(m)
	if err != nil {
		t.Fatalf("parseHashlineEditFromMap: %v", err)
	}
	if edit.Type != HashlineEditReplace {
		t.Errorf("type = %v, want HashlineEditReplace", edit.Type)
	}
}

func TestParseHashlineEditFromMap_UnknownType(t *testing.T) {
	m := map[string]interface{}{
		"type": "unknown_type",
	}
	_, err := parseHashlineEditFromMap(m)
	if err == nil {
		t.Fatal("expected error for unknown edit type")
	}
}

// ---------------------------------------------------------------------------
// SetEditMode
// ---------------------------------------------------------------------------

func TestEditTool_SetEditMode(t *testing.T) {
	tool, _ := newEditToolInTempDir(t)
	tool.SetEditMode(EditModeHashline)
	if tool.editMode != EditModeHashline {
		t.Errorf("editMode = %v, want EditModeHashline", tool.editMode)
	}
}
