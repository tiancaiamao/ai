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

//go:embed "base.md"
var basePrompt string

//go:embed "subagent_base.md"
var subagentBasePrompt string

//go:embed "headless_base.md"
var headlessBasePrompt string

//go:embed "context_management.md"
var contextManagementPrompt string

//go:embed "compact_system.md"
var compactSystemPrompt string

//go:embed "compact_summarize.md"
var compactSummarizePrompt string

//go:embed "compact_update.md"
var compactUpdatePrompt string

//go:embed "subagent.md"
var DefaultSubagentPrompt string

//go:embed "task_tracking.md"
var taskTrackingPrompt string

//go:embed "task_strategy.md"
var taskStrategyPrompt string

// RPCBasePrompt returns the base system prompt for interactive RPC mode.
func RPCBasePrompt() string {
	return basePrompt
}

// CompactorBasePrompt returns the baseline prompt used by compactor requests.
func CompactorBasePrompt() string {
	return "You are a helpful coding assistant."
}

// HeadlessBasePrompt returns the base system prompt for headless mode.
func HeadlessBasePrompt(isSubagent bool) string {
	if isSubagent {
		return subagentBasePrompt
	}

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

// LLMContextInfo provides llm context content for prompt generation.
type LLMContextInfo interface {
	Load() (string, error)
	GetPath() string
	GetDetailDir() string
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

	// Resident prompt (for LLM Context section)
	llmContext LLMContextInfo

	// Context meta (for LLM Context section, set by agent loop)
	contextMeta string

	// Token usage percent (for hint message generation)
	tokensPercent float64

	// Task tracking enabled (controls task tracking inclusion)
	taskTrackingEnabled bool

	// Context management enabled (controls context_management.md inclusion)
	contextManagementEnabled bool
}

// NewBuilder creates a new prompt builder.
func NewBuilder(_, cwd string) *Builder {
	return &Builder{
		cwd:                        cwd,
		minimal:                    false,
		taskTrackingEnabled:          true,
		contextManagementEnabled:      true,
	}
}

// NewBuilderWithWorkspace creates a new prompt builder with dynamic workspace support.
func NewBuilderWithWorkspace(_ string, ws *tools.Workspace) *Builder {
	return &Builder{
		workspace:                  ws,
		minimal:                    false,
		taskTrackingEnabled:          true,
		contextManagementEnabled:      true,
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
	if v.Kind() == reflect.Slice {
		result := make([]ToolInfo, v.Len())
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i).Interface()
			if tool, ok := elem.(ToolInfo); ok {
				result[i] = tool
			}
		}
		return result
	}
	return nil
}

// SetSkills sets the available skills.
func (b *Builder) SetSkills(skills []skill.Skill) *Builder {
	b.skills = skills
	return b
}

// SetLLMContext sets the llm context for the prompt.
func (b *Builder) SetLLMContext(wm LLMContextInfo) *Builder {
	b.llmContext = wm
	return b
}

// SetContextMeta sets the context metadata string.
func (b *Builder) SetContextMeta(meta string) *Builder {
	b.contextMeta = meta
	return b
}

// SetTokensPercent sets the token usage percent.
func (b *Builder) SetTokensPercent(percent float64) *Builder {
	b.tokensPercent = percent
	return b
}

// SetTaskTrackingEnabled sets whether task tracking is enabled.
func (b *Builder) SetTaskTrackingEnabled(enabled bool) *Builder {
	b.taskTrackingEnabled = enabled
	return b
}

// SetContextManagementEnabled sets whether context management is enabled.
func (b *Builder) SetContextManagementEnabled(enabled bool) *Builder {
	b.contextManagementEnabled = enabled
	return b
}

// Build builds final system prompt by replacing placeholders in the template.
func (b *Builder) Build() string {
	result := promptTemplate

	// Replace workspace section (optional - empty when noWorkspace is true)
	workspaceSection := ""
	if !b.noWorkspace {
		workspaceDir := b.GetCWD()
		workspaceNotes := ""
		if b.workspaceNotes != "" {
			workspaceNotes = "\n" + b.workspaceNotes
		}
		workspaceSection = fmt.Sprintf(`## Workspace
Your working directory is: %s
Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.%s`, workspaceDir, workspaceNotes)
	}
	result = strings.ReplaceAll(result, "%WORKSPACE_SECTION%", workspaceSection)

	// Replace task tracking (optional section)
	taskTracking := ""
	if b.taskTrackingEnabled {
		taskTracking = b.buildTaskTrackingContent()
	}
	result = strings.ReplaceAll(result, "%TASK_TRACKING_CONTENT%", taskTracking)

	// Replace context management (optional section)
	contextManagement := ""
	if b.contextManagementEnabled && b.llmContext != nil {
		contextManagement = contextManagementPrompt
	}
	result = strings.ReplaceAll(result, "%CONTEXT_MANAGEMENT_CONTENT%", contextManagement)

	// Replace task strategy (optional section)
	taskStrategy := ""
	if !b.noWorkspace && !b.isSubagent {
		taskStrategy = taskStrategyPrompt
	}
	result = strings.ReplaceAll(result, "%TASK_STRATEGY_CONTENT%", taskStrategy)

	// Replace tools (required)
	tools := b.buildToolsContent()
	result = strings.ReplaceAll(result, "%TOOLS%", tools)

	// Replace skills hint (optional, only shown if skills exist)
	skillsHint := ""
	if !b.minimal && len(b.skills) > 0 {
		skillsHint = "\n\nIf you need additional capabilities, check the available Skills below."
	}
	result = strings.ReplaceAll(result, "%SKILLS_HINT%", skillsHint)

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

func (b *Builder) buildToolsContent() string {
	if len(b.tools) == 0 {
		return ""
	}

	lines := []string{}
	for _, tool := range b.tools {
		lines = append(lines, fmt.Sprintf("- %s: %s", tool.Name(), tool.Description()))
	}
	return joinLines(lines)
}

func (b *Builder) buildTaskTrackingContent() string {
	content := `## Task Tracking
Track multi-step tasks using ` + "`llm_context_update`" + `.

**Update** (with markdown content) when:
- Task status changes, decisions made, files changed
- Progress milestone reached, blocker emerged/resolved

**Skip** (with ` + "`skip=true, reasoning=\"...\"`" + `) when:
- Simple questions, no progress, routine responses

**Important:** Always call ` + "`llm_context_update`" + ` — with content or ` + "`skip=true`" + `. This prevents reminder spam.

### Update Example

**Good:** Specific, actionable status

` + "```markdown" + `
## Current Task
- Implementing feature X
- Status: 60% complete
- Done: Core logic, unit tests
- Next: Integration tests
` + "```" + `

**Bad:** Too vague, no actionable info

` + "```markdown" + `
Working on it...
` + "```" + `

**When to skip (skip=true):**
- Simple questions without task progress
- Routine responses without state changes
- Quick clarifications or confirmations`

	if b.contextMeta != "" {
		content += `

---

<context_meta>
` + b.contextMeta + `
</context_meta>`
	}

	return content
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