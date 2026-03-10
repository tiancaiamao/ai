# LLM Context - 设计和实现文档

## 1. 设计目的

### 1.1 核心问题

Agent 在长期对话中面临以下挑战：
- **上下文限制**：LLM 上下文窗口有限，无法承载完整对话历史
- **信息丢失**：随着对话增长，早期重要信息逐渐被遗忘
- **冗余信息**：大量历史消息包含重复、过时或无关内容
- **状态管理困难**：缺乏明确机制来跟踪"当前任务状态"和"下一步行动"

### 1.2 解决方案

**LLM Context** 是一个持久化的状态跟踪机制，用于：
- 存储和更新关键信息（任务状态、决策、上下文）
- 替代对完整历史消息的依赖
- 提供轻量级、可快速访问的状态摘要
- 支持跨会话的状态连续性
- 通过 hint 机制让 AI 主动控制上下文管理

### 1.3 设计原则

1. **不依赖历史消息**：主动维护，而非被动依赖历史
2. **单一事实源**：所有关键状态信息在此集中管理
3. **增量更新**：随着对话进展逐步更新，而非重写
4. **轻量化**：只保留必要信息，避免冗余
5. **AI 自主控制**：通过 hint 机制让 AI 主动管理上下文

---

## 2. 当前实现

### 2.1 存储位置

```
/Users/genius/.ai/sessions/--<cwd>--/<session-id>/llm-context/
├── overview.md                  # 核心状态摘要（必须维护）
├── truncate-compact-hint.md     # 上下文管理 hint 文件（AI 写入，系统处理）
└── detail/                      # 详细文档存档目录
    ├── session-summary.md        # 压缩后的历史对话
    ├── design-doc.md             # 设计决策文档
    └── ...
```

**关键说明**：
- `<cwd>` 使用双连字符编码：`--<工作目录路径>--`
- 例如：`--Users-genius-project-ai--` 表示 `/Users/genius/project/ai`
- 每个会话有独立的 llm context 目录

### 2.2 overview.md 结构

```markdown
# LLM Context

## 核心设计原则
（设计原则和关键理念）

## 项目上下文
（项目特定信息：技术栈、关键路径、命令）

## 已知问题
（当前已知的问题和限制）

## 当前会话
（会话状态、Session ID、下一步行动）
```

### 2.3 当前行为

**Agent 的维护责任**：
- ✅ 在任务进展时更新状态
- ✅ 在做出决策时记录原因
- ✅ 在上下文变化时更新项目信息
- ✅ 执行 context 压缩后更新 summary
- ✅ 根据 runtime_state 主动写入 hint 文件管理上下文

**系统自动处理**：
- 每次轮次开始前检查并处理 `truncate-compact-hint.md`
- `overview.md` 的内容在 compact 后自动注入到系统提示中（恢复记忆）
- 正常请求时不注入，依赖 `llm_context_update` 工具的 output 留在上下文中
- detail/ 目录的内容不会自动注入（需主动读取）

---

## 3. Truncate-Compact Hint 机制

### 3.1 设计目的

让 AI 通过写入 hint 文件主动控制上下文大小，无需依赖手动工具调用。

**关键特性**：
- AI 自主决策何时压缩上下文
- 每次轮次开始前处理（确保 LLM 请求时使用压缩后的上下文）
- 支持两种操作：TRUNCATE（删除过期工具输出）和 COMPACT（完整对话压缩）
- 支持概率性 COMPACT，避免频繁压缩

### 3.2 Hint 文件格式

**存储位置**：`llm-context/truncate-compact-hint.md`

**示例**：
```markdown
## TRUNCATE
call_abc123, call_def456, call_ghi789

## COMPACT
target: all
confidence: 80%
```

**配置项**：
- **TRUNCATE section**：
  - 格式：`call_id1, call_id2, call_id3`（逗号分隔或换行）
  - 作用：删除指定 tool_call_id 的工具输出内容
  - 时机：立即生效，在下一个 LLM 请求前

- **COMPACT section**：
  - `target`: 压缩目标
    - `all` - 压缩所有消息
    - `conversation` - 仅压缩对话历史（用户/助手消息）
    - `tools` - 仅压缩工具输出
  - `strategy`: 压缩方式
    - 留空 - 使用默认策略
    - `archive` - 归档到 detail/ 目录
  - `keep_recent`: 保留最近 N 条（可选，默认由系统决定）
  - `confidence`: 执行概率（可选）
    - 格式：`80%` 或 `0.8`
    - 作用：系统生成随机数 roll ∈ [0, 1)，如果 `roll ≤ confidence` 则执行
    - 默认：100%（未设置时总是执行）
  - `confidence_range`: 概率范围（可选）
    - 格式：`70%-90%`
    - 作用：最终使用平均值 `(min + max) / 2` 作为 confidence
    - 示例：`confidence_range: 70%-90%` 等价于 `confidence: 80%`

### 3.3 Hint 处理流程

```go
// 在每次轮次开始前（runInnerLoop 的 for 循环内）
turnCount++

// 检查 LLMContext 是否可用
llmContextAvailable := agentCtx.LLMContext != nil
if llmContextAvailable {
    hintProcessor := NewTruncateCompactHint(config.Compactor)
    hintResult, err := hintProcessor.Process(ctx, agentCtx)
    // 处理 hint 文件
}

// 然后调用 LLM（使用压缩后的上下文）
msg, err := streamAssistantResponseWithRetry(...)
```

**关键时机**：
- Hint 处理发生在 `turn_start` 之后、`llm_request_json` 之前
- 确保 LLM 请求时使用的 messages 已经被 truncate/compact
- Hint 文件处理后自动删除（避免重复处理）

### 3.4 Agent Metadata Tags

工具输出现在包含元数据标签，帮助 AI 识别需要 truncate 的输出：

```html
<agent:tool id="call_xxx" name="read" chars="91" stale="5" />
```

**标签含义**：
- `id`: tool_call_id（用于 TRUNCATE）
- `name`: 工具名称
- `chars`: 原始内容字符数
- `stale`: 年龄排名（越小越旧，0-10 表示最近 10 个工具输出）

```html
<agent:tool id="call_xxx" name="read" chars="91" truncated="true" />
```

**标签含义**：
- `truncated="true"`: 该输出已经被截断（避免重复截断）

### 3.5 Trace Events

新增 trace 事件用于监控 hint 处理：

| 事件名称 | 含义 |
|---------|------|
| `truncate_compact_hint_start` | Hint 处理开始 |
| `truncate_compact_hint_read_attempt` | 尝试读取 hint 文件 |
| `truncate_compact_hint_read` | 成功读取 hint 文件 |
| `tool_output_truncated_via_hint` | 工具输出被截断 |
| `compact_skipped_via_hint_confidence` | COMPACT 因 roll > confidence 被跳过 |
| `compact_performed_via_hint` | COMPACT 执行成功 |
| `truncate_compact_hint_processed` | Hint 处理完成 |

---

## 4. Runtime Context Management

### 4.1 上下文压缩策略

Agent 会根据 `runtime_state.context_meta` 动态调整：

| Token 使用量 | Action Hint | 压缩策略 | Hint 格式 |
|------------|-------------|---------|-----------|
| 0-20% | normal | 正常运行，无需压缩 | 无需操作 |
| 20-40% | light_compression | 轻度压缩：移除冗余工具输出 | `## TRUNCATE` + 逗号分隔的 stale 工具输出 ID |
| 40-60% | medium_compression | 中度压缩：归档旧讨论到 detail/ | `## COMPACT\nconfidence: 80%` |
| 60-75% | heavy_compression | 重度压缩：仅保留关键决策+当前任务 | `## COMPACT\nconfidence: 90%` |
| 75%+ | emergency_compression | 紧急压缩（可能触发系统回退） | `## COMPACT\nconfidence: 100%` |

**Turn Protocol（运行时的操作指南）**：
```markdown
1. Read runtime_state and classify this turn as: no_action | memory_update_only.
2. Fast path: if fast_path_allowed=yes and no task state changed, no_action is acceptable.
3. If task state changed, update overview.md in this same turn.
4. If overview points to detail files needed for current task, read them explicitly.
5. Check tool_output_pressure.tool_outputs_summary:
   - If it is not "none", consider writing llm-context/truncate-compact-hint.md.
   - Use ## TRUNCATE with tool_call_id values (comma-separated or one per line).
   - Use ## COMPACT when you want one compaction pass.
   - Include confidence for COMPACT, e.g. confidence: 80% or confidence_range: 70%-90%.
   - Example:
     ## TRUNCATE
     call_abc123, call_def456

     ## COMPACT
     target: all
     confidence: 80%
6. Then answer the user.
```

### 4.2 compact_history 工具

**用途**：压缩对话历史和工具输出来管理上下文（现在主要通过 hint 机制使用）

**参数**：
- `target`: 压缩对象
  - `"conversation"` - 压缩对话历史（用户/助手消息）
  - `"tools"` - 压缩工具输出（通常较大且价值递减）
  - `"all"` - 压缩两者
- `strategy`: 压缩方式
  - `"summarize"` - 创建摘要
  - `"archive"` - 移动到 detail/ 文件（默认：当 llm context 可用时）
- `keep_recent`: 保留最近 N 项（默认 5）
- `archive_to`: 归档路径（可选，默认 detail/ 自动生成文件名）

**使用示例**：
```json
{
  "target": "conversation",
  "strategy": "archive",
  "keep_recent": 5,
  "archive_to": "llm-context/detail/session-summary.md"
}
```

**保留原则**：
- 始终保留最近 3-5 轮对话协议上下文
- 始终保留当前任务状态和下一步行动
- 始终保留关键决策和理由

---

## 5. 已知问题和改进方向

### 5.1 当前已知问题

#### Issue #1: compact_history 不自动更新 llm context

**描述**：
调用 `compact_history` 工具后，不会自动更新 `overview.md`，需要 LLM 手动调用 `write` 工具。

**影响**：
- LLM 可能忘记更新 llm context
- 造成压缩后的历史和当前状态不同步
- 增加手动操作负担

**缓解方案**：
- 使用 truncate-compact-hint 机制，系统会自动处理
- hint 文件处理时会自动更新 `LastCompactionSummary`

#### Issue #2: detail/ 目录内容不自动注入

**描述**：
`detail/` 目录下的文档不会被自动注入到请求上下文中，需要 LLM 主动使用 `read` 工具读取。

**影响**：
- 存档的详细信息可能被遗忘
- 需要额外的工具调用来访问历史细节

**潜在改进**：
- 在 `overview.md` 中添加索引，记录 detail/ 中的重要文档
- 提供机制让 LLM 主动感知 detail/ 中有相关文档

### 5.2 增强建议

#### Suggestion #1: 结构化状态格式

当前 `overview.md` 是自由格式 Markdown，建议引入结构化格式：

```yaml
# LLM Context

meta:
  version: "1.0"
  last_updated: "2025-01-15T10:30:00Z"
  session_id: "0c2c8ce1-ec0d-4968-942d-d84b861c938e"

project:
  name: "ai"
  description: "Go-based RPC-first Agent Core"
  tech_stack:
    language: "Go 1.24.0"
    api: "ZAI API (OpenAI-compatible)"
  key_paths:
    rpc_handlers: "cmd/ai/rpc_handlers.go"
    agent_loop: "pkg/agent/loop.go"
    shared_types: "pkg/rpc/types.go"
  commands:
    build: "go build -o bin/ai ./cmd/ai && ./bin/ai --mode rpc"
    test: "go test ./pkg/agent -v"

current_state:
  status: "implementing"
  task: "Add compact_history auto-update feature"
  next_actions:
    - "Review compact_history implementation"
    - "Add auto-update logic"
    - "Test feature"

issues:
  - id: "issue-1"
    title: "compact_history doesn't update llm context"
    severity: "medium"
    status: "proposed"
    proposal: "Auto-update overview.md after compression"
```

**优点**：
- 机器可读，便于工具解析
- 明确的字段定义，避免遗漏
- 支持版本控制

#### Suggestion #2: 自动压缩触发

当前压缩主要通过 hint 机制手动触发，可以添加自动触发机制：

```go
type CompressionPolicy struct {
    LightThreshold   float64 // 0.20 (20% tokens)
    MediumThreshold  float64 // 0.40 (40% tokens)
    HeavyThreshold   float64 // 0.60 (60% tokens)
    EmergencyThreshold float64 // 0.75 (75% tokens)
    AutoCompress      bool   // 是否启用自动压缩
}

// 在每次请求后检查
if contextMeta.TokensUsed > policy.LightThreshold {
    suggestCompression(contextMeta)
}
```

#### Suggestion #3: 事件驱动的状态更新

定义关键事件，触发 llm context 自动更新：

```go
type StateEvent struct {
    Type    string // "task_started", "decision_made", "bug_found", etc.
    Context map[string]interface{}
}

// 事件处理器
func (wm *LLMContext) OnEvent(event StateEvent) {
    switch event.Type {
    case "task_started":
        wm.UpdateCurrentTask(event.Context["task"].(string))
    case "decision_made":
        wm.RecordDecision(event.Context["decision"].(string))
    case "compression_completed":
        wm.UpdateSummary(event.Context["summary"].(string))
    }
}
```

---

## 6. 最佳实践

### 6.1 维护 llm context 的时机

**应该更新时**：
- ✅ 任务状态发生变化（开始、进行中、完成）
- ✅ 做出重要设计决策
- ✅ 发现新的问题或限制
- ✅ 项目上下文发生变化（新的依赖、命令等）
- ✅ 执行 context 压缩后
- ✅ 根据 runtime_state 主动管理上下文

**不应更新时**：
- ❌ 简单的问答或闲聊
- ❌ 临时调试尝试（除非导致重大发现）
- ❌ 重复的相同信息

### 6.2 使用 Truncate-Compact Hint

**何时写入 hint 文件**：
- ✅ runtime_state 显示 tool_outputs_summary 有大量 stale 工具输出
- ✅ context_usage 超过 40%，建议压缩
- ✅ 连续多个工具调用后，工具输出积累

**如何写入 TRUNCATE**：
```markdown
## TRUNCATE
call_abc123, call_def456, call_ghi789
```
从工具输出的元数据标签中复制 `tool_call_id`。

**如何写入 COMPACT**：
```markdown
## COMPACT
target: all
confidence: 80%
```
根据上下文压力调整 confidence：
- 40-60% token 使用：`confidence: 70%`
- 60-75% token 使用：`confidence: 85%`
- 75%+ token 使用：`confidence: 100%`（不跳过）

### 6.3 保持 overview.md 简洁

**推荐做法**：
- 使用要点列表而非长段落
- 聚焦当前相关，删除过时信息
- 详细内容移到 detail/，overview 只保留引用

### 6.4 压缩历史而非删除

**归档到 detail/**：
- 保留完整的讨论历史以备查询
- overview 中保留关键决策的摘要
- 使用清晰的文件名：`design-discussion.md`, `debug-session.md`

---

## 7. 相关文件

- **Session 协议**：项目根目录的 `<session-id>/messages.jsonl`
- **Agent Guidelines**：`AGENTS.md`
- **Architecture**：`ARCHITECTURE.md`
- **Commands**：`COMMANDS.md`
- **Tools**：`TOOLS.md`

---

## 8. 变更历史

| 日期 | 版本 | 变更内容 |
|------|------|---------|
| 2025-01-15 | 1.0 | 初始版本，整理设计和实现细节 |
| 2025-03-04 | 1.1 | 添加 truncate-compact-hint 机制说明，更新上下文压缩策略 |