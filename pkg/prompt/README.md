# pkg/prompt

System prompt builder with embedded templates for agent, compaction, and PGE orchestration prompts.

## Overview

Constructs the system prompt sent to the LLM by combining a base template with tool descriptions, skill content, and structured sections. Also provides specialized prompts for compaction, context management, and PGE (Planner-Generator-Evaluator) orchestration.

## Templates

The package uses Go's `embed` directive to bundle markdown templates:

| Template | File | Purpose |
|----------|------|---------|
| Base prompt | `prompt.md` | Main system prompt for the agent |
| Compact system | `compact_system.md` | System prompt for compaction LLM calls |
| Compact summarize | `compact_summarize.md` | Prompt for initial summarization |
| Compact update | `compact_update.md` | Prompt for incremental summary updates |
| LLM decide check | `llm_decide_check.md` | Prompt for LLM-based compaction decision |
| Orchestrator | `orchestrator.md` | PGE orchestrator system prompt |
| Validator | `validator.md` | PGE validator system prompt |

## Builder

```go
type Builder struct { ... }

func NewBuilder(_, cwd string) *Builder
func NewBuilderWithWorkspace(_ string, ws *tools.Workspace) *Builder

func (b *Builder) GetCWD() string
func (b *Builder) SetMinimal(minimal bool) *Builder
func (b *Builder) SetTools(tools interface{}) *Builder
func (b *Builder) SetSkills(skills []skill.Skill) *Builder
func (b *Builder) SetSkillStats(stats *skill.SkillStatsFile) *Builder
func (b *Builder) SetTemplate(t string) *Builder
func (b *Builder) SetContextMeta(meta string) *Builder
func (b *Builder) SetTokensPercent(pct float64) *Builder
func (b *Builder) Build() string
func (b *Builder) BuildSkillsMessage() string
func (b *Builder) BuildInstructionsMessage() string
```

Fluent builder pattern. Constructs the final system prompt by:
1. Loading the base template (or custom template via `SetTemplate`)
2. Removing empty sections

Skills and AGENTS.md instructions are injected per-LLM-call as user-role messages via `BuildSkillsMessage()` and `BuildInstructionsMessage()`, keeping the system prompt stable for caching.

## Accessors

```go
func CompactorBasePrompt() string
func CompactSystemPrompt() string
func CompactSummarizePrompt() string
func CompactUpdatePrompt() string
func LLMDecideCheckPrompt() string
func OrchestratorTemplate() string
func ValidatorTemplate() string
func TemplateForRole(role string) (string, error) // "coder", "orchestrator", "validator"
```

Direct access to embedded prompt templates for use by `pkg/compact` and `pkg/agent`.

## Thinking Level Helpers

```go
func ThinkingInstruction(level string) string        // Returns instruction text for a thinking level
func NormalizeThinkingLevel(level string) string     // Normalizes: off/minimal/low/medium/high/xhigh
```

## Key Files

| File | Description |
|------|-------------|
| `builder.go` | `Builder`, `ToolInfo`, template accessors, thinking level helpers |
| `prompt.md` | Base system prompt template |
| `compact_system.md` | Compaction system prompt |
| `compact_summarize.md` | Initial summarization prompt |
| `compact_update.md` | Incremental update prompt |
| `llm_decide_check.md` | LLM-based compaction decision prompt |
| `orchestrator.md` | PGE orchestrator prompt |
| `validator.md` | PGE validator prompt |