# Context Management Redesign — Cross-Project Analysis

## 横向对比

### Q1: 何时 compact（阈值）

| 项目 | 阈值机制 | 基础 | 缓存感知 |
|------|---------|------|---------|
| **Codex** | 90% of context window（可配置，clamp 到 ≤90%）| 百分比 | ✅ `BodyAfterPrefix` 模式：扣减缓存 prefix baseline |
| **Claude Code** | contextWindow - 13K buffer tokens | **绝对值** | ✅ 多层 |
| **Goose** | 80%（可配置 `GOOSE_AUTO_COMPACT_THRESHOLD`）| 百分比 | ❌ compaction 时缓存断裂 |
| **Pi** | contextWindow - 16K reserveTokens | **绝对值** | ❌ compaction 时缓存断裂 |

**结论：Claude Code 和 Pi 用绝对值，验证了我们的判断。**
Codex 的 `BodyAfterPrefix` 模式（缓存 prefix 部分不计入预算）是一个值得借鉴的缓存感知设计。

### Q2: compact 做什么

| 项目 | 全量总结 | 增量/分层 | 工具输出处理 |
|------|---------|----------|------------|
| **Codex** | ✅ 全量替换（memento），丢弃所有工具 artifact | ❌ | 插入时截断 10K bytes×1.2 |
| **Claude Code** | ✅ autocompact 全量总结 | ✅ **6层流水线**：per-msg budget → snip → microcompact → collapse → autocompact → reactive | per-msg budget + 大结果落盘 + microcompact 清理 |
| **Goose** | ✅ 全量总结 | ✅ **后台 tool-pair 总结**（batch 10，从老到新，middle-out）| >200K chars 落盘 |
| **Pi** | ✅ 全量总结（迭代式：上次 summary → 更新）| ❌ | 工具级截断 2000行/50KB |

**关键发现：Claude Code 的 6 层流水线是业界最复杂的。Goose 的后台 tool-pair 总结是最接近我们 delta compaction 的设计。**

### Q3: 缓存命中率处理

| 项目 | 缓存策略 | 亮点 |
|------|---------|------|
| **Codex** | prompt_cache_key per thread，prefix cache window tracking | `BodyAfterPrefix` scope 扣减缓存 prefix |
| **Claude Code** | **最复杂** | cached microcompact（`cache_edits` API，清工具结果但保 prefix cache）；time-based MC 匹配 cache TTL（60min）；forked agent 共享 parent cache；cache-break 检测 |
| **Goose** | Anthropic cache_control（system + last 2 msgs + last tool spec）| rolling 2-checkpoint，但 compaction 时断裂 |
| **Pi** | cache_control on system + last tool + last msg | session affinity headers；compaction 时故意断裂 |

**关键发现：**
1. **Claude Code 的 time-based microcompact 是天才设计**：60 分钟 idle 后清理工具结果——此时缓存已过期，清理是"免费"的。
2. **Claude Code 的 `cache_edits` API**：用 Anthropic 专有 API 在不清空 prefix 的情况下清除旧工具结果。这是终极的缓存友好方案，但依赖 Anthropic 专有 API。
3. **Goose 的 tool-pair summarization 在后台执行**，不阻塞主循环，用 fast model。这是 delta compaction 的实际实现。

### Q4: 状态保留（compact 后如何保持任务连续性）

| 项目 | 保留内容 |
|------|---------|
| **Codex** | summary text + 最近用户消息（20K local / 64K remote）+ 增量 settings diff |
| **Claude Code** | session memory files + 最近读取的文件（token budget）+ skills + plans + tool listings |
| **Goose** | 结构化 summary：用户意图、技术概念、文件+代码、错误+修复、待办、当前工作、下一步 |
| **Pi** | 结构化 summary + 保留最近 20K tokens 原始消息 + 文件操作追踪 |

**关键发现：Claude Code 的 post-compact restoration 最成熟——每类附件有独立 token 预算（5K/skill, 25K总, 5K/file, 50K总）。**

---

## 对我们方案的影响

### 验证了的判断

1. ✅ **绝对值阈值**：Claude Code 和 Pi 都用绝对值（contextWindow - buffer），不是百分比
2. ✅ **多层策略**：业界都在用多层流水线（Claude Code 6 层，Goose 2 层），单靠全量 compact 不够
3. ✅ **增量/delta 方向正确**：Goose 的 tool-pair summarization 和 Pi 的 iterative summary 都验证了增量方向

### 需要修正的认知

1. ⚠️ **truncate 在 delta 模型下价值更低**：Claude Code 用 `cache_edits` API 清理工具结果（不破坏 prefix）；Goose 用后台 tool-pair summarization。我们没有这些 API，但 delta compaction 可以吸收 truncate 的功能——如果 delta 周期内的大工具输出在下次 delta compaction 时会被压缩，单独做 truncate 的收益就更小了（还破坏缓存）。

2. ⚠️ **cache TTL 是一个关键变量**：Claude Code 在 cache 过期后才做 microcompact（60min idle）。我们之前完全没考虑 cache TTL——如果缓存过期了，清理工具结果是"免费"的。GLM5.2 的缓存 TTL 是多少？这直接影响策略。

3. ⚠️ **后置 restoration 非常重要**：四个项目都在 compact 后做了精细的状态恢复（重新注入文件、skills、plans）。我们的 LLMContext 机制需要增强——不只是文本 summary，而是结构化的任务状态。

### 新发现的可借鉴设计

| 设计 | 来源 | 对我们的价值 |
|------|------|-------------|
| **Cache TTL aware timing** | Claude Code | 如果缓存已过期，清理操作零成本 |
| **后台增量 tool-pair summarization** | Goose | delta compaction 的实际可行实现 |
| **iterative summary（上次 summary → 更新）** | Pi | delta compaction 的状态传递方式 |
| **per-category restoration budget** | Claude Code | compact 后精细的状态恢复 |
| **BodyAfterPrefix（缓存 prefix 不计入预算）** | Codex | 缓存感知的阈值计算 |
| **visibility metadata（不删消息，只标记不可见）** | Goose | 类似我们的 sliding window offset |