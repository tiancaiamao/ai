# Agent 编排最佳实践调研

## 调研对象

| 项目 | Stars | 模式 | 技术栈 |
|------|-------|------|--------|
| oh-my-claudecode | 27k | 多模式切换 | Node.js plugin |
| multi-agent-shogun | 15k | 封建层级 | 纯 bash + tmux |
| OpenSwarm | — | DAG + Pair Pipeline | Node.js + SQLite |
| agor | 1.1k | 空间画布 + worktree | Node.js + Zellij |
| gentle-ai (原 agent-teams-lite) | — | SDD 9 阶段 | Go binary |

---

## 核心发现

### 1. Shogun 的做法：纯 bash + YAML，零基础设施

最激进地"低技术"方案。没有消息队列，没有 Go runtime，没有数据库。

**通信机制**：YAML 文件 + `inbox_write.sh`

```
Shogun → queue/shogun_to_karo.yaml → Karo 读
Karo   → queue/tasks/ashigaruN.yaml → inbox_write.sh → Ashigaru 读
Ashigaru → queue/reports/ashigaruN_report.yaml → Gunshi(QC) → inbox Karo
```

**协调机制**：tmux pane 状态检测（读最后几行判断 busy/idle）

**角色定义**：Markdown + YAML frontmatter（Karo 的指令文件有完整的 workflow steps 定义）

**关键设计**：
- 事件驱动而非轮询（Ashigaru 做完 → Gunshi 检查 → 通知 Karo）
- `bloom_level`（L1-L6）按任务复杂度路由到不同模型
- `blocked_by` 字段在任务间管理依赖

**对我们的启示**：bash + YAML 能做到的事比你想象的多的多。Shogun 14k stars 证明这条路走得通。

### 2. OpenSwarm 的做法：PairPipeline + DAG，两层组合

这是最接近你想要的架构。

**底层**：`PairPipeline`
```
Worker → Reviewer → Tester → Documenter
  ↑                            |
  └──── 失败时回到 Worker ─────┘
  maxIterations = 3
```

**上层**：`Workflow`（DAG 引擎）
```typescript
interface WorkflowStep {
  id: string;
  prompt: string;
  dependsOn?: string[];        // DAG 依赖
  onFailure: 'rollback' | 'retry' | 'skip' | 'abort';
  condition?: string;          // 条件执行
}
```

**中间层**：`AgentBus`（文件系统消息总线）
```typescript
// 每个 execution 一个目录
~/.openswarm/bus/<exec-id>/
  context.json       // 共享状态（stepOutputs, changedFiles, errors）
  messages/
    <timestamp>-<random>.json  // 消息文件

// 操作
bus.publish('step_completed', sender, payload)
bus.getData(key) / bus.setData(key, value)
bus.createStepContext(stepId, dependsOn)  // 自动注入上游产出
```

**调度层**：`TaskScheduler`
- 优先级队列
- 并发槽位控制（maxConcurrent）
- worktree 隔离（每个任务在自己的 worktree 中执行，避免冲突）
- 项目忙检测（同一项目不并发）

**对我们的启示**：PairPipeline 封装了 worker-judge 循环，DAG 引擎把 PairPipeline 当作一步来调用。**两层各管各的**——DAG 管依赖和顺序，PairPipeline 管质量和迭代。

### 3. OMC（oh-my-claudecode）的做法：多模式菜单

不走一条路，而是给用户 6 种模式选：

| 模式 | 用途 |
|------|------|
| Team | 多 agent 协作（显式 `/team`） |
| Autopilot | 单 agent 自治执行 |
| Ultrawork | 最大并行（非 team） |
| Ralph | 持久验证修复循环 |
| Pipeline | 严格顺序多阶段 |
| Deep Interview | 苏格拉底式需求澄清 |

**智能模型路由**：简单任务 Haiku，复杂推理 Opus。

**对我们的启示**：不用选 DAG 还是 Team，可以都提供，让用户/agent 按场景选。

### 4. Agor 的做法：空间画布 + Zone Triggers

最"可视化"的方案。核心原语不是 CLI 而是画布上的"Zone"：

```
拖 worktree 到 Analyze zone → 自动触发分析 prompt
拖到 Develop zone → 自动启动开发 agent
拖到 Review zone → 自动启动 review
```

**关键技术**：
- Zellij（替代 tmux）管理 terminal sessions
- 每个 worktree 独立环境（端口自动分配）
- MCP service 让 agent 之间能通过 Agor 内部协议通信

**对我们的启示**：Zone 触发器本质上就是"空间化的 DAG"——你把节点拖到不同区域，触发不同的 agent action。

---

## 回到你的核心问题

### 问题 1：代码写死 DAG vs agent 动态编排

**调研结论：不二选一，而是分两层**

OpenSwarm 的做法最清楚：

```
Layer 1: Pattern（确定性）
  PairPipeline = Worker → Reviewer → Tester → loop
  这是代码写的，不会变

Layer 2: Workflow（半确定性）
  DAG 定义了步骤之间的依赖
  但步骤内容由 agent 动态生成

Layer 3: Task（动态）
  agent 拆解任务，动态创建 task
  task 的执行走 Layer 1 的 Pattern
```

你的 feature 流程：
```
brainstorm → spec → plan → tasks → implement → verify
                                       ↑
                                  这里是动态的
                                  agent 拆出来的 task
                                  数量和内容不可预知
                                  但每个 task 的执行方式是固定的（worker-judge）
```

### 问题 2：Plan 和 Task 拆解后，执行不能预写死

**调研结论：Shogun 和 OpenSwarm 都解决了这个问题**

Shogun 的做法：
```yaml
# Karo（manager agent）动态写 task YAML
# 每个 task 有 bloom_level，路由到合适的 worker
# Ashigaru（worker）按 task YAML 执行
# Gunshi（QC agent）验收
# 结果写回 report YAML
# Karo 读到 report，解锁 blocked_by 的下游 task
```

关键：**Karo 是一个 agent，它来动态分配，但分配的格式是固定的 YAML**。

OpenSwarm 的做法：
```typescript
// decisionEngine.ts — agent 决定拆成什么任务
// taskScheduler.ts — 代码调度这些任务（并发、优先级、worktree 隔离）
// pairPipeline.ts — 每个任务执行 worker-judge 循环
```

关键：**决策是 agent 的，调度是代码的，执行是 pattern 的**。

### 问题 3：什么样的 CLI 原语能组合出 pattern 且可代码化

综合所有调研，最佳实践是三层 CLI：

```
原子命令（你已经有设计）
  ag spawn / wait / kill / output / send / recv

Pattern 命令（封装常用组合）
  ag run-pipeline spec-plan-implement
  ag run-pair "task description"
  ag run-parallel explore 3

编排命令（最高层）
  ag workflow start feature "add oauth"
  ag workflow status
  ag workflow resume
```

**Pattern 命令才是关键中间层**。它不是原子操作的 bash 脚本包装，而是 **agent 可以调用的稳定 API**。

---

## 推荐架构

基于调研和你自己的需求，推荐的架构：

```
┌─────────────────────────────────────────────────┐
│  ag workflow (可选的 DAG 层)                      │
│  - 定义阶段和依赖                                 │
│  - 每个阶段是一个 pattern 调用                    │
│  - 支持动态 task 创建（由 agent 拆解）            │
│  - checkpoint + resume                           │
├─────────────────────────────────────────────────┤
│  ag run-pair / ag run-parallel / ag run-pipeline │
│  (Pattern 层 — 封装好的模式)                      │
│  - worker-judge 循环                             │
│  - 并行探索 + 汇总                               │
│  - 串行 pipeline                                 │
│  - 每个都是独立的可执行命令                       │
├─────────────────────────────────────────────────┤
│  ag spawn / wait / send / recv / task            │
│  (原子 CLI 层)                                    │
│  - agent 生命周期                                │
│  - 消息传递                                      │
│  - 任务管理                                      │
└─────────────────────────────────────────────────┘
```

**核心原则**：
1. **每层独立可用** — 你可以只用原子层，也可以只用 pattern 层
2. **Pattern 是代码，不是 prompt** — worker-judge 循环用 bash 写死，不给 agent 机会搞砸
3. **DAG 是骨架，不是牢笼** — 阶段之间的依赖是确定的，阶段内部是灵活的
4. **动态拆解走 task** — agent 创建 task，代码调度 task，pattern 执行 task

**和 OpenSwarm 的关键区别**：
- OpenSwarm 用 TypeScript class 实现 pattern
- 我们用 bash 脚本 + CLI 命令实现 pattern
- 更轻量，更可组合，不依赖 Node.js runtime

**和 Shogun 的关键区别**：
- Shogun 没有 pattern 层，Karo agent 自己拼组合
- 我们把常用 pattern 固化成命令，减少 agent 出错空间
- 但保留了 Shogun 的 bash + 文件系统哲学