package prompt

import (
	_ "embed"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

//go:embed "prompt.md"
var promptTemplate string

//go:embed "subagent_base.md"
var subagentBasePrompt string

//go:embed "headless_base.md"
var headlessBasePrompt string

//go:embed "compact_system.md"
var compactSystemPrompt string

//go:embed "compact_summarize.md"
var compactSummarizePrompt string

//go:embed "compact_update.md"
var compactUpdatePrompt string

//go:embed "subagent.md"
var DefaultSubagentPrompt string

//go:embed "context_management.md"
var contextManagementSystemPrompt string

// CompactorBasePrompt returns the baseline prompt used by compactor requests.
func CompactorBasePrompt() string {
	return "You are a context management assistant. You are called periodically by the system to maintain conversation context health."
}

// HeadlessBasePrompt returns the base system prompt for headless mode.
func HeadlessBasePrompt(isSubagent bool) string {
	if isSubagent {
		return subagentBasePrompt
	}

	return headlessBasePrompt
}

// RPCBasePrompt returns the base system prompt for RPC mode.
func RPCBasePrompt() string {
	return headlessBasePrompt
}

// JSONModeBasePrompt returns the base system prompt for JSON mode.
func JSONModeBasePrompt() string {
	return HeadlessBasePrompt(false)
}

// CompactSystemPrompt returns the system prompt for compaction.
func CompactSystemPrompt() string {
	return compactSystemPrompt
}

// CompactSummarizePrompt returns the prompt for initial summarization.
func CompactSummarizePrompt() string {
	return compactSummarizePrompt
}

// CompactUpdatePrompt returns the prompt for updating existing summary.
func CompactUpdatePrompt() string {
	return compactUpdatePrompt
}

// ToolInfo describes a tool for prompt generation.
type ToolInfo interface {
	Name() string
	Description() string
}

// Builder constructs system prompts with structured sections.
type Builder struct {
	// Working directory (can be static or dynamic via workspace)
	cwd       string
	workspace *tools.Workspace

	// Minimal mode (excludes optional sections like skills, project context)
	minimal bool

	// No workspace mode (excludes workspace section, for chat bots like claw)
	noWorkspace bool

	// Subagent mode (excludes task strategy section to prevent recursive subagent use)
	isSubagent bool

	// Workspace notes (optional reminders)
	workspaceNotes string

	// Available tools (for Tooling section)
	tools []ToolInfo

	// Skills (for Skills section)
	skills []skill.Skill

	// Context meta (for runtime_state telemetry, set by agent loop)
	contextMeta string

	// Token usage percent (for hint message generation)
	tokensPercent float64
}

// NewBuilder creates a new prompt builder.
func NewBuilder(_, cwd string) *Builder {
	return &Builder{
		cwd:     cwd,
		minimal: false,
	}
}

// NewBuilderWithWorkspace creates a new prompt builder with dynamic workspace support.
func NewBuilderWithWorkspace(_ string, ws *tools.Workspace) *Builder {
	return &Builder{
		workspace: ws,
		minimal:   false,
	}
}

// GetCWD returns the current working directory.
func (b *Builder) GetCWD() string {
	if b.workspace != nil {
		return b.workspace.GetCWD()
	}
	return b.cwd
}

// SetMinimal enables/disables minimal mode.
func (b *Builder) SetMinimal(minimal bool) *Builder {
	b.minimal = minimal
	return b
}

// SetNoWorkspace enables/disables no-workspace mode.
func (b *Builder) SetNoWorkspace(noWorkspace bool) *Builder {
	b.noWorkspace = noWorkspace
	return b
}

// SetSubagent enables/disables subagent mode.
func (b *Builder) SetSubagent(isSubagent bool) *Builder {
	b.isSubagent = isSubagent
	return b
}

// SetWorkspaceNotes sets optional workspace notes.
func (b *Builder) SetWorkspaceNotes(notes string) *Builder {
	b.workspaceNotes = notes
	return b
}

// SetTools sets the available tools.
func (b *Builder) SetTools(tools interface{}) *Builder {
	b.tools = convertTools(tools)
	return b
}

func convertTools(tools interface{}) []ToolInfo {
	if tools == nil {
		return nil
	}
	v := reflect.ValueOf(tools)
	if v.Kind() != reflect.Slice {
		return nil
	}
	result := make([]ToolInfo, v.Len())
	for i := 0; i < v.Len(); i++ {
		result[i] = v.Index(i).Interface().(ToolInfo)
	}
	return result
}

// SetSkills sets the available skills.
func (b *Builder) SetSkills(skills []skill.Skill) *Builder {
	b.skills = skills
	return b
}

// SetContextMeta sets the runtime_state telemetry metadata.
func (b *Builder) SetContextMeta(meta string) *Builder {
	b.contextMeta = meta
	return b
}

// SetTokensPercent sets the current token usage percentage.
func (b *Builder) SetTokensPercent(pct float64) *Builder {
	b.tokensPercent = pct
	return b
}

// Build builds final system prompt by replacing placeholders in the template.
func (b *Builder) Build() string {
	result := promptTemplate

	// Replace workspace section (optional - empty when noWorkspace is true)
	workspaceSection := ""
	if !b.noWorkspace {
		workspaceNotes := ""
		if b.workspaceNotes != "" {
			workspaceNotes = "\n" + b.workspaceNotes
		}
		workspaceSection = fmt.Sprintf(`## Workspace
Workspace location is runtime-managed and may change during execution (for example, when switching git worktrees).
Do not assume a fixed directory in the system prompt.
Use runtime_state fields for path truth:
- current_workdir
- startup_path
For command-local directory changes, use bash with "cd <dir> && <command>".
For persistent workspace switching across subsequent tool calls, use change_workspace when available.%s`, workspaceNotes)
	}
	result = strings.ReplaceAll(result, "%WORKSPACE_SECTION%", workspaceSection)

	// Replace skills (optional section)
	skills := ""
	if !b.minimal && len(b.skills) > 0 {
		if skillsText := skill.FormatForPrompt(b.skills); skillsText != "" {
			skills = skillsText
		}
	}
	result = strings.ReplaceAll(result, "%SKILLS%", skills)

	// Replace project context (optional section)
	projectContext := ""
	if !b.minimal && !b.noWorkspace {
		projectContext = b.buildProjectContext()
	}
	result = strings.ReplaceAll(result, "%PROJECT_CONTEXT%", projectContext)

	// Remove empty sections (optional sections that were not enabled)
	result = b.cleanupEmptySections(result)

	return result
}

// Bootstrap files to search for in workspace.
var bootstrapFiles = []string{
	"AGENTS.md",   // Agent identity and behavior
	"CLAUDE.md",   // Project guidelines (fallback when AGENTS.md is absent)
	"TOOLS.md",    // Tool usage instructions
	"IDENTITY.md", // User/owner identity
}

func (b *Builder) buildProjectContext() string {
	contexts := []string{}
	hasAgents := b.loadBootstrapFile("AGENTS.md") != ""

	for _, filename := range bootstrapFiles {
		if filename == "CLAUDE.md" && hasAgents {
			continue
		}
		content := b.loadBootstrapFile(filename)
		if content != "" {
			contexts = append(contexts, fmt.Sprintf("### %s\n\n%s", filename, content))
		}
	}

	if len(contexts) == 0 {
		return ""
	}

	return "## Project Context\n" + joinLines(contexts)
}

func (b *Builder) loadBootstrapFile(filename string) string {
	cwd := b.GetCWD()

	// Try project-local first: .ai/<filename>
	projectPath := fmt.Sprintf("%s/.ai/%s", cwd, filename)
	if content, err := os.ReadFile(projectPath); err == nil {
		return string(content)
	}

	// Try workspace root: <cwd>/<filename>
	rootPath := fmt.Sprintf("%s/%s", cwd, filename)
	if content, err := os.ReadFile(rootPath); err == nil {
		return string(content)
	}

	return ""
}

func (b *Builder) cleanupEmptySections(prompt string) string {
	// Remove sections that are completely empty (only section header and blank lines)
	lines := strings.Split(prompt, "\n")
	cleaned := []string{}

	for i := 0; i < len(lines); {
		line := lines[i]

		// Check if this line is a section header
		if strings.HasPrefix(line, "## ") {
			// Look ahead to see if the section has any non-empty content
			hasContent := false
			nextIdx := i + 1

			for nextIdx < len(lines) {
				nextLine := lines[nextIdx]
				if nextLine == "" {
					nextIdx++
					continue
				}
				if strings.HasPrefix(nextLine, "## ") {
					// Next section header - no content found
					break
				}
				hasContent = true
				break
			}

			if !hasContent {
				// Skip this empty section (header + empty lines)
				i = nextIdx
				continue
			}
		}

		cleaned = append(cleaned, line)
		i++
	}

	return strings.Join(cleaned, "\n")
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

// ThinkingInstruction returns the thinking instruction for the given level.
func ThinkingInstruction(level string) string {
	level = NormalizeThinkingLevel(level)
	switch level {
	case "off":
		return "Thinking level is off. Do not emit reasoning/thinking content. Respond directly with concise results and tool calls when needed."
	case "minimal":
		return "Thinking level is minimal. Keep reasoning very brief and only include what is strictly necessary."
	case "low":
		return "Thinking level is low. Keep reasoning concise and focused."
	case "medium":
		return "Thinking level is medium. Use balanced reasoning depth."
	case "high":
		return "Thinking level is high. Use thorough reasoning where needed."
	case "xhigh":
		return "Thinking level is xhigh. Use very thorough reasoning before final answers and tool calls."
	default:
		return ""
	}
}

// NormalizeThinkingLevel normalizes the thinking level string.
func NormalizeThinkingLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(level))
	case "":
		return "high"
	default:
		return "high"
	}
}

// ContextManagementSystemPrompt returns system prompt for context management.
func ContextManagementSystemPrompt() string {
	return contextManagementSystemPrompt
}