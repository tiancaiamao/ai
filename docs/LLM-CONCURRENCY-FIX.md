# LLM 并发限制修复 - 解决 429 Rate Limit 问题

## 问题分析

之前遇到 `API error (429): Rate limit reached` 报错，这不是 5 小时内的请求数限制，而是**并发请求限制**。

### 根本原因

当前代码中有多个地方同时调用 LLM API，且没有任何全局并发控制：

| 位置 | 用途 | 优先级 |
|------|------|--------|
| `loop_llm.go:170` | 主循环的 LLM 调用 | 高 |
| `tool_summary_async.go:106` | 异步工具摘要 | 低 |
| `loop_tools.go:206` | 工具执行（可能触发 LLM） | 中 |
| `compact/compact.go:377` | 历史压缩 | 低 |

**问题场景**：
```
用户请求
    |
    v
主循环调用 LLM ────────────────────────> 429 Rate Limit!
    |
    +-- 工具摘要器同时调用 LLM ─────────> 429 Rate Limit!
    |
    +-- 3个工具并发执行 ─────────────────> 429 Rate Limit!
```

## 解决方案

实现了**全局 LLM 并发控制器**，具有以下特性：

### 1. 并发限制（默认 2）

默认情况下，最多允许 2 个并发 LLM 请求。可通过环境变量配置：

```bash
export ZAI_MAX_CONCURRENT_LLMS=2  # 默认值
export ZAI_MAX_CONCURRENT_LLMS=3  # 更高的并发（如果你的 API 支持）
export ZAI_MAX_CONCURRENT_LLMS=1  # 最保守的设置
```

### 2. 优先级队列

不同类型的 LLM 调用有不同的优先级：

- **PriorityHigh**: 用户交互的主循环（最高优先级）
- **PriorityNormal**: 默认优先级
- **PriorityLow**: 工具摘要、压缩等后台任务（最低优先级）

高优先级的请求会优先获得执行槽位。

### 3. 监控和统计

可以通过以下方式监控并发状态：

```go
import "github.com/tiancaiamao/ai/pkg/llm"

// 获取并发统计
stats := llm.GetLLMConcurrencyStats()
// {
//   "maxConcurrent": 2,
//   "totalRequests": 150,
//   "waitingLow": 0,
//   "waitingNormal": 0,
//   "waitingHigh": 0,
//   "avgWaitTime": "15ms",
//   "maxWaitObserved": "200ms"
// }

// 重置统计
llm.ResetLLMConcurrencyStats()
```

### 4. 详细的日志

当等待时间超过 100ms 时，会自动记录警告日志：

```
[LLM] Long wait for concurrent slot waitTime=200ms priority=0 waitingLow=2 waitingNormal=0 waitingHigh=1
```

## 使用示例

### 代码中设置优先级

```go
import "github.com/tiancaiamao/ai/pkg/llm"

// 为低优先级任务（如工具摘要）设置低优先级
ctx = llm.WithLLMPriority(ctx, llm.PriorityLow)

// 为高优先级任务（如用户交互）设置高优先级
ctx = llm.WithLLMPriority(ctx, llm.PriorityHigh)
```

### 已自动设置的优先级

以下代码已自动设置优先级：

1. **工具摘要** (`tool_summary.go`) - `PriorityLow`
2. **历史压缩** (`compact.go`) - `PriorityLow`
3. **主循环** (`loop_llm.go`) - `PriorityNormal`（可通过 context 提升为 High）

## 与 Claude Code 的差异

| 特性 | Claude Code | 当前项目 |
|------|-------------|----------|
| 并发控制 | ✅ 内置 | ✅ 现已实现 |
| 优先级队列 | ✅ | ✅ 现已实现 |
| 可配置并发数 | ❌ | ✅ 通过环境变量 |
| 透明度 | ❌ | ✅ 统计 API |

## 建议

1. **默认设置**（推荐）：`ZAI_MAX_CONCURRENT_LLMS=2`
   - 适合大多数 API 提供商
   - 平衡响应速度和稳定性

2. **遇到 429 报错时**：降低并发数
   ```bash
   export ZAI_MAX_CONCURRENT_LLMS=1
   ```

3. **确认 API 支持更高并发时**：可以提高并发数
   ```bash
   export ZAI_MAX_CONCURRENT_LLMS=3
   ```

4. **完全禁用工具摘要**（如果仍然有问题）：
   ```bash
   export ZAI_TOOL_SUMMARY_STRATEGY=off
   ```

## 实现文件

- `pkg/llm/concurrency.go` - 并发控制器实现
- `pkg/llm/client.go` - 集成到 StreamLLM
- `pkg/agent/tool_summary.go` - 工具摘要使用低优先级
- `pkg/compact/compact.go` - 压缩使用低优先级

## 测试

```bash
# 构建项目
go build ./...

# 运行测试
go test ./pkg/llm/... -v
go test ./pkg/agent/... -v
```
