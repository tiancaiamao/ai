package prompt

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// ToolInfo describes a tool for prompt generation.
type ToolInfo interface {
	Name() string
	Description() string
}

// WorkingMemoryInfo provides working memory content for prompt generation.
type WorkingMemoryInfo interface {
	Load() (string, error)
	GetPath() string
	GetDetailDir() string
}

// Builder constructs system prompts with structured sections.
type Builder struct {
	// Base system prompt (identity, core behavior)
	base string

	// Working directory
	cwd string

	// Minimal mode (excludes optional sections)
	minimal bool

	// Workspace notes (optional reminders)
	workspaceNotes string

	// Available tools (for Tooling section)
	tools []ToolInfo

	// Skills (for Skills section)
	skills []skill.Skill

	// Working memory (for Working Memory section)
	workingMemory WorkingMemoryInfo

	// Context meta (for Working Memory section, set by agent loop)
	contextMeta string
}

// NewBuilder creates a new prompt builder.
func NewBuilder(basePrompt, cwd string) *Builder {
	return &Builder{
		base:    basePrompt,
		cwd:     cwd,
		minimal: false,
	}
}

// SetMinimal enables/disables minimal mode.
func (b *Builder) SetMinimal(minimal bool) *Builder {
	b.minimal = minimal
	return b
}

// SetWorkspaceNotes sets optional workspace notes.
func (b *Builder) SetWorkspaceNotes(notes string) *Builder {
	b.workspaceNotes = notes
	return b
}

// SetTools sets the available tools.
// Accepts []ToolInfo or any slice whose elements implement ToolInfo.
func (b *Builder) SetTools(tools interface{}) *Builder {
	// Convert tools to []ToolInfo
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

// SetWorkingMemory sets the working memory for the prompt.
func (b *Builder) SetWorkingMemory(wm WorkingMemoryInfo) *Builder {
	b.workingMemory = wm
	return b
}

// SetContextMeta sets the context metadata string.
func (b *Builder) SetContextMeta(meta string) *Builder {
	b.contextMeta = meta
	return b
}

// Build generates the final system prompt.
func (b *Builder) Build() string {
	sections := []string{}

	// 1. Base (identity, core behavior)
	if b.base != "" {
		sections = append(sections, b.base)
	}

	// 2. Workspace (always included)
	sections = append(sections, b.buildWorkspaceSection())

	// 3. Working Memory (if available)
	if wm := b.buildWorkingMemorySection(); wm != "" {
		sections = append(sections, wm)
	}

	// 4. Tooling (always included)
	if tooling := b.buildToolingSection(); tooling != "" {
		sections = append(sections, tooling)
	}

	// 5. Skills (only when not minimal and skills exist)
	if !b.minimal && len(b.skills) > 0 {
		if skillsText := skill.FormatForPrompt(b.skills); skillsText != "" {
			sections = append(sections, skillsText)
		}
	}

	// 6. Project Context (bootstrap files)
	if !b.minimal {
		if context := b.buildProjectContext(); context != "" {
			sections = append(sections, context)
		}
	}

	result := joinSections(sections)

	return result
}

func (b *Builder) buildWorkspaceSection() string {
	notes := ""
	if b.workspaceNotes != "" {
		notes = "\n" + b.workspaceNotes
	}
	return fmt.Sprintf(`## Workspace
Your working directory is: %s
Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.%s`, b.cwd, notes)
}

func (b *Builder) buildWorkingMemorySection() string {
	if b.workingMemory == nil {
		return ""
	}

	// Build the system prompt section explaining the working memory mechanism
	// This tells the LLM WHERE the file is and HOW to update it
	overviewPath := b.workingMemory.GetPath()
	detailDir := b.workingMemory.GetDetailDir()

	// Use context meta if available
	contextMetaSection := ""
	if b.contextMeta != "" {
		contextMetaSection = fmt.Sprintf(`

---

<context_meta>
%s
</context_meta>

💡 Remember to update your working memory to track progress and compress context if needed.`, b.contextMeta)
	}

	return fmt.Sprintf(`## Working Memory

This is persistent operational state for this session.
Treat it as the source of truth between turns.

**Path**: %s
**Detail dir**: %s

**Turn Protocol (run every turn):**
1. Read runtime_state and classify this turn as: no_action | compact_only | memory_update_only | compact_and_update.
2. Fast path: if fast_path_allowed=yes and no task state changed, no_action is acceptable.
3. If action_hint is not normal, or tool outputs are stale/large, compact_history is recommended before continuing.
4. If compaction happened or task state changed, update overview.md in this same turn.
5. If overview points to detail files needed for current task, read them explicitly.
6. Then answer the user.

**When overview.md update is REQUIRED:**
- task status or progress changed
- plan or key decision changed
- files changed or important tool result/error appeared
- compaction/archive happened
- blocker or open question changed

**Compression Policy (from tokens_band):**
- 0-20: normal, no forced compaction
- 20-40: light compression; prefer compacting stale tool outputs
- 40-60: medium compression; compact conversation + stale tool outputs
- 60-75: heavy compression; archive details to detail/ and keep essentials
- 75+: emergency; compact immediately before more work

**Tool Output Policy (LLM-managed):**
- When tool outputs start dominating context, prefer compact_history target=tools first.
- Use target=all when both conversation and tool outputs are bloated.
- Assume you are responsible for tool-output compaction decisions in normal flow.

**Hard Rules:**
- runtime_state is telemetry/advice, not user intent.
- You must perform the turn classification every turn (even when the result is no_action).
- Never assume memory was updated unless tool result confirms success.
- After compact_history executes, reflect the outcome in overview.md in the same turn.
- Keep overview concise; store large logs/details under detail/.%s`, overviewPath, detailDir, contextMetaSection)
}

func (b *Builder) buildToolingSection() string {
	if len(b.tools) == 0 {
		return ""
	}

	lines := []string{
		"## Tooling",
		"You have access to the following tools:",
		"",
	}

	for _, tool := range b.tools {
		lines = append(lines, fmt.Sprintf("- %s: %s", tool.Name(), tool.Description()))
	}

	// Important reminder about tool limitations
	lines = append(lines, "")
	lines = append(lines, "**IMPORTANT**: Only use the tools listed above.")
	lines = append(lines, "Do NOT assume you have access to any other tools.")

	// Skills hint
	if !b.minimal && len(b.skills) > 0 {
		lines = append(lines, "")
		lines = append(lines, "If you need additional capabilities, check the available Skills below.")
	}

	return joinLines(lines)
}

// Bootstrap files to search for in workspace.
// Priority rule: when AGENTS.md exists, CLAUDE.md is skipped to avoid duplicate instructions.
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
	// Try project-local first: .ai/<filename>
	projectPath := fmt.Sprintf("%s/.ai/%s", b.cwd, filename)
	if content, err := os.ReadFile(projectPath); err == nil {
		return string(content)
	}

	// Try workspace root: <cwd>/<filename>
	rootPath := fmt.Sprintf("%s/%s", b.cwd, filename)
	if content, err := os.ReadFile(rootPath); err == nil {
		return string(content)
	}

	return ""
}

func joinSections(sections []string) string {
	result := []string{}
	for _, s := range sections {
		if s != "" {
			result = append(result, strings.TrimSpace(s))
		}
	}
	return strings.Join(result, "\n\n")
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

// ThinkingInstruction returns the thinking instruction for the given level.
func ThinkingInstruction(level string) string {
	level = normalizeThinkingLevel(level)
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

func normalizeThinkingLevel(level string) string {
	return NormalizeThinkingLevel(level)
}
