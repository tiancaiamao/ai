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

	if !strings.Contains(result, "<available_skills>") {
		t.Error("expected <available_skills> tag")
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
