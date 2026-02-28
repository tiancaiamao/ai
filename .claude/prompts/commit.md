# Commit Message Prompt

Use this prompt template when generating commit messages.

## Instructions

Generate a commit message following these conventions.

## Commit Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

## Types

| Type | Description |
|------|-------------|
| feat | New feature |
| fix | Bug fix |
| docs | Documentation only |
| style | Formatting, no code change |
| refactor | Refactoring, no functional change |
| test | Adding or modifying tests |
| chore | Build process, dependencies |
| perf | Performance improvement |

## Rules

1. **Subject line**:
   - Use imperative mood ("add" not "added")
   - No period at the end
   - Max 72 characters
   - Start with lowercase

2. **Body** (optional):
   - Explain WHAT and WHY, not HOW
   - Separate from subject with blank line
   - Wrap at 72 characters

3. **Footer** (optional):
   - Breaking changes: `BREAKING CHANGE: ...`
   - Close issues: `Closes #123`

## Examples

### Simple
```
fix(rpc): handle empty response body
```

### With body
```
feat(agent): add parallel task execution

Implement concurrent task processing with configurable
concurrency limit. Uses worker pool pattern to control
the number of parallel operations.

Closes #42
```

### Breaking change
```
refactor(api)!: change RPC protocol to use JSON-RPC 2.0

BREAKING CHANGE: The RPC protocol has been changed from
custom format to JSON-RPC 2.0. All clients must update.
```

## Usage

```
User: commit these changes
→ Analyze diff and generate commit message
```