# ai 项目代码精简建议报告

## 📊 项目概况

- **总代码行数**: ~30,811 行
- **主要语言**: Go 1.24.0
- **核心问题**: 多个超大文件，职责不清晰，存在重复代码

---

## 🔴 高优先级问题

### 1. pkg/agent/loop.go (2003行, 43个函数)

**问题**: 核心循环逻辑过于庞大，43个函数混杂在一起

**建议重构**:

```
pkg/agent/
├── loop.go              # 主循环 (约200行)
├── loop_tool.go         # 工具执行逻辑 (约300行)
├── loop_message.go      # 消息处理 (约300行)
├── loop_context.go      # 上下文管理 (约300行)
├── loop_history.go      # 历史消息处理 (约300行)
├── loop_snapshot.go     # 快照/追踪 (约200行)
├── loop_recovery.go     # 错误恢复 (约300行)
└── loop_metrics.go      # 运行时元数据 (约200行)
```

**函数拆分方案**:

| 原位置 | 函数 | 新文件 |
|--------|------|--------|
| loop.go | RunLoop, runInnerLoop | loop.go |
| loop.go | executeToolCalls, buildInvalidToolArgsMessage | loop_tool.go |
| loop.go | streamAssistantResponseWithRetry, streamAssistantResponse | loop_message.go |
| loop.go | selectMessagesForLLM, extractRecentMessages | loop_history.go |
| loop.go | emitLLMRequestSnapshot, buildLLMRequestSnapshot | loop_snapshot.go |
| loop.go | maybeRecoverMalformedToolCall, shouldRecoverMalformedToolCall | loop_recovery.go |
| loop.go | updateRuntimeMetaSnapshot, runtimeTokenBand | loop_metrics.go |
| loop.go | buildRuntimeUserAppendix, buildRuntimeSystemAppendix | loop_context.go |

**预期收益**: 从2003行拆分为8个~250行文件，每个文件职责单一

---

### 2. pkg/rpc/server.go (1005行, 30+个Set*Handler)

**问题**: 重复的Set*Handler模式，每个handler只有5-8行代码

**现状**:
```go
func (s *Server) SetPromptHandler(handler func(req PromptRequest) error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.handlers.prompt = handler
}
// ... 重复 30+ 次
```

**建议**: 使用泛型 + HandlerMap

```go
// 新增类型
type Handler[T any] func(T) error

type HandlerRegistry struct {
    mu       sync.RWMutex
    handlers map[string]any
}

func (r *HandlerRegistry) Set[T any](name string, handler Handler[T]) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.handlers == nil {
        r.handlers = make(map[string]any)
    }
    r.handlers[name] = handler
}

func (r *HandlerRegistry) Get[T any](name string) (Handler[T], bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    h, ok := r.handlers[name]
    if !ok {
        return nil, false
    }
    handler, ok := h.(Handler[T])
    return handler, ok
}

// Server中使用
type Server struct {
    handlers *HandlerRegistry
    // ...
}

// 简化为单个方法
func (s *Server) SetHandler[T any](name string, handler Handler[T]) {
    s.handlers.Set(name, handler)
}

// 注册代码从30+行减少到:
s.SetHandler("prompt", func(req PromptRequest) error { ... })
s.SetHandler("steer", func(message string) error { ... })
```

**预期收益**: 从1005行减少到约600行，消除重复代码

---

### 3. cmd/ai/helpers.go (483行, 20+函数)

**问题**: 辅助函数分散，部分函数过于简单

**建议重组**:

| 函数 | 建议操作 |
|------|----------|
| `countMessages`, `summarizeMessages` | 移到 pkg/session/session.go |
| `formatMessageCount`, `formatSessionTokenStats` | 移到 pkg/rpc/formatter.go (新文件) |
| `buildActiveSpecContext` | 移到 pkg/config/model.go |
| `modelInfoFromSpec` | 合并到 pkg/config/model.go |
| `resolveActiveModelSpec` | 合并到 pkg/config/model.go |
| `printAvailableModels` | 移到 pkg/config/model.go (用于CLI) |
| `cycleModel`, `cycleThinkingLevel` | 移到 cmd/ai/model_helpers.go |
| `calculateTokenStats` | 移到 pkg/session/session.go |

**预期收益**: helpers.go 从483行减少到~150行，只保留真正的通用辅助函数

---

## 🟡 中优先级优化

### 4. cmd/ai/rpc_handlers.go (1591行)

**问题**: RPC处理逻辑集中，部分可以抽取

**建议**:

```
cmd/ai/
├── rpc_handlers.go        # 核心RPC处理 (~500行)
├── rpc_setup.go          # 初始化/配置 (~300行)
├── rpc_metrics.go        # 指标收集 (~200行)
└── rpc_debug.go          # 调试/追踪 (~200行)
```

---

### 5. pkg/agent/metrics.go (882行)

**问题**: 指标收集逻辑复杂

**建议**: 检查是否有冗余的指标，考虑按类型拆分

---

### 6. pkg/compact/compact.go (816行)

**问题**: 压缩逻辑复杂

**建议**: 按策略拆分 (summary, archive, truncate等)

---

## 🟢 低优先级清理

### 7. 未使用的函数/变量

**建议**: 运行工具检测:
```bash
go install github.com/gordonklaus/ineffassign@latest
go install github.com/kisielk/errcheck@latest
ineffassign ./...
errcheck ./...
```

---

### 8. 代码重复检测

**建议**: 使用 gocyclo 检查复杂度
```bash
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
gocyclo -over 15 ./...
```

---

## 📋 重构优先级清单

### Phase 1: 高收益 (减少 >1500行)
- [ ] **pkg/agent/loop.go 拆分** (2003→8×250行)
- [ ] **pkg/rpc/server.go 泛型化** (1005→600行)
- [ ] **cmd/ai/helpers.go 重组** (483→150行)

### Phase 2: 中等收益 (减少 ~800行)
- [ ] **cmd/ai/rpc_handlers.go 拆分** (1591→1200行)
- [ ] **pkg/agent/metrics.go 简化** (882→700行)
- [ ] **pkg/compact/compact.go 拆分** (816→650行)

### Phase 3: 清理 (减少 ~500行)
- [ ] 删除未使用的代码
- [ ] 合并重复逻辑
- [ ] 代码格式统一

---

## 🎯 预期总收益

| 阶段 | 预期减少行数 | 工作量 |
|------|-------------|--------|
| Phase 1 | ~1,700 行 | 3-4天 |
| Phase 2 | ~450 行 | 2-3天 |
| Phase 3 | ~300 行 | 1天 |
| **总计** | **~2,450 行 (~8%)** | **6-8天** |

---

## 💡 其他建议

1. **增加测试覆盖**: 在重构前添加测试，确保行为不变
2. **使用 CI 检查**: 添加代码复杂度和重复度检查
3. **文档更新**: 重构后更新 ARCHITECTURE.md 和 COMMANDS.md
4. **渐进式重构**: 每次只改一个文件，确保可回滚

---

## 📝 具体操作示例

### 示例1: 拆分 loop.go

```bash
# 1. 创建新文件
touch pkg/agent/loop_tool.go
touch pkg/agent/loop_message.go

# 2. 移动函数 (保持包名一致)
# 3. 运行测试确保正常
go test ./pkg/agent -v

# 4. 提交
git add pkg/agent/loop_*.go
git commit -m "refactor: split loop.go into focused files"
```

### 示例2: 泛型化 server.go

```bash
# 1. 添加 HandlerRegistry 类型
# 2. 逐步替换 Set*Handler 为 SetHandler
# 3. 保持向后兼容 (保留旧方法deprecated标记)
// deprecated: Use SetHandler instead
func (s *Server) SetPromptHandler(...) { ... }
```

---

生成时间: 2025-01-XX
分析工具: 手动代码审查