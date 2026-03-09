---
name: orchestrate
description: Automatically analyze tasks, decompose complex ones, and coordinate subagents to complete them. Users only see final results.
tools: [bash]
---

# Orchestrate Skill

Automatically analyze user tasks, determine if decomposition is needed, spawn appropriate subagents with suitable personas, and aggregate results back to the user.

## When to Use

- **Complex multi-step tasks** that would benefit from decomposition
- **Tasks requiring different skills** (research + implementation + review)
- **Large implementation projects** that need organized execution
- **Any task** where automatic decomposition might help efficiency

## How It Works

```
User Input → Analyze Complexity → Simple? → Execute directly
                            ↓
                     Complex? → Decompose → Select Personas → Spawn Subagents → Aggregate Results → Return to User
```

## Usage

### Basic Usage

```json
{
  "tool": "bash",
  "command": "ai --mode headless --no-session --subagent \"<orchestration prompt>\""
}
```

The orchestration prompt should include:
1. The user's original task
2. Request to analyze and decompose if needed
3. Instructions to use agent personas from `skills/orchestrate/references/`

## Agent Personas

Agent personas are defined as Markdown files in `skills/orchestrate/references/`:

| Persona File | Role | Use When |
|--------------|------|----------|
| `implementer.md` | Implementation | Building features, writing code |
| `reviewer.md` | Code Review | Reviewing, validating, checking quality |
| `researcher.md` | Research | Investigating, gathering information |
| `explorer.md` | Exploration | Understanding codebase, finding patterns |

### Loading Persona

```bash
# Use --system-prompt @path to load persona file
ai --mode headless --no-session --subagent --system-prompt @{skills-path}/orchestrate/references/implementer.md "Task description"
```

## Task Analysis

The orchestrator analyzes tasks based on:

1. **Complexity indicators**:
   - Multiple files mentioned
   - Multi-step processes (design → implement → test)
   - Domain knowledge required (research first)
   - Large scope keywords ("implement", "build", "create", "system")

2. **Simple task**: Can be done in 1-2 turns with current context
   - Single file modification
   - Quick question or lookup
   - Straightforward implementation

3. **Complex task**: Benefits from decomposition
   - Multiple files across directories
   - Requires research before implementation
   - Needs review/validation step
   - Large feature or system

## Decomposition Strategy

### Pattern 1: Research + Implementation + Review

```
Task: "Add user authentication to the app"

Decomposition:
1. Researcher: "Research best practices for auth in Go"
2. Implementer: "Implement auth based on research"
3. Reviewer: "Review auth implementation for security"
```

### Pattern 2: Exploration + Implementation

```
Task: "Fix the login bug"

Decomposition:
1. Explorer: "Find login-related code, identify potential issues"
2. Implementer: "Fix the identified bug"
```

### Pattern 3: Multi-part Implementation

```
Task: "Add REST API for users"

Decomposition:
1. Implementer: "Create user model and database schema"
2. Implementer: "Add CRUD handlers"
3. Reviewer: "Review API implementation"
```

## Command Construction

Build subagent command:

```bash
# Template
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/<persona>.md \
  --max-turns <N> \
  "<subtask description>"
```

### Persona Selection Heuristics

| Task Keyword | Persona |
|--------------|---------|
| "research", "investigate", "find out" | researcher |
| "review", "check", "validate", "audit" | reviewer |
| "explore", "analyze", "understand", "find" | explorer |
| "implement", "build", "create", "add", "fix" | implementer |

## Result Aggregation

After subagents complete:

1. **Collect outputs** from each subagent
2. **Format as structured summary**:
   ```
   ## Results
   
   ### Research Phase
   <researcher output>
   
   ### Implementation Phase
   <implementer output>
   
   ### Review Phase
   <reviewer output>
   
   ## Summary
   <concise summary of what was accomplished>
   ```
3. **Return to user** - user sees unified result, not individual subagent outputs

## Examples

### Example 1: Implement Feature

```bash
# Orchestrator analyzes: "Add OAuth2 login" is complex
# Decomposition: Research → Implement → Review

# Step 1: Research
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/researcher.md \
  "Research OAuth2 implementation options for Go web apps" > /tmp/research.txt

# Step 2: Implement
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/implementer.md \
  "Implement OAuth2 login using the research findings. Files: auth.go, oauth.go" > /tmp/implement.txt

# Step 3: Review
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/reviewer.md \
  "Review the OAuth2 implementation for security issues" > /tmp/review.txt

# Aggregate results
echo "=== Research ===" && cat /tmp/research.txt
echo "=== Implementation ===" && cat /tmp/implement.txt
echo "=== Review ===" && cat /tmp/review.txt
```

### Example 2: Bug Fix with Exploration

```bash
# Task: "Fix the memory leak in the cache"

# Step 1: Explore to find the issue
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/explorer.md \
  "Find potential memory leak sources in cache package. Look for unclosed resources, growing maps, missing cleanup." > /tmp/explore.txt

# Step 2: Fix based on findings
ai --mode headless --no-session --subagent \
  --system-prompt @skills/orchestrate/references/implementer.md \
  "Fix the memory leak in cache package. Issues found: $(cat /tmp/explore.txt)" > /tmp/fix.txt
```

## Best Practices

- ✅ Analyze task complexity first before decomposing
- ✅ Choose persona based on subtask type
- ✅ Aggregate results into coherent summary
- ✅ Let user see final result, not internal subagent outputs
- ❌ Don't decompose trivial one-step tasks
- ❌ Don't spawn too many parallel subagents (max 3-4)
- ❌ Don't forget to aggregate results

## Persona File Format

Each persona file is plain Markdown. The content becomes the system prompt:

```markdown
# Persona: Implementer

You are an expert software developer focused on efficient implementation.

Your characteristics:
- Write clean, maintainable code
- Follow project conventions
- Add appropriate tests
- Document your changes

When working:
1. Understand requirements fully before coding
2. Make minimal, focused changes
3. Verify changes compile/pass tests
4. Report what you did clearly
```

## Configuration

Default locations:
- Persona directory: `skills/orchestrate/references/`
- Max parallel subagents: 3

Note: `--max-turns` is optional. Omit it to let subagents run until completion. Only use it when you need to limit resource usage.
