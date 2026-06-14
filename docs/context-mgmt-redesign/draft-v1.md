# Context Management Redesign — Draft v1

> Status: 初稿，待 explore 结果后修订

## 背景

GLM5.2 从 200K → 1M context window。当前两套上下文管理机制的阈值按百分比缩放，
但管理成本按绝对值增长，导致：

1. compact 75% 阈值在大窗口下要堆到 ~700K 才触发，summarization 成本极高
2. ContextManager 频繁进入（20% = 200K 即起步），每次全量 LLM call
3. **缓存经济学灾难**：cache hit/miss 价差 50-120 倍，任何修改中间消息的操作
   都会导致后续所有 token cache miss

## 核心洞察

### 1. 阈值应为绝对值，非百分比

cache-hit 成本和上下文质量衰减由绝对 token 数决定，与 window 大小无关。
50K 在 200K 模型是 25%，在 1M 模型是 5%，但每 turn 成本相同。

### 2. Delta compaction：永远在最便宜的区间

不做全量 compaction（在 700K 时触发），改为 delta compaction：
每隔 ~50K 做一次增量压缩，把 delta 压成 ~2-5K 的 summary。
上下文永远在 2K~50K 之间振荡。

### 3. 缓存经济学：compact > truncate

- **truncate（改中间消息）**：miss penalty 正比于截断点之后的**剩余消息**（大）
- **compaction（全量压缩）**：miss penalty 正比于**压缩后大小**（小）

200K 上下文，truncate 一条 15K 消息，后面还有 120K：
回本需要 ~291 turn。

200K 压到 20K：回本只需 ~5 turn。

### 4. Truncate 的甜区很窄

在 delta 模型下，truncate 只有满足三个条件才划算：
1. 体积大（> 10K chars）
2. 位置在 delta 窗口的 30-60%（不太旧也不太新）
3. delta 周期早期（currentDelta < 60% × deltaInterval）

## 提案架构

```
0 ~ 50K（delta 间隔）:
  → 什么都不做，纯吃 cache hit
  → Truncate: 仅对甜区内的大输出，且周期早期

到 50K（触及 delta 间隔）:
  → delta compaction: [old_summary + 新增 delta] → new_summary (~2-5K)

接近 window 上限:
  → 更激进，缩小 delta 间隔（经济模型动态判定）
```

### 经济模型触发判据

```go
// compact 值得做当且仅当: 压缩后 miss 成本 < 压缩前 hit 成本
// s/S < 1/r （r = miss/hit 比值）
func shouldManageContext(currentTokens, estimatedCompactedTokens int, missHitRatio float64) bool {
    return float64(estimatedCompactedTokens) * missHitRatio < float64(currentTokens)
}
```

### Delta compaction 成本

- 每次处理 ~50K delta（不是 700K 全量）
- compaction LLM call 成本固定且低
- 缓存断裂仅 summary 部分（~2-5K），miss penalty 极小
- 每 turn 摊销：~75K hit单位（vs 全量 compact 的 ~1125K）

## 待定问题

- [ ] delta 间隔 50K 是否最优？是否应该自适应？
- [ ] summary 压缩到 2K vs 5K 的质量差异？
- [ ] truncate 在 delta 模型下是否值得保留？还是让 delta compaction 全包？
- [ ] 如何估算"压缩后大小"（在没实际压缩前）？

## 待 explore 验证

- codex / claude-code / goose / pi 的上下文管理策略
- 它们如何兼顾缓存命中率
- 它们如何处理上下文质量