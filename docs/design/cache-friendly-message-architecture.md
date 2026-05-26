# Design: Cache-Friendly Message Architecture

## Problem

当前 ai agent 使用 DeepSeek 等 prefix-caching provider 时，缓存命中率接近 **0%**。

### Cache Hit 的数学模型

LLM prefix cache 要求每次请求的**字节前缀完全一致**。匹配从第一条消息开始，逐 token 向后。一旦遇到不一致，该点之后全部 cache miss。

```
请求 = [System Prompt] [msg₁] [msg₂] ... [msgₙ]
                 ↑                    ↑
           一定命中              从此处开始可能断裂
```

DeepSeek v4-flash 价格差：
- 命中缓存：$0.004/百万 token
- 未命中：$0.14/百万 token（**35 倍**）

### 为什么当前命中率为 0%

`runtime_state` 消息每轮重新计算，注入到 `[]llm.LLMMessage` 的**临时副本**上，不持久化到 session journal。下一轮完全重建：

```
Turn 1 API: [system][runtime₁][user₁]
Turn 2 API: [system][user₁][asst₁][runtime₂][user₂]   ← runtime₁ 消失了
Turn 3 API: [system][user₁][asst₁][user₂][asst₂][runtime₃][user₃]  ← runtime₂ 也消失了
```

`runtime₁` 不是「变了」，而是**消失了**。下一轮的前缀从 `user₁` 开始就跟上一轮不同，导致系统提示词之后立刻断裂。

### 排除的伪问题

经过 brainstorm 确认，以下问题**不存在**或**不值得优化**：

| 原判定 | 实际情况 |
|--------|---------|
| ~~消息时间戳~~ | `time.Now().UnixMilli()` 写入 `AgentMessage.Timestamp`，但 `ConvertMessagesToLLM()` 转成 `LLMMessage` 时 timestamp 不参与 API 序列化。**非问题。** |
| ~~Tool Call ID~~ | ID 首次生成后写入 Content blocks，持久化到 session journal，后续轮次读取存储值不重新生成。**非问题。** |
| ~~Session Resume~~ | 跨 session 的 cache 在 provider 端早已冷掉，优化无意义。**过度优化。** |
| ~~异常路径消息~~ | Malformed recovery 和 loop guard 消息已持久化到 RecentMessages，下一轮作为历史带上。**已正确。** |

### 真正的核心矛盾

**缓存优先 vs 上下文优先**是结构性对立：

| 缓存优先 | 上下文优先 |
|---------|-----------|
| 所有消息追加、永不修改、永不删除 | truncate 冗余 tool output |
| runtime_state 持久化保留 | compact 摘要替换旧消息 |
| 前缀稳定 → 命中 → 省钱 | 上下文精简 → 任务质量高 |

两者不是调参数能调和的。解决方案是**双模式配置 + 实验验证**。

---

## Design

### 核心原则

> **一旦消息发送给 LLM，就固化下来，后续不修改、不删除、不移位。**

违反此原则就会破坏前缀缓存。但上下文管理（truncate、compact）本质上就是要修改/删除历史消息。所以两种模式分开：

### 双模式

```
Cache-First Mode (DeepSeek 等 prefix-caching provider):
  - runtime_state 持久化追加到 RecentMessages
  - 不主动 truncate tool output（除非超大影响 token 预算）
  - compaction 只在必要时触发，接受 cache miss
  - compaction 时顺便清理旧的 runtime_state（反正已经 miss 了）

Context-First Mode (GLM coding plan 等固定费用 provider):
  - runtime_state 随用随弃（当前行为）
  - truncate / llm_context_update / compact 积极执行
  - 上下文精简优先
```

### 自动检测

基于 `--model` 参数自动选择模式：
- DeepSeek 模型 → cache-first
- 其他 → context-first（默认，兼容当前行为）
- 用户可通过配置覆盖

---

### 核心改动：Runtime State 持久化（Cache-First Mode）

**当前行为**（context-first）：
```
injectRuntimeMeta() 计算 runtime_state
  → 注入到 []llm.LLMMessage 临时副本
  → insertBeforeLastUserMessage()
  → 不持久化，下一轮丢弃
```

**新行为**（cache-first）：
```
injectRuntimeMeta() 计算 runtime_state
  → 创建 AgentMessage{Role: "user", Kind: "runtime_state", Visibility: agent-only}
  → 追加到 agentCtx.RecentMessages（持久化）
  → 下一轮作为历史消息带上，位置不变
```

消息流：
```
Turn 1: RecentMessages = [..., runtime_msg₁, user₁]
        API: [system][...][runtime_msg₁][user₁]
        LLM 回复 → asst₁

Turn 2: RecentMessages = [..., runtime_msg₁, user₁, asst₁, runtime_msg₂, user₂]
        API: [system][...][runtime_msg₁][user₁][asst₁][runtime_msg₂][user₂]
        前缀: [system][...][runtime_msg₁][user₁][asst₁] ✅ 完全一致
```

#### 关键设计决策

**消息 Role**：`user`，与当前一致。因为：
- 在 user message 之前，LLM 自然聚焦最后的 user message
- 不是独立消息在末尾，不存在「LLM 以为要回复」的问题

**消息 Visibility**：`agent-visible, user-hidden`。用户在 TUI 不需要看到重复的 runtime_state。

**CWD 传递**：workspace/cwd 信息通过 runtime_state 传递，这是唯一来源（system prompt 中故意不硬编码 cwd）。持久化不影响 CWD 变更场景 — 新的 cwd 写入新的 runtime_msg，旧的不变。

**Compaction 交互**：compaction 时可以清理所有旧的 runtime_msg（反正 compaction 已经 cache miss 了）。只保留最新的 runtime_msg 作为 compaction 后的起始状态。

#### Token 开销

每条 runtime_msg ~200-400 tokens。10 轮后多 ~3000 tokens。
相比缓存命中的节省（35 倍价差），这是值得的。

---

### 不需要改动的部分

以下行为经验证已对缓存友好，不需要修改：

| 行为 | 原因 |
|------|------|
| Tool definitions | 静态注册，session 内不变 |
| System prompt 模板 | session 内稳定 |
| Skill index | 进程启动后固定，不变化 |
| Tag parser (XML tool call) | 原地修改 assistant 消息，不追加 |
| Tool output truncation | 原地修改，不追加 |
| Malformed tool call recovery | 追加并持久化到 RecentMessages ✅ |
| Loop guard feedback | 追加并持久化到 RecentMessages ✅ |

---

## Known Limitations: Context Management vs Cache

当前 context management 架构（见 `docs/context-management.md`）的核心设计是 **LLM 驱动的上下文管理** — 系统决定时机，LLM 决定策略。LLM 通过 4 个工具操作历史消息：

| 工具 | 操作 | 缓存影响 |
|------|------|---------|
| `truncate_messages` | 删除中间消息 | 从删除点开始前缀断裂 |
| `update_llm_context` | 修改 LLMContext 内容 | 持久化的 runtime_msg 中 `<llm_context>` 块字节变化 |
| `compact` | 替换全部消息为 summary | 前缀完全归零 |
| `no_action` | 不动 | ✅ 唯一缓存友好 |

三个工具都是破坏性的 — 不是内容微调，而是消息数组结构本身被改变。

### 自加速问题

Cache-first 模式下存在正反馈循环：

```
runtime_state 持久化 → token 消耗更快
→ 更快触发 20%/33%/50% 阈值
→ 更频繁触发 context management cycle
→ LLM 更频繁调用 truncate/compact
→ 更频繁破坏缓存
→ 缓存命中率反而下降
```

### 当前处理方式

本设计**不修改 context management 架构**：

1. **Cache-first 模式下**：接受 context management 带来的 cache miss。Compaction 和 truncate 是低频事件，每次发生接受一次 miss，之后前缀重新建立。
2. **Context-first 模式下**：行为完全不变。

### 待未来演化的问题

以下问题不在本次 scope 内，但接口设计必须保留演化能力：

#### 问题 1：`update_llm_context` 与 runtime_msg 稳定性矛盾

`<llm_context>` 内容由 LLM 维护，每次 update 后写入新的 runtime_msg。旧 runtime_msg 中的 `<llm_context>` 与新 runtime_msg 中的不同 — 两条消息的字节不同，这是正确的（前缀稳定）。但如果 update 只修改旧 runtime_msg 而不追加新的，就会破坏已有前缀。

**演化方向**：`llm_context` 作为独立的消息类型，而非嵌入 runtime_msg。或者 `llm_context` 只追加不修改。

#### 问题 2：`truncate_messages` 与前缀单调增长矛盾

删除中间消息 = 前缀从删除点断裂。cache-first 理想下不应删除任何已发送的消息。

**演化方向**：用「软删除」（visibility 标记）替代真删除。`IsAgentVisible()` 已有过滤机制，可复用。

#### 问题 3：Compact 与前缀完全归零

Compact 替换全部历史为 summary，是最激进的上下文管理操作。

**演化方向**：改为 Reasonix 式的「尾部替换」— 只替换 Region 2 的后半部分，保留前半部分不动。

#### 问题 4：Context management 触发频率

持久化 runtime_state 增加每轮 token 消耗，可能加速触发阈值。

**演化方向**：cache-first 模式下，阈值计算排除 runtime_state 消息的 token。

---

## Interface Design for Evolvability

### MessageMutationPolicy

所有消息变更操作通过统一接口，Cache policy 可以拦截或调整：

```go
// MessageMutationPolicy — 消息变更策略接口
// 不同 cache mode 提供不同实现
type MessageMutationPolicy interface {
    // 是否允许删除指定消息
    CanTruncate(messages []AgentMessage, indices []int) (allowed []int, denied []int)
    // 是否允许修改指定消息
    CanModify(messages []AgentMessage, index int) bool
    // 是否允许 compact（替换全部消息）
    CanCompact(messages []AgentMessage) CompactDecision
    // Runtime state 注入方式
    RuntimeStateStrategy() RuntimeStateStrategy
}

type RuntimeStateStrategy int
const (
    RuntimeStateEphemeral  RuntimeStateStrategy = iota // 随用随弃（context-first）
    RuntimeStatePersist                                  // 持久化追加（cache-first）
)

type CompactDecision int
const (
    CompactAllow    CompactDecision = iota // 允许
    CompactDefer                           // 延迟到更紧急时
    CompactDeny                            // 拒绝，改用其他策略
)
```

**当前 Phase 1 实现**：
- Context-first policy：`CanTruncate` 全部允许，`RuntimeStateStrategy()` 返回 `Ephemeral`
- Cache-first policy：`CanTruncate` 全部允许但记录影响，`RuntimeStateStrategy()` 返回 `Persist`

**未来演化**：
- Cache-first policy 可以演进为：`CanTruncate` 返回 denied，建议软删除
- `CanCompact` 可以返回 `CompactDefer`，推迟 compaction
- 不需要修改 context management 核心逻辑，只换 policy 实现

### Context Management Tool Availability

Context management 工具通过 `ToolRegistry` 注册，cache policy 可以调整哪些工具可用：

```go
// cache-first 模式下可以：
registry.Disable("truncate_messages")
// 或保持启用但通过 MutationPolicy 拦截
```

未来实验可以测试「禁用 truncate 对 cache hit rate 和任务通过率的影响」。

### 配置驱动的行为选择

```yaml
cache:
  mode: auto  # auto | cache | context

  # Phase 1: 只控制 runtime_state
  persist_runtime_state: true

  # Phase 2+: 控制 context management 行为（预留，当前不实现）
  # context_management:
  #   allow_truncate: true        # false = truncate 工具不可用
  #   allow_compact: true         # false = compact 工具不可用
  #   soft_delete: false          # true = 用 visibility 替代真删除
  #   compact_threshold_modifier: 1.0  # >1.0 = 提高 compact 触发阈值
```

Phase 2+ 的配置项预留但不实现，避免过度设计。等 evolve 实验证明需要时再逐个打开。

---

## 实验验证框架

### 指标

| 指标 | 数据来源 | 说明 |
|------|---------|------|
| **Cache hit rate** | API response `usage.cache_read_input_tokens / usage.prompt_tokens` | 核心目标 |
| **Cost per task** | `usage.total_tokens × pricing` | 省钱效果 |
| **Task pass rate** | Benchmark 结果 | 上下文质量是否退化 |
| **Agentic score** | Benchmark 评分 | 细粒度质量 |

### Evolve 集成

`evolve-config.yaml` 增加：
```yaml
evolve:
  axes:
    - name: cache_mode
      values: ["cache", "context"]
    - name: runtime_state_content
      values: ["full", "minimal"]
    - name: clean_runtime_on_compact
      values: [true, false]
```

Evolve planner 可探索配置组合，观察：
1. cache 模式下任务通过率是否退化
2. 是否存在「缓存友好且上下文更好」的配置组合（理想情况）
3. 退化幅度 vs 节省金额的 trade-off

### 对比矩阵

| 实验 | 模型 | 模式 | 预期 |
|------|------|------|------|
| Baseline | GLM | context | 当前行为基准 |
| Cache baseline | DeepSeek | context | 验证当前 DeepSeek 下的 0% 命中率 |
| Cache optimized | DeepSeek | cache | 验证缓存命中率提升 |
| Cross-check | GLM | cache | 验证 GLM 下缓存模式不退化 |
| Cross-check | DeepSeek | context | 对比 cache 模式的收益 |

---

## Acceptance Scenarios

### AS-1: Cache-first 模式下 runtime_state 持久化

**Given** `cache.mode: cache` 且使用 DeepSeek 模型
**When** 执行 3 轮对话
**Then**:
- 第 2 轮 API 请求中，前缀 `[system][runtime_msg₁][user₁][asst₁]` 与第 1 轮 API 请求中对应部分**字节一致**
- 第 3 轮 API 请求中，前缀包含第 1-2 轮的所有历史消息，且**字节一致**
- `RecentMessages` 中包含 `Kind: "runtime_state"` 的持久化消息

**验证方法**：
- 单元测试：mock `AgentContext`，执行 3 轮 `streamAssistantResponse`，序列化每轮的 `[]llm.LLMMessage`，对前 N-2 条消息做 `bytes.Equal`
- 集成测试：使用 mock provider 记录每次请求，断言前缀一致性

### AS-2: Context-first 模式下行为不变

**Given** `cache.mode: context`（或 auto + 非 DeepSeek 模型）
**When** 执行任意对话
**Then**: 行为与当前完全一致 — runtime_state 不持久化，`insertBeforeLastUserMessage`

**验证方法**：
- 单元测试：断言 context 模式下 `RecentMessages` 中不包含 `Kind: "runtime_state"` 的消息
- Evolve benchmark：运行现有 baseline 任务，通过率不低于当前 baseline

### AS-3: Auto 模式正确检测

**Given** `cache.mode: auto`
**When** model 为 "deepseek-chat" / "deepseek-reasoner" 等
**Then**: 自动选择 cache 模式
**When** model 为 "glm-4" 或其他
**Then**: 自动选择 context 模式

**验证方法**：单元测试，覆盖各种 model 名称

### AS-4: Compaction 清理 runtime_state

**Given** cache 模式，RecentMessages 中有 5 条 runtime_state 消息
**When** compaction 触发
**Then**: 旧的 runtime_state 消息被清理，只保留最新一条（或全部清理，compaction summary 中包含 cwd）

**验证方法**：单元测试，检查 compaction 后 RecentMessages 中的 runtime_state 数量

### AS-5: 缓存命中率可量化

**Given** cache 模式 + DeepSeek 模型
**When** 运行 10 轮对话
**Then**: 从 API response `usage` 中可提取 `cache_read_input_tokens`，缓存命中率 > 50%

**验证方法**：
- Mock provider 在 response 中返回 `usage.cache_read_input_tokens` 字段
- Agent 统计模块记录每轮的 cache hit/miss，计算累计命中率
- 输出到 benchmark result JSON

### AS-6: 不引入 regression

**Given** 任何 cache.mode 设置
**When** 运行 evolve benchmark 全部任务
**Then**: 通过率不低于当前 baseline

**验证方法**：
- `make evolve-run` 对比 baseline
- 新增 cache 相关 axis 到 evolve config

---

## Implementation Order

```
Phase 1: 核心改动
  ├── 定义 cache mode 配置（auto/cache/context）
  ├── 定义 MessageMutationPolicy 接口（最小实现：只控制 runtime_state）
  ├── 实现 auto 检测逻辑（model name matching）
  ├── Cache 模式：runtime_state 持久化追加
  ├── Context 模式：保持当前行为
  └── 确保两种模式通过同一代码路径，只是分支不同

Phase 2: 测试
  ├── AS-1 ~ AS-4 单元测试
  ├── AS-5 集成测试（mock provider + cache 统计）
  └── AS-6 evolve baseline 对比

Phase 3: 可观测性
  ├── 从 API response 提取 cache 统计到 benchmark result
  ├── TUI 展示缓存命中率（cache 模式下）
  └── Evolve 集成

Phase 4: 实验驱动优化（后续迭代）
  ├── runtime_state 内容精简（minimal mode）
  ├── compaction 时 runtime_state 清理策略
  ├── MessageMutationPolicy 完整实现（truncate 拦截、soft delete 等）
  ├── 多配置组合的 evolve 实验
  └── 探索是否存在 cache + context 兼得的方案
```

---

## Risks

| 风险 | 缓解 |
|------|------|
| Cache 模式下上下文冗余影响任务质量 | Evolve baseline 对比；如有退化可调参数 |
| runtime_state 持久化增加 token 消耗 | 约每轮 +300 tokens，但缓存节省远大于此 |
| CWD 变更场景下旧 runtime_state 中的 cwd 过时 | LLM 以最新 runtime_state 为准；compaction 时清理 |
| 双模式增加代码复杂度 | 共享代码路径，只分支持久化逻辑；默认 context 模式不改变任何行为 |
| Cache 模式下 truncate/compact 与缓存矛盾 | 已知限制，见 Known Limitations 章节；通过 MessageMutationPolicy 接口保留未来演化能力 |
| 自加速问题（持久化加速触发阈值） | 已知限制，Phase 4 可通过阈值调整解决 |