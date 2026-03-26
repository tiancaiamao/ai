# Tasks: 命令注册机制重构

## Task 1: 创建 CommandRegistry 基础实现

**文件**: `pkg/agent/command_registry.go`

**子任务**:
- [ ] 定义 `CommandHandler` 函数签名
- [ ] 定义 `CommandRegistry` 结构体
- [ ] 实现 `NewCommandRegistry()` 构造函数
- [ ] 实现 `Register(name, handler)` 方法
- [ ] 实现 `Get(name)` 方法
- [ ] 实现 `List()` 方法
- [ ] 添加并发安全保护（sync.RWMutex）

**验收标准**:
- [ ] 创建 `pkg/agent/command_registry_test.go`
- [ ] 测试通过所有方法的基本功能
- [ ] 测试并发注册和查找

**预估时间**: 20 分钟

---

## Task 2: 实现内置命令框架

**文件**: `pkg/agent/command_builtin.go`

**子任务**:
- [ ] 创建 `registerBuiltinCommands(agent *Agent)` 函数
- [ ] 实现 `/help` 命令处理器
  - [ ] 列出所有已注册的命令
  - [ ] 显示每个命令的简短描述
- [ ] 实现 `/commands` 命令处理器
  - [ ] 列出所有可用命令

**验收标准**:
- [ ] 创建 `pkg/agent/command_builtin_test.go`
- [ ] 测试 `/help` 命令输出格式正确
- [ ] 测试 `/commands` 命令列出所有命令

**预估时间**: 30 分钟

---

## Task 3: 集成 CommandRegistry 到 Agent

**文件**: `pkg/agent/agent.go`

**子任务**:
- [ ] 在 `Agent` 结构体中添加 `commands *CommandRegistry` 字段
- [ ] 在 `NewAgent()` 中初始化 `CommandRegistry`
- [ ] 调用 `registerBuiltinCommands()` 注册内置命令
- [ ] 添加 `RegisterCommand(name, handler)` 方法（供外部扩展使用）

**验收标准**:
- [ ] Agent 初始化成功
- [ ] 内置命令注册成功
- [ ] 可以通过 `agent.commands.List()` 查看所有命令

**预估时间**: 15 分钟

---

## Task 4: 修改 Agent Loop 处理命令消息

**文件**: `pkg/agent/loop.go`

**子任务**:
- [ ] 在 `RunLoop` 的消息处理逻辑中添加 `/` 前缀检测
- [ ] 如果是命令（以 `/` 开头）：
  - [ ] 调用 `CommandRegistry.HandleCommand`
  - [ ] 返回命令响应作为文本消息
  - [ ] 不发送给 LLM
- [ ] 如果不是命令：
  - [ ] 正常发送给 LLM 处理

**验收标准**:
- [ ] 普通消息正常发送给 LLM
- [ ] `/help` 命令返回帮助文本
- [ ] `/commands` 命令列出所有命令

**预估时间**: 25 分钟

---

## Task 5: 实现更多内置命令

**文件**: `pkg/agent/command_builtin.go`

**子任务**:
- [ ] 实现 `/session` 命令（显示当前会话信息）
- [ ] 实现 `/clear` 命令（清除会话上下文）
- [ ] 实现 `/model` 命令（切换/查看模型）
- [ ] 实现 `/trace_events` 命令（控制跟踪事件）
- [ ] 实现 `/set_thinking_level` 命令（设置思考级别）

**注意**: 参考 `cmd/ai/rpc_handlers.go` 中的现有实现逻辑

**验收标准**:
- [ ] 每个命令测试通过
- [ ] 命令响应格式清晰
- [ ] 错误处理正确

**预估时间**: 60 分钟

---

## Task 6: 调整 RPC Handlers

**文件**: `cmd/ai/rpc_handlers.go`

**子任务**:
- [ ] 移除 `GetCommands` handler 中的硬编码逻辑
- [ ] 移除 `GetSessionStats` handler（由 `/session` 命令替代）
- [ ] 移除 `SetModel` handler（由 `/model` 命令替代）
- [ ] 移除 `SetTraceEvents` handler（由 `/trace_events` 命令替代）
- [ ] 移除 `SetThinkingLevel` handler（由 `/set_thinking_level` 命令替代）
- [ ] 保留 `GetCommands` 作为兼容接口（调用 Agent 的 `/commands`）

**验收标准**:
- [ ] 现有的 JSON-RPC 客户端不受影响
- [ ] 可以通过 `{"type": "prompt", "data": {"message": "/help"}}` 调用命令
- [ ] 所有测试通过

**预估时间**: 20 分钟

---

## Task 7: 更新 Agent 的 Session 访问

**文件**: `pkg/agent/command_builtin.go`

**子任务**:
- [ ] 修改 `CommandHandler` 签名以访问 Session
- [ ] 更新所有内置命令以访问 Session 状态
- [ ] 确保命令可以读取/修改 Session 数据

**验收标准**:
- [ ] `/session` 命令显示正确的会话信息
- [ ] `/clear` 命令正确清除会话上下文
- [ ] 并发访问安全

**预估时间**: 25 分钟

---

## Task 8: 添加全面的测试

**文件**: `pkg/agent/command_registry_test.go`, `pkg/agent/command_builtin_test.go`

**子任务**:
- [ ] 测试 CommandRegistry 并发安全性
- [ ] 测试所有内置命令的功能
- [ ] 测试命令与普通消息的隔离
- [ ] 测试 Agent 作为独立 SDK 的使用场景
- [ ] 添加集成测试（通过 RPC 调用命令）

**验收标准**:
- [ ] 所有测试通过
- [ ] 测试覆盖率 > 80%
- [ ] 无回归问题

**预估时间**: 40 分钟

---

## Task 9: 更新文档

**文件**: `AGENTS.md`, `COMMANDS.md`（如果存在）

**子任务**:
- [ ] 更新 AGENTS.md 描述新的命令注册机制
- [ ] 添加命令扩展示例到文档
- [ ] 更新 COMMANDS.md（如果存在）列出所有内置命令

**验收标准**:
- [ ] 文档准确描述新的命令机制
- [ ] 包含使用示例

**预估时间**: 20 分钟

---

## Task 10: 运行完整测试套件

**子任务**:
- [ ] 运行 `go test ./pkg/agent -v`
- [ ] 运行 `go test ./pkg/rpc -v`
- [ ] 运行 `go test ./cmd/ai -v`
- [ ] 运行 `go test ./... -cover`
- [ ] 修复任何失败的测试

**验收标准**:
- [ ] 所有测试通过
- [ ] 代码覆盖率保持或提升

**预估时间**: 15 分钟

---

## 总预估时间

约 4.5 小时

---

## 依赖关系

```
Task 1 (CommandRegistry)
  ↓
Task 2 (内置命令框架)
  ↓
Task 3 (集成到 Agent)
  ↓
Task 4 (修改 Loop)
  ↓
Task 5 (更多内置命令)
  ↓
Task 6 (调整 RPC Handlers) ← 可以与 Task 5 并行
Task 7 (Session 访问)
  ↓
Task 8 (全面测试)
  ↓
Task 9 (更新文档)
  ↓
Task 10 (完整测试)
```

## 并行任务

- Task 5 和 Task 6 可以并行执行（在不同文件中）
- Task 8 可以在 Task 5 完成后开始