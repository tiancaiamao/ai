---
name: implement
description: 对话式执行实现阶段。用户给出"开始/继续实现"意图，agent 在后台完成任务分发、实现、评审与收尾。
---

# Implement — Conversation Interface

Implement skill 面向用户的交互是自然语言，不是 shell 命令。

## User Contract (对用户)

用户可以这样说：

- "开始实现这个 plan"
- "继续 implement 阶段"
- "汇报剩余任务和阻塞依赖"
- "先暂停，等我确认后再继续"
- "把失败任务重试一轮"

用户不需要手工运行 `ag` 或脚本。

---

## ⛔ MANDATORY — Pre-Flight Checklist

**在写任何代码之前，必须完成以下全部项目。缺少任何一项 = 违规。**

- [ ] **读 PLAN** — 读取 `PLAN.yml` / `PLAN.md`，理解所有任务和依赖关系
- [ ] **输出 Pre-Flight** — 向用户输出以下格式的确认：

```
📋 Implementation Pre-Flight

PLAN: [PLAN.md / PLAN.yml 路径]
任务总数: [N]
执行模式: 直接执行 / team mode
选择理由: [为什么选这个模式？如果跳过 team mode，必须说明原因]
并行度: [同时执行的任务数]
预计轮次: [几轮完成]
```

- [ ] **等待用户确认** — Pre-Flight 输出后等待用户说 "ok" / "开始" / 确认

**设计理由：**
1. 强制显式决策 — 不能默认跳到"直接干"
2. 输出即承诺 — 公开声明的计划遵守度更高
3. 用户可纠偏 — 看到计划不对可以立即干预

---

## ⛔ MANDATORY — Per-Task Ritual

**每完成一个任务后，必须执行以下三步，然后才能开始下一个任务：**

### Step 1: 持久化进度

```bash
workflow-ctl note "Task X/N done: [brief summary], tests: [pass/fail]"
```

这确保 session 中断后进度不丢失。

### Step 2: 向用户汇报

```
✅ X/N done — [task name]
   Tests: [pass/fail] | Lines changed: [delta]
🔄 Next: Task [X+1] — [task name]
```

### Step 3: Self-Check

在开始下一个任务之前，快速检查：

| 检查项 | 通过条件 |
|--------|----------|
| `go build ./...` | 零错误 |
| 受影响的包测试通过 | `go test ./affected/pkg/...` |
| 没有遗漏 TODO | 代码中无临时 HACK/FIXME |

**只有全部通过才继续下一个任务。** 如果失败，修复后再汇报。

---

## Agent Contract (对 agent)

agent 必须在后台完成以下流程：

1. 读取 `PLAN.yml` / `PLAN.md`，检查可执行性。
2. **输出 Pre-Flight Checkpoint，等待用户确认。**
3. 选择执行模式：
   1. 小任务（1-2 个，无依赖）可直接执行。
   2. 中/大任务（3+ 个，有依赖）默认 team mode（依赖感知并行）。
4. 对每个任务执行：
   1. 实现
   2. 规格符合性评审（spec compliance）
   3. 代码质量评审（quality）
5. 评审失败时进行受限重试（最多 N 轮）。
6. 任务完成后执行 Per-Task Ritual（note + 汇报 + self-check）。
7. 全部完成后输出 `impl-report.md` 与测试结论。

### 执行模式选择规则

| 任务数 | 有依赖？ | 推荐模式 | 理由 |
|--------|---------|---------|------|
| 1-2 | 无 | 直接执行 | 开销不值得 |
| 3-6 | 无/轻 | team mode 或直接执行 | 看 agent 数量限制 |
| 3+ | 有 | team mode | 依赖感知调度是核心价值 |
| 7+ | 有 | team mode（必须） | 不用 team mode = 几乎一定会乱 |

**⚠️ 反模式：** 读完全部任务后，觉得"我都能做"，然后按顺序逐个手动实现。这跳过了并行调度、评审、进度汇报等核心流程。如果任务数 ≥ 3，必须有明确的理由才能跳过 team mode。

---

## Team Mode（内部执行细节，不对用户暴露）

Team mode 使用 `ag task` + `ag agent` 组合实现，**不是** `ag team`（该命令不存在）。

### 核心流程

```
1. ag task import-plan PLAN.yml        # 导入任务 + 依赖关系
2. 波次循环:
   while 有未完成任务:
     a. ag task list --status pending   # 查看可执行任务
     b. ag task next --as worker-N      # 领取下一个无阻塞任务
     c. ag agent spawn worker-N --input "$(task description)"
     d. ag agent wait worker-N --timeout 600
     e. 检查结果:
        - 成功 → ag task done <task-id> --summary "$(ag agent output worker-N)"
        - 失败 → ag task fail <task-id> --error "..." --retryable
     f. ag agent rm worker-N             # 清理已完成的 agent
3. 全部完成 → 生成 impl-report.md
```

### 关键命令对照

| 目的 | 命令 |
|------|------|
| 导入计划 | `ag task import-plan PLAN.yml` |
| 查看任务状态 | `ag task list [--status pending\|claimed\|done\|failed]` |
| 领取下一个可执行任务 | `ag task next --as worker-1` |
| 手动认领指定任务 | `ag task claim <id> --as worker-1` |
| 标记完成 | `ag task done <id> --summary "..."` |
| 标记失败 | `ag task fail <id> --error "..." --retryable` |
| 查看任务详情 | `ag task show <id>` |
| 管理依赖 | `ag task dep add <id> <dep-id>` / `ag task dep ls <id>` |
| 生成 agent | `ag agent spawn <id> --input "..."` |
| 等待 agent 完成 | `ag agent wait <id> --timeout 600` |
| 获取 agent 输出 | `ag agent output <id>` |
| 清理 agent | `ag agent rm <id>` |

### 并发限制

⚠️ **同时存活的 agent（含主 agent 自身）≤ 3**。即最多同时 spawn 2 个子 agent。

策略：
- 按依赖拓扑分波次执行
- 每波最多 2 个 agent
- 前一波全部完成后（`ag agent wait` + `ag agent rm`），再 spawn 下一波
- 如果任务多但无依赖，依然要分批，每批 ≤ 2 个

### 错误处理

- agent 失败：`ag task fail` 标记，retryable=true 的任务可在下一波重试
- agent 卡住：`ag agent kill` 强制终止，然后 `ag task fail`
- 重试上限：每个任务最多重试 2 次，超过则标记为永久失败并汇报给用户

这些属于内部实现细节，不应转嫁给用户手工执行。

---

## Progress Reporting

每完成一个任务后向用户汇报（不是做完一大段才汇报）：

1. ✅/❌ 已完成任务数 / 总任务数
2. 🔄 当前活跃任务
3. ⏸️ 阻塞任务与依赖原因
4. ❗ 失败/重试情况
5. 📋 下一步计划

---

## ⛔ MANDATORY — Self-Check Assertions

**在实现过程中的任何时刻，如果以下任一条件为真，必须立即停下：**

| 断言 | 触发条件 | 修正动作 |
|------|----------|----------|
| Pre-Flight 未输出 | 开始写代码但没输出 Pre-Flight | 停下，先输出 Pre-Flight |
| 进度未记录 | 完成一个任务但没调 `workflow-ctl note` | 补上 note，以后每个任务都记 |
| 手动实现 ≥3 任务 | 逐个手动实现 ≥3 个任务且没用 team mode | 解释为什么跳过 team mode，如无充分理由则切换 |
| 跳过测试 | 任务完成但没跑受影响的测试 | 停下，跑测试，通过后再继续 |
| 一口气做完 | 连续完成 ≥2 任务没向用户汇报 | 立即汇报已完成任务的状态 |

**这些断言是对 agent 的硬约束，不是建议。**

---

## Conversation-First Rules

1. 不把 CLI 参数当作用户主交互层。
2. 不要求用户自己执行命令来推进任务。
3. 出现失败时，agent 先自恢复（重试、降并行、回退），再向用户汇报决策。
4. 仅当用户明确要求时，才展示底层命令与脚本细节。