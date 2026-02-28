# Working Memory - 设计和实现文档

## 1. 设计目的

### 1.1 核心问题

Agent 在长期对话中面临以下挑战：
- **上下文限制**：LLM 上下文窗口有限，无法承载完整对话历史
- **信息丢失**：随着对话增长，早期重要信息逐渐被遗忘
- **冗余信息**：大量历史消息包含重复、过时或无关内容
- **状态管理困难**：缺乏明确机制来跟踪"当前任务状态"和"下一步行动"

### 1.2 解决方案

**Working Memory** 是一个持久化的状态跟踪机制，用于：
- 存储和更新关键信息（任务状态、决策、上下文）
- 替代对完整历史消息的依赖
- 提供轻量级、可快速访问的状态摘要
- 支持跨会话的状态连续性

### 1.3 设计原则

1. **不依赖历史消息**：主动维护，而非被动依赖历史
2. **单一事实源**：所有关键状态信息在此集中管理
3. **增量更新**：随着对话进展逐步更新，而非重写
4. **轻量化**：只保留必要信息，避免冗余

---

## 2. 当前实现

### 2.1 存储位置

```
/Users/genius/.ai/sessions/--<cwd>--/<session-id>/working-memory/
├── overview.md          # 核心状态摘要（必须维护）
└── detail/              # 详细文档存档目录
    ├── session-summary.md  # 压缩后的历史对话
    ├── design-doc.md       # 设计决策文档
    └── ...
```

**关键说明**：
- `<cwd>` 使用双连字符编码：`--<工作目录路径>--`
- 例如：`--Users-genius-project-ai--` 表示 `/Users/genius/project/ai`
- 每个会话有独立的 working memory 目录

### 2.2 overview.md 结构

```markdown
# Working Memory

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
- ⚠️ compact_history 工具调用后需要手动更新（已知问题）

**LLM 自动注入**：
- 每次请求会自动注入 `overview.md` 的内容
- detail/ 目录的内容不会自动注入（需主动读取）

---

## 3. Runtime Context Management

### 3.1 上下文压缩策略

Agent 会根据 `runtime_state.context_meta` 动态调整：

| Token 使用量 | Action Hint | 压缩策略 |
|------------|-------------|---------|
| 0-20% | normal | 正常运行，无需压缩 |
| 20-40% | light_compression | 轻度压缩：移除冗余工具输出 |
| 40-60% | medium_compression | 中度压缩：归档旧讨论到 detail/ |
| 60-75% | heavy_compression | 重度压缩：仅保留关键决策+当前任务 |
| 75%+ | emergency_compression | 紧急压缩（可能触发系统回退） |

### 3.2 compact_history 工具

**用途**：压缩对话历史和工具输出来管理上下文

**参数**：
- `target`: 压缩对象
  - `"conversation"` - 压缩对话历史（用户/助手消息）
  - `"tools"` - 压缩工具输出（通常较大且价值递减）
  - `"all"` - 压缩两者
- `strategy`: 压缩方式
  - `"summarize"` - 创建摘要
  - `"archive"` - 移动到 detail/ 文件（默认：当 working memory 可用时）
- `keep_recent`: 保留最近 N 项（默认 5）
- `archive_to`: 归档路径（可选，默认 detail/ 自动生成文件名）

**使用示例**：
```json
{
  "target": "conversation",
  "strategy": "archive",
  "keep_recent": 5,
  "archive_to": "working-memory/detail/session-summary.md"
}
```

**保留原则**：
- 始终保留最近 3-5 轮对话协议上下文
- 始终保留当前任务状态和下一步行动
- 始终保留关键决策和理由

---

## 4. 已知问题和改进方向

### 4.1 当前已知问题

#### Issue #1: compact_history 不自动更新 working memory

**描述**：
调用 `compact_history` 工具后，不会自动更新 `overview.md`，需要 LLM 手动调用 `write` 工具。

**影响**：
- LLM 可能忘记更新 working memory
- 造成压缩后的历史和当前状态不同步
- 增加手动操作负担

**建议修复方案**：
```go
// 在 compact_history 执行完成后
if target == "conversation" || target == "all" {
    // 自动生成摘要并更新 overview.md
    summary := generateSummary(compressedContent)
    updateOverviewMarkdown(summary)
}
```

#### Issue #2: detail/ 目录内容不自动注入

**描述**：
`detail/` 目录下的文档不会被自动注入到请求上下文中，需要 LLM 主动使用 `read` 工具读取。

**影响**：
- 存档的详细信息可能被遗忘
- 需要额外的工具调用来访问历史细节

**潜在改进**：
- 在 `overview.md` 中添加索引，记录 detail/ 中的重要文档
- 提供机制让 LLM 主动感知 detail/ 中有相关文档

### 4.2 增强建议

#### Suggestion #1: 结构化状态格式

当前 `overview.md` 是自由格式 Markdown，建议引入结构化格式：

```yaml
# Working Memory

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
    - "Test the feature"

issues:
  - id: "issue-1"
    title: "compact_history doesn't update working memory"
    severity: "medium"
    status: "proposed"
    proposal: "Auto-update overview.md after compression"
```

**优点**：
- 机器可读，便于工具解析
- 明确的字段定义，避免遗漏
- 支持版本控制

#### Suggestion #2: 自动压缩触发

当前压缩是手动触发，建议添加自动触发机制：

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

定义关键事件，触发 working memory 自动更新：

```go
type StateEvent struct {
    Type    string // "task_started", "decision_made", "bug_found", etc.
    Context map[string]interface{}
}

// 事件处理器
func (wm *WorkingMemory) OnEvent(event StateEvent) {
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

## 5. 最佳实践

### 5.1 维护 working memory 的时机

**应该更新时**：
- ✅ 任务状态发生变化（开始、进行中、完成）
- ✅ 做出重要设计决策
- ✅ 发现新的问题或限制
- ✅ 项目上下文发生变化（新的依赖、命令等）
- ✅ 执行 context 压缩后

**不应更新时**：
- ❌ 简单的问答或闲聊
- ❌ 临时调试尝试（除非导致重大发现）
- ❌ 重复的相同信息

### 5.2 保持 overview.md 简洁

**推荐做法**：
- 使用要点列表而非长段落
- 聚焦当前相关，删除过时信息
- 详细内容移到 detail/，overview 只保留引用

### 5.3 压缩历史而非删除

**归档到 detail/**：
- 保留完整的讨论历史以备查询
- overview 中保留关键决策的摘要
- 使用清晰的文件名：`design-discussion.md`, `debug-session.md`

---

## 6. 相关文件

- **Session 协议**：项目根目录的 `<session-id>/messages.jsonl`
- **Agent Guidelines**：`AGENTS.md`
- **Architecture**：`ARCHITECTURE.md`
- **Commands**：`COMMANDS.md`

---

## 7. 变更历史

| 日期 | 版本 | 变更内容 |
|------|------|---------|
| 2025-01-15 | 1.0 | 初始版本，整理设计和实现细节 |