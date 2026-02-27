package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name          string
		parentDir     string
		expectErrors  bool
		errorContains []string
	}{
		{
			name:         "valid-name",
			parentDir:    "valid-name",
			expectErrors: false,
		},
		{
			name:         "another-valid-name-123",
			parentDir:    "another-valid-name-123",
			expectErrors: false,
		},
		{
			name:          "InvalidName",
			parentDir:     "InvalidName",
			expectErrors:  true,
			errorContains: []string{"invalid characters"},
		},
		{
			name:          "-starts-with-hyphen",
			parentDir:     "-starts-with-hyphen",
			expectErrors:  true,
			errorContains: []string{"start or end with a hyphen"},
		},
		{
			name:          "ends-with-hyphen-",
			parentDir:     "ends-with-hyphen-",
			expectErrors:  true,
			errorContains: []string{"start or end with a hyphen"},
		},
		{
			name:          "has--double",
			parentDir:     "has--double",
			expectErrors:  true,
			errorContains: []string{"consecutive hyphens"},
		},
		{
			name:          "mismatch",
			parentDir:     "different",
			expectErrors:  true,
			errorContains: []string{"does not match parent directory"},
		},
		{
			name:          strings.Repeat("a", 100),
			parentDir:     strings.Repeat("a", 100),
			expectErrors:  true,
			errorContains: []string{"exceeds"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateName(tt.name, tt.parentDir)

			if tt.expectErrors && len(errors) == 0 {
				t.Errorf("expected errors but got none")
			}

			if !tt.expectErrors && len(errors) > 0 {
				t.Errorf("expected no errors but got: %v", errors)
			}

			if len(tt.errorContains) > 0 {
				for _, expected := range tt.errorContains {
					found := false
					for _, err := range errors {
						if strings.Contains(err, expected) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got: %v", expected, errors)
					}
				}
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		name         string
		description  string
		expectErrors bool
	}{
		{
			name:         "valid description",
			description:  "This is a valid skill description.",
			expectErrors: false,
		},
		{
			name:         "empty description",
			description:  "",
			expectErrors: true,
		},
		{
			name:         "whitespace only",
			description:  "   ",
			expectErrors: true,
		},
		{
			name:         "too long",
			description:  strings.Repeat("a", MaxDescriptionLength+1),
			expectErrors: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateDescription(tt.description)

			if tt.expectErrors && len(errors) == 0 {
				t.Errorf("expected errors but got none")
			}

			if !tt.expectErrors && len(errors) > 0 {
				t.Errorf("expected no errors but got: %v", errors)
			}
		})
	}
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectName  string
		expectDesc  string
		expectError bool
	}{
		{
			name: "valid frontmatter",
			content: `---
name: test-skill
description: A test skill
---
This is the content.`,
			expectName: "test-skill",
			expectDesc: "A test skill",
		},
		{
			name:       "no frontmatter",
			content:    `Just content without frontmatter.`,
			expectName: "",
			expectDesc: "",
		},
		{
			name: "missing closing delimiter",
			content: `---
name: test
This is invalid`,
			expectError: true,
		},
		{
			name: "with allowed-tools",
			content: `---
name: test
description: Test
allowed-tools: [read, write]
---
Content`,
			expectName: "test",
			expectDesc: "Test",
		},
		{
			name: "with disable-model-invocation",
			content: `---
name: test
description: Test
disable-model-invocation: true
---
Content`,
			expectName: "test",
			expectDesc: "Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter([]byte(tt.content))

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if fm.Name != tt.expectName {
				t.Errorf("expected name %q, got %q", tt.expectName, fm.Name)
			}

			if fm.Description != tt.expectDesc {
				t.Errorf("expected description %q, got %q", tt.expectDesc, fm.Description)
			}

			if tt.expectDesc != "" && len(body) == 0 {
				t.Error("expected body content but got none")
			}
		})
	}
}

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal text", "normal text"},
		{"text with <tag>", "text with &lt;tag&gt;"},
		{"text with &amp;", "text with &amp;amp;"},
		{"text with \"quotes\"", "text with &quot;quotes&quot;"},
		{"text with 'apostrophe'", "text with &apos;apostrophe&apos;"},
		{"all & < > \" ' together", "all &amp; &lt; &gt; &quot; &apos; together"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeXML(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestFormatForPrompt(t *testing.T) {
	skills := []Skill{
		{
			Name:                   "test-skill",
			Description:            "A test skill",
			FilePath:               "/path/to/test-skill/SKILL.md",
			DisableModelInvocation: false,
		},
		{
			Name:                   "hidden-skill",
			Description:            "A hidden skill",
			FilePath:               "/path/to/hidden-skill/SKILL.md",
			DisableModelInvocation: true,
		},
	}

	result := FormatForPrompt(skills)

	if !strings.Contains(result, "test-skill") {
		t.Error("expected test-skill in output")
	}

	if strings.Contains(result, "hidden-skill") {
		t.Error("hidden-skill should not be in output (disable-model-invocation=true)")
	}

	// Check for markdown heading instead of XML tag
	if !strings.Contains(result, "## Skills") {
		t.Error("expected markdown heading '## Skills'")
	}

	if !strings.Contains(result, "A test skill") {
		t.Error("expected skill description in output")
	}
}

func TestFormatForPromptEmpty(t *testing.T) {
	result := FormatForPrompt([]Skill{})
	if result != "" {
		t.Errorf("expected empty string for no skills, got %q", result)
	}

	skills := []Skill{
		{
			Name:                   "skill",
			Description:            "Description",
			DisableModelInvocation: true, // All disabled
		},
	}

	result = FormatForPrompt(skills)
	if result != "" {
		t.Errorf("expected empty string when all skills disabled, got %q", result)
	}
}

func TestLoaderLoadFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a simple skill file
	skillDir := filepath.Join(tmpDir, "test-skill")
	err := os.MkdirAll(skillDir, 0755)
	if err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill for testing
---
This is the skill content.`

	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)
	if err != nil {
		t.Fatalf("failed to write skill file: %v", err)
	}

	loader := NewLoader(tmpDir)
	result := loader.loadFromDir(tmpDir, "test", true)

	if len(result.Diagnostics) > 0 {
		t.Errorf("unexpected diagnostics: %v", result.Diagnostics)
	}

	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Skills))
	}

	skill := result.Skills[0]
	if skill.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", skill.Name)
	}

	if skill.Description != "A test skill for testing" {
		t.Errorf("expected description 'A test skill for testing', got %q", skill.Description)
	}
}

func TestResolvePath(t *testing.T) {
	loader := NewLoader("/tmp")

	tests := []struct {
		input    string
		cwd      string
		expected string // Check if result starts with this
	}{
		{
			input:    "/absolute/path",
			cwd:      "/cwd",
			expected: "/absolute/path",
		},
		{
			input:    "relative/path",
			cwd:      "/cwd",
			expected: "/cwd/",
		},
		{
			input:    "~/",
			cwd:      "/cwd",
			expected: os.Getenv("HOME"),
		},
		{
			input:    "~/path",
			cwd:      "/cwd",
			expected: os.Getenv("HOME"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := loader.resolvePath(tt.input, tt.cwd)
			if !strings.HasPrefix(result, tt.expected) {
				t.Errorf("expected result to start with %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsSkillCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid skill command", "/skill:test", true},
		{"with leading space", "  /skill:test", true},
		{"with arguments", "/skill:test with args", true},
		{"not a skill command", "regular text", false},
		{"different slash command", "/other:command", false},
		{"empty string", "", false},
		{"just prefix", "/skill:", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSkillCommand(tt.input)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestExtractSkillName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"basic", "/skill:test-skill", "test-skill"},
		{"with args", "/skill:test-skill some arguments", "test-skill"},
		{"with space before args", "/skill:test-skill   with args", "test-skill"},
		{"with tab", "/skill:test-skill\twith args", "test-skill"},
		{"empty name", "/skill:", ""},
		{"not a command", "regular text", ""},
		{"just prefix", "/skill:", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractSkillName(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExpandCommand(t *testing.T) {
	skills := []Skill{
		{
			Name:        "test-skill",
			Description: "A test skill",
			FilePath:    "/path/to/test-skill/SKILL.md",
			BaseDir:     "/path/to/test-skill",
			Content:     "This is the skill content.\nIt has multiple lines.",
		},
		{
			Name:        "another-skill",
			Description: "Another skill",
			FilePath:    "/path/to/another/SKILL.md",
			BaseDir:     "/path/to/another",
			Content:     "Simple content",
		},
	}

	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:  "expand skill without args",
			input: "/skill:test-skill",
			contains: []string{
				`<skill name="test-skill"`,
				`location="/path/to/test-skill/SKILL.md"`,
				`References are relative to /path/to/test-skill`,
				`This is the skill content.`,
				`</skill>`,
			},
		},
		{
			name:  "expand skill with args",
			input: "/skill:test-skill some extra arguments here",
			contains: []string{
				`<skill name="test-skill"`,
				`This is the skill content.`,
				`some extra arguments here`,
			},
		},
		{
			name:  "expand another skill",
			input: "/skill:another-skill",
			contains: []string{
				`<skill name="another-skill"`,
				`Simple content`,
			},
		},
		{
			name:     "unknown skill returns original",
			input:    "/skill:unknown-skill",
			contains: []string{"/skill:unknown-skill"},
		},
		{
			name:     "non-command returns original",
			input:    "just regular text",
			contains: []string{"just regular text"},
		},
		{
			name:     "empty skill name returns original",
			input:    "/skill:",
			contains: []string{"/skill:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandCommand(tt.input, skills)
			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("expected result to contain %q\ngot: %s", expected, result)
				}
			}
		})
	}
}

func TestExpandCommandWithSpecialCharacters(t *testing.T) {
	skills := []Skill{
		{
			Name:        "test-skill",
			Description: "A test skill",
			FilePath:    "/path/with<>/test-SKILL.md",
			BaseDir:     "/path/base",
			Content:     "Content with & < > \" ' characters",
		},
	}

	result := ExpandCommand("/skill:test-skill", skills)

	// Check XML attributes are escaped
	if !strings.Contains(result, "/path/with&lt;&gt;/test-SKILL.md") {
		t.Error("expected < and > in file path to be escaped")
	}

	// Content is included as-is (not escaped, as per pi-mono behavior)
	if !strings.Contains(result, "Content with & < > \" ' characters") {
		t.Error("expected content to be included as-is")
	}
}
