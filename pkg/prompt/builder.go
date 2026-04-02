package prompt

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiancaiamao/ai/pkg/context"
)

//go:embed "normal_system.md"
var normalSystemPrompt string

//go:embed "context_mgmt_system.md"
var contextMgmtSystemPrompt string

// Bootstrap files to search for in workspace.
var bootstrapFiles = []string{
	"AGENTS.md",   // Agent identity and behavior
	"CLAUDE.md",   // Project guidelines (fallback when AGENTS.md is absent)
	"TOOLS.md",    // Tool usage instructions
	"IDENTITY.md", // User/owner identity
}

// BuildSystemPrompt builds the system prompt for the given mode.
func BuildSystemPrompt(mode context.AgentMode) string {
	switch mode {
	case context.ModeNormal:
		return normalSystemPrompt
	case context.ModeContextMgmt:
		return contextMgmtSystemPrompt
	default:
		return normalSystemPrompt
	}
}

// BuildSystemPromptWithThinking builds the system prompt with a thinking level instruction appended.
// Only ModeNormal gets a thinking instruction; other modes return the base prompt as-is.
func BuildSystemPromptWithThinking(mode context.AgentMode, thinkingLevel string) string {
	base := BuildSystemPrompt(mode)
	if mode != context.ModeNormal {
		return base
	}
	normalized := NormalizeThinkingLevel(thinkingLevel)
	if normalized == "off" {
		return base
	}
	instruction := ThinkingInstruction(normalized)
	if instruction == "" {
		return base
	}
	return fmt.Sprintf("%s\n\n<thinking_instruction>\n%s\n</thinking_instruction>", base, instruction)
}

// BuildSystemPromptWithExtras builds the system prompt for normal mode with skills and project context
// substituted into their respective placeholders, then appends the thinking level instruction.
func BuildSystemPromptWithExtras(mode context.AgentMode, thinkingLevel string, skillsText string, projectContext string) string {
	if mode != context.ModeNormal {
		return BuildSystemPrompt(mode)
	}

	base := normalSystemPrompt

	// Substitute %SKILLS% placeholder
	result := strings.ReplaceAll(base, "%SKILLS%", skillsText)

	// Substitute %PROJECT_CONTEXT% placeholder
	result = strings.ReplaceAll(result, "%PROJECT_CONTEXT%", projectContext)

	// Clean up empty sections (e.g., "## Skills" with no content below it)
	result = cleanupEmptySections(result)

	// Append thinking instruction
	normalized := NormalizeThinkingLevel(thinkingLevel)
	if normalized != "off" {
		if instruction := ThinkingInstruction(normalized); instruction != "" {
			result = fmt.Sprintf("%s\n\n<thinking_instruction>\n%s\n</thinking_instruction>", result, instruction)
		}
	}

	return result
}

// BuildProjectContext reads bootstrap files (AGENTS.md, CLAUDE.md, TOOLS.md, IDENTITY.md)
// from the given working directory and formats them as project context.
// Looks in .ai/<file> first, then <cwd>/<file>.
// If AGENTS.md exists, CLAUDE.md is skipped.
func BuildProjectContext(cwd string) string {
	contexts := []string{}
	hasAgents := loadBootstrapFile(cwd, "AGENTS.md") != ""

	for _, filename := range bootstrapFiles {
		if filename == "CLAUDE.md" && hasAgents {
			continue
		}
		content := loadBootstrapFile(cwd, filename)
		if content != "" {
			contexts = append(contexts, fmt.Sprintf("### %s\n\n%s", filename, content))
		}
	}

	if len(contexts) == 0 {
		return ""
	}

	return "## Project Context\n" + strings.Join(contexts, "\n\n")
}

// loadBootstrapFile reads a bootstrap file from the workspace.
// Tries .ai/<filename> first, then <cwd>/<filename>.
func loadBootstrapFile(cwd string, filename string) string {
	// Try project-local first: .ai/<filename>
	projectPath := filepath.Join(cwd, ".ai", filename)
	if content, err := os.ReadFile(projectPath); err == nil {
		return string(content)
	}

	// Try workspace root: <cwd>/<filename>
	rootPath := filepath.Join(cwd, filename)
	if content, err := os.ReadFile(rootPath); err == nil {
		return string(content)
	}

	return ""
}

// cleanupEmptySections removes sections that are completely empty
// (section header with only blank lines following it, until the next section or EOF).
func cleanupEmptySections(prompt string) string {
	lines := strings.Split(prompt, "\n")
	cleaned := []string{}

	for i := 0; i < len(lines); {
		line := lines[i]

		// Check if this line is a section header
		if strings.HasPrefix(line, "## ") {
			// Look ahead to see if the section has any non-empty content
			hasContent := false
			j := i + 1
			for j < len(lines) {
				nextLine := lines[j]
				// Stop at next section header
				if strings.HasPrefix(nextLine, "## ") || strings.HasPrefix(nextLine, "# ") {
					break
				}
				if strings.TrimSpace(nextLine) != "" {
					hasContent = true
					break
				}
				j++
			}

			if !hasContent {
				// Skip this section header and its trailing blank lines
				i = j
				// Skip any trailing blank lines after the removed section
				for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
					i++
				}
				continue
			}
		}

		cleaned = append(cleaned, line)
		i++
	}

	result := strings.Join(cleaned, "\n")

	// Clean up multiple consecutive blank lines
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(result)
}
