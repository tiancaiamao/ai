package tools

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/skill"
)

func TestLoadSkillTool_Name(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	if tool.Name() != "load_skill" {
		t.Errorf("Expected name 'load_skill', got '%s'", tool.Name())
	}
}

func TestLoadSkillTool_Description(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
}

func TestLoadSkillTool_Parameters(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	params := tool.Parameters()

	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got '%v'", params["type"])
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties should be a map[string]any")
	}

	if _, exists := props["name"]; !exists {
		t.Error("parameters should have 'name' property")
	}

	required, ok := params["required"]
	if !ok {
		t.Fatal("required should be present")
	}

	// Check if it's a []string or []any
	requiredSlice, ok := required.([]string)
	if !ok {
		// Try []any
		requiredAny, ok := required.([]any)
		if !ok {
			t.Fatalf("required should be an array, got %T", required)
		}
		if len(requiredAny) != 1 || requiredAny[0] != "name" {
			t.Errorf("Expected required=['name'], got %v", requiredAny)
		}
	} else {
		if len(requiredSlice) != 1 || requiredSlice[0] != "name" {
			t.Errorf("Expected required=['name'], got %v", requiredSlice)
		}
	}
}

func TestLoadSkillTool_Execute_Success(t *testing.T) {
	skills := []skill.Skill{
		{
			Name:      "test-skill",
			FilePath:  "/path/to/test/skill.md",
			BaseDir:   "/path/to/test",
			Content:   "Skill content here",
		},
	}

	tool := NewLoadSkillTool(skills)
	ctx := context.Background()
	args := map[string]any{
		"name": "test-skill",
	}

	content, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(content))
	}

	textContent, ok := content[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Expected TextContent, got %T", content[0])
	}

	expectedContent := `<skill name="test-skill" location="/path/to/test/skill.md">
References are relative to /path/to/test.

Skill content here
</skill>`

	if textContent.Text != expectedContent {
		t.Errorf("Content mismatch:\nGot:\n%s\n\nExpected:\n%s", textContent.Text, expectedContent)
	}
}

func TestLoadSkillTool_Execute_NotFound(t *testing.T) {
	skills := []skill.Skill{
		{
			Name:     "existing-skill",
			FilePath: "/path/to/skill.md",
			BaseDir:  "/path/to",
			Content:  "Content",
		},
	}

	tool := NewLoadSkillTool(skills)
	ctx := context.Background()
	args := map[string]any{
		"name": "non-existent-skill",
	}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Expected error for non-existent skill")
	}

	expectedError := "skill not found: non-existent-skill"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestLoadSkillTool_Execute_MissingName(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	ctx := context.Background()
	args := map[string]any{}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Expected error for missing name")
	}

	expectedError := "invalid name argument"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestLoadSkillTool_Execute_InvalidNameType(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	ctx := context.Background()
	args := map[string]any{
		"name": 123, // Invalid type
	}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Expected error for invalid name type")
	}

	expectedError := "invalid name argument"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestLoadSkillTool_XMLEscaping(t *testing.T) {
	skills := []skill.Skill{
		{
			Name:      "skill<script>",
			FilePath:  "/path/to/\"file\".md",
			BaseDir:   "/base&dir",
			Content:   "Content with <tags> and &ampersands;",
		},
	}

	tool := NewLoadSkillTool(skills)
	ctx := context.Background()
	args := map[string]any{
		"name": "skill<script>",
	}

	content, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	textContent, ok := content[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Expected TextContent, got %T", content[0])
	}

	// Verify XML escaping - skill name should be escaped
	if !strings.Contains(textContent.Text, "skill&lt;script&gt;") {
		t.Errorf("Expected escaped skill name in output, got: %s", textContent.Text)
	}

	// File path should be escaped
	if !strings.Contains(textContent.Text, "&quot;file&quot;.md") {
		t.Errorf("Expected escaped file path in output, got: %s", textContent.Text)
	}

	// Base dir should be escaped
	if !strings.Contains(textContent.Text, "base&amp;dir") {
		t.Errorf("Expected escaped base dir in output, got: %s", textContent.Text)
	}
}