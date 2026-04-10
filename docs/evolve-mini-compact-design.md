# Mini Compact 自进化框架 — 完整设计

## 核心理念

**让 LLM 替代人工迭代。** LLM 自己改代码、改 prompt、改工具，自己测试评分，自己找方向。

人的角色：搭框架 + 定义测试集 + 定义评分标准。
LLM 的角色：改代码 → 编译 → 跑测试 → 看分数 → 想新的改进方向 → 再改代码。

## 架构

```
┌──────────────────────────────────────────────────────────────────────┐
│  evolve-mini run                                                     │
│                                                                      │
│  ┌─────────────────┐                                                │
│  │ Snapshot Suite   │  ← 一组有代表性的 test case（~10-20 个）       │
│  │ (diverse cases)  │    每个都有：上下文快照 + 后续任务 + 正确答案   │
│  └────────┬────────┘                                                │
│           │                                                          │
│           ▼                                                          │
│  ┌─────────────────┐    ┌──────────────────┐    ┌────────────────┐  │
│  │  Worker 二进制   │    │  LLM Judge 评分   │    │ Optimize Agent │  │
│  │  (每代编译)      │    │  (每个 case 评分)  │    │ (分析分数+改代码)│  │
│  │                  │    │                    │    │                │  │
│  │  对 suite 中每个  │───>│  汇总为：          │───>│  看整体表现     │  │
│  │  snapshot 执行   │    │  综合分 + 分维度   │    │  找弱点         │  │
│  │  compact         │    │  + 逐 case 分析   │    │  改代码         │  │
│  └─────────────────┘    └──────────────────┘    └───────┬────────┘  │
│           ▲                                              │           │
│           │          ┌──────────────────┐                │           │
│           │          │  Git Worktree    │◄───────────────┘           │
│           │          │  隔离每代代码     │                            │
│           └──────────│                  │                            │
│                      └──────────────────┘                            │
└──────────────────────────────────────────────────────────────────────┘
```

## Snapshot Suite = 一组有代表性的 test case

不是单个 case 的得分，而是一组 case 的**综合表现**。

### 为什么必须是 suite？

1. **防过拟合**：在某个 case 上刷高分没意义，可能换个场景就崩了
2. **体现多样性**：不同类型的 session 考验 compact 的不同能力
3. **暴露弱点**：一个 case 上得分低可能只是偶发，但多个类似 case 都低就是系统性问题

### Case 分类（追求多样性）

| 类型 | 特征 | 考验什么 |
|------|------|----------|
| **大量探索输出** | ls/cat/find/grep 多，tool output 大 | truncate 是否敢于截断 |
| **深层调试** | 反复读同一批文件，追踪 bug | 是否识别出哪些输出已过时 |
| **多文件重构** | 涉及 10+ 文件，决策链长 | 关键决策是否保留 |
| **短对话大输出** | 消息不多但每次工具输出巨大 | 是否精确选择截断目标 |
| **长对话累积** | 100+ 轮，上下文缓慢膨胀 | LLMContext 是否准确概括 |
| **多任务切换** | 用户中途换了话题 | 是否识别出过时上下文 |
| **约束密集** | 用户提了很多具体要求 | 是否完整保留约束条件 |
| **Code Review** | 大量 diff 输出 | 是否只保留关键 diff |

### Snapshot 结构

```go
type Snapshot struct {
    ID             string
    Description    string            // 一句话描述这个 case 的特征
    Tags           []string          // 分类标签
    
    // 完整的 AgentContext 状态（compact 触发时点）
    RecentMessages []AgentMessage
    LLMContext     string
    AgentState     *AgentState
    ContextWindow  int
    
    // 评估锚点
    FollowUpTask   string            // 接下来用户会问什么
    FollowUpAnswer string            // 正确执行需要保留的关键信息
    
    // 元数据
    SourceSession  string
    ExtractedAt    string
}
```

### Snapshot 提取策略

从 session JSONL 中提取多个时点的快照：

1. **选取候选 session**：30+ 条消息，50KB+，有大的 tool output
2. **滑动窗口采样**：在一个 session 内可以提取多个 snapshot（不同上下文压力阶段）
3. **后续消息作为锚点**：snapshot 之后的 3-5 条消息自动作为 FollowUpTask/Answer
4. **人工/LLM 标注**：最终由人或 LLM 标注 description 和 tags

**目标：提取 10-20 个 case，覆盖上述 8 种类型。**

## 评分 = Suite 级别的综合评判

### 每个 Case 的评分

对每个 snapshot，Worker 执行 compact 后，LLM Judge 看：

```
输入:
1. 原始上下文（compact 前）— RecentMessages + LLMContext
2. compact 后的上下文 — RecentMessages + LLMContext
3. 后续任务 + 关键信息 — FollowUpTask + FollowUpAnswer
4. 执行日志 — 调了什么工具，参数是什么
```

5 个维度打分（1-5）：

| 维度 | 权重 | 评估什么 |
|------|------|----------|
| 信息保留 | 3x | 关键信息还在吗？ |
| 任务可执行性 | 3x | 能继续执行后续任务吗？ |
| 决策正确性 | 2x | truncate 选对了吗？ |
| 上下文准确性 | 2x | LLMContext 准确吗？ |
| token 效率 | 1x | 节省了多少？ |

### Suite 级别的汇总

**核心原则：不只看平均分，要看分布。**

```go
type SuiteScore struct {
    // 总分
    WeightedAverage float64         // 所有 case 的加权平均
    
    // 分维度汇总
    DimensionScores map[string]float64  // 每个维度跨 case 的平均分
    
    // 分布分析（暴露弱点）
    CaseScores      []CaseScore     // 每个 case 的详细分数
    WorstCases      []string        // 得分最低的 3 个 case（告诉 Optimize Agent 哪里弱）
    BestCases       []string        // 得分最高的 3 个 case
    
    // 一致性
    StdDev          float64         // 分数标准差（越大越不稳定）
    MinScore        float64         // 最低分（不能有灾难性 case）
    
    // 趋势（与上一代对比）
    DeltaVsBaseline float64         // 相对 baseline 的变化
    ImprovedCases   int             // 改善的 case 数
    RegressedCases  int             // 退步的 case 数
}
```

### 什么时候算"更好"？

不是简单地 avg_score > best_score，而是：

1. **加权总分更高** AND
2. **最低分不低于 baseline 的最低分**（不引入新的灾难性退化）AND
3. **退步 case ≤ 2**（不能大面积退步）

如果只满足部分条件，记录但不采纳，给 Optimize Agent 反馈。

## 候选 = 完整的代码实现（git worktree 隔离）

每一轮进化，Optimize Agent 可以修改任何东西：

| 可修改项 | 对应文件 |
|----------|----------|
| System prompt | `pkg/prompt/llm_mini_compact_system.md` |
| Compact 执行逻辑 | `pkg/compact/llm_mini_compact.go` |
| 工具定义和实现 | `pkg/tools/context_mgmt/*.go` |
| 消息构建方式 | `buildContextMgmtMessages()` 等 |
| 阈值常量 | 各种 const |
| 新增工具 | 新文件 |
| 执行流程 | 多轮/单轮/条件分支 |

**隔离方式**：每代一个 git worktree，互不干扰，可并行。

## 进化循环

```
初始化:
  提取 snapshot suite (10-20 cases)
  baseline = 当前代码 (generation_0)
  baseline_score = 跑 suite + judge → SuiteScore

循环 (generation = 1, 2, 3, ...):

  1. Optimize Agent 分析
     输入:
       - baseline 的 SuiteScore（每个 case 的详细分数 + judge 反馈）
       - 最差的 3 个 case 的完整上下文
       - 当前代码的关键文件
     输出:
       - 改进思路（一句话）
       - 具体的代码修改（完整文件内容）
  
  2. 创建 git worktree: generation_{N}
     写入 Optimize Agent 的代码修改
     go build 验证
  
  3. 如果编译失败:
     反馈编译错误给 Optimize Agent，回到步骤 1
     （最多重试 3 次，超过则跳过此方向）
  
  4. 跑 suite
     对每个 snapshot:
       复制 AgentContext 快照
       用新编译的 worker 二进制执行 compact
       收集: compact 前后上下文 + 执行日志
  
  5. LLM Judge 评分
     对每个 case 的结果，调 LLM judge 打分
     汇总为 SuiteScore
  
  6. 比较
     如果优于 baseline（综合分更高 + 无灾难退化）:
       标记为 new_best
       baseline = 此代代码
       baseline_score = 此代分数
     否则:
       标记为 failed
       记录退步原因
  
  7. 为下一轮准备
     把 SuiteScore 详细反馈（逐 case 分析 + 分维度分析 + 退步 case）
     + 当前 best 代码
     交给 Optimize Agent
  
  8. 停止条件
     连续 5 轮无改进 → 停止
     达到最大轮数 → 停止
```

### Optimize Agent 的 Prompt

```markdown
你是 mini compact 系统的自进化优化引擎。

## 目标
优化 AI coding agent 的上下文管理质量。
好的 compact = agent 用 compact 后的上下文能继续正确执行任务，且不丢失关键信息。

## 当前表现（suite 级别）

### 总分: {weighted_average}（baseline: {baseline_score}）
### 维度得分:
- 信息保留: {score}（baseline: {baseline}）
- 任务可执行性: {score}（baseline: {baseline}）
- 决策正确性: {score}（baseline: {baseline}）
- 上下文准确性: {score}（baseline: {baseline}）
- token 效率: {score}（baseline: {baseline}）

### 最差的 case（需要重点改善）:
{worst_case_details}

### 最优的 case（当前实现的优势）:
{best_case_details}

### 退步的 case:
{regressed_case_details}

## 你可以修改的文件
所有 pkg/compact/、pkg/tools/context_mgmt/、pkg/prompt/ 下的文件。
你也可以新增文件。修改必须能通过 go build。

## 当前代码
{key_source_files}

## 输出格式
先写一段分析：你观察到了什么问题，打算怎么改。
然后对每个要修改的文件，输出完整的新文件内容，用 ```<filepath> 包裹。
```

## Worker 接口

每代编译出 `evolve-mini-worker` 二进制，框架通过子进程调用：

```go
// 输入 JSON (stdin)
type WorkerInput struct {
    Snapshot     Snapshot    `json:"snapshot"`
    ModelConfig  ModelConfig `json:"model_config"`
}

// 输出 JSON (stdout)
type WorkerOutput struct {
    Success         bool             `json:"success"`
    Error           string           `json:"error,omitempty"`
    TokensBefore    int              `json:"tokens_before"`
    TokensAfter     int              `json:"tokens_after"`
    ToolCalls       []ToolCallRecord `json:"tool_calls"`
    ContextBefore   string           `json:"context_before"`
    ContextAfter    string           `json:"context_after"`
    LLMContextAfter string           `json:"llm_context_after"`
}
```



## 运行指南

### 前置条件

1. **确保 zai API 可用**
   ```bash
   # 检查 API 配置
   cat ~/.ai/auth.json    # 确保包含 zai API key
   cat ~/.ai/models.json  # 确保包含 glm-4.7 模型配置
   ```

2. **编译二进制文件**
   ```bash
   cd /Users/genius/project/ai
   go build ./cmd/evolve-mini/
   go build ./cmd/evolve-mini-worker/
   ```

### Step 1: 提取 Snapshot Suite

从现有会话中提取代表性的 snapshots：

```bash
cd /Users/genius/project/ai

# 提取 20 个多样化的 snapshots
./evolve-mini snapshot extract ~/.ai/sessions/ /tmp/evolve-test/suite/ --max-sessions 20

# 查看提取的 suite
./evolve-mini snapshot list /tmp/evolve-test/suite/
```

**输出示例：**
```
Extracted 18 snapshots
Saved to /tmp/evolve-test/suite/

Suite Summary:
  Total snapshots: 18
  Tags:
    exploration_heavy: 5
    multi_turn: 4
    bug_fix: 3
    ...
```

### Step 2: 修复 Snapshot 格式（如有需要）

如果 snapshot 格式缺少 WorkerInput 包裹，修复它：

```python
import json, os, glob

# 修复 snapshots，添加 WorkerInput 包裹
for f in glob.glob("/tmp/evolve-test/suite/*.json"):
    with open(f, 'r') as file:
        data = json.load(file)
    
    worker_input = {
        "Snapshot": data,
        "ModelConfig": {
            "provider": "zai",
            "modelID": "glm-4.7"
        }
    }
    
    output_file = os.path.join("/tmp/evolve-test/suite-fixed/", os.path.basename(f))
    with open(output_file, 'w') as out_file:
        json.dump(worker_input, out_file, ensure_ascii=False, indent=2)
```

### Step 3: 运行完整进化

```bash
cd /Users/genius/project/ai

# 运行 5 代进化
./evolve-mini run /tmp/evolve-test/suite-fixed/ 5
```

**输出示例：**
```
=== evolve-mini: Starting evolution ===
Suite: /tmp/evolve-test/suite-fixed/
Max generations: 5

Loaded 18 snapshots from suite

=== Generation 0 (baseline) ===
Loading suite from /tmp/evolve-test/suite-fixed/ ...
Loaded 18 snapshots

[1/18] Scoring 000_298df63d_62.json ...
  Worker succeeded: 10019 -> 7338 tokens (saved 2681, 26.7%)
  Score: 48 (Good compression, minimal information loss)

[2/18] Scoring 001_919b11c9_22.json ...
  Worker succeeded: 88645 -> 81288 tokens (saved 7357, 8.3%)
  Score: 52 (Excellent compression with perfect task continuation)

...

=== Generation 1 ===
Step 1: Generating mutation...
Step 2: Creating worktree gen_1...
Step 3: Applying mutations...
Step 4: Building worker...
Step 5: Scoring...
Best score improved! 48.5 -> 51.2
Accepting generation 1

...
```

### Step 4: 查看进化结果

```bash
cd /Users/genius/project/ai

# 查看进化历史
cat data/evolution_history.json

# 查看各代记录
ls data/generations/
for gen in data/generations/gen_*/generation_record.json; do
  echo "=== $gen ==="
  cat "$gen" | jq '.SuiteScore.WeightedAverage'
done

# 查看最佳代的代码变更
best_gen=$(cat data/evolution_history.json | jq -r '.BestGeneration')
cat data/generations/gen_$best_gen/generation_record.json | jq '.Changes'
```

### Step 5: 创建 PR 到主分支

```bash
cd /Users/genius/project/ai

# 从最佳代创建 PR
./evolve-mini pr data/generations

# 或者手动应用变更
./evolve-mini apply data/generations/gen_N
```

**PR 创建流程：**
1. 识别最佳 generation（最高 SuiteScore）
2. 创建单独分支：`evolve-mini/gen-N`
3. 在分支上 commit 代码变更
4. 使用 `gh` CLI 创建 PR
5. 人工审查并合并到 main

**重要：主分支不接受直接合并，只接受 PR！**

### 高级用法

#### 查看特定 generation 的状态
```bash
./evolve-mini status data/generations
```

#### 重新编译 worker 并测试
```bash
cd /Users/genius/project/ai
go build ./cmd/evolve-mini-worker/

# 测试单个 snapshot
cat /tmp/evolve-test/suite-fixed/000_test.json | ./evolve-mini-worker
```

#### 只运行 baseline（无进化）
```bash
./evolve-mini run /tmp/evolve-test/suite-fixed/ 0
```

### 故障排查

#### API Rate Limit (429)
**错误：** `API error (429): Rate limit reached`

**解决：**
1. 等待 API 配额重置（通常 1 分钟）
2. 或者切换到其他 provider
3. 框架会自动重试（最多 4 次）

#### Worker Timeout
**错误：** `Worker failed: context deadline exceeded`

**解决：**
1. 增加 timeout（`cmd/evolve-mini/scorer.go` 中的 `5*time.Minute`）
2. 减少同时运行的 worker 数
3. 检查 API 连接

#### Judge API Timeout
**错误：** `Judge failed: context deadline exceeded`

**解决：**
1. 增加 judge API timeout
2. 简化 judge prompt（减少 tokens）
3. 使用更快的模型

### 性能指标

| 指标 | 说明 | 目标值 |
|--------|------|--------|
| Token Savings | 压缩节省的 tokens | > 20% |
| Info Retention | 信息保留程度 | > 4/5 |
| Task Executability | 任务可执行性 | > 4/5 |
| Weighted Average | 综合评分 | > 45/55 |

### 输出文件

```
data/
  evolution_history.json        # 完整进化历史
  generations/
    gen_0/
      generation_record.json   # 第 0 代记录
    gen_1/
      generation_record.json   # 第 1 代记录
      diff.patch            # 代码变更（如果有）
    gen_2/
      ...
```



## 文件结构

```
cmd/evolve-mini/
  main.go              # CLI: snapshot extract / run / score / status
  snapshot.go          # Snapshot 提取逻辑
  suite.go             # Suite 管理（加载、多样性分析）
  scorer.go            # LLM Judge 评分 + Suite 汇总
  worktree.go          # Git worktree 管理
  builder.go           # 编译 worker + 执行
  optimize_agent.go    # 调用 LLM 做优化决策
  history.go           # 进化历史（每代的分数、diff、反馈）

cmd/evolve-mini-worker/
  main.go              # Worker 入口：读 stdin JSON → 执行 compact → 写 stdout JSON

  data/                # 运行时数据（gitignored）
    suite/              # Snapshot suite JSON 文件
    generations/        # 每代记录
      gen_0/
        suite_score.json
        case_results/
      gen_1/
        diff.patch
        suite_score.json
        case_results/
```

## 实现步骤

### Phase 1: Snapshot Suite 提取
1. `cmd/evolve-mini/snapshot.go` — 从 session JSONL 提取 snapshot
2. `cmd/evolve-mini/suite.go` — Suite 加载和多样性分析
3. `cmd/evolve-mini/main.go` — `snapshot extract` 和 `snapshot list` 命令

### Phase 2: Worker 执行器
4. `cmd/evolve-mini-worker/main.go` — 读 snapshot → 重建 AgentContext → 执行 compact → 输出结果
5. `cmd/evolve-mini/builder.go` — 编译 + 子进程执行 worker

### Phase 3: 评分系统
6. `cmd/evolve-mini/scorer.go` — LLM Judge prompt + 评分解析 + Suite 汇总
7. `cmd/evolve-mini/main.go` — `score` 命令

### Phase 4: 进化循环
8. `cmd/evolve-mini/worktree.go` — Git worktree 管理
9. `cmd/evolve-mini/optimize_agent.go` — LLM 优化决策
10. `cmd/evolve-mini/main.go` — `run` 命令（完整循环）