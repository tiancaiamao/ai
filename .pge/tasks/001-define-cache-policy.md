# Task 001: Define CacheMode types and MessageMutationPolicy interface

## Goal
定义 `pkg/agent/cache_policy.go`，包含 CacheMode 枚举、IsCacheMode 自动检测函数、MessageMutationPolicy 接口及其两个默认实现（cache-first 和 context-first）。

## Files (scope)
- `pkg/agent/cache_policy.go` — 新建
- `pkg/agent/cache_policy_test.go` — 新建

## Estimated Size
M (100-300 lines)

## Dependencies
None

## Task Details

### 1. CacheMode 类型

```go
type CacheMode int
const (
    CacheModeAuto    CacheMode = iota // auto-detect from model name
    CacheModeCache                    // cache-first: persist runtime_state
    CacheModeContext                  // context-first: ephemeral injection (current behavior)
)
```

### 2. IsCacheMode 函数

```go
func IsCacheMode(model string) CacheMode
```

- model 包含 "deepseek" (case-insensitive) → CacheModeCache
- 其他 → CacheModeContext
- 如果传入空字符串 → CacheModeContext

### 3. RuntimeStateStrategy

```go
type RuntimeStateStrategy int
const (
    RuntimeStateEphemeral  RuntimeStateStrategy = iota // context-first: 随用随弃
    RuntimeStatePersist                                 // cache-first: 持久化追加
)

type MessageMutationPolicy interface {
    RuntimeStateStrategy() RuntimeStateStrategy
    // Phase 1 只需要这一个方法，Phase 2+ 可扩展
}
```

### 4. 两个默认实现

```go
type cacheFirstPolicy struct{}
type contextFirstPolicy struct{}

func DefaultMutationPolicy(mode CacheMode) MessageMutationPolicy
```

- CacheModeCache → cacheFirstPolicy{RuntimeStateStrategy: Persist}
- CacheModeContext / CacheModeAuto (resolve first) → contextFirstPolicy{RuntimeStateStrategy: Ephemeral}

### 5. LoopConfig 增加 CacheMode

在 `pkg/agent/loop.go` 的 `LoopConfig` 中增加：
```go
CacheMode CacheMode // default: CacheModeAuto
```

### 6. 测试

- `TestAutoCacheModeDetection`: 覆盖 "deepseek-chat", "deepseek-reasoner", "glm-4", "", "claude-3"
- `TestDefaultMutationPolicyCache`: CacheModeCache → RuntimeStateStrategy == Persist
- `TestDefaultMutationPolicyContext`: CacheModeContext → RuntimeStateStrategy == Ephemeral

## Acceptance
- `go build ./...` 通过
- 所有测试通过
- spec.md AS-3 (Auto 检测) 验证通过