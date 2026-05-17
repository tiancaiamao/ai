# pkg/tools

Tool implementations and registry for agent tool execution.

## Overview

Provides the `Registry` for tool registration and concrete tool implementations. Each tool implements the `context.Tool` interface (`Name`, `Description`, `Parameters`, `Execute`).

## Registry

```go
type Registry struct { ... }

func NewRegistry() *Registry
func (r *Registry) Register(tool context.Tool)
func (r *Registry) Get(name string) (context.Tool, bool)
func (r *Registry) All() []context.Tool
func (r *Registry) ToLLMTools() []map[string]any
```

The registry maps tool names to implementations. `ToLLMTools()` converts all registered tools into the JSON format expected by LLM function-calling APIs.

## Built-in Tools

| Tool | File | Description |
|------|------|-------------|
| `bash` | `bash.go` | Execute shell commands with timeout |
| `read` | `read.go` | Read file contents (supports offset/limit) |
| `write` | `write.go` | Write content to files |
| `edit` | `edit.go` | Edit files by replacing text ranges |
| `grep` | `grep.go` | Search file contents with regex |
| `find_skill` | `find_skill.go` | Search and load agent skills |
| `change_workspace` | `change_workspace.go` | Change working directory |
| `hashline` | `hashline.go` | Content-addressed line references for edit verification |
| `workspace` | `workspace.go` | Workspace info and path resolution |

## Tool Output Processing

Tool output is processed through the truncation pipeline before being stored:
- `ToolOutputLimits` controls max character count
- Long outputs are truncated with head/tail preservation
- Hashlines may be appended for content verification

## Key Files

| File | Description |
|------|-------------|
| `registry.go` | Tool registry |
| `bash.go` | Shell command execution |
| `read.go` | File reading |
| `write.go` | File writing |
| `edit.go` | File editing |
| `grep.go` | Content search |
| `find_skill.go` | Skill discovery |
| `change_workspace.go` | Working directory management |
| `hashline.go` | Content-addressed references |
| `workspace.go` | Workspace utilities |