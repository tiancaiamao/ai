# Context Management Improvement Design

> 状态：Draft | 讨论：2025-07

## 背景

当前 context management 架构经过一次重构（从"LLM 主动管理"到"系统触发 + LLM 决策"），方向正确。但在实际使用中暴露了几个问题：

1. **Compact 吞噬关键指令**（已导致 Symphony 任务失败）
2. **Truncate 过于粗暴**（完全删除消息，丢失"发生过"的记忆）
3. **Context mgmt 信息不足**（LLM 无法做最优决策）
4. **单轮决策**（LLM 只有一次机会，压力大，容易过度或不足）
5. **操作缺乏安全网**（LLM 可能过度激进，丢关键信息）

本文档描述改进方案。

---

## 改进 1：Truncate 留首尾

### 问题

当前 truncate 是 binary 操作：标记 `Truncated=true` → `buildNormalModeRequest` 直接跳过。消息完全消失，LLM 丢失"这件事发生过"的记忆。

### 方案

truncate 后保留 head + tail 预览，中间用 `[N chars truncated]` 替换。

```
Before: 完整的 8200 字符 bash 输出
After:  前 150 字符... [7900 chars truncated] ...后 50 字符
```

### 实现细节

**数据模型变更**（`pkg/context/message.go`）：

```go
type AgentMessage struct {
    // ...existing fields...
    
    // TruncatedPreview is the head+tail preview shown after truncation.
    // Empty if not truncated or if full delete (legacy behavior).
    TruncatedPreview string
}
```

**Truncate 逻辑变更**（`pkg/tools/context_mgmt/truncate_messages.go`）：

```go
func generateTruncatePreview(content string) string {
    const maxPreviewChars = 400
    const headRatio = 0.7
    
    if len(content) <= maxPreviewChars {
        return content  // 太短，不需要 truncate
    }
    
    headChars := int(float64(maxPreviewChars) * headRatio)
    tailChars := maxPreviewChars - headChars
    truncatedChars := len(content) - headChars - tailChars
    
    return content[:headChars] + 
           fmt.Sprintf("\n... [%d chars truncated] ...\n", truncatedChars) + 
           content[len(content)-tailChars:]
}
```

**渲染逻辑变更**（`pkg/context/render.go`）：

```go
func RenderToolResult(msg *AgentMessage, mode AgentMode, stale int) string {
    // 如果消息被 truncate 了，渲染 preview 而不是跳过
    if msg.Truncated && msg.TruncatedPreview != "" {
        if mode == ModeContextMgmt {
            return fmt.Sprintf(
                `<agent:tool id="%s" name="%s" truncated chars="%d">%s</agent:tool>`,
                msg.ToolCallID, msg.ToolName, msg.OriginalSize, msg.TruncatedPreview,
            )
        }
        return msg.TruncatedPreview
    }
    // ...existing logic...
}
```

**Normal mode request building**（`pkg/agent/loop_normal.go` 或 `pkg/llm/request_builder.go`）：

当前逻辑：`if msg.IsTruncated() { continue }`（跳过）

改为：如果 `msg.TruncatedPreview != ""`，渲染 preview 而不是跳过。

### 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `TruncatePreviewMaxChars` | 400 | 预览最大字符数 |
| `TruncatePreviewHeadRatio` | 0.7 | head 占比 |
| `TruncatePreviewMinOriginal` | 600 | 原始内容小于此值不生成 preview（直接全删） |

对于很短的消息（比如 < 600 chars），truncate 的收益不大，不值得保留 preview。

---

## 改进 2：更完整的 Context Mgmt 决策信息

### 问题

当前 `buildContextMgmtMessages()` 给 LLM 看：
- 所有 stale output（可能 36 个，消耗大量 token）
- 最近 10 条消息
- 基础 runtime_state（tokens_percent, stale count）

缺少：
- 每条消息的 token 消耗（不知道删哪条最划算）
- 预估 savings（不知道 truncate 后能省多少）
- 上次 context mgmt 做了什么
- 消息分组和聚合

### 方案

### 2a. 改进 runtime_state

当前：
```xml
<agent:runtime_state>
tokens_used: 62220
tokens_limit: 200000
tokens_percent: 31.1
recent_messages: 135
stale_outputs: 36
turn: 16
</agent:runtime_state>
```

改进后：
```xml
<agent:runtime_state>
tokens_used: 62220 / 200000 (31.1%)
messages: 135 total (3 user, 42 assistant, 86 tool_result, 4 other)
stale_outputs: 36 (top 5 consume ~18500 tokens)
estimated_savings_if_all_stale_truncated: ~32000 tokens → would drop to 15.2%
last_context_mgmt: turn 12, actions: truncated 5 messages (saved ~8000 tokens)
urgency: medium
</agent:runtime_state>
```

关键字段：
- **messages breakdown**：让 LLM 理解消息组成
- **top N token consumers**：直接告诉 LLM 最值得删的
- **estimated savings**：让 LLM 知道目标是什么
- **last context mgmt history**：避免重复操作

### 2b. Stale output 排序 + 截断展示

当前：36 个 stale output 全部展示。

改进：
1. 按 `stale_score × chars` 降序排列（又老又大的排前面）
2. 展示 top 15-20 个（带完整元数据）
3. 剩余的按工具类型聚合：
```
... and 16 more stale outputs (grep: 8 × ~500 chars avg, bash: 5 × ~1200 chars avg, read: 3 × ~3000 chars avg)
```

### 2c. Per-message token 估算

在 context mgmt 模式的 `<agent:tool>` 渲染中加 `tokens` 字段：

```xml
<agent:tool id="abc123" name="bash" stale="20" chars="8200" tokens="~2050">
```

### 实现变更

主要改 `pkg/llm/context_mgmt_input.go` 的 `BuildContextMgmtInput()` 和 `pkg/context/render.go`。

---

## 改进 3：多轮 Context Management

### 问题

当前 `executeContextMgmtTools` 只调用一次 LLM，LLM 一次选一个工具就结束。

结果：
- 要么不够（选了 truncate 5 条，token 还是很高）
- 要么过度（一次 truncate 太多，丢了重要信息）

### 方案

改成循环，最多 N 轮：

```go
func (a *AgentNew) executeContextMgmtTools(ctx context.Context, urgency string) error {
    const maxMgmtRounds = 5
    
    for round := 0; round < maxMgmtRounds; round++ {
        // 1. 每轮重新 build messages（包含上一轮操作的结果）
        messages := a.buildContextMgmtMessages()
        
        // 2. 调用 LLM
        toolCalls, err := a.callContextMgmtLLM(ctx, messages)
        if err != nil {
            return err
        }
        
        // 3. 执行工具
        hasNoAction := false
        for _, tc := range toolCalls {
            if tc.Function.Name == "no_action" {
                hasNoAction = true
                break
            }
            a.executeContextMgmtTool(ctx, tc, tools)
        }
        
        // 4. 退出条件
        if hasNoAction {
            break  // LLM 认为不需要继续
        }
        
        tokenPercent := a.snapshot.EstimateTokenPercent()
        if tokenPercent < 0.15 {
            break  // token 已经很低了
        }
    }
    
    // 最终处理：checkpoint, reset counters
    // ...
}
```

### 多轮流程示例

```
Round 1:
  State: 31.1%, 36 stale outputs
  LLM decision: truncate 8 large grep outputs
  Result: saved ~12000 tokens → 25.0%

Round 2:
  State: 25.0%, 28 stale outputs remaining
  LLM decision: truncate 5 more + update_llm_context
  Result: saved ~6000 tokens → 21.5%

Round 3:
  State: 21.5%, 23 stale outputs remaining (mostly small)
  LLM decision: no_action
  → Done
```

### 为什么多轮能降低激进程度

**单轮模式**：LLM 必须一次决策删多少 → 倾向于多删（因为怕下次没机会）

**多轮模式**：LLM 可以保守地删一点 → 看效果 → 再决定 → 决策压力小 → 不容易过度

配合 truncate 留首尾（改进 1），即使某轮删多了，head/tail 仍然保留关键信息。

---

## 改进 4：操作安全网

### 4a. Undo Truncate

新增 `undo_truncate` 工具，让 LLM 可以恢复上一轮的 truncate 操作。

```go
type UndoTruncateTool struct {
    snapshot *agentctx.ContextSnapshot
    journal  *agentctx.Journal
}
```

参数：`message_ids`（要恢复的 tool call IDs）

行为：
- 把 `Truncated` 设回 `false`
- 清除 `TruncatedPreview`
- 写入 journal（UndoTruncateEvent）

因为 journal 保存了原始数据，undo 就是恢复标记。

### 4b. 受保护消息

当前只有"最后 30 条"受保护。增加：

**最近被引用的 tool output**：最近 K 条 assistant 消息中提到的 tool output 不允许 truncate。

```go
func (t *TruncateMessagesTool) isRecentlyReferenced(toolCallID string) bool {
    // 检查最近 5 条 assistant 消息是否引用了这个 tool output
    checkStart := len(t.snapshot.RecentMessages) - 5
    if checkStart < 0 {
        checkStart = 0
    }
    
    for i := checkStart; i < len(t.snapshot.RecentMessages); i++ {
        msg := t.snapshot.RecentMessages[i]
        if msg.Role == "assistant" {
            text := msg.ExtractText()
            // 简单匹配：tool call ID 出现在 assistant 消息中
            if strings.Contains(text, toolCallID) {
                return true
            }
        }
    }
    return false
}
```

### 4c. LLMContext 缩小保护

`update_llm_context` 不应该允许大幅缩小 LLMContext：

```go
func (t *UpdateLLMContextTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
    newContext := params["llm_context"].(string)
    oldLen := len(t.snapshot.LLMContext)
    
    // 不允许缩小到原来的 40% 以下
    minLen := oldLen * 40 / 100
    if len(newContext) < minLen && oldLen > 200 {
        return nil, fmt.Errorf(
            "new LLMContext too short (%d chars < %d min). Keep existing content and add new information.",
            len(newContext), minLen,
        )
    }
    // ...
}
```

### 4d. 分级激进策略

不同 urgency level 对应不同的操作约束，在 system prompt 中动态注入：

```xml
<agent:constraints>
Current urgency: medium (31.1%)
Allowed actions: truncate (stale ≥ 10), update_llm_context, no_action
NOT allowed: compact (reserved for urgency ≥ high)
Max truncations per round: 10
After truncating, you SHOULD update_llm_context to reflect changes
</agent:constraints>
```

| Urgency | Token % | 允许的操作 | Truncate 约束 |
|---------|---------|-----------|--------------|
| low | < 25% | update_llm_context, no_action | 不允许 truncate |
| medium | 25-50% | truncate (stale ≥ 10), update_llm_context, no_action | 最多 10 条/轮 |
| high | 50-70% | truncate (stale ≥ 5), update_llm_context, compact | 最多 20 条/轮 |
| urgent | > 70% | 所有操作 | 无限制 |

### 4e. 操作后自动验证

每轮 context mgmt 结束后，轻量检查：

```go
func (a *AgentNew) validateMgmtRound(before, after *ContextSnapshot) []string {
    var warnings []string
    
    // 1. LLMContext 不应该大幅缩小
    if len(after.LLMContext) < len(before.LLMContext)/2 && len(before.LLMContext) > 500 {
        warnings = append(warnings, "LLMContext shrank significantly — information may be lost")
    }
    
    // 2. 最近 5 条消息不应该被 truncate
    for i := len(after.RecentMessages) - 5; i < len(after.RecentMessages); i++ {
        if i >= 0 && after.RecentMessages[i].Truncated {
            warnings = append(warnings, "Recent message was truncated — may break active context")
        }
    }
    
    // 3. 如果 token 不降反升
    if after.EstimateTokenPercent() > before.EstimateTokenPercent() {
        warnings = append(warnings, "Token usage increased after context management")
    }
    
    return warnings
}
```

Warnings 会被注入到下一轮的 context mgmt input 中，让 LLM 知道上一轮可能有问题，有机会 undo。

---

## 改进 5：Compact 保护持久指令（P0）

### 问题

Compact 把旧消息等权对待 → WORKFLOW.md 中的关键指令被压缩为 summary → 指令丢失 → Symphony 任务失败。

### 方案：三层存储

当前：
```
ContextSnapshot {
    LLMContext     string          // LLM 维护的结构化上下文
    RecentMessages []AgentMessage  // 对话历史
    AgentState     AgentState      // 系统元数据
}
```

改为：
```
ContextSnapshot {
    Instructions       string          // 持久指令，compact 不碰
    LLMContext         string          // LLM 维护的结构化上下文
    RecentMessages     []AgentMessage  // 对话历史
    AgentState         AgentState      // 系统元数据
}
```

### Instructions 的生命周期

1. **写入时机**：第一条 user message 时，或通过新工具 `set_instructions`
2. **Compact 行为**：`performCompaction()` 后，在 summary 前注入 Instructions
3. **渲染**：Normal mode 请求中，作为独立的 user message 注入（在 LLMContext 之前）

```go
// Compact 后的消息结构
newMessages := []agentctx.AgentMessage{
    NewUserMessage("[Instructions]\n" + snapshot.Instructions),     // 不变
    NewUserMessage("[Previous conversation summary]\n" + summary),  // compact 产物
}
newMessages = append(newMessages, recentMessages...)
```

### 谁来填充 Instructions

**自动方案**：第一条 user message 自动作为 Instructions。简单，但不精确——不是所有首条消息都是指令。

**显式方案**：Symphony（或用户）通过参数传入 `instructions`，Agent 在 `NewAgentNew()` 时设置。

**推荐**：两者结合——
- 如果创建 agent 时传入了 `instructions` 参数 → 用它
- 否则 → 第一条 user message 自动成为 Instructions
- 后续可以通过 `update_instructions` 工具修改

---

## 实施计划

### Phase 1：止血（P0 + 最小安全）

| 任务 | 涉及文件 | 风险 |
|------|----------|------|
| Truncate 留首尾 | `message.go`, `truncate_messages.go`, `render.go`, `loop_normal.go` | 低 |
| Compact 保护 Instructions | `snapshot.go`, `agent_new.go`, `compact.go` | 中 |

### Phase 2：决策质量

| 任务 | 涉及文件 | 风险 |
|------|----------|------|
| 改进 runtime_state | `context_mgmt_input.go`, `request_builder.go` | 低 |
| Stale output 排序 + 聚合 | `context_mgmt_input.go` | 低 |
| Per-message token 估算 | `render.go`, `token_estimation.go` | 低 |
| LLMContext 缩小保护 | `update_llm_context.go` | 低 |
| 分级激进策略 | `context_mgmt_system.md`, `loop_context_mgmt.go` | 低 |

### Phase 3：多轮 + 安全网

| 任务 | 涉及文件 | 风险 |
|------|----------|------|
| 多轮 context mgmt | `loop_context_mgmt.go` | 中 |
| undo_truncate 工具 | 新文件 `undo_truncate.go` | 低 |
| 受保护消息检查 | `truncate_messages.go` | 低 |
| 操作后自动验证 | `loop_context_mgmt.go` | 低 |

### 依赖关系

```
Phase 1 (truncate preview + instructions)
    ↓
Phase 2 (better info + safety constraints)
    ↓
Phase 3 (multi-round + undo)
```

Phase 2 依赖 Phase 1（因为 truncate preview 改变了消息渲染方式）。
Phase 3 依赖 Phase 2（因为多轮需要更好的信息来支持每轮决策）。

---

## 整体效果

改进后的 context mgmt 流程：

```
Trigger fired (31.1% tokens, 36 stale)
    ↓
Round 1:
  System: "urgency medium, you may truncate stale ≥ 10, max 10/round"
  State:  "top 5 consumers: [abc:2050t, def:1800t, ghi:1500t, ...]"
          "truncate all stale → would drop to 15.2%"
  LLM: truncate abc, def, ghi, jkl, mno (5 large grep outputs)
  Result: 31.1% → 25.0% (saved 12200 tokens)
  Validate: ✅ OK
    ↓
Round 2:
  State:  "25.0%, 31 stale remaining"
          "top 3: [pqr:1200t, stu:1100t, vwx:900t]"
  LLM: truncate pqr, stu + update_llm_context
  Result: 25.0% → 21.5%
  Validate: ✅ OK
    ↓
Round 3:
  State:  "21.5%, remaining stale are small (< 400 chars avg)"
  LLM: no_action
  → Done, return to normal mode
```

对比当前行为（一次调用 → 选一个工具 → 结束），改进后：
- 信息更充分（知道删什么最划算）
- 操作更安全（留首尾 + 分级约束 + 可撤销）
- 渐进式调整（多轮，每轮看效果）
- 关键指令不被 compact 吞噬
