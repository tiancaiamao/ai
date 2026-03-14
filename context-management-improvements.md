# Context Management 改进建议

## 问题分析

### 1. Compact 拒绝但提醒继续 ⚠️ 关键问题

**现象**：
- LLM 请求 compact 10 次，只执行了 1 次
- 每次 compact 被拒绝后，reminder 机制继续提醒
- LLM 不知道 compact 被拒绝，浪费 tokens 重复请求

**根本原因**：

```go
// pkg/compact/compact.go - ShouldCompact()
func (c *Compactor) ShouldCompact(messages []agentctx.AgentMessage) bool {
    threshold := c.calculateDynamicThreshold()  // 75% of available
    tokens := c.EstimateContextTokens(messages)
    return tokens >= threshold  // 16.4% < 75% → 拒绝
}
```

- Compact 只在 token 使用率 >= 75% 时执行
- 但 reminder 机制在 action_required = "compact" 时就提醒
- **冲突点**: reminder 建议做 compact，但 compact 会拒绝

**修复方案 A**: 修改 reminder 逻辑

```go
// pkg/agent/loop.go - generateRuntimeMeta()
func runtimeContextManagementHint(percent float64) string {
    switch {
    case percent < 20:
        return "Low usage (10-20%% tone): stay on task, only TRUNCATE obviously stale/large tool outputs."
    case percent < 30:
        return "Mild pressure (20-30%% tone): proactively review stale outputs and TRUNCATE in batches."
    case percent < 50:
        return "Moderate pressure (30-50%% tone): prepare one COMPACT pass after current mini-step."
    // ...
    }
}
```

**建议**:
- 当 token < 50% 时，不应该建议 compact
- compact 是昂贵操作（需要 LLM 调用生成 summary）
- 只有在真正需要时才建议

**修复方案 B**: 添加 compact 拒绝反馈

```go
// pkg/tools/llm_context_decision.go
case "compact":
    if t.compactor == nil {
        result.WriteString("Compactor not available, skipped compaction.\n")
        break
    }
    
    // 检查是否应该 compact
    if !t.compactor.ShouldCompact(agentCtx.Messages) {
        threshold := t.compactor.calculateDynamicThreshold()
        current := t.compactor.EstimateContextTokens(agentCtx.Messages)
        percent := float64(current) / float64(threshold) * 100
        
        result.WriteString(fmt.Sprintf("Compact rejected: token usage too low (%.1f%% of threshold).\n", percent))
        result.WriteString("Consider using TRUNCATE instead to clean up stale tool outputs.\n")
        break
    }
    
    // 执行 compact...
```

**推荐**: 方案 B 更好，因为它提供明确的反馈

---

### 2. LLM 不主动管理 Context

**现象**：
- 100% 的 llm_context_decision 调用都是提醒触发
- proactive_decisions = 0

**原因分析**：

1. **System Prompt 没有强调主动性**

当前 prompt:
```
When runtime_state shows context_management.action_required is not "none", 
you MUST call this tool BEFORE answering the user.
```

这是被动的："当提醒时才做"

2. **没有激励机制**

LLM 不知道主动管理会得到更好的 score

**修复方案**: 更新 System Prompt

```markdown
## Context Management Protocol

### Your Responsibility
You are RESPONSIBLE for managing your context window proactively.

### Proactive Actions (RECOMMENDED)
1. **After completing a task phase** → TRUNCATE stale outputs
2. **Before starting a new topic** → Consider COMPACT if recent context is large
3. **When you notice stale tool outputs** → Batch TRUNCATE 50-100 at once

### Proactive Score Tracking
- proactive=1, reminded=0 → score=excellent (you managed context yourself)
- proactive=0, reminded=1 → score=needs_improvement (you needed reminder)

### Decision Guidelines
| Token Usage | Recommended Action |
|-------------|-------------------|
| < 20% | TRUNCATE stale outputs only |
| 20-40% | TRUNCATE in batches, prepare for COMPACT |
| 40-60% | Consider COMPACT after current task |
| 60-75% | COMPACT soon |
| > 75% | COMPACT immediately |

### Topic Shift Detection
When you detect a topic shift (new user request, phase change), 
proactively evaluate context management needs BEFORE the system reminds you.
```

---

### 3. Truncate 重复尝试问题

**现象**：
- 第一次 truncate: `Truncated 0 tool output(s). No outputs were truncated (already truncated or IDs not found).`

**分析**：
- **这不是 bug！** 代码已有 `filterAlreadyTruncated()` 保护
- LLM 试图 truncate 已经 truncated 的 IDs
- 保护机制正常工作，阻止了重复 truncate

**建议**: 在 reminder 中明确说明

```go
// pkg/context/llm_context.go - GetDecisionReminderMessage()
return fmt.Sprintf(`
...

HOW TO TRUNCATE (IMPORTANT):
1. Find IDs with stale="N" attribute: <agent:tool id="call_xxx" stale="5" ...
2. Skip IDs with truncated="true" - already truncated (DON'T INCLUDE THESE!)
3. Get many IDs (批量清理！一次清理 50-100 条)
4. Pass as comma-separated string to truncate_ids

WARNING: Including already-truncated IDs will result in "0 truncated"
`, ...)
```

---

### 4. 话题转换检测

**现状**：
- Compact 只基于 token 使用率触发
- 没有考虑话题转换

**建议**: 添加话题转换信号

```go
// pkg/agent/loop.go
type topicShiftSignal struct {
    userMessageCount     int  // 距离上次用户消息的轮次
    phaseChange          bool // 是否有明显的阶段变化
    taskCompleted        bool // 任务是否完成
    newTopicKeywords     bool // 是否有新话题关键词
}

func detectTopicShift(messages []agentctx.AgentMessage) topicShiftSignal {
    // 检测逻辑
}
```

**在 runtime_state 中暴露**:

```yaml
compact_decision_signals:
  context_usage_percent: 16.4
  topic_shift_since_last_user: true   # 新增
  phase_completed_recently: true      # 新增
  llm_judge_hint: "Consider COMPACT after completing current task phase"
```

---

## 实施优先级

### P0 - 立即修复
1. **添加 compact 拒绝反馈** - 让 LLM 知道为什么被拒绝
2. **修改 reminder 逻辑** - token < 50% 时不建议 compact

### P1 - 短期改进
3. **更新 System Prompt** - 强调主动管理责任
4. **改进 reminder 消息** - 明确说明不要 include truncated IDs

### P2 - 中期优化
5. **添加话题转换检测** - 更智能的 compact 触发
6. **Proactive score 可见性** - 让 LLM 看到自己的 score

---

## 代码修改建议

### 1. pkg/tools/llm_context_decision.go

```go
case "compact":
    if t.compactor == nil {
        result.WriteString("Compactor not available, skipped compaction.\n")
        agentCtx.ContextMgmtState.RecordDecision(turn, "compact", wasReminded)
        break
    }

    // 新增：检查是否应该 compact
    if !t.compactor.ShouldCompact(agentCtx.Messages) {
        threshold := t.compactor.calculateDynamicThreshold()
        current := t.compactor.EstimateContextTokens(agentCtx.Messages)
        percent := float64(current) / float64(threshold) * 100
        
        result.WriteString(fmt.Sprintf("**Compact Rejected**\n\n"))
        result.WriteString(fmt.Sprintf("Reason: Token usage too low (%.1f%% of threshold)\n", percent))
        result.WriteString(fmt.Sprintf("Current: %d tokens, Threshold: %d tokens\n\n", current, threshold))
        result.WriteString("**Recommendation:** Use TRUNCATE instead to clean up stale tool outputs.\n")
        result.WriteString("COMPACT is only effective when token usage is high (≥50%).\n")
        
        agentCtx.ContextMgmtState.RecordDecision(turn, "compact_rejected", wasReminded)
        break
    }

    // 继续原有逻辑...
```

### 2. pkg/agent/loop.go

```go
func runtimeContextManagementHint(percent float64) string {
    switch {
    case percent < 20:
        return "Low usage (10-20%% tone): stay on task, only TRUNCATE obviously stale/large tool outputs. COMPACT is NOT recommended at this level."
    case percent < 30:
        return "Mild pressure (20-30%% tone): proactively TRUNCATE stale outputs in batches (50-100 at once). COMPACT is optional."
    case percent < 50:
        return "Moderate pressure (30-50%% tone): TRUNCATE stale outputs, consider COMPACT after completing current task phase."
    case percent < 65:
        return "High pressure (50-65%% tone): prepare for COMPACT, keep only active context and key decisions."
    case percent < 75:
        return "Critical pressure (65-75%% tone): COMPACT soon, fallback auto-compaction is approaching."
    default:
        return "Emergency pressure (75%%+ tone): COMPACT immediately, forced fallback compaction may trigger next."
    }
}
```

### 3. pkg/context/llm_context.go

```go
func (wm *LLMContext) GetDecisionReminderMessage(availableToolIDs []string) string {
    // ... 现有代码 ...
    
    return fmt.Sprintf(`<agent:remind comment="system message by agent, not from real user">

💡 Context management required: tokens at %d%%, %d stale tool outputs.

<context_meta>
tokens_used: %d
tokens_max: %d
tokens_percent: %.0f%%
messages_in_history: %d
</context_meta>

Current state suggests: %s

HOW TO TRUNCATE (IMPORTANT):
1. Find IDs with stale="N" attribute: <agent:tool id="call_xxx" stale="5" />
2. **SKIP IDs with truncated="true"** - these are already truncated!
3. Batch clean: get 50-100 IDs at once
4. Pass as comma-separated string: truncate_ids: "call_abc, call_def, ..."

EXAMPLE (copy and modify):
decision: "truncate"
reasoning: "Cleaning up %d stale tool outputs"
truncate_ids: %s

⚠️ WARNING: Including already-truncated IDs will result in "0 truncated".

For COMPACT: Only use when token usage ≥50%%. Current: %d%%`,
        int(meta.TokensPercent), staleCount,
        meta.TokensUsed, meta.TokensMax, meta.TokensPercent, meta.MessagesInHistory,
        getSuggestedAction(meta.TokensPercent, staleCount),
        staleCount, truncateIDsExample,
        int(meta.TokensPercent))
}
```

---

## 测试计划

1. **Compact 拒绝反馈测试**
   - Token usage < 50% 时请求 compact
   - 验证是否收到拒绝反馈
   - 验证是否建议使用 truncate

2. **Reminder 逻辑测试**
   - Token usage < 20% 时不建议 compact
   - Token usage 20-50% 时建议 truncate 为主
   - Token usage > 50% 时才建议 compact

3. **Truncate ID 过滤测试**
   - 尝试 truncate 已经 truncated 的 IDs
   - 验证返回 "0 truncated" 或被过滤

---

## 预期效果

1. **减少无效 compact 请求**: 从 10 次请求 1 次执行 → 只在真正需要时请求
2. **提高 proactive score**: 从 0% → 目标 30%+
3. **减少 reminder 频率**: LLM 主动管理，减少被提醒次数
4. **更好的 context 管理**: 基于话题转换而不只是 token 压力