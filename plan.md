# Plan: 将命令注册机制改为注册表模式

## 背景

当前 `ai` 项目的命令注册机制依赖 RPC Server 的回调函数，而 `claw/cmd/aiclaw` 项目使用了更灵活的注册表模式（CommandRegistry）。目标是使 Agent 更适合作为 SDK 使用，将命令处理与 RPC 层解耦。

## 技术上下文

### 现有实现（ai 项目）

1. **RPC Server 层** (`pkg/rpc/server.go`)
   - 定义了 `onPrompt`, `onSteer` 等回调函数字段
   - 通过 JSON-RPC 协议分发命令

2. **RPC Handler 层** (`cmd/ai/rpc_handlers.go`)
   - 注册所有命令的处理器
   - 包含控制命令的逻辑（如 get_commands, get_session_stats 等）

3. **Agent 层** (`pkg/agent/agent.go`, `pkg/agent/loop.go`)
   - 执行 AI 逻辑和主循环
   - 不直接处理命令

### 目标实现（参考 aiclaw）

1. **CommandRegistry** 存储命令处理器
   - 定义：`type CommandHandler func(args string) (string, error)`
   - 方法：`Register(name, handler)`, `Get(name)`, `List()`

2. **Agent 内置命令**
   - 在 Agent 初始化时注册 `/help`, `/clear` 等命令
   - 通过消息前缀 `/` 检测并处理控制命令

3. **协议层解耦**
   - RPC Server 作为纯协议层，负责 JSON 解析和事件分发
   - 控制命令转发到 Agent 处理

## 架构设计

### 数据模型

```go
// CommandHandler 命令处理函数签名
type CommandHandler func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error)

// CommandRegistry 命令注册表
type CommandRegistry struct {
    mu        sync.RWMutex
    commands  map[string]CommandHandler
}

// Agent 结构体添加字段
type Agent struct {
    // 现有字段...
    commands *CommandRegistry  // 新增：命令注册表
}
```

### API 设计

```go
// CommandRegistry 方法
func NewCommandRegistry() *CommandRegistry
func (r *CommandRegistry) Register(name string, handler CommandHandler)
func (r *CommandRegistry) Get(name string) (CommandHandler, bool)
func (r *CommandRegistry) List() []string
func (r *CommandRegistry) HandleCommand(ctx context.Context, name, args string, agent *Agent, sessionKey string) (string, error)
```

### 命令处理流程

```
用户发送消息 (prompt/steer/follow_up)
    ↓
RPC Server 解析 JSON
    ↓
RPC Handler 调用 agent.Prompt/Steer/FollowUp
    ↓
Agent.RunLoop 接收消息
    ↓
检查消息是否以 / 开头
    ↓
是 → CommandRegistry.HandleCommand → 返回响应
否 → 发送给 LLM → 正常处理
```

## 实现策略

### 文件结构

```
pkg/agent/
├── command_registry.go      [新建] 命令注册表实现
├── command_builtin.go       [新建] 内置命令实现
├── agent.go                 [修改] 添加 commands 字段
└── loop.go                  [修改] 处理 / 前缀消息

cmd/ai/
└── rpc_handlers.go          [修改] 简化命令处理逻辑

pkg/rpc/
├── types.go                 [保留] JSON-RPC 类型定义
└── server.go                [保留] 协议层实现
```

### 核心修改点

1. **新建 `pkg/agent/command_registry.go`**
   - 实现 `CommandRegistry` 结构体
   - 提供注册、查找、列出命令的方法

2. **新建 `pkg/agent/command_builtin.go`**
   - 实现内置命令处理器
   - 包括：`/help`, `/session`, `/clear`, `/model`, `/trace_events` 等
   - 参考 `cmd/ai/rpc_handlers.go` 中的现有逻辑

3. **修改 `pkg/agent/agent.go`**
   - 在 `NewAgent` 中初始化 `CommandRegistry`
   - 调用 `registerBuiltinCommands()` 注册内置命令

4. **修改 `pkg/agent/loop.go`**
   - 在处理用户消息前检查 `/` 前缀
   - 如果是命令，调用 `CommandRegistry.HandleCommand`
   - 返回命令响应而不是发送给 LLM

5. **调整 `cmd/ai/rpc_handlers.go`**
   - 移除硬编码的控制命令逻辑（保留 prompt/steer/follow_up 等核心命令）
   - 对于 `/` 开头的消息，正常发送给 Agent（由 Agent 处理）

### 向后兼容性

- ✅ 现有的 JSON-RPC 客户端不受影响
- ✅ `prompt`, `steer`, `follow_up` 等核心命令保持不变
- ✅ 控制命令可以通过两种方式触发：
  - 客户端发送 `{"type": "prompt", "data": {"message": "/help"}}`
  - 返回帮助文本作为 LLM 响应

## 测试策略

### 单元测试

1. **CommandRegistry 测试** (`pkg/agent/command_registry_test.go`)
   - 测试注册、查找、列出命令
   - 测试并发安全性

2. **内置命令测试** (`pkg/agent/command_builtin_test.go`)
   - 测试每个内置命令的功能
   - 测试错误处理

3. **Agent 集成测试**
   - 测试 `/` 前缀消息被正确识别和处理
   - 测试普通消息正常发送给 LLM

### 集成测试

- 测试通过 RPC Server 发送 `/help` 命令
- 测试命令注册后的动态扩展
- 测试向后兼容性

## 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 破坏现有功能 | 高 | 全面测试，确保向后兼容 |
| 性能退化 | 中 | 命令查找是 O(1)，影响很小 |
| 命令冲突 | 中 | 使用注册表，后注册覆盖先注册，添加日志警告 |
| 并发问题 | 中 | CommandRegistry 使用读写锁保护 |

## 实现顺序

1. 创建 `CommandRegistry`（最小可用实现）
2. 实现 1-2 个简单内置命令（如 `/help`）
3. 修改 Agent 处理 `/` 前缀消息
4. 测试基本功能
5. 迁移所有内置命令
6. 调整 RPC handlers
7. 全面测试
8. 代码 review 和修复

## 验收标准

- [ ] 所有现有测试通过
- [ ] 新增测试覆盖 CommandRegistry 和内置命令
- [ ] 可以通过 RPC 发送 `/help` 命令并得到正确响应
- [ ] 普通消息正常发送给 LLM
- [ ] Agent 可以作为独立 SDK 使用（不依赖 RPC Server）
- [ ] 代码通过 review 技能检查