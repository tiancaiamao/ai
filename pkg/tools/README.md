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

## Workspace

```go
type Workspace struct { ... }

func NewWorkspace(initialCwd string) (*Workspace, error)
func MustNewWorkspace(initialCwd string) *Workspace

func (w *Workspace) GetCWD() string
func (w *Workspace) SetCWD(cwd string) error
func (w *Workspace) GetInitialCWD() string
func (w *Workspace) GetGitRoot() string
func (w *Workspace) ResolvePath(path string) string
func (w *Workspace) GetRelativePath(path string) (string, error)
func (w *Workspace) IsGitRepository() bool
```

Tracks the current working directory across tool invocations, with git root detection for session storage that shares sessions across worktrees.

## Tool Output Processing

Tool output is processed through the truncation pipeline before being stored:
- `ToolOutputLimits` controls max character count
- Long outputs are truncated with head/tail preservation

## Key Files

| File | Description |
|------|-------------|
| `registry.go` | `Registry` — tool registration and lookup |
| `workspace.go` | `Workspace` — dynamic working directory with git root detection |
| `bash.go` | Shell command execution |
| `read.go` | File reading |
| `write.go` | File writing |
| `edit.go` | File editing |
| `grep.go` | Content search |
| `find_skill.go` | Skill discovery |
| `change_workspace.go` | Working directory management |