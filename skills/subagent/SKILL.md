---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
tools: [bash]
---

# Subagent Tool

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated process** using headless mode with a clean session and **focused system prompt**.

## When to Use

- **Parallel execution** of independent tasks
- **Complex problems** that need focused attention
- **Breaking down** large tasks into sub-tasks
- **Tasks requiring isolation** from main conversation context

## Usage Patterns

### Single Task

```json
{
  "tool": "bash",
  "command": "ai --mode headless --no-session --subagent --max-turns 10 \"<task description>\""
}
```

### Parallel Tasks

Execute multiple tasks concurrently:

```bash
ai --mode headless --no-session --subagent "Task 1" &
ai --mode headless --no-session --subagent "Task 2" &
ai --mode headless --no-session --subagent "Task 3" &
wait
```

### With Tool Restrictions

```json
{
  "tool": "bash",
  "command": "ai --mode headless --no-session --subagent --tools read,grep \"Analyze code\""
}
```

## Command-Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--mode headless` | Run in headless mode (required) | - |
| `--no-session` | Don't persist session to disk (required) | false |
| `--subagent` | Use focused subagent system prompt (recommended) | false |
| `--max-turns N` | Maximum conversation turns | 0 (unlimited) |
| `--tools T1,T2` | Comma-separated tool whitelist | all tools |

## Output Format

Single JSON line:

```json
{"text":"Result...","usage":{"input_tokens":150,"output_tokens":50,"total_tokens":200},"exit_code":0}
```

## Examples

### Example 1: Code Analysis

```json
{
  "tool": "bash",
  "command": "./bin/ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \"Review src/auth for security vulnerabilities\""
}
```

### Example 2: Parallel Analysis

```bash
ai --mode headless --no-session --subagent "Analyze src/auth" > /tmp/auth.txt &
ai --mode headless --no-session --subagent "Analyze src/api" > /tmp/api.txt &
wait && cat /tmp/auth.txt /tmp/api.txt
```

## Best Practices

- ✅ Always use `--no-session` to avoid polluting session files
- ✅ Always use `--subagent` for focused, concise output
- ✅ Set `--max-turns` to prevent runaway conversations
- ✅ Use `--tools` to restrict capabilities
- ✅ Run independent tasks in parallel
- ❌ Don't use for simple single-file reads (use `read` tool)
- ❌ Don't nest subagents

## Agent Profiles

For specialized tasks, use tool restrictions to create focused agent profiles:

| Profile | Tools | Purpose | Example |
|---------|-------|---------|---------|
| **Explorer** | `read,grep` | Read-only analysis, code exploration | `ai --mode headless --subagent --tools read,grep "Analyze auth flow"` |
| **Reviewer** | `read,grep` | Code review, validation | `ai --mode headless --subagent --tools read,grep "Review for security issues"` |
| **Builder** | `read,write,edit,bash` | Implementation, file changes | `ai --mode headless --subagent --tools read,write,edit,bash "Implement feature X"` |
| **Tester** | `read,bash,grep` | Test execution, debugging | `ai --mode headless --subagent --tools read,bash,grep "Run tests and report failures"` |
| **General** | (all) | Full capability tasks | `ai --mode headless --subagent "Complex task requiring all tools"` |

### Profile Selection Guide

| Task Type | Use Profile | Why |
|-----------|-------------|-----|
| Code analysis / architecture review | Explorer | No writes needed, fastest execution |
| Security / style review | Reviewer | Read-only prevents accidental changes |
| Feature implementation | Builder | Needs write access + bash for tests |
| Test triage / debugging | Tester | Needs to run tests but not modify code |
| Unknown / complex | General | Full tool access for flexibility |

### Profile Examples

```bash
# Explorer: Analyze codebase structure
ai --mode headless --subagent --tools read,grep --max-turns 5 \
  "List all API endpoints in src/api/"

# Reviewer: Security-focused review
ai --mode headless --subagent --tools read,grep --max-turns 8 \
  "Review auth.go for SQL injection vulnerabilities"

# Builder: Feature with testing
ai --mode headless --subagent --tools read,write,edit,bash --max-turns 15 \
  "Implement user registration with tests"

# Tester: Diagnose failures
ai --mode headless --subagent --tools read,bash,grep --max-turns 10 \
  "Run go test ./auth and diagnose any failures"
```

## Important Notes

1. **No nesting**: Subagent cannot spawn another subagent
2. **Isolated context**: No access to parent conversation
3. **Process isolation**: Runs as separate OS process
4. **Focused prompt**: Specialized system prompt for concise, efficient execution
5. **Working directory**: Inherits from parent
