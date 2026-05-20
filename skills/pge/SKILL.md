---
name: pge
description: Planner-Generator-Executor 编排模式。通过 ai serve/send/watch/kill 控制子 agent 执行复杂任务。
---

# PGE — Dynamic Multi-Agent Orchestration

PGE 模式让编排器 agent 通过 `ai` CLI 控制多个子 agent，完成复杂任务的拆解-执行-验证循环。

## When to Use

- 复杂功能实现（多文件、多模块、有验收标准）
- 用户说 "用 PGE 模式" / "pge" / "编排模式"
- 任务需要验证闭环（实现 → 验证 → 修复循环）

**不要用于：** 简单 bug 修复、单文件改动、快速问答

## Activation

PGE 通过 `--role orchestrator` 启动：

```bash
ai run --role orchestrator "implement dark mode for the web app"
# 或
ai serve --role orchestrator --name "my-orchestrator"
```

## ⚠️ Critical: Sub-Agent Spawning Pattern

`ai serve` 是**阻塞命令**。直接调用会卡死编排器。必须用 tmux 后台运行。

### Standard Spawn-Monitor-Control Pattern

```bash
# 1. Spawn: tmux 后台启动，不阻塞编排器
tmux new-session -d -s "gen-001" "ai serve --name 'gen-001-add-auth'"

# 2. Get ID: 等 1 秒让 serve 打印 ID，然后捕获
sleep 1
RUN_ID=$(tmux capture-pane -t "gen-001" -p | head -1)

# 3. Start watch BEFORE sending task (避免丢失早期事件)
ai watch --follow --id "$RUN_ID" > /tmp/pge-gen-001.jsonl 2>&1 &
WATCH_PID=$!
sleep 0.5

# 4. Send task
ai send --id "$RUN_ID" "Read .pge/spec.md for context. Then: <task instruction>"

# 5. Wait for completion (watch --follow 在 agent 结束后自动退出)
#    timeout 根据任务大小调整: S=300s, M=600s, L=900s, XL=1200s
timeout $TIMEOUT wait $WATCH_PID

# 6. Check result
if [ $? -eq 124 ]; then
    echo "TIMEOUT: agent didn't finish in 10 minutes"
    ai kill --id "$RUN_ID"
    # → retry or report failure
fi

# 7. Cleanup tmux session
tmux kill-session -t "gen-001" 2>/dev/null
```

### Validator Spawn Pattern

Validator 使用专用 system prompt（通过 `--role validator` 嵌入 binary）：

```bash
# Spawn validator with dedicated system prompt
tmux new-session -d -s "val-001" \
  "ai serve --name 'val-001-check-auth' --role validator"
sleep 1
VAL_ID=$(tmux capture-pane -t "val-001" -p | head -1)

# Watch and send validation task
ai watch --follow --pretty --id "$VAL_ID" > /tmp/pge-val-001.log 2>&1 &
WATCH_PID=$!
sleep 0.5

ai send --id "$VAL_ID" "Generator 完成了 <task>。独立验证 .pge/spec.md 的验收标准。"

# Timeout: validation 通常比 generation 快，用任务 timeout 的一半
timeout $VAL_TIMEOUT wait $WATCH_PID

# Parse validation results
cat /tmp/pge-val-001.log

# Cleanup
tmux kill-session -t "val-001" 2>/dev/null
```

### Parallel Spawn Pattern

```bash
# Spawn multiple generators in parallel
tmux new-session -d -s "gen-001" "ai serve --name 'gen-001-backend'"
tmux new-session -d -s "gen-002" "ai serve --name 'gen-002-frontend'"

sleep 1
GEN1=$(tmux capture-pane -t "gen-001" -p | head -1)
GEN2=$(tmux capture-pane -t "gen-002" -p | head -1)

# Watch both in background
ai watch --follow --id "$GEN1" > /tmp/pge-gen-001.jsonl 2>&1 &
WATCH1=$!
ai watch --follow --id "$GEN2" > /tmp/pge-gen-002.jsonl 2>&1 &
WATCH2=$!
sleep 0.5

# Send tasks
ai send --id "$GEN1" "implement API endpoint"
ai send --id "$GEN2" "implement UI component"

# Wait both (adjust timeout per task size)
timeout $TIMEOUT wait $WATCH1
timeout $TIMEOUT wait $WATCH2

# Parse each output file...
```

### Mid-Run Intervention

```bash
# Agent 跑偏了，实时修正
ai send --id "$RUN_ID" "/steer focus only on the API, skip the frontend for now"

# Agent 彻底搞砸了，终止重来
ai kill --id "$RUN_ID"
tmux kill-session -t "gen-001"

# 重新 spawn
tmux new-session -d -s "gen-001-v2" "ai serve --name 'gen-001-v2'"
# ...
```

### Health Check

```bash
# 检查子 agent 是否还活着
ai ls --json | python3 -c "
import sys, json
runs = json.loads(sys.stdin.read())
for r in runs:
    if r['id'].startswith('$RUN_ID'[:4]):
        print(f\"{r['id']}: {r['status']}\")
"

# 看 tmux 里的 stderr
tmux capture-pane -t "gen-001" -p -S -50
```

## Execution Flow

### Phase 1: Spec Alignment

1. **Understand** — 和用户讨论需求
2. **Write spec** — 写入 `.pge/spec.md`

```markdown
# Spec: <title>

## Goal
<one sentence>

## Acceptance Criteria
- [ ] <criterion 1 — must be specific and verifiable>
- [ ] <criterion 2>

## Constraints
- <technical constraints>

## Out of Scope
- <explicitly excluded>
```

3. **Get user confirmation** — 展示 spec，等用户说 ok

### Phase 2: Task Decomposition

分析 spec，拆解成可执行的 task。写入 `.pge/tasks/NNN-<name>.md`。

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files (scope)
<expected files to modify/create — MUST be explicit for parallel conflict checking>

## Estimated Size
<S(<100) / M(100-300) / L(300-500) / XL(>500, consider splitting)>

## Dependencies
<which tasks must complete first, if any>

## Acceptance
<how to verify this task is done>
```

**Decomposition rules:**
- 每个 task 应该能由一个 coding agent 独立完成
- 如果 task 需要改 >5 个文件，考虑进一步拆分
- 标记哪些 task 可以并行（不同文件、无依赖）
- **预估行数**：每个 task 写入预估。>500 行的 task 应拆分，<80 行的 task 应合并到其他 task
- **File scope 声明**：每个 task 必须列出预期修改/创建的文件列表。并行 task 的 file scope **必须互斥**——编排器在 spawn 前检查重叠

### Phase 3: Execution Loop

```
while unchecked acceptance criteria remain:
    1. Pick next task(s)
    2. Spawn generator(s) via tmux pattern (see above)
    3. Monitor via watch --follow
    4. Parse agent_end from output
    5. Update .pge/state.md with completed files and decisions
    6. Spawn validator to check acceptance criteria
    7. Update .pge/spec.md checkboxes
    8. If failed → create fix tasks → loop
```

### Timeout Guide

任务 Estimated Size 对应 timeout：

| Size | Lines | Generator Timeout | Validator Timeout |
|------|-------|-------------------|-------------------|
| S | <100 | 300s (5min) | 150s |
| M | 100-300 | 600s (10min) | 300s |
| L | 300-500 | 900s (15min) | 450s |
| XL | >500 | 1200s (20min) | 600s |

```bash
# 根据任务大小设置
case $TASK_SIZE in
  S)  TIMEOUT=300; VAL_TIMEOUT=150 ;;
  M)  TIMEOUT=600; VAL_TIMEOUT=300 ;;
  L)  TIMEOUT=900; VAL_TIMEOUT=450 ;;
  XL) TIMEOUT=1200; VAL_TIMEOUT=600 ;;
esac
```

## ⛔ Role Separation Rules

**核心原则：Generator 说"完成了"不算数。只有 Validator 独立确认的完成才算数。**

### Generator

| 规则 | 原因 |
|------|------|
| **不许写测试文件** | 测试是验证手段，不是实现。Generator 写测试 = 自己考自己 |
| **不许修改 spec.md** | 需求不是实现者定义的 |
| Task 描述要明确说 "不需要写测试，只需 `go build` 通过" | 防止 agent 习惯性写测试 |

### Validator

Validator 是**独立裁判**，不是测试工具。它的职责是独立确认完成标准，手段由它自己决定：

| 可以做的验证方式 | 说明 |
|-----------------|------|
| 代码 review | 读实现代码，检查正确性、边界条件、错误处理 |
| 写测试并运行 | 对公共接口写测试验证行为 |
| 构建检查 | `go build`, `go vet` |
| 行为检查 | 运行程序，验证输出符合预期 |
| 结构检查 | 文件存在性、函数签名（`go doc`） |
| 任意组合 | Validator 自主选择最适合的验证方式 |

| 必须遵守的规则 | 原因 |
|---------------|------|
| **必须是独立 agent（不同 tmux session）** | 和 Generator 进程隔离 |
| **起始点是验收标准，不是 Generator 的报告** | Generator 可能说做了但没做 |
| **结论必须是自己的，不是 Generator 的自我评估** | 独立裁判 |
| **不许修改非测试的源文件** | 你不是 Generator |
| 汇报格式：✅/❌/⚠️ + 具体证据 | 让 Planner 能决策下一步 |

### Generator Task 模板

```
Read .pge/spec.md and .pge/state.md for context. Then: <具体实现要求>

要求：
- 只实现功能代码，不需要写测试文件
- 确保 go build ./... 通过
- 完成后列出修改了哪些文件和关键设计决策（用于更新 state.md）
```

### Validator Task 模板

```
Generator 完成了 <task描述>。
请独立验证 .pge/spec.md 中以下验收标准是否真正被满足：

<列出验收标准>

验证方式由你决定——代码 review、写测试、构建检查、行为验证等均可。

特别注意：
- 不仅验证功能正确性，也验证行为细节（输出格式、错误提示、边界行为等）
- 如果 spec 提到了具体的输出格式或行为，必须对照检查

对每条标准给出判断：
  ✅ <标准>: <你验证了什么，怎么验证的>
  ❌ <标准>: <哪里不满足，具体证据>
  ⚠️ <标准>: <部分满足，缺少什么>

最后输出总结：X/Y 条完全通过。
```

### Phase 4: Report

- 更新 `.pge/spec.md` 所有 checkbox 为 `[x]`
- 写最终报告到 `.pge/progress.md`
- 向用户汇报

## Error Handling

| Scenario | Detection | Action |
|----------|-----------|--------|
| Agent timeout | `timeout` exits 124 | `ai kill` → retry once with simpler task |
| Agent crash | `ai ls` shows `failed` or `killed` | Check rpc.log → retry with modified instructions |
| Agent off-track | Parse output, see wrong direction | `/steer` correction, or kill + respawn |
| Same task fails 2× | Two consecutive failures | **Stop. Report to user.** |
| Validator says not done | Criteria not all checked | Create specific fix tasks, loop |
| tmux session died | `tmux has-session` fails | Check `ai ls` for status, may need cleanup |

## Output Parsing

`ai watch --follow` 输出 JSONL。关键事件：

| Event | Meaning |
|-------|---------|
| `{"type":"response","command":"prompt","success":true}` | Command accepted |
| `{"type":"agent_start",...}` | Agent started processing |
| `{"type":"turn_end",...}` | One turn complete (tool call finished) |
| `{"type":"agent_end",...}` | Agent finished. Has `messages` array with full history |

**提取最终回复：**
```bash
grep '"agent_end"' /tmp/pge-gen-001.jsonl | tail -1 | python3 -c "
import sys, json
d = json.loads(sys.stdin.read())
stop = d.get('messages',[])[-1].get('metadata',{}).get('stopReason','')
print(f'stopReason: {stop}')
for m in d.get('messages', []):
    if m['role'] == 'assistant':
        for c in m.get('content', []):
            if c.get('type') == 'text':
                print(c['text'])
"
```

## Progress Tracking

`.pge/progress.md`（append-only）：

```markdown
## 14:30 — Started
- Spec: implement dark mode
- Acceptance criteria: 5

## 14:35 — Task 001: Create theme tokens
- Generator: a1b2c3 (tmux: gen-001)
- Status: done
- Files: src/theme.ts, src/tokens.css

## 14:42 — Validation round 1
- Criteria passed: 3/5
- Failed: toggle persistence, system preference detection
- Fix tasks created: 003, 004

## 15:00 — All criteria passed
```

## Delegation Tips

**Give WHAT (outcome), not HOW.** But include enough context for independent work.

### ✅ Good
```
"Implement JWT authentication for /api/login endpoint. Use the existing
User model in src/models/user.ts. Store tokens in http-only cookies."
```

### ❌ Bad
```
"Add auth. Look at how auth usually works."
```

## File Structure

```
.pge/
  spec.md              # Requirements + acceptance criteria
  state.md             # Current state — updated after each generator
  tasks/
    001-theme-tokens.md
    002-toggle-ui.md
    progress.md          # Execution log (append-only)
```

## Key Constraints

1. **Orchestrator never writes implementation code** — delegates to generators
2. **Each generator gets one clear task** — not a laundry list
3. **Validate against spec, not against tasks** — tasks are means, spec is the end
4. **Stop on repeated failure** — don't burn tokens retrying forever
5. **Commit after each successful generator run** — incremental progress
6. **Always use tmux to spawn** — `ai serve` is blocking, direct call freezes orchestrator

## ⛔ Mandatory Self-Check

| Assertion | Trigger | Fix |
|-----------|---------|-----|
| Direct `ai serve` call | Using `ai serve` without tmux | Wrap in tmux |
| No spec written | Starting execution without .pge/spec.md | Write spec first |
| No user confirmation | Executing without user approval | Show spec, wait for ok |
| Generator task too vague | Task description < 2 sentences | Add more context |
| Skipped validation | Task done but criteria not checked | Run validator |
| Generator wrote tests | Output includes `*_test.go` files | Kill, strip tests, respawn validator separately |
| No independent validator | Only Generator ran, no Validator agent | Must spawn separate Validator |
| Silent failure | Generator failed but didn't report | Always check exit status |
| No state.md update | Generator completed but state.md not updated | Write state.md before spawning next agent |
| Parallel file overlap | Two parallel tasks list same file | Make sequential or re-scope |
| Task too large | Estimated >500 lines | Split into smaller tasks |
| Task too small | Estimated <80 lines | Merge with adjacent task |