package hint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHintFile(t *testing.T) {
	content := `# Truncate & Compact Hint

## TRUNCATE
- turn: 5
- section: "detailed_analysis"
- reason: "verbose explanation, no longer needed"

## COMPACT
- confidence: 0.75
- reason: "Topic switched from debugging to new feature"
- keep_last_turns: 5
`

	hint, err := ParseHintFile(content)
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate == nil {
		t.Fatal("Truncate hint is nil")
	}
	if hint.Truncate.Turn != 5 {
		t.Errorf("Expected turn 5, got %d", hint.Truncate.Turn)
	}
	if hint.Truncate.Section != "detailed_analysis" {
		t.Errorf("Expected section 'detailed_analysis', got %s", hint.Truncate.Section)
	}
	if hint.Truncate.Reason != "verbose explanation, no longer needed" {
		t.Errorf("Unexpected reason: %s", hint.Truncate.Reason)
	}

	if hint.Compact == nil {
		t.Fatal("Compact hint is nil")
	}
	if hint.Compact.Confidence != 0.75 {
		t.Errorf("Expected confidence 0.75, got %f", hint.Compact.Confidence)
	}
	if hint.Compact.Reason != "Topic switched from debugging to new feature" {
		t.Errorf("Unexpected reason: %s", hint.Compact.Reason)
	}
	if hint.Compact.KeepTurns != 5 {
		t.Errorf("Expected keep_last_turns 5, got %d", hint.Compact.KeepTurns)
	}
}

func TestParseHintFileEmpty(t *testing.T) {
	hint, err := ParseHintFile("")
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate != nil {
		t.Error("Truncate should be nil")
	}
	if hint.Compact != nil {
		t.Error("Compact should be nil")
	}
}

func TestParseHintFileOnlyTruncate(t *testing.T) {
	content := `# Truncate & Compact Hint

## TRUNCATE
- turn: 3
- section: "error_log"
`

	hint, err := ParseHintFile(content)
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate == nil {
		t.Fatal("Truncate hint is nil")
	}
	if hint.Truncate.Turn != 3 {
		t.Errorf("Expected turn 3, got %d", hint.Truncate.Turn)
	}
	if hint.Compact != nil {
		t.Error("Compact should be nil")
	}
}

func TestParseHintFileOnlyCompact(t *testing.T) {
	content := `# Truncate & Compact Hint

## COMPACT
- confidence: 0.9
- reason: "Phase completed"
`

	hint, err := ParseHintFile(content)
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate != nil {
		t.Error("Truncate should be nil")
	}
	if hint.Compact == nil {
		t.Fatal("Compact hint is nil")
	}
	if hint.Compact.Confidence != 0.9 {
		t.Errorf("Expected confidence 0.9, got %f", hint.Compact.Confidence)
	}
}

func TestParseHintFileWithToolTruncate(t *testing.T) {
	content := `# Truncate & Compact Hint

## TRUNCATE
- tool_name: "read"
- tool_ids: "id1, id2, id3"
- reason: "large file outputs no longer needed"
`

	hint, err := ParseHintFile(content)
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate == nil {
		t.Fatal("Truncate hint is nil")
	}
	if hint.Truncate.ToolName != "read" {
		t.Errorf("Expected tool_name 'read', got %s", hint.Truncate.ToolName)
	}
	if len(hint.Truncate.ToolIDs) != 3 {
		t.Errorf("Expected 3 tool IDs, got %d", len(hint.Truncate.ToolIDs))
	}
	if hint.Truncate.ToolIDs[0] != "id1" || hint.Truncate.ToolIDs[1] != "id2" || hint.Truncate.ToolIDs[2] != "id3" {
		t.Errorf("Unexpected tool IDs: %v", hint.Truncate.ToolIDs)
	}
	if hint.Compact != nil {
		t.Error("Compact should be nil")
	}
}

func TestParseHintFileWithMixedTruncate(t *testing.T) {
	content := `# Truncate & Compact Hint

## TRUNCATE
- turn: 5
- section: "detailed_analysis"
- tool_name: "grep"
- tool_ids: "grep_1, grep_2"
- reason: "search results processed"
`

	hint, err := ParseHintFile(content)
	if err != nil {
		t.Fatalf("ParseHintFile failed: %v", err)
	}

	if hint.Truncate == nil {
		t.Fatal("Truncate hint is nil")
	}
	if hint.Truncate.Turn != 5 {
		t.Errorf("Expected turn 5, got %d", hint.Truncate.Turn)
	}
	if hint.Truncate.Section != "detailed_analysis" {
		t.Errorf("Expected section 'detailed_analysis', got %s", hint.Truncate.Section)
	}
	if hint.Truncate.ToolName != "grep" {
		t.Errorf("Expected tool_name 'grep', got %s", hint.Truncate.ToolName)
	}
	if len(hint.Truncate.ToolIDs) != 2 {
		t.Errorf("Expected 2 tool IDs, got %d", len(hint.Truncate.ToolIDs))
	}
}

func TestShouldTriggerTruncate(t *testing.T) {
	tests := []struct {
		name         string
		hint         *Hint
		expectTruncate bool
	}{
		{
			name:         "no hint",
			hint:         &Hint{},
			expectTruncate: false,
		},
		{
			name: "with truncate hint",
			hint: &Hint{
				Truncate: &TruncateHint{Reason: "test"},
			},
			expectTruncate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldTriggerTruncate(tt.hint)
			if result != tt.expectTruncate {
				t.Errorf("Expected %v, got %v", tt.expectTruncate, result)
			}
		})
	}
}

func TestShouldTriggerCompact(t *testing.T) {
	tests := []struct {
		name              string
		hint              *Hint
		usage             float64
		expectTrigger    bool
		expectConfidence  float64
		expectReason      string
	}{
		{
			name:             "low usage, no hint",
			hint:             &Hint{},
			usage:            0.3,
			expectTrigger:    false,
			expectConfidence: 0,
			expectReason:     "",
		},
		{
			name: "compact hint with confidence",
			hint: &Hint{
				Compact: &CompactHint{Confidence: 0.8, Reason: "topic changed"},
			},
			usage:            0.5,
			expectTrigger:    true,
			expectConfidence: 0.8,
			expectReason:     "topic changed",
		},
		{
			name:             "high usage, forced",
			hint:             &Hint{},
			usage:            0.8,
			expectTrigger:    true,
			expectConfidence: 1.0,
			expectReason:     "forced: usage exceeded 75%",
		},
		{
			name: "exactly 75% usage, forced",
			hint: &Hint{},
			usage:            0.75,
			expectTrigger:    true,
			expectConfidence: 1.0,
			expectReason:     "forced: usage exceeded 75%",
		},
		{
			name: "below 75% with compact hint",
			hint: &Hint{
				Compact: &CompactHint{Confidence: 0.5, Reason: "context irrelevant"},
			},
			usage:            0.74,
			expectTrigger:    true,
			expectConfidence: 0.5,
			expectReason:     "context irrelevant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger, conf, reason := ShouldTriggerCompact(tt.hint, tt.usage)

			if trigger != tt.expectTrigger {
				t.Errorf("Expected trigger %v, got %v", tt.expectTrigger, trigger)
			}
			if conf != tt.expectConfidence {
				t.Errorf("Expected confidence %f, got %f", tt.expectConfidence, conf)
			}
			if reason != tt.expectReason {
				t.Errorf("Expected reason '%s', got '%s'", tt.expectReason, reason)
			}
		})
	}
}

func TestGetTruncateHintMessage(t *testing.T) {
	tests := []struct {
		name     string
		usage    float64
		expected string
	}{
		{"low usage", 0.2, ""},
		{"30% usage", 0.3, "TRUNCATE_HINT"},
		{"45% usage", 0.45, "TRUNCATE_HINT"},
		{"60% usage", 0.6, "TRUNCATE_HINT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := GetTruncateHintMessage(tt.usage)

			if tt.expected == "" && msg != "" {
				t.Errorf("Expected empty message, got %s", msg)
			}
			if tt.expected != "" && msg == "" {
				t.Errorf("Expected message containing %s, got empty", tt.expected)
			}
		})
	}
}

func TestGetCompactHintMessage(t *testing.T) {
	tests := []struct {
		name     string
		usage    float64
		expected string
	}{
		{"low usage", 0.3, ""},
		{"55% usage", 0.55, "COMPACT_HINT"},
		{"65% usage", 0.65, "COMPACT_HINT"},
		{"72% usage", 0.72, "COMPACT_HINT"},
		{"80% usage", 0.8, "COMPACT_HINT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := GetCompactHintMessage(tt.usage)

			if tt.expected == "" && msg != "" {
				t.Errorf("Expected empty message, got %s", msg)
			}
			if tt.expected != "" && msg == "" {
				t.Errorf("Expected message containing %s, got empty", tt.expected)
			}
		})
	}
}

func TestFormatHint(t *testing.T) {
	hint := &Hint{
		Truncate: &TruncateHint{
			Turn:    5,
			Section: "detailed_analysis",
			Reason:  "verbose explanation",
		},
		Compact: &CompactHint{
			Confidence: 0.75,
			Reason:     "topic changed",
			KeepTurns:  5,
		},
	}

	formatted := FormatHint(hint)

	if formatted == "" {
		t.Error("Formatted hint is empty")
	}

	// Check for expected sections
	if !contains(formatted, "## TRUNCATE") {
		t.Error("Missing TRUNCATE section")
	}
	if !contains(formatted, "## COMPACT") {
		t.Error("Missing COMPACT section")
	}
	if !contains(formatted, "turn: 5") {
		t.Error("Missing turn")
	}
	if !contains(formatted, "confidence: 0.75") {
		t.Error("Missing confidence")
	}
}

func TestLoadHintFile(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	llmContextDir := filepath.Join(sessionDir, "llm-context")

	if err := os.MkdirAll(llmContextDir, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}

	// Test with non-existent file
	hint, err := LoadHintFile(sessionDir)
	if err != nil {
		t.Fatalf("LoadHintFile failed: %v", err)
	}
	if hint == nil {
		t.Error("Hint should not be nil")
	}

	// Test with existing file
	hintPath := filepath.Join(llmContextDir, "truncate-compact-hint.md")
	content := `# Truncate & Compact Hint

## COMPACT
- confidence: 0.8
- reason: "test reason"
`
	if err := os.WriteFile(hintPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write hint file: %v", err)
	}

	hint, err = LoadHintFile(sessionDir)
	if err != nil {
		t.Fatalf("LoadHintFile failed: %v", err)
	}
	if hint.Compact == nil {
		t.Fatal("Compact hint should not be nil")
	}
	if hint.Compact.Confidence != 0.8 {
		t.Errorf("Expected confidence 0.8, got %f", hint.Compact.Confidence)
	}
}

func TestLoadHintFileLegacyMigration(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	llmContextDir := filepath.Join(sessionDir, "llm-context")

	if err := os.MkdirAll(llmContextDir, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}

	// Test legacy file migration
	legacyPath := filepath.Join(llmContextDir, "truncate-hint.md")
	content := `# Truncate & Compact Hint

## TRUNCATE
- tool_name: "read"
- reason: "legacy file test"
`

	if err := os.WriteFile(legacyPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write legacy hint file: %v", err)
	}

	// LoadHintFile should migrate legacy file to new filename
	hint, err := LoadHintFile(sessionDir)
	if err != nil {
		t.Fatalf("LoadHintFile failed: %v", err)
	}

	// Verify migration worked
	newPath := filepath.Join(llmContextDir, "truncate-compact-hint.md")
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("New hint file should exist after migration")
	}

	// Legacy file should be removed
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("Legacy hint file should be removed after migration")
	}

	// Verify hint content
	if hint.Truncate == nil {
		t.Fatal("Truncate hint should not be nil")
	}
	if hint.Truncate.ToolName != "read" {
		t.Errorf("Expected tool_name 'read', got %s", hint.Truncate.ToolName)
	}
}

func TestClearHintFile(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	llmContextDir := filepath.Join(sessionDir, "llm-context")

	if err := os.MkdirAll(llmContextDir, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}

	// Create a hint file
	hintPath := filepath.Join(llmContextDir, "truncate-compact-hint.md")
	if err := os.WriteFile(hintPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to write hint file: %v", err)
	}

	// Clear it
	if err := ClearHintFile(sessionDir); err != nil {
		t.Fatalf("ClearHintFile failed: %v", err)
	}

	// Check it's gone
	if _, err := os.Stat(hintPath); !os.IsNotExist(err) {
		t.Error("Hint file should be removed")
	}

	// Clear again (should not error)
	if err := ClearHintFile(sessionDir); err != nil {
		t.Fatalf("ClearHintFile failed on non-existent file: %v", err)
	}
}

func TestClearHintFileWithLegacy(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	llmContextDir := filepath.Join(sessionDir, "llm-context")

	if err := os.MkdirAll(llmContextDir, 0755); err != nil {
		t.Fatalf("Failed to create test directories: %v", err)
	}

	// Create both legacy and new hint files
	legacyPath := filepath.Join(llmContextDir, "truncate-hint.md")
	newPath := filepath.Join(llmContextDir, "truncate-compact-hint.md")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("Failed to write legacy file: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0644); err != nil {
		t.Fatalf("Failed to write new file: %v", err)
	}

	// Clear both
	if err := ClearHintFile(sessionDir); err != nil {
		t.Fatalf("ClearHintFile failed: %v", err)
	}

	// Both should be gone
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("Legacy hint file should be removed")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Error("New hint file should be removed")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}