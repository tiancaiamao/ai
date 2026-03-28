# Explorer: PicoClaw SubAgent 编排实现

**Date:** 2025-01-09
**Target:** PicoClaw subagent/orchestrator 实现

## Overview
PicoClaw 实现了一个完整的 SubTurn 机制，允许工具启动隔离的嵌套 agent 循环来处理复杂子任务。核心特点是会话隔离、并发控制、深度限制和优雅的错误恢复。

## Tech Stack
- **Language:** Go 1.25+
- **Architecture:** Event-driven agent loop with concurrent turn management
- **Key Libraries:** sync/atomic, context, channels

## 项目结构
```
pkg/agent/
├── subturn.go           # SubTurn 核心实现
├── turn.go              # turnState 和 turn 生命周期管理
├── loop.go              # AgentLoop 主循环和结果轮询
├── events.go            # SubTurn 相关事件定义
├── subturn_test.go      # 完整的测试套件
├── steering.go          # 结果收集辅助函数
└── instance.go          # AgentInstance 配置

pkg/config/
└── config.go            # SubTurnConfig 配置定义

docs/
└── subturn.md           # 详细文档
```

## Core Components

### Component 1: SubTurnConfig
- **File:** `pkg/agent/subturn.go:86-142`
- **Responsibility:** 配置子 agent 的执行参数
- **Key Fields:**
  - `Model` (string) - 必需，指定 LLM 模型
  - `Tools` ([]tools.Tool) - 可选，默认继承父 agent 的工具
  - `SystemPrompt` (string) - 子任务描述
  - `Async` (bool) - 控制结果传递模式（同步/异步）
  - `Critical` (bool) - 父 agent 优雅结束时是否继续运行
  - `Timeout` (time.Duration) - 超时控制（默认 5 分钟）
  - `MaxContextRunes` (int) - 上下文软限制（0=自动计算 75%，-1=无限制）

### Component 2: spawnSubTurn (内部函数)
- **File:** `pkg/agent/subturn.go:245-487`
- **Responsibility:** 创建并执行子 agent 循环
- **Key Steps:**
  1. 验证配置和深度限制
  2. 获取并发槽位（semaphore）
  3. 创建独立的 ephemeral session
  4. 设置独立 context 和超时
  5. 执行 agent 循环
  6. 传递结果到父 agent
  7. 释放并发槽位

### Component 3: turnState
- **File:** `pkg/agent/turn.go:34-99`
- **Responsibility:** 管理 turn 的运行时状态和父子关系
- **Key Fields:**
  - `depth` (int) - SubTurn 深度（root=0）
  - `parentTurnID` (string) - 父 turn ID
  - `childTurnIDs` ([]string) - 子 turn ID 列表
  - `pendingResults` (chan *tools.ToolResult) - SubTurn 结果通道（buffer=16）
  - `concurrencySem` (chan struct{}) - 并发控制信号量
  - `isFinished` (atomic.Bool) - turn 是否结束
  - `finishedChan` (chan struct{}) - turn 结束信号

### Component 4: AgentLoopSpawner
- **File:** `pkg/agent/subturn.go:204-242`
- **Responsibility:** 实现 tools.SubTurnSpawner 接口，避免循环依赖
- **Key Method:**
  - `SpawnSubTurn(ctx, cfg)` - 转换配置类型后调用内部函数

## Key Patterns

### Pattern 1: 会话隔离
**Location:** `pkg/agent/subturn.go:548-618`
```go
type ephemeralSessionStore struct {
    mu      sync.Mutex
    history []providers.Message
    summary string
}

// 自动截断，最多保留 50 条消息
func (e *ephemeralSessionStore) truncateLocked() {
    if len(e.history) > maxEphemeralHistorySize {
        e.history = e.history[len(e.history)-maxEphemeralHistorySize:]
    }
}
```
**Usage:** 每个 SubTurn 使用独立的内存会话存储，与父 agent 完全隔离，任务结束后自动销毁

### Pattern 2: 并发控制（Semaphore）
**Location:** `pkg/agent/subturn.go:281-297`
```go
if parentTS.concurrencySem != nil {
    select {
    case parentTS.concurrencySem <- struct{}{}:
        // 成功获取槽位
        acquired = true
    case <-time.After(rtCfg.concurrencyTimeout):
        // 等待 30 秒后超时
        return nil, ErrConcurrencyTimeout
    case <-parentTS.Finished():
        // 父 turn 已结束
        return nil, ctx.Err()
    }
}
```
**Usage:** 使用 buffered channel 作为 semaphore，限制每个父 turn 最多 5 个并发 SubTurn

### Pattern 3: 独立 Context
**Location:** `pkg/agent/subturn.go:350-358`
```go
// Child uses INDEPENDENT context, not derived from parent
childCtx, childCancel := context.WithTimeout(context.Background(), timeout)
childTS.ctx = childCtx
childTS.cancelFunc = childCancel
```
**Usage:** 子 turn 使用 `context.Background()` 而非父 context，确保 critical SubTurn 在父结束时继续运行

### Pattern 4: 结果传递（同步 vs 异步）
**Location:** `pkg/agent/subturn.go:490-555`
```go
// Async=true: 结果发送到 channel
if cfg.Async {
    select {
    case resultChan <- result:
        // 成功传递
    case <-parentTS.Finished():
        // 父已结束，结果成为孤儿
    }
}
// Async=false: 结果只通过返回值传递，不发送到 channel
```
**Usage:** 控制结果传递路径，异步模式支持批量收集，同步模式立即返回

### Pattern 5: 结果轮询
**Location:** `pkg/agent/loop.go:1733-1746, 2540-2556`
```go
// 在 agent 循环中轮询 SubTurn 结果
if ts.pendingResults != nil {
    select {
    case result, ok := <-ts.pendingResults:
        if ok && result != nil && result.ForLLM != "" {
            msg := providers.Message{
                Role: "user",
                Content: fmt.Sprintf("[SubTurn Result] %s", result.ForLLM),
            }
            pendingMessages = append(pendingMessages, msg)
        }
    default:
        // 无结果可用，不阻塞
    }
}
```
**Usage:** 父 agent 在每次 LLM 迭代前轮询 pendingResults，将 SubTurn 结果注入上下文

## Dependencies
- **External:**
  - context (标准库)
  - sync/atomic (并发原语)
  - time (超时控制)
- **Internal:**
  - `pkg/tools` (ToolResult 类型)
  - `pkg/providers` (Message, LLMProvider)
  - `pkg/session` (SessionStore 接口)
  - `pkg/config` (配置管理)
  - `pkg/logger` (日志)

## Conventions
- **Coding style:** Go 标准风格，清晰的注释和文档
- **Naming:**
  - SubTurn = 子 agent turn
  - ephemeralSession = 临时会话
  - pendingResults = 待处理结果通道
  - orphan = 孤儿结果（无法传递）
- **Error handling:**
  - 定义明确的错误类型（ErrDepthLimitExceeded, ErrConcurrencyTimeout 等）
  - 使用 defer 确保资源清理
  - panic recovery 防止崩溃

## Key Findings

### 1. 启动方式
```go
// 方式 1: 公共 API（从工具中调用）
cfg := agent.SubTurnConfig{
    Model: "gpt-4o-mini",
    SystemPrompt: "Analyze code...",
    Async: false,
}
result, err := agent.SpawnSubTurn(ctx, cfg)

// 方式 2: 通过 spawner 接口（避免循环依赖）
spawner := agent.NewSubTurnSpawner(agentLoop)
spawner.SpawnSubTurn(ctx, toolsCfg)
```

### 2. 任务和上下文传递
- **SystemPrompt** 作为第一条 user message 发送给子 agent
- **Tools** 可显式指定或继承父 agent 的所有工具
- **Context** 通过 `withTurnState()` 和 `WithAgentLoop()` 注入
- **会话隔离** 使用独立的 ephemeralSessionStore，最多 50 条消息

### 3. 并行/串行支持
- **并行支持:** ✅ 通过 semaphore 控制最多 5 个并发 SubTurn
- **串行支持:** ✅ 调用方自行控制启动顺序
- **配置:** `maxConcurrentSubTurns = 5`, `concurrencyTimeout = 30s`

### 4. 依赖分析
- **无内置依赖分析:** ❌ SubTurn 之间没有 DAG 或依赖关系管理
- **执行顺序:** 由调用方（工具或 agent）自行控制
- **深度限制:** 最多 3 层嵌套（防止无限递归）

### 5. 结果汇总
- **同步模式 (Async=false):** 结果直接返回，不进入 channel
- **异步模式 (Async=true):** 结果发送到 `pendingResults` channel (buffer=16)
- **轮询机制:** 父 agent 在每次迭代轮询 channel，将结果注入上下文
- **孤儿处理:** 父结束时未传递的结果触发 `SubTurnOrphan` 事件
- **批量收集:** steering.go 提供辅助函数批量读取所有待处理结果

## 优点
1. **完全隔离:** 每个 SubTurn 有独立的会话、context 和工具集
2. **并发安全:** 使用 semaphore、atomic、sync.Map 确保线程安全
3. **资源保护:** 深度限制、并发限制、超时控制、会话大小限制
4. **错误恢复:** 自动处理 context exceeded 和 truncated response
5. **灵活性:** 支持同步/异步、critical/non-critical、工具继承/限制
6. **可观测性:** 完整的事件系统（Spawn/End/Delivered/Orphan）
7. **优雅退出:** 区分 graceful finish 和 hard abort，critical SubTurn 可继续运行

## 缺点
1. **无依赖分析:** 没有 DAG 或任务编排能力，需调用方自行管理
2. **无优先级:** 所有 SubTurn 平等竞争，无优先级调度
3. **有限的结果聚合:** 只有简单的 channel 轮询，无复杂的结果处理管道
4. **无进度跟踪:** 只有完成/错误状态，无中间进度报告
5. **固定配置:** 并发数、深度限制、buffer 大小都是常量，运行时不可调整
6. **孤儿结果丢失:** 父结束后未传递的结果只发事件，不持久化
7. **学习曲线:** Async 标志的行为（仍阻塞）可能令人困惑

## 关键文件路径
```
核心实现:
- pkg/agent/subturn.go           # SubTurn 逻辑（550+ 行）
- pkg/agent/turn.go              # turnState 管理（300+ 行）
- pkg/agent/loop.go              # 结果轮询（2700+ 行）
- pkg/agent/events.go            # 事件定义

配置:
- pkg/config/config.go           # SubTurnConfig

测试:
- pkg/agent/subturn_test.go      # 完整测试（1900+ 行）

文档:
- docs/subturn.md                # 详细文档（300+ 行）
```

## 与任务的关联
此实现展示了如何在一个 Go agent 框架中构建 subagent 编排系统：
- **会话隔离** 通过独立 session store 实现
- **并发控制** 通过 semaphore + timeout 实现
- **生命周期管理** 通过 context + atomic 状态实现
- **结果传递** 通过 channel + 轮询实现
- **可观测性** 通过事件总线实现

这是一个**工具级**的 subagent 实现，而非**编排级**的 DAG 任务调度器。适合简单的并行子任务，但不支持复杂的依赖分析和任务编排。