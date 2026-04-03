# Context Snapshot Architecture Reference

Session-analyzer 分析会话时，需要理解被分析对象（ai agent）的运行架构。本文档描述当前的 Context Snapshot 架构。

## 核心概念

### 1. Trampoline 模式

Agent 在两种模式间交替运行（实现在 `pkg/agent/turn.go`）：

```
ModeNormal（任务执行）
    ↓ 触发条件满足
ModeContextMgmt（上下文管理）
    ↓ 管理完成
ModeNormal（任务执行）
    ↓ ...
```

- **ModeNormal**：执行用户任务，有完整的工具集
- **ModeContextMgmt**：管理上下文，只有 4 个专用工具

### 2. ContextSnapshot（内存状态）

```
ContextSnapshot {
    LLMContext      string          // LLM 维护的 markdown 格式上下文
    RecentMessages  []AgentMessage  // 最近的消息列表
    AgentState      AgentState      // 系统维护的元数据
}
```

- **LLMContext**：agent 自己管理的内容（任务状态、决策、关键信息），存储为 markdown
- **RecentMessages**：最近的对话消息（经过截断/压缩后的）
- **AgentState**：token 使用、触发追踪等元数据

### 3. Journal（持久化）

`messages.jsonl` 是唯一数据源，append-only。支持 3 种 entry：

| type | 说明 |
|------|------|
| `message` | 标准消息（user/assistant/toolResult） |
| `truncate` | 截断事件（tool_call_id, turn, timestamp） |
| `compact` | 压缩事件（summary, kept_message_count, turn） |

### 4. Checkpoint（快照）

定期保存的 snapshot 快照，存储在 `checkpoints/checkpoint_NNNNN/`：

- `llm_context.txt` — 当时的 LLM 记忆内容
- `agent_state.json` — 当时的 agent 状态

**重建**：从最新的 checkpoint + 之后 journal replay = 完整当前状态。

## Trigger 系统

### 触发条件（来自 `pkg/context/trigger_config.go`）

| 条件 | 阈值 | 紧急级别 |
|------|------|----------|
| Token 使用率 ≥ 70% | TokenUrgent | UrgencyUrgent |
| Token 使用率 ≥ 50% | TokenHigh | UrgencyNormal |
| Token 使用率 ≥ 30% | TokenMedium | UrgencyPeriodic |
| Token 使用率 ≥ 20% | TokenLow | — |
| Stale output ≥ 15 | StaleOutputThreshold | UrgencyNormal |

### 紧急级别与间隔

| 紧急级别 | 行为 | 工具调用间隔要求 |
|----------|------|------------------|
| UrgencyUrgent | 无视间隔，立即触发 | 0 |
| UrgencyNormal | 需满足间隔 | 30 次工具调用 |
| UrgencyPeriodic | 周期性检查 | 15 次工具调用 |
| UrgencySkip | 不触发 | — |

### 触发追踪字段（agent_state.json）

- `LastTriggerTurn`：上次触发的 turn
- `TurnsSinceLastTrigger`：距上次触发的 turn 数
- `ToolCallsSinceLastTrigger`：距上次触发的工具调用数
- `LastLLMContextUpdate`：上次更新 LLM context 的 turn
- `LastCheckpoint`：上次创建 checkpoint 的 turn

## Context Management 工具（ModeContextMgmt 下可用）

### 1. `update_llm_context`
- **作用**：更新 snapshot.LLMContext（agent 的记忆）
- **参数**：`content`（markdown 文本）
- **分析价值**：评估 agent 保留了什么信息、是否遗漏关键内容

### 2. `truncate_messages`
- **作用**：按 tool_call_id 截断指定工具输出
- **参数**：`tool_call_ids`（要截断的 ID 列表）
- **效果**：在 journal 中记录 truncate event，从 RecentMessages 中移除对应内容
- **限制**：最后 30 条消息受保护，不可截断
- **分析价值**：截断选择是否合理？是否截掉了重要内容？

### 3. `compact_messages`
- **作用**：触发消息压缩
- **流程**：agent 发信号 → 系统调用 LLM 生成摘要 → 替换 RecentMessages
- **效果**：在 journal 中记录 compact event（含完整 summary）
- **分析价值**：summary 质量、压缩后 agent 是否行为退化

### 4. `no_action`
- **作用**：表示上下文健康，不需要操作
- **效果**：重置触发追踪字段
- **分析价值**：高频 no_action 意味着触发阈值可能需要调整

## 架构关键约束

1. **Protected Region**：最后 30 条消息不可被 truncate
2. **Duplicate Detection**：连续相同工具调用（含相同参数）最多 7 次
3. **Concurrency**：snapshotMu 保护所有 snapshot 访问，Journal 有独立 mutex
4. **Token Estimation**：ApproxBytesPerToken = 4（用于触发判断）
5. **Compaction**：实际由 `pkg/compact/compact.go` 执行，通过 LLM 生成摘要

## 分析中的模式识别

### 正常的 Context Management 流程

```
Turn N:   ModeNormal 执行任务
Turn N+1: 触发检查 → 满足条件 → 进入 ModeContextMgmt
          → 调用 update_llm_context（更新记忆）
          → 调用 truncate_messages（截断大输出）
          → 返回 ModeNormal
Turn N+2: ModeNormal 继续执行
```

### 异常信号

| 信号 | 含义 |
|------|------|
| 连续触发 context management | 截断/压缩不够有效 |
| truncate 后立即又触发 | 截断了错误的内容或输出持续增长 |
| compact 后 agent 迷失 | summary 质量差，丢失了关键上下文 |
| 高频 no_action | 触发阈值过低，浪费时间 |
| UrgencyUrgent 频繁出现 | token 预算不足或任务过于复杂 |
| 单个工具输出反复被 truncate | 该工具输出太大，可能需要分页或限制 |

## 相关代码文件

| 文件 | 职责 |
|------|------|
| `pkg/context/snapshot.go` | ContextSnapshot 结构 |
| `pkg/context/message.go` | AgentMessage, ContentBlock 类型 |
| `pkg/context/agent_state.go` | AgentState 元数据 |
| `pkg/context/trigger.go` | TriggerChecker |
| `pkg/context/trigger_config.go` | 触发阈值常量 |
| `pkg/context/journal.go` | Journal entry 类型定义 |
| `pkg/context/checkpoint.go` | Checkpoint 创建/保存/加载 |
| `pkg/context/reconstruction.go` | 从 checkpoint + journal 重建状态 |
| `pkg/context/stale.go` | Stale output 计算 |
| `pkg/context/render.go` | 模式特定渲染 |
| `pkg/agent/turn.go` | Trampoline 模式（ModeNormal ↔ ModeContextMgmt） |
| `pkg/agent/loop_normal.go` | ModeNormal 主循环 |
| `pkg/agent/loop_context_mgmt.go` | ModeContextMgmt 执行 |
| `pkg/tools/context_mgmt/` | 4 个上下文管理工具实现 |
| `pkg/compact/compact.go` | 压缩实现 |
