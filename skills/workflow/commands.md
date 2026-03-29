# Workflow Commands

User-friendly frontend for orchestrate CLI.

## Architecture

```
User Command
    ↓
Workflow Skill (Frontend)
  - Parse user input
  - Friendly commands
  - Template selection
    ↓
Orchestrate CLI (Backend)
  - Task scheduling
  - State management
  - Worker execution
```

## Commands

### `/workflow start <template> [description]`

Start a new workflow.

```bash
# Examples
/workflow start bugfix "Fix login timeout"
/workflow start feature "Add user authentication"
/workflow start hotfix "Emergency: prod crash"

# Auto-detect template from description
/workflow start "Fix broken API endpoint"  # → bugfix
/workflow start "Research new framework"   # → spike
```

**Implementation:**
```bash
# 1. Ensure orchestrate binary exists
ensure_orchestrate_binary

# 2. Resolve template
template="${1:-auto}"
description="$2"

if [ "$template" = "auto" ]; then
  template=$(auto_detect "$description")
fi

# 3. Call orchestrate CLI
~/.ai/skills/workflow/orchestrate/bin/orchestrate start \
  --workflow "$template" \
  --name "$description" \
  --project "$(basename $PWD)"
```

### `/workflow status`

Show current workflow state.

```bash
# Ensure orchestrate binary exists
ensure_orchestrate_binary

# Show all workflows
~/.ai/skills/workflow/orchestrate/bin/orchestrate status

# Show specific workflow
~/.ai/skills/workflow/orchestrate/bin/orchestrate status --project myproject
```

### `/workflow stop`

Stop running workflow.

```bash
# Ensure orchestrate binary exists
ensure_orchestrate_binary

~/.ai/skills/workflow/orchestrate/bin/orchestrate stop
```

### `/workflow logs`

Show workflow logs.

```bash
# Ensure orchestrate binary exists
ensure_orchestrate_binary

# All logs
~/.ai/skills/workflow/orchestrate/bin/orchestrate logs

# Specific phase
~/.ai/skills/workflow/orchestrate/bin/orchestrate logs --phase diagnose
```

### `/workflow approve <task-id>`

Approve a pending review.

```bash
~/.ai/skills/workflow/orchestrate/bin/orchestrate approve "$task-id"
```

### `/workflow templates [info <name>]`

List or show template details.

```bash
# List all templates
~/.ai/skills/workflow/orchestrate/bin/orchestrate templates

# Show template info
~/.ai/skills/workflow/orchestrate/bin/orchestrate templates info bugfix
```

## Template Resolution

### Aliases

```bash
resolve_by_name() {
  local name="$1"
  case "$name" in
    bug|fix)      echo "bugfix" ;;
    hot)          echo "hotfix" ;;
    feat|feature) echo "feature" ;;
    research)     echo "spike" ;;
    *)            echo "$name" ;;
  esac
}
```

### Auto-detect

```bash
auto_detect() {
  local desc="$1"
  local lower=$(echo "$desc" | tr '[:upper:]' '[:lower:]')

  if echo "$lower" | grep -qE 'bug|issue|error|wrong|broken|fix'; then
    echo "bugfix"
  elif echo "$lower" | grep -qE 'hot|emergency|prod|urgent'; then
    echo "hotfix"
  elif echo "$lower" | grep -qE 'security|vulnerability|exploit'; then
    echo "security"
  elif echo "$lower" | grep -qE 'research|explore|spike|investigate'; then
    echo "spike"
  elif echo "$lower" | grep -qE 'refactor|restructure|cleanup|technical.?debt'; then
    echo "refactor"
  else
    echo "feature"
  fi
}
```

## Available Templates

| Template | Description | Phases |
|----------|-------------|--------|
| `bugfix` | Fix bugs with root-cause analysis | reproduce, diagnose, fix, verify |
| `hotfix` | Emergency production fix | reproduce, diagnose, fix, verify, ship |
| `feature` | Build new features | design, implement, test, review |
| `refactor` | Improve code structure | analyze, refactor, verify |
| `spike` | Research and explore | research, document, present |

## Workflow Execution Flow

```
1. User: /workflow start bugfix "Fix login bug"
   ↓
2. Resolve template → "bugfix"
   ↓
3. Call: orchestrate start --workflow bugfix --name "Fix login bug"
   ↓
4. Orchestrate loads: templates/bugfix.yaml
   ↓
5. Creates tasks:
   - reproduce (pending, no deps)
   - diagnose (blocked by: reproduce)
   - fix (blocked by: diagnose)
   - verify (blocked by: fix)
   ↓
6. Runtime starts monitor loop
   ↓
7. reconcile() schedules workers
   ↓
8. Workers execute in tmux sessions
   ↓
9. Workflow completes when all tasks done
```

## State Storage

All state is managed by orchestrate CLI:

```
.ai/team/
├── config.json       # Team configuration
├── state.json        # Current runtime state
├── tasks/            # Individual task states
├── workers/          # Worker execution directories
├── logs/             # Execution logs
└── reviews/          # Review requests
```

## Integration with Orchestrate

### Workflow Skill (Frontend)

**Responsibilities:**
- User-friendly commands
- Template selection and aliasing
- Parameter parsing
- Human-readable output

### Orchestrate CLI (Backend)

**Responsibilities:**
- Task scheduling and dependency resolution
- Worker pool management
- State persistence
- Tmux session management
- Retry and recovery logic

### API Contract

```bash
# Start workflow
orchestrate start --workflow <template> --name <name>

# Show status
orchestrate status

# Stop workflow
orchestrate stop

# Show logs
orchestrate logs [--phase <phase>]

# Approve review
orchestrate approve <task-id>

# List templates
orchestrate templates [info <template>]
```

## Error Handling

```bash
# Ensure orchestrate binary exists before use
ensure_orchestrate_binary() {
  local script_dir="$HOME/.ai/skills/workflow/orchestrate"
  local binary="$script_dir/bin/orchestrate"

  if [ ! -f "$binary" ]; then
    echo "🔨 Building orchestrate binary..."
    cd "$script_dir"
    go build -o bin/orchestrate ./cmd/main.go
    echo "✅ Build complete"
  fi
}

# Wrapper function
run_orchestrate() {
  # Ensure binary exists
  ensure_orchestrate_binary

  local output
  output=$(~/.ai/skills/workflow/orchestrate/bin/orchestrate "$@" 2>&1)
  local exit_code=$?

  if [ $exit_code -ne 0 ]; then
    echo "❌ Orchestrate error: $output"
    return $exit_code
  fi

  echo "$output"
  return 0
}
```

## Custom Templates

To add custom templates:

```bash
# 1. Create template file
~/.ai/skills/workflow/orchestrate/templates/my-template.yaml

# 2. Use it
/workflow start my-template "My custom workflow"
```

Template format:
```yaml
name: My Custom Workflow
description: Custom workflow for my team

phases:
  - id: phase1
    subject: "Phase One"
    description: "Do phase one work"
    blocked_by: []

  - id: phase2
    subject: "Phase Two"
    description: "Do phase two work"
    blocked_by: [phase1]

human_loop:
  checkpoints: [phase2]
```