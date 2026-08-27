package tools

import (
	"context"
	"strings"
	"testing"
)

// TestCompare_OldVsNew_SingleSpaceError compares OLD vs NEW behavior for single-space error.
//
// OLD VERSION (Levenshtein fuzzy matching):
//   - oldText has 7 spaces, file has 8 spaces
//   - editDistance is 1 per line
//   - With 3 lines, total score = 3 < 10
//   - OLD VERSION ACCEPTS (BAD!) → file gets corrupted
//
// NEW VERSION (exact + normalized matching):
//   - Leading whitespace must match exactly
//   - 7 spaces ≠ 8 spaces
//   - NEW VERSION REJECTS (GOOD!) → error message, file intact
func TestCompare_OldVsNew_SingleSpaceError(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)

	// File content with 8 spaces
	fileContent := `def function():
        print("hello")
        return "done"
`
	writeFile(t, dir, "test.py", fileContent)

	// oldText with 7 spaces (weak model error!)
	oldText := `def function():
       print("hello")
       return "done"
`
	newText := `def function():
        print("hello")
        return "done"
        # Added comment`

	// OLD VERSION behavior (before fix):
	//   - fuzzy match would accept this with score=3 (<10)
	//   - file would be CORRUPTED with wrong indentation
	//
	// NEW VERSION behavior (after fix):
	//   - should reject because 7 spaces ≠ 8 spaces
	//   - file should remain INTACT
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.py",
		"oldText": oldText,
		"newText": newText,
	})

	// NEW VERSION should REJECT incorrect indentation
	if err == nil {
		t.Fatal("NEW VERSION should reject oldText with 7 spaces when file has 8 spaces")
	}

	// File should remain UNCHANGED
	resultContent := readFile(t, dir, "test.py")
	if resultContent != fileContent {
		t.Errorf("File should not be modified!\nExpected:\n%s\nGot:\n%s", fileContent, resultContent)
	}
}

// TestCompare_OldVsNew_IndentationDrift tests the OLD bug where indentation drift
// would be accepted, causing gradual corruption over multiple edits.
func TestCompare_OldVsNew_IndentationDrift(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)

	// File with correct 4-space indentation
	fileContent := `services:
    database:
      port: 5432
`
	writeFile(t, dir, "config.yaml", fileContent)

	// Model got confused and provided 3 spaces instead of 4
	oldText := `services:
   database:
     port: 5432
`
	newText := `services:
    database:
      port: 5432
      enabled: true`

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "config.yaml",
		"oldText": oldText,
		"newText": newText,
	})

	// NEW VERSION should REJECT
	if err == nil {
		t.Fatal("NEW VERSION should reject indentation drift (3 spaces vs 4 spaces)")
	}

	// File should remain UNCHANGED
	resultContent := readFile(t, dir, "config.yaml")
	if resultContent != fileContent {
		t.Errorf("File should not be modified!\nExpected:\n%s\nGot:\n%s", fileContent, resultContent)
	}
}

// TestCompare_OldVsNew_TrailingSpacesOK tests that NEW VERSION still accepts
// trailing space differences (this is acceptable normalization).
func TestCompare_OldVsNew_TrailingSpacesOK(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)

	fileContent := `def function():
        print("hello")
        return "done"
`
	writeFile(t, dir, "test.py", fileContent)

	// oldText with trailing spaces (model added extra spaces at end of lines)
	// This should be ACCEPTED by NEW VERSION because only trailing whitespace differs
	oldText := `def function():
        print("hello")   
        return "done"
`
	newText := `def function():
        print("hello")
        return "done"
        # Added comment`

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.py",
		"oldText": oldText,
		"newText": newText,
	})

	// NEW VERSION should ACCEPT (trailing space normalization)
	if err != nil {
		t.Fatalf("NEW VERSION should accept trailing space differences: %v", err)
	}

	// File should be modified
	resultContent := readFile(t, dir, "test.py")
	if !strings.Contains(resultContent, "# Added comment") {
		t.Error("Expected comment not found")
	}
}
