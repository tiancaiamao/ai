package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntegrationRealSkills tests loading skills from the real ~/.ai/skills directory.
func TestIntegrationRealSkills(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	agentDir := filepath.Join(homeDir, ".ai")
	loader := NewLoader(agentDir)

	result := loader.Load(&LoadOptions{
		CWD:             homeDir,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	t.Logf("Loaded %d skills", len(result.Skills))
	for _, skill := range result.Skills {
		t.Logf("  - %s: %s", skill.Name, skill.Description)
	}

	if len(result.Diagnostics) > 0 {
		t.Logf("Diagnostics: %d", len(result.Diagnostics))
		for _, diag := range result.Diagnostics {
			t.Logf("  [%s] %s: %s", diag.Type, diag.Path, diag.Message)
		}
	}

	// Format for prompt
	if len(result.Skills) > 0 {
		prompt := FormatForPrompt(result.Skills)
		t.Logf("Skills in prompt:\n%s", prompt)
	}
}
