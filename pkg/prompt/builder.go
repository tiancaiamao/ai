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

//go:embed "compact_system.md"
var compactSystemPrompt string

//go:embed "compact_summarize.md"
var compactSummarizePrompt string

//go:embed "compact_update.md"
var compactUpdatePrompt string

//go:embed "orchestrator.md"
var orchestratorTemplate string

//go:embed "validator.md"
var validatorTemplate string

//go:embed "delta_compaction_decision.md"
var deltaCompactionDecisionPrompt string

//go:embed "delta_compaction_forced.md"
var deltaCompactionForcedPrompt string

// CompactorBasePrompt returns the baseline prompt used by compactor requests.
func CompactorBasePrompt() string {
	return "You are a context management assistant. You are called periodically by the system to maintain conversation context health."
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

// DeltaCompactionDecisionPrompt returns the body of the context compaction
// decision message injected when delta compaction is being considered.
func DeltaCompactionDecisionPrompt() string {
	return deltaCompactionDecisionPrompt
}

// DeltaCompactionForcedPrompt returns the body of the forced context compaction
// message injected when delta tokens reach the hard limit.
func DeltaCompactionForcedPrompt() string {
	return deltaCompactionForcedPrompt
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

	// Available tools (for Tooling section)
	tools []ToolInfo

	// Skills (for Skills section)
	skills []skill.Skill

	// Skill usage stats (optional, for progressive disclosure)
	skillStats *skill.SkillStatsFile

	// Custom template (if empty, uses embedded promptTemplate)
	template string

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

// SetSkillStats sets the skill usage statistics for progressive disclosure.
func (b *Builder) SetSkillStats(stats *skill.SkillStatsFile) *Builder {
	b.skillStats = stats
	return b
}

// SetTemplate sets a custom prompt template. If empty, uses the default embedded prompt.md.
func (b *Builder) SetTemplate(t string) *Builder {
	b.template = t
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

// BuildSkillsMessage formats the skills list as a user message wrapped in
// <agent:skills> tags, ready for injection as a user message before the
// last user input on each LLM call. Returns empty string when no skills
// are available or minimal mode is enabled.
//
// This replaces the former %SKILLS% placeholder in the system prompt template.
// Skills are now injected per-LLM-call as a user-role message (similar to
// AgentInstructions), keeping the system prompt stable for caching while still
// providing skill context on every turn.
func (b *Builder) BuildSkillsMessage() string {
	if b.minimal || len(b.skills) == 0 {
		return ""
	}
	skillsText := skill.FormatForPrompt(b.skills, b.skillStats)
	if skillsText == "" {
		return ""
	}
	return fmt.Sprintf("<agent:skills>\n%s\n</agent:skills>", skillsText)
}

// Build builds final system prompt from the template.
// Skills are no longer included here — use BuildSkillsMessage() to get the
// skills content for per-LLM-call user message injection.
func (b *Builder) Build() string {
	result := promptTemplate
	if b.template != "" {
		result = b.template
	}

	// Remove empty sections (optional sections that were not enabled)
	result = b.cleanupEmptySections(result)

	return result
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

// loadAgentInstructions reads AGENTS.md content from the workspace.
// Lookup order: .ai/AGENTS.md (project-local) → AGENTS.md (workspace root).
// Returns empty string if not found.
func (b *Builder) loadAgentInstructions() string {
	return b.loadBootstrapFile("AGENTS.md")
}

// BuildInstructionsMessage returns the AGENTS.md content wrapped in
// <agent:instructions> tags, ready for injection as a user message.
// Returns empty string when no AGENTS.md is present.
//
// Unlike Build() output (which becomes the static system prompt), this content
// is injected per-LLM-call as a user-role message placed before the user's
// actual input — matching the codex contextual_user_message pattern.
func (b *Builder) BuildInstructionsMessage() string {
	content := b.loadAgentInstructions()
	if content == "" {
		return ""
	}
	return fmt.Sprintf("<agent:instructions>\n%s\n</agent:instructions>", content)
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

// OrchestratorTemplate returns the PGE orchestrator prompt template.
func OrchestratorTemplate() string {
	return orchestratorTemplate
}

// ValidatorTemplate returns the PGE validator prompt template.
func ValidatorTemplate() string {
	return validatorTemplate
}

// TemplateForRole returns the system prompt template for the given role.
// Supported roles: "coder" (default), "orchestrator", "validator".
// Returns empty string for unknown roles.
func TemplateForRole(role string) (string, error) {
	switch role {
	case "", "coder":
		return promptTemplate, nil
	case "orchestrator":
		return orchestratorTemplate, nil
	case "validator":
		return validatorTemplate, nil
	default:
		return "", fmt.Errorf("unknown role %q (valid: coder, orchestrator, validator)", role)
	}
}
