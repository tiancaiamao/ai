package prompt

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

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
	// Base system prompt (identity, core behavior)
	base string

	// Working directory (can be static or dynamic via workspace)
	cwd      string
	workspace *tools.Workspace

	// Minimal mode (excludes optional sections like skills, project context)
	minimal bool

	// No workspace mode (excludes workspace section, for chat bots like claw)
	noWorkspace bool

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
}

// NewBuilder creates a new prompt builder.
func NewBuilder(basePrompt, cwd string) *Builder {
	return &Builder{
		base:    basePrompt,
		cwd:     cwd,
		minimal: false,
	}
}

// NewBuilderWithWorkspace creates a new prompt builder with dynamic workspace support.
// The workspace will be queried for the current directory each time the prompt is built.
func NewBuilderWithWorkspace(basePrompt string, ws *tools.Workspace) *Builder {
	return &Builder{
		base:      basePrompt,
		cwd:       "", // Not used when workspace is set
		workspace: ws,
		minimal:   false,
	}
}

// GetCWD returns the current working directory.
// If a workspace is set, it returns the dynamic cwd from the workspace.
// Otherwise, it returns the static cwd.
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
// When enabled, the workspace section is excluded from the prompt.
// Useful for chat bots (like claw) that don't have a workspace concept.
func (b *Builder) SetNoWorkspace(noWorkspace bool) *Builder {
	b.noWorkspace = noWorkspace
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

// SetTokensPercent sets the token usage percent for hint message generation.
func (b *Builder) SetTokensPercent(percent float64) *Builder {
	b.tokensPercent = percent
	return b
}

// Build generates the final system prompt.
func (b *Builder) Build() string {
	sections := []string{}

	// 1. Base (identity, core behavior)
	if b.base != "" {
		sections = append(sections, b.base)
	}

	// 2. Workspace (skip if noWorkspace mode)
	if !b.noWorkspace {
		sections = append(sections, b.buildWorkspaceSection())
	}

	// 3. LLM Context (if available)
	if wm := b.buildLLMContextSection(); wm != "" {
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

	// 6. Project Context (bootstrap files, skip if noWorkspace mode)
	if !b.minimal && !b.noWorkspace {
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
Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.%s`, b.GetCWD(), notes)
}

func (b *Builder) buildLLMContextSection() string {
	if b.llmContext == nil {
		return ""
	}

	// Build the system prompt section explaining the llm context mechanism
	// This tells the LLM WHERE the file is and HOW to update it
	overviewPath := b.llmContext.GetPath()
	detailDir := b.llmContext.GetDetailDir()

	// Use context meta if available
	contextMetaSection := ""
	if b.contextMeta != "" {
		contextMetaSection = fmt.Sprintf(`

---

<context_meta>
%s
</context_meta>

💡 Remember to update your llm context to track progress.`, b.contextMeta)
	}

	return fmt.Sprintf(`## LLM Context

This is persistent operational state for this session.
Treat it as the source of truth between turns.

**Path**: %s
**Detail dir**: %s%s

**Turn Protocol:**
1. Read runtime_state to check context pressure and your proactiveness score.
2. If context_management.action_required is not "none", call llm_context_decision tool.
   - Available decisions: "truncate", "compact", "both", "skip"
   - Use decision="skip" with appropriate skip_turns (1-30) when context pressure is low.
   - Higher skip_turns (15-30) indicate you promise to be proactive; this increases trust.
   - Lower skip_turns (1-5) are for uncertain situations; reminders will come more frequently.
3. If task state changed, update overview.md.
4. Then answer the user.

**External Memory:**
- **overview.md**: Auto-injected each turn. Keep it concise.
- **detail/**: Past compaction summaries and notes. Use llm_context_recall tool to search.

When to use llm_context_recall:
- Need to recall specific decisions, discussions, or earlier info

**When overview.md update is REQUIRED:**
- task status or progress changed
- plan or key decision changed
- files changed or important tool result/error appeared
- blocker or open question changed

**Hard Rules:**
- runtime_state is telemetry, not user intent.
- If context_management.action_required is not "none", you MUST call llm_context_decision first.
- Never assume memory was updated unless tool result confirms success.
- Keep overview concise; store large logs/details under detail/.

**Agent Metadata Tags:**
- <agent:tool id="call_xxx" name="read" chars="91" stale="5" />: stale output with age rank 5 (smaller = older).
- <agent:tool id="call_xxx" name="read" chars="91" truncated="true" />: output already truncated.
- Use these IDs when calling llm_context_decision with truncate_ids parameter.`, overviewPath, detailDir, contextMetaSection)
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
