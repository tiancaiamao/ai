# Context Management Redesign — Final v3.1

> Status: 终稿（grill-me + orchestrator review 完成）
> 基于 draft-v1/v2 + cross-project-analysis + 18 个决策点 + orchestrator 48 问 Q&A
> v3.1: 纳入 orchestrator review 的 3 个 P0 修正和实现细节澄清

## 背景

GLM5.2 从 200K → 1M context window。当前两套上下文管理机制的阈值按百分比缩放，
但管理成本（LLM call + 缓存失效 + output token）按绝对值增长。

核心矛盾：阈值是百分比，成本是绝对值。

## 问题诊断

1. compact 75% 阈值在大窗口下过高（~700K 才触发），summarization 成本极高
2. ContextManager 频繁进入且每次全量 LLM call（20% = 200K 即起步）
3. 缓存经济学灾难：cache hit/miss 价差 50-120 倍，任何修改中间消息的操作
   都导致截断点之后所有 token cache miss
4. output token 成本被忽略：output 是 hit input 的 100 倍，是 compaction 主要成本

## 成本模型

以 GLM/DeepSeek 价格为基准（cache-hit input = 1）：

| | 相对成本 |
|---|---|
| input cache hit | 1x |
| input cache miss | 50x |
| output | 100x |

delta compaction（方式 B：复用主对话缓存前缀）的单次成本：
```
输入: delta_size × 1 (hit) + decision_msg × 50 (miss) ≈ delta_size + 10K hit单位
输出: (summary + thinking) × 100 ≈ 300K~600K hit单位
```

**output 是 compaction 的主要成本。summary 越小越省钱。**

回本 turn 数（summary = 1K~3K，delta = 50K，含 compaction 后缓存重建成本）：
```
回本 ≈ compaction总成本 / 每 turn 节省(delta - summary)
    ≈ 10~15 turn（比初版估算 7~10 略高，因为纳入了缓存重建 cost）
```
仍然可接受——delta compaction 间隔内的 turn 数通常远超 15。

## 设计决策（18 个决策点）

### D1：阈值用绝对值，不用百分比

cache-hit 成本和上下文质量衰减由绝对 token 数决定，与 window 大小无关。
（Claude Code 和 Pi 都用绝对值，验证了此判断。）

### D2：delta compaction 为主线，不做全量 compaction

每隔约 50K 做一次增量压缩，把 delta 压成 1K-3K 的 summary。
上下文永远在低水位区间振荡。

### D3：compact 复用主对话缓存前缀（方式 B）

**不使用独立的 context management LLM call。**
在主 agent 消息流末尾追加 `<agent:context_compaction_decision>` user 消息，
让主 agent 自己做 compaction。

**修正（orchestrator P0-1）：注入的 decision_msg 不重复 delta 内容。**
delta 消息已在 RecentMessages 中（缓存命中），decision_msg 只是一个 ~200 tokens 的
简短指令，不包含要压缩的消息内容。LLM 回顾上方已有对话（全部缓存命中）生成 summary。

成本对比：
- 方式 A（独立 call）：50K × 50 (全 miss) = 2500K hit单位
- 方式 B（缓存前缀）：50K × 1 (hit) + 200 × 50 (miss) + output = ~460K hit单位
- **42 倍差距（仅 input 部分）**

原因：旧的独立 context management 是没有考虑 cache miss 而设计的，被证明不好。

### D4：compact 指令用 `<agent:context_compaction>` 格式注入

作为 role:user 消息，使用 `<agent:context_compaction>` 边界标记。
兼容性最好，和现有 `<agent:runtime_state>` 等注入格式一致。

### D5：多条 summary 追加（B2），不做融合

每次 delta compaction 产生独立 summary，追加到 summary 序列。已有 summary 永不修改。

```
B1（融合）: [summary_v3] → [summary_v4]（替换）
B2（追加）: [s1][s2][s3] → [s1][s2][s3][s4]（追加）
```

B2 优势：
- 缓存更友好（已有 summary 永久稳定）
- 信息保真度更高（无反复稀释）
- compaction call 更简单（只总结 delta，不融合旧 summary）
- RecentMessages 维护更简单（纯 append）

### D6：journal 模型从"全局 cut-point"改为"区间压缩"

现有 `buildSessionContext` 只认最后一条 compaction entry。
改为：每个 delta compaction entry 记录 `[fromID, toID]` 区间和 summary text。
回放时区间内消息被替换为 summary，区间外保留。

```
journal: [msg1][msg2][delta_compact_1][msg3][msg4][delta_compact_2][msg5]...

回放:
  1. delta_compact_1: summary_1 替换 msg1~msg2
  2. delta_compact_2: summary_2 替换 msg3~msg4
  3. snapshot = [summary_1][summary_2][msg5]
```

RecentMessages 是回放产出的内存 snapshot，不存在物理移除。

### D7：可靠性靠 prompt 保障，不做兜底

- 保障 1：`<agent:context_compaction>` 指令写得足够明确
- 保障 2：system prompt 预置一段 context compaction 说明
- 不做格式校验兜底（引入复杂性），先观察效果

### D8：触发用定期询问 + 硬阈值兜底

不靠 telemetry 信号让 agent 自主触发（经验证明：context 大了 LLM 会迷失，不重视信号）。
定期插入 `<agent:context_compaction_decision>` user 消息询问 LLM。

如果 LLM 决定不触发，接受，下次再问。压力越大问得越频繁。

### D9：询问频率按 delta tier + tool call interval

```
delta 0 ~ 30K：不问
delta ≥ 30K：首次询问，no 后隔 10 次工具调用再问
delta ≥ 50K：no 后隔 7 次工具调用再问
delta ≥ 80K：no 后隔 3 次工具调用再问（最低间隔，不每 turn 问）
delta ≥ 120K：硬阈值，强制触发
```

**修正（orchestrator P0-2）：注入不需要等待"无 tool call 的 turn"。**
检查条件是 `delta >= tier AND tool_calls_since_last_check >= interval`，不要求
LLM 处于自然暂停状态。agent loop 已支持在 tool call 序列中间接收 user 消息（busy steer 模式），
LLM 可以在输出 tool calls 的同一回复中同时输出 compaction summary。
这避免了"LLM 长时间连续调用工具导致 delta 爆炸"的问题。

### Delta 的精确定义

Delta = 上一次 delta_compact journal entry 之后新增的 agent-visible 任务消息的
token 估算值。不包括 summary 消息、runtime_state 消息、context_compaction_decision
消息本身——这些是元数据。

实现：AgentState 加 `TokensSinceLastDeltaCompaction` 字段，每次 delta compaction 后归零，
每 turn 估算新增消息 token 并累加。接受估算误差累积——tier 阈值不需要精确。

### D10：一步合并——decision 和 summary 在同一个 round trip

指令同时问两件事：
1. 当前是否需要 compaction？
2. 如果是，请输出更新后的任务状态摘要。

LLM 按结构化格式回复：
```
<decision>yes|no</decision>
<summary>...</summary>
```

### D11：summary 积累不合并

多条 summary 追加，初期不做合并。1M 窗口下即使 100K summary 也只占 10%。
观察效果后如有问题再优化。

### D12：不做独立 truncate，delta compaction 全包

调研发现：没有专有 cache_edit API 的项目（Pi、Codex）都不做 context-level truncate。
truncate 原地修改消息内容在任何 provider 的 prefix cache 下都会断裂缓存。
让 delta compaction 统一处理大 tool output。

保留 tool output normalization（工具执行时截断到 10K chars），这是源头控制。

### D13：cut-point 从 delta 末尾倒推，protected 预算 10K token

delta compaction 触发时：
```
从 delta 区间末尾倒推，累计 token 直到达到 10K 预算
  → protected（保留）
  → 之前的（压缩成 summary）
```

**边界确定由代码完成，不由 LLM。** 流程：
1. 代码从 delta 末尾倒推 10K token 预算，确定 protected 范围
2. decision_msg 只是指令，不告诉 LLM 边界——LLM 回顾上方已有对话即可
3. LLM 只输出 summary 文本
4. 代码拼装：`[existing summaries] + [new_summary] + [protected_msgs]`
5. journal entry 的 `[fromID, toID]` = delta 起始 ~ protected 起始之前

**cut-point 不能落在 tool call 序列中间。** 如果倒推落在 tool/toolResult 上，
向前调整到 assistant 消息边界（和现有 extractRecentMessages 逻辑一致）。

**修正（orchestrator P0-3）：decision_msg 和 LLM_response 是 ephemeral 的。**
compaction 后，decision_msg（注入的 `<agent:context_compaction_decision>`）和
LLM_response（含 summary 的回复）都不写入 session journal。
- decision_msg 通过 `metadata.kind="context_compaction_decision"` 标记，临时注入内存 RecentMessages
- LLM_response 中的 summary 被提取后，写入 delta_compact journal entry
- snapshot replay 后自然不包含这两条（它们从未持久化）
- delta_compact entry 的 `[fromID, toID]` 只覆盖被压缩的 delta 消息，不含 protected

这和现有 context-first 模式下 runtime_state 的处理方式一致——临时注入，不持久化。

### D14：硬阈值直接注入 compaction 指令，跳过 decision

delta ≥ 120K 时，不再问"要不要"，直接注入 `<agent:context_compaction>`
让 LLM 生成 summary。LLM 的决策空间在硬阈值之上关闭。

### D15：summary 大小 1K-3K tokens

prompt 指定范围（约 4K-12K chars），不固定值。
output 是 100x hit input，summary 越小越省钱。
protected messages 不让 LLM 输出，代码拼装。

回本 turn 数：10~15 turn（含缓存重建成本）。

### D16：heavyweight Compactor 保留为兜底，阈值调高

保留现有全量 compactor 作为安全网，阈值调高到接近 window 上限（~90% window）。
日常不干预，仅在 delta compaction 连续失败或上下文失控时触发。

**Heavyweight 与 delta compaction 不互斥，可同 session 共存。**
- heavyweight 的 `ShouldCompact` 仍然每 turn 检查（包括 delta compaction turn），检查的是总 context 大小
- heavyweight 触发时，已有 delta summary 被读入作为全量 summarization 的输入，然后被全量 summary 替代
- heavyweight 用旧格式 journal entry（全局 cut-point），触发后重置 `TokensSinceLastDeltaCompaction`
- delta compaction 和 heavyweight 检查独立：delta compaction 先执行，如果总 context 降下来了，heavyweight 可能就不触发了

### D17：分三阶段落地

**阶段 1：止血**
- 把现有 ContextManager 的百分比阈值改为绝对值
- 让 GLM5.2 走 cache-first（跳过旧的 proactive context management）
- 调高 heavyweight compactor 兜底阈值

**阶段 2：实现 delta compaction**
- 新的 journal 区间压缩模型
- `<agent:context_compaction_decision>` 注入 + 结构化输出
- 多条 summary 追加（B2）

**阶段 3：观察 + 调优**
- 实际运行，看 summary 质量、压缩比、触发频率
- 根据观察调整参数

### D18：首次 compaction 无特殊处理

指令模板统一，第一次时 "previous summary" 部分为空。
指令里加条件："如果没有之前的摘要，就总结全部内容。"

## 架构总览

```
┌─────────────────────────────────────────────────────────┐
│  Delta Cycle                                             │
│                                                          │
│  [summary_1]...[summary_N] [protected_10K] [delta_msgs] │
│                                                          │
│  低水位（0 ~ 30K delta）：不问，纯吃 cache hit            │
│  ≥ 30K delta：定期询问 <agent:context_compaction_decision>│
│    LLM 回复 yes + summary → 写 journal delta_compact     │
│    LLM 回复 no → 隔 N 次工具调用再问                      │
│  ≥ 120K delta：硬阈值，直接注入 compaction 指令           │
│                                                          │
│  delta compaction 后：                                   │
│  [summary_1]...[summary_N][summary_{N+1}][new_protected] │
│                              ↑ delta 被压成 ~1-3K summary  │
│  新 delta 周期开始                                        │
│                                                          │
│  循环 ↺                                                  │
└─────────────────────────────────────────────────────────┘

兜底：heavyweight Compactor @ ~90% window（仅紧急）
```

## 与现有代码的关系

### 保留
- AgentContext 机制（LLMContext 字段保留但不主动写入，向后兼容旧 session）
- session/journal 持久化（session journal 新增 delta_compact entry type）
- tool output normalization（源头截断 10K chars）
- heavyweight Compactor（降级为兜底，阈值调高）

### 修改
- ContextManager → 废弃，被 delta compaction 内联方式取代
- ShouldCompact 触发逻辑 → 绝对值 delta tier + tool call interval
- session journal replay（buildSessionContext）→ 支持区间压缩模型（新旧格式共存）
- runtime_state telemetry → 加入 delta_since_compact 信号
- system prompt / AGENTS.md → 加入 context compaction 说明
- checkpoint guard → 不依赖 LLMContext，改为检查消息数量或 turn count
- heavyweight compaction 路径 → 触发后重置 TokensSinceLastDeltaCompaction

### 废弃/降级
- ContextManager 的独立 LLM call（方式 A）
- truncate_messages tool（不做独立 truncate）
- update_llm_context tool（delta summary 取代）
- LLMContext 主动写入（字段保留，不主动写入）
- heavyweight Compactor 的 75% 百分比阈值（调高为兜底）

### 持久化层（orchestrator R8 澄清）

delta_compact entry 写入 **session journal**（SessionEntry JSONL），新增 EntryType："delta_compact"：
```json
{
  "type": "delta_compact",
  "fromEntryId": "msg_042",
  "toEntryId": "msg_087",
  "summary": "LLM 生成的 summary 文本"
}
```

不走 context journal（messages.jsonl）。两种 entry type（EntryTypeCompaction 旧格式 + delta_compact 新格式）
可以在同一个 session journal 中共存。回放逻辑：先找最后一条旧格式 compaction（如果有），从
FirstKeptEntryID 开始；之后的 delta_compact entries 按 [fromID, toID] 区间替换。

### LLMContext 与 delta summary 的关系（orchestrator R15-R17 澄清）

在新架构中，delta summary 取代 LLMContext 作为任务状态的唯一来源。
- LLMContext 字段保留在 AgentContext 中，但不主动写入（向后兼容旧 session 加载）
- 现有 LLMContext 注入逻辑（`<llm_context>` 块）被多条 delta summary 消息取代
- 迁移时，已有的 LLMContext 内容转换为第一条 delta summary

## 参考

- [Draft v1](draft-v1.md) — 最初方案
- [Cross-project analysis](cross-project-analysis.md) — 四个项目（codex/claude-code/goose/pi）的上下文管理策略对比

## 附录：Orchestrator Review 关键澄清

以下实现细节在 orchestrator review（48 问 Q&A）中明确，不属于架构决策但影响实现：

- **Delta compaction 注入点**：agent loop 每轮末尾，processToolCalls + heavyweight check 之后、下一轮 LLM call 之前
- **compaction turn 行为**：算正常 agent turn（递增 turnCount、写 checkpoint、回复写入 RecentMessages、成为下次 delta 的一部分）
- **Session 恢复 delta 计算**：lazy，首次 delta check 时从最后一条 delta_compact entry 之后的消息估算（< 1ms）
- **旧格式 session 兼容**：回放逻辑先找旧格式 compaction entry，从 FirstKeptEntryID 开始；之后有新 delta_compact entries 则按区间替换。旧 entry 不需要迁移
- **delta tier 数值**：阶段 1 写死为常量，阶段 3 观察后可提取为可配置