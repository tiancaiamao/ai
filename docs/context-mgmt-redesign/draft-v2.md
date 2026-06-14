# Context Management Redesign — Final Draft v2

> Status: 终稿，待 grill-me 精确化
> 基于 draft-v1 + cross-project-analysis + 讨论修正

## 背景

GLM5.2 从 200K → 1M context window。当前两套上下文管理机制的阈值按百分比缩放，
但管理成本（LLM call + 缓存失效）按绝对值增长。

核心矛盾：阈值是百分比，成本是绝对值。

## 问题诊断

### 问题 1：compact 75% 阈值在大窗口下过高

1M window 的 75% = ~700K tokens。在此触发全量 summarization：
- 输入巨大，延迟/成本/质量恶化
- 全量重算的 cache miss 极贵

### 问题 2：ContextManager 频繁进入且每次全量

20% = 200K 即开始触发，每次 LLM call 发送全量对话。
长任务几百次 tool call，触发几十次，每次 O(全量) 开销。

### 问题 3：缓存经济学灾难

cache hit/miss 价差 50-120 倍。
任何修改中间消息内容的操作（如 truncate_messages 原地替换），
都导致截断点之后所有 token cache miss。

### 问题 4：truncate 的缓存经济性

修正了之前错误的分析：truncate 的 miss penalty 是**一次性**的（新 prefix 下个 turn 重建缓存），
不是每 turn 重复支付。但 truncate 中间消息的 miss penalty 正比于截断点之后的剩余消息量，
位置越靠前代价越大。

## 设计原则

### 原则 1：阈值用绝对值，不用百分比

cache-hit 成本和上下文质量衰减由绝对 token 数决定，与 window 大小无关。
50K 在 200K 模型是 25%，在 1M 模型是 5%，但每 turn 的 cache-hit 成本相同。
（Claude Code 和 Pi 都用绝对值，验证了此判断。）

### 原则 2：delta compaction 为主线

不做全量 compaction（在 700K 时触发），改为 delta compaction：
每隔约 50K 做一次增量压缩，把 delta 压成约 2-5K 的 summary。
上下文永远在 2K~50K 之间振荡，永远在最便宜的区间。

每次 delta compaction 的输入是：
  [previous_summary (2-5K)] + [新增 delta 消息 (50K)]
输出是：
  [updated_summary (2-5K)] + [保留的最近消息 (protected)]

这是 iterative summary（参考 Pi 的 UPDATE_SUMMARIZATION_PROMPT 方式）。

### 原则 3：经济模型触发判据

```
compact 值得做当且仅当：压缩后 miss 成本 < 压缩前 hit 成本
  s × r < S   （r = miss/hit 比值，s = 压缩后大小，S = 压缩前大小）
  即 s/S < 1/r
```

r=50 时，压缩比 < 2% 就在第一个 turn 回本。
10:1 压缩比（50K→5K）约 5-6 个 turn 回本。

### 原则 4：truncate 保留，作为 delta 窗口内的局部优化

truncate 的 miss penalty 是一次性的（新 prefix 下个 turn 重建缓存）。
在 delta 模型下，delta 窗口小（50K），miss penalty 被窗口大小封顶。

truncate 值得做的条件（三个 AND）：
1. 体积大（tool output ≥ 10K chars）——省的 cache-hit token 值得
2. 位置在 delta 窗口后半段——miss penalty 小
3. delta 周期早期（currentDelta < 60% × deltaInterval）——离下次 delta compaction
   还有足够 turn 来摊销 miss penalty

### 原则 5：放弃 cache TTL aware timing

Claude Code 在缓存过期（60min idle）后才做 microcompact。
我们的场景是长任务持续执行，cache 一直在被命中，不会过期。此设计不适用。

## 架构

```
┌─────────────────────────────────────────────────┐
│  Delta Window (0 ~ 50K)                          │
│                                                  │
│  [summary_prev] [msg1] [msg2] ... [msgN]        │
│                                                  │
│  低水位（0 ~ 50K）：什么都不做，纯吃 cache hit    │
│  Truncate: 仅对甜区内大输出 + 周期早期            │
│                                                  │
│  触及 50K（delta 间隔）：                         │
│  → delta compaction:                             │
│    [summary_prev + 全部 delta] → [summary_new]    │
│  → 新 delta 周期开始                              │
│                                                  │
│  接近 window 上限：                               │
│  → 更激进，缩小 delta 间隔（经济模型动态判定）     │
│                                                  │
│  循环 ↺                                          │
└─────────────────────────────────────────────────┘
```

### 数据流

```
Turn N（delta compaction 触发）:
  输入: [LLMContext_prev (2-5K)] + [delta_msgs (50K)]
  LLM call: 总结以上内容 → LLMContext_new (2-5K)
  输出: [LLMContext_new] + [recent_protected_msgs]
  AgentContext.RecentMessages 被替换
  缓存断裂: 仅 summary prefix 部分（小），miss penalty 可控

Turn N+1 ~ N+M:
  [LLMContext_new] + [new_msgs...]
  缓存命中: LLMContext_new 部分命中（稳定 prefix）
  只有新增消息是 miss
  成本: ~avg(2K~50K) × hit_price
```

### delta 间隔的选择

delta 间隔（~50K）的选择依据：
- 不能太小：delta compaction 太频繁，每次都有 LLM call 成本 + 一次 miss penalty
- 不能太大：单次 delta compaction 的输入大，且上下文长时间偏高
- 50K 对应：旧 200K 模型满载的 25%，合理的工作上下文量

是否应该自适应：
- 初期用固定 50K
- 后续可根据 economic model 动态调整

### summary 质量保障

LLMContext 的 summary prompt 应明确要求保留：
- 用户意图和当前任务目标
- 正在编辑的文件和未解决的错误
- 已做的关键决策和原因
- 当前计划和下一步
- 关键的技术约束

（参考 Goose 的结构化 summary prompt + Claude Code 的 per-category restoration 思路）
但不做代码级的 post-compact restoration（如重新注入文件），靠 summary prompt 质量。

## 与现有代码的关系

### 保留

- AgentContext / LLMContext 机制
- session/journal 持久化
- truncate_messages tool（但触发逻辑改变）
- LLM-driven 决策（让 LLM 判断哪条消息有用/没用）

### 修改

- ContextManager: 从"全量对话 LLM call"改为"delta + prev_summary LLM call"
- ShouldCompact 触发逻辑: 从百分比+interval改为绝对值+经济模型
- truncate 触发逻辑: 加入 delta 窗口位置 + 周期早期判断

### 废弃/降级

- heavyweight Compactor 的 75% 百分比阈值: 被 delta compaction 取代，
  仅保留为兜底（接近硬 window 上限时的紧急压缩）

## 待 grill-me 精确化的问题

- [ ] delta 间隔 50K 是否最优？固定值 vs 自适应？
- [ ] summary 压缩到 2K vs 5K 的质量/成本平衡点？
- [ ] truncate 是否应该被 delta compaction 完全吸收？还是保留为独立机制？
- [ ] 如何在 delta compaction 之前估算压缩后大小（s）？
- [ ] protected recent messages 保留多少条/多少 token？
- [ ] delta compaction 的 LLM call 用什么 model？主模型还是 fast model？
- [ ] 接近 window 上限时的"更激进"策略具体是什么？
- [ ] 首次 compaction（没有 prev_summary）怎么处理？