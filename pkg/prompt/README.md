# pkg/prompt

System prompt builder with embedded templates for agent, compaction, and context management prompts.

## Overview

Constructs the system prompt sent to the LLM by combining a base template with tool descriptions, skill content, and structured sections. Also provides specialized prompts for compaction and context management operations.

## Templates

The package uses Go's `embed` directive to bundle markdown templates:

| Template | File | Purpose |
|----------|------|---------|
| Base prompt | `prompt.md` | Main system prompt for the agent |
| Compact system | `compact_system.md` | System prompt for compaction LLM calls |
| Compact summarize | `compact_summarize.md` | Prompt for initial summarization |
| Compact update | `compact_update.md` | Prompt for incremental summary updates |
| Context management | `context_management.md` | System prompt for context manager |

## Builder

```go
type Builder struct { ... }

func NewBuilder() *Builder
func (b *Builder) WithTools(tools []ToolInfo) *Builder
func (b *Builder) WithSkills(skills []skill.Skill) *Builder
func (b *Builder) WithContext(ctx string) *Builder
func (b *Builder) Build() string
```

Fluent builder pattern. Constructs the final system prompt by:
1. Loading the base template
2. Injecting tool descriptions (name, description, parameters schema)
3. Injecting skill content (markdown)
4. Injecting LLM context (structured content)

## Accessors

```go
func CompactorBasePrompt() string
func CompactSystemPrompt() string
func CompactSummarizePrompt() string
func CompactUpdatePrompt() string
func ContextManagementSystemPrompt() string
```

Direct access to embedded prompt templates for use by `pkg/compact` and `pkg/agent`.

## Key Files

| File | Description |
|------|-------------|
| `builder.go` | `Builder` — system prompt construction |
| `prompt.md` | Base system prompt template |
| `compact_system.md` | Compaction system prompt |
| `compact_summarize.md` | Initial summarization prompt |
| `compact_update.md` | Incremental update prompt |
| `context_management.md` | Context manager system prompt |