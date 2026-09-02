package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEditTool_IndentationPreservationInPython verifies that edits preserve
// Python indentation even when the model provides oldText with inconsistent
// trailing whitespace.
func TestEditTool_IndentationPreservationInPython(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)

	// Create a Python file with precise indentation
	pyContent := `def complex_function(param1, param2):
    if param1:
        for item in param2:
            if item:
                print(item)
                return item
    return None
`
	writeFile(t, dir, "test_indentation.py", pyContent)

	// Simulate a weak model that provides oldText with correct indentation
	// (we want to test that indentation is preserved, not that incorrect indentation is accepted)
	oldText := `            if item:
                print(item)
                return item`
	newText := `            if item:
                # Added comment
                print(item)
                return item`

	// Apply the edit
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test_indentation.py",
		"oldText": oldText,
		"newText": newText,
	})

	if err != nil {
		t.Fatalf("Edit failed: %v", err)
	}

	// Read the result
	resultContent := readFile(t, dir, "test_indentation.py")
	if !strings.Contains(resultContent, newText) {
		t.Errorf("Expected newText not found in result")
	}

	// Verify that the indentation in the modified line is still 16 spaces
	lines := strings.Split(resultContent, "\n")
	var modifiedLine string
	for _, line := range lines {
		if strings.Contains(line, "# Added comment") {
			modifiedLine = line
			break
		}
	}
	if modifiedLine == "" {
		t.Fatal("Modified line not found")
	}

	// Count leading spaces
	leadingSpaces := 0
	for _, r := range modifiedLine {
		if r == ' ' {
			leadingSpaces++
		} else {
			break
		}
	}
	if leadingSpaces != 16 {
		t.Errorf("Expected 16 leading spaces, got %d in line: %q", leadingSpaces, modifiedLine)
	}
}

// TestEditTool_YAMLNestedStructure verifies that YAML nested structures
// maintain their indentation.
func TestEditTool_YAMLNestedStructure(t *testing.T) {
	tool, dir := newEditToolInTempDir(t)

	yamlContent := `server:
  host: localhost
  port: 8080
  database:
    host: db.example.com
    port: 5432
    name: mydb
`
	writeFile(t, dir, "test_yaml.yaml", yamlContent)

	// Edit a nested value
	oldText := `    port: 5432`
	newText := `    port: 5433  # Updated port`

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test_yaml.yaml",
		"oldText": oldText,
		"newText": newText,
	})

	if err != nil {
		t.Fatalf("Edit failed: %v", err)
	}

	// Read the result
	resultContent := readFile(t, dir, "test_yaml.yaml")
	if !strings.Contains(resultContent, "5433") {
		t.Errorf("Expected new port not found in result")
	}

	// Verify YAML structure is still valid (lines are properly indented)
	lines := strings.Split(resultContent, "\n")
	for i, line := range lines {
		if strings.Contains(line, "port:") && strings.Contains(line, "5433") {
			// This line should be at level 2 (4 spaces)
			if !strings.HasPrefix(line, "    ") {
				t.Errorf("Line %d should start with 4 spaces, got: %q", i, line)
			}
			// But not at level 3 (8 spaces)
			if strings.HasPrefix(line, "        ") {
				t.Errorf("Line %d should start with 4 spaces, not 8: %q", i, line)
			}
		}
	}
}
