# PGE Skill — Planner-Generator-Executor Orchestration

## What

PGE 模式让一个 agent（编排器）通过 `ai serve`/`ai send`/`ai watch`/`ai kill` 控制多个子 agent，实现复杂任务的拆解-执行-验证循环。

**触发方式：**
```bash
ai run --peg "implement dark mode for the web app"
ai serve --peg --name "orchestrator"
```

`--peg` 将 system prompt 替换为编排器模板（`pkg/prompt/orchestrator.md`），描述了子 agent 控制协议。

## Why

当前 `implement` skill 的 `ag task run` 是静态 DAG 调度：
- task 在执行前全部定义好
- scheduler 按固定依赖推进
- 无法根据中间结果调整计划

PGE 的编排器是 **动态的**：
- 根据 spec 拆出第一批 task
- 执行后看结果，决定下一批
- 验证失败时重新规划
- 直到所有验收标准通过

## Architecture

```
User
  │
  ▼
┌─────────────────────────┐
│  Orchestrator (ai --peg) │  ← planner + coordinator
│  System: orchestrator.md │
└────────┬────────────────┘
         │ ai serve / ai send / ai watch / ai kill
         │
    ┌────┴────┐
    ▼         ▼
┌───────┐ ┌─────────┐
│ Gen 1 │ │ Gen 2   │  ← normal ai serve (default prompt)
└───┬───┘ └────┬────┘
    │          │
    ▼          ▼
┌─────────────────┐
│   Validator     │  ← normal ai serve (default prompt)
└─────────────────┘
```

**三个角色：**

| Role | Who | What |
|------|-----|------|
| **Planner** | Orchestrator itself | 分析需求，拆解 task，验证结果 |
| **Generator** | Sub-agent (`ai serve`) | 写代码，执行具体 task |
| **Validator** | Sub-agent (`ai serve`) | 验证代码是否符合 spec |

Planner 不写代码。Generator 和 Validator 用默认 coding agent prompt。

## Sub-Agent Control Protocol

编排器通过 bash 工具调用 `ai` CLI：

```bash
# 启动子 agent
RUN_ID=$(ai serve --name "gen-001-add-auth")

# 发送任务
ai send --id $RUN_ID "implement JWT authentication for /api/login"

# 实时监控（JSONL 流，agent 结束后自动退出）
ai watch --follow --id $RUN_ID

# 中途调整方向
ai send --id $RUN_ID "/steer also add rate limiting"

# 终止
ai kill --id $RUN_ID
```

### Event Parsing

`ai watch --follow` 输出 JSONL。关键事件：

| Event | Meaning |
|-------|---------|
| `agent_start` | Agent started |
| `message_update` | Streaming delta (look for `assistantMessageEvent.type`) |
| `turn_end` | Turn complete, has final message + usage |
| `agent_end` | Agent finished, has full message history |

Generator 完成的标志：`agent_end` + `stopReason == "stop"`。

### Output Extraction

从 `agent_end` 事件中提取 assistant 最终回复：

```bash
ai watch --follow --id $RUN_ID | while read -r line; do
  TYPE=$(echo "$line" | python3 -c "import sys,json; print(json.loads(sys.stdin.read()).get('type',''))" 2>/dev/null)
  if [ "$TYPE" = "agent_end" ]; then
    echo "$line" | python3 -c "
import sys,json
d=json.loads(sys.stdin.read())
for m in d.get('messages',[]):
  if m['role']=='assistant':
    for c in m.get('content',[]):
      if c.get('type')=='text':
        print(c['text'])
" 2>/dev/null
  fi
done
```

## Execution Flow

### Phase 1: Spec (user + orchestrator)

1. 用户描述需求
2. 编排器写入 `.pge/spec.md`：
   ```markdown
   # Spec: <title>
   
   ## Goal
   <one sentence>
   
   ## Acceptance Criteria
   - [ ] <criterion 1>
   - [ ] <criterion 2>
   
   ## Constraints
   - <constraint>
   
   ## Out of Scope
   - <excluded>
   ```
3. 用户确认 spec

### Phase 2: Execute (autonomous)

编排器循环：

```
while spec has unchecked acceptance criteria:
    1. Pick next work item (may parallelize)
    2. Create task file → .pge/tasks/NNN-<name>.md
    3. Spawn generator: ai serve --name "gen-NNN"
    4. Send task + watch --follow
    5. Parse result, check if task done
    6. When enough tasks done → spawn validator
    7. Validator checks acceptance criteria
    8. Update .pge/spec.md checkboxes
    9. If criteria fail → create fix tasks, loop
```

### Phase 3: Report

- Summary of what was done
- Final spec with checkmarks
- Any deviations or open issues

## Task File Format

`.pge/tasks/NNN-<name>.md`:
```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files
<expected files to modify>

## Status
pending | running | done | failed

## Result
<filled after generator completes>
```

## Progress File

`.pge/progress.md` — append-only log:
```markdown
## [timestamp] Task NNN started
- Generator: <run ID>
- Input: <task summary>

## [timestamp] Task NNN completed
- Result: <summary>
- Files changed: <list>

## [timestamp] Validation run
- Spec criteria checked: N/M passed
```

## Parallelization Rules

- **Parallel**: Tasks touch different files, no data dependency
- **Sequential**: Task B needs output from Task A

编排器自行判断并发度。简单规则：同目录的文件串行，不同目录可并行。

## Error Handling

| Scenario | Action |
|----------|--------|
| Generator failed (exit error) | Retry once, then report |
| Same task fails 3 times | Pause, report to user |
| Validator says criteria not met | Create fix tasks, loop |
| Spec changed by user mid-run | Re-evaluate, mark affected tasks |
| Generator timeout | Kill, retry once |

## File Layout

```
.pge/
  spec.md              # Requirements + acceptance criteria
  tasks/
    001-add-auth.md
    002-add-ratelimit.md
  progress.md          # Append-only execution log
```

## Key Differences from `implement` Skill

| | implement (ag task) | PGE (ai --peg) |
|---|---|---|
| Task definition | Static DAG (tasks.md) | Dynamic (planner decides on-the-fly) |
| Scheduling | Fixed dependency order | Adaptive based on results |
| Validation | Per-task review | Spec-level acceptance criteria |
| Infrastructure | ag CLI + scheduler | ai CLI + orchestrator |
| Failure recovery | Retry ×3, manual after | Planner adjusts plan dynamically |
| Human involvement | Setup only | Spec approval + error escalation |

## Non-Goals

- Not replacing simple tasks (`ai run "fix this bug"` still works)
- Not building a framework/library — it's a skill (SKILL.md)
- Not multi-user/multi-tenant
- Not modifying `ai` CLI itself (PGE is pure skill layer)