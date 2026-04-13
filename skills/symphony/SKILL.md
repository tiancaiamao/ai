---
name: symphony
description: Interact with Symphony task orchestration system.
  Create tasks, check status, manage workflow automation.
  Use for AI-driven bug fixes, features, and automation.
---

# Symphony - Task Orchestration Skill

## What is Symphony?

Symphony is an **AI-powered task orchestration system** that:
- 📋 Manages tasks on a Kanban board
- 🤖 Automatically executes tasks using AI agents
- 🔄 Handles state transitions (Inbox → Todo → Running → Done/Failed)
- 🔔 Triggers hooks for automation (git clone, test, PR, notify)
- 📂 Isolates each task in its own workspace

## Commands

| Command | Description |
|---------|-------------|
| `/symphony task create <title>` | Create a new task |
| `/symphony task list [state]` | List tasks (all or by state) |
| `/symphony task show <id>` | Show task details |
| `/symphony task move <id> <state>` | Move task to new state |
| `/symphony task retry <id>` | Retry a failed task |
| `/symphony status` | Show scheduler status |
| `/symphony board` | Open Kanban board in browser |

## Quick Examples

### Create a Bug Fix Task

```bash
/symphony task create "Fix login timeout after 30s"

# With description
/symphony task create "Fix SSO login" --desc "Users report 500 error when logging in with SSO"
```

### Create a Feature Task

```bash
/symphony task create "Add dark mode support" --desc "Implement system-wide dark mode with toggle"

# Move to Todo to trigger execution
/symphony task move <task-id> todo
```

### Monitor Progress

```bash
# List all tasks
/symphony task list

# List only running tasks
/symphony task list running

# Check scheduler status
/symphony status

# Open Kanban board
/symphony board
```

## Task Workflow

```
┌─────────┐   drag/   ┌─────────┐   auto    ┌─────────┐   done   ┌─────────┐
│  Inbox  │ ─────────→│   Todo  │ ─────────→│ Running │ ───────→│  Done   │
└─────────┘   API     └─────────┘  scheduler └─────────┘  agent  └─────────┘
     ↑                                                       │
     │                                                       │
     └───────────────────────────────────────────────────────┘
                        failed (retryable)
```

### States

| State | Description |
|-------|-------------|
| `inbox` | New tasks, not yet scheduled |
| `todo` | Ready to be picked up by scheduler |
| `running` | AI agent is working on it |
| `done` | Completed successfully |
| `failed` | Failed (may retry automatically) |

## Automation Hooks

Symphony can automate the entire workflow using hooks in `~/.symphony/config.yaml`:

```yaml
hooks:
  # After task creation: setup workspace
  after_create: |
    cd {{.Workspace}}
    git clone https://github.com/yourorg/yourrepo .
    git checkout -b {{.TaskID}}
  
  # Before AI starts: install dependencies
  before_run: |
    cd {{.Workspace}}
    npm install  # or mix deps.get, pip install
  
  # After AI finishes: test + review + PR
  after_run: |
    cd {{.Workspace}}
    npm test
    ai review --output review.md
    git add . && git commit -m "{{.TaskTitle}}"
    git push origin {{.TaskID}}
    gh pr create --title "{{.TaskTitle}}" --body-file review.md
    curl -X POST $SLACK_WEBHOOK -d "{\"text\": \"✅ {{.TaskTitle}}\"}"
  
  # Before cleanup: auto-merge
  before_remove: |
    cd {{.Workspace}}
    gh pr merge --squash --delete-branch
```

## Task Types

### Bug Fix

```bash
/symphony task create "[BUG] Fix login timeout" --desc "
Steps to reproduce:
1. Open app
2. Wait 30 seconds
3. Try to login
Expected: Login succeeds
Actual: Timeout error
"
```

### Feature

```bash
/symphony task create "[FEATURE] Add dark mode" --desc "
Requirements:
- System-wide dark mode toggle
- Persist preference in localStorage
- Support auto (system preference)
- Apply to all components
"
```

### Refactor

```bash
/symphony task create "[REFACTOR] Extract auth logic" --desc "
Move authentication logic from App.jsx to:
- hooks/useAuth.js
- contexts/AuthContext.jsx
- services/auth.js
"
```

### Spike/Research

```bash
/symphony task create "[SPIKE] Evaluate GraphQL vs REST" --desc "
Research and document:
1. Performance comparison
2. Developer experience
3. Migration effort
4. Recommendation
"
```

## Integration with Other Skills

| Skill | When to Use |
|-------|-------------|
| `workflow` | For complex multi-step work WITHIN a task |
| `ag` | When task needs parallel execution or multi-agent orchestration |
| `review` | To review task code before completion |
| `tmux` | For long-running task execution |
| `github` | To create/manage PRs from tasks |

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Symphony Server (localhost:8081)                       │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐            │
│  │  Kanban  │  │   API    │  │Scheduler │            │
│  │   UI     │  │ /api/*   │  │ (30s)    │            │
│  └──────────┘  └──────────┘  └──────────┘            │
│                      ↓                                  │
│                ┌──────────┐                            │
│                │ AI Agent │                            │
│                │  (ai)    │                            │
│                └──────────┘                            │
│                      ↓                                  │
│  ┌──────────────────────────────────────────────────┐ │
│  │ Workspaces (~/.symphony/workspaces/)             │ │
│  │  ├─ task-abc123/  (running)                      │ │
│  │  ├─ task-def456/  (done)                         │ │
│  │  └─ task-xyz789/  (todo)                         │ │
│  └──────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## Configuration

Config file: `~/.symphony/config.yaml`

```yaml
# Workspace settings
workspace:
  root: ~/.symphony/workspaces

# Agent settings
agent:
  kind: ai
  command: ~/project/ai/bin/ai
  args: ["--mode", "rpc"]
  max_concurrent_agents: 3
  max_turns: 100
  env:
    OPENAI_API_KEY: ${OPENAI_API_KEY}

# Hooks for automation
hooks:
  after_create: ""
  before_run: ""
  after_run: ""
  before_remove: ""
  timeout_ms: 60000

# Polling interval
polling:
  interval_ms: 30000
```

## Best Practices

1. **Start with clear descriptions** - Help AI understand the task
2. **Use templates** - Bug, Feature, Refactor, Spike prefixes
3. **Check status first** - `/symphony status` before creating new tasks
4. **Review running tasks** - Monitor AI progress on Kanban board
5. **Configure hooks** - Automate repetitive work (test, PR, notify)
6. **Set limits** - `max_concurrent_agents` to avoid overwhelming system

## Example: Full Automation Flow

```bash
# 1. Create task
/symphony task create "[BUG] Fix login timeout" --desc "
Users report timeout after 30 seconds.
Reproduce: Login → Wait 30s → Timeout error
Expected: Login should succeed within 5s
"

# 2. Check it was created
/symphony task list inbox

# 3. Move to Todo (triggers scheduler)
/symphony task move <task-id> todo

# 4. Monitor progress
/symphony status
# or open board
/symphony board

# 5. AI will automatically:
#    - Clone repo
#    - Investigate bug
#    - Fix the issue
#    - Run tests
#    - Create PR
#    - Send Slack notification

# 6. You review PR and merge
# 7. Move task to Done
/symphony task move <task-id> done

# 8. Cleanup (auto-merge if configured)
/symphony task move <task-id> done
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Task stuck in running | Check `/symphony status`, agent logs |
| Agent not starting | Verify `agent.command` path, check logs |
| Hooks not working | Check hook syntax, run manually |
| Too many concurrent tasks | Reduce `max_concurrent_agents` in config |
| Task failed | Check error message, use `/symphony task retry <id>` |

## API Reference

If you need direct API access:

```bash
# Create task
curl -X POST http://localhost:8081/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"title": "Fix bug", "description": "..."}'

# List tasks
curl http://localhost:8081/api/tasks

# Update task
curl -X PUT http://localhost:8081/api/tasks/<id> \
  -H "Content-Type: application/json" \
  -d '{"state": "todo"}'

# Get status
curl http://localhost:8081/api/status
```

## Anti-Patterns

❌ **Don't create tasks without descriptions** - AI needs context
❌ **Don't skip Todo state** - Scheduler needs to pick it up
❌ **Don't ignore failed tasks** - Check error, fix, or delete
❌ **Don't run too many concurrent** - System may become unstable
❌ **Don't forget to check status** - Monitor AI progress
