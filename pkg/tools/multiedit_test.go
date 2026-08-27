package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: create a temp dir with a workspace and multi-edit tool
func newMultiEditToolInTempDir(t *testing.T) (*MultiEditTool, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return NewMultiEditTool(ws), dir
}

func argsFor(path string, edits [][2]string) map[string]any {
	items := make([]any, len(edits))
	for i, e := range edits {
		items[i] = map[string]any{"oldText": e[0], "newText": e[1]}
	}
	return map[string]any{"path": path, "edits": items}
}

func TestMultiEdit_AppliesAllEditsOnOriginalContent(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "alpha\nbeta\ngamma\n")

	blocks, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"alpha", "ALPHA"},
		{"gamma", "GAMMA"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := readFile(t, dir, "a.txt")
	want := "ALPHA\nbeta\nGAMMA\n"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
}

// Even if edit 1 rewrites text that edit 2 also matches, the ranges are
// resolved against the original file, so both apply independently.
func TestMultiEdit_RangesResolvedAgainstOriginal(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "x=1\ny=2\nx again\n")

	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"x=1", "x=10"},
		{"y=2", "y=20"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := readFile(t, dir, "a.txt")
	if got != "x=10\ny=20\nx again\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestMultiEdit_DuplicateMatchRejected(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "dup\nmid\ndup\n")

	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"dup", "one"},
	}))
	if err == nil || !strings.Contains(err.Error(), "matches 2 locations") {
		t.Fatalf("err = %v, want multiple-match error listing L1 and L3", err)
	}
	if !strings.Contains(err.Error(), "L1") || !strings.Contains(err.Error(), "L3") {
		t.Fatalf("error should list line numbers L1/L3, got: %v", err)
	}
}

func TestMultiEdit_NoMatchErrorEnrichedWithContext(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "func handleRequest(w http) {\n\treturn\n}\n")

	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"func handelRequest(w http) {", "replaced"},
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	// Goose-style enrichment should point at the similar real line.
	if !strings.Contains(err.Error(), "handleRequest") || !strings.Contains(err.Error(), "L1") {
		t.Fatalf("no-match error lacks context suggestion, got: %v", err)
	}
}

func TestMultiEdit_OverlapRejectedAtomically(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	content := "aaaa\nbbbb\ncccc\n"
	writeFile(t, dir, "a.txt", content)

	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"aaaa\nbbbb", "X"},
		{"bbbb\ncccc", "Y"},
	}))
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("err = %v, want overlap rejection", err)
	}
	// Atomicity: nothing may be written when any edit fails.
	if got := readFile(t, dir, "a.txt"); got != content {
		t.Fatalf("file modified on failed multi-edit: %q", got)
	}
}

func TestMultiEdit_AtomicOnAnyFailure(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	content := "one\ntwo\nthree\n"
	writeFile(t, dir, "a.txt", content)

	// Second edit has no match — first must not be applied either.
	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"one", "ONE"},
		{"does not exist", "X"},
	}))
	if err == nil {
		t.Fatal("expected error for missing oldText")
	}
	if got := readFile(t, dir, "a.txt"); got != content {
		t.Fatalf("partial application detected: %q", got)
	}
}

func TestMultiEdit_IdenticalOldNewRejected(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	writeFile(t, dir, "a.txt", "same\n")

	_, err := tool.Execute(t.Context(), argsFor("a.txt", [][2]string{
		{"same", "same"},
	}))
	if err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("err = %v, want identical rejection", err)
	}
}

func TestMultiEdit_EmptyEditsRejected(t *testing.T) {
	tool, _ := newMultiEditToolInTempDir(t)
	if _, err := tool.Execute(t.Context(), map[string]any{"path": "a.txt", "edits": []any{}}); err == nil {
		t.Fatal("expected error for empty edits array")
	}
}

func TestMultiEdit_FileMustExist(t *testing.T) {
	tool, dir := newMultiEditToolInTempDir(t)
	if _, err := tool.Execute(t.Context(), argsFor(filepath.Join(dir, "nope.txt"), [][2]string{{"a", "b"}})); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("multi_edit must not create files")
	}
}
