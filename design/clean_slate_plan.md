# Clean Slate Plan: New Architecture in Separate Directory

## Approach: Copy Only What's Needed

Instead of renaming individual files, create a new directory with only the code we need.

## New Directory Structure

```
ai/
├── cmd/                    (keep both)
│   ├── ai/                 (旧代码)
│   └── ai_v2/              (新代码，复制并修改)
├── pkg/                    (keep both)
│   ├── agent/              (保留)
│   ├── config/             (保留 - 不需要修改)
│   ├── context/            (旧代码保留，新代码在新目录)
│   ├── context_v2/         (新实现)
│   ├── llm/                (保留 - 不需要修改)
│   ├── logger/             (保留 - 不需要修改)
│   ├── modelselect/        (保留 - 不需要修改)
│   ├── prompt/             (保留 - 可能需要小幅修改)
│   ├── rpc/                (保留 - 不需要修改)
│   ├── session/            (旧代码保留，新代码在新目录)
│   ├── session_v2/         (新实现)
│   ├── skill/              (保留 - 不需要修改)
│   ├── tools/              (保留 - 移除 context_management.go)
│   └── traceevent/         (保留 - 不需要修改)
```

## Execution Steps

### Step 1: 撤销之前的重命名
```bash
cd /Users/genius/project/ai/pkg
mv context_deprecated context
mv compact_deprecated compact
mv truncate_deprecated truncate
cd /Users/genius/project/ai/pkg/tools
mv context_management_deprecated.go context_management.go
mv context_management_deprecated_test.go context_management_test.go
```

### Step 2: 恢复 cmd/ai/rpc_handlers.go
```bash
git checkout cmd/ai/rpc_handlers.go
```

### Step 3: 创建新目录结构
```bash
mkdir -p /Users/genius/project/ai/pkg/context_v2
mkdir -p /Users/genius/project/ai/pkg/session_v2
```

### Step 4: Commit 干净状态
```bash
git add .
git commit -m "chore: prepare for new context management architecture"
```

## 新架构开发流程

1. 在 `pkg/context_v2/` 实现新的 ContextSnapshot、Trigger、Checkpoint 等
2. 在 `pkg/session_v2/` 实现基于 event sourcing 的 session 管理
3. 创建 `cmd/ai_v2/` 实现新的 agent（基于旧的 agent）
4. 通过 feature flag 或环境变量选择使用哪个版本

## 优势

| 优势 | 说明 |
|------|------|
| **干净状态** | 旧代码完全不受影响 |
| **并行开发** | 可以随时对比新旧实现 |
| **渐进迁移** | 可以逐步验证新架构 |
| **易于回滚** | 如果新架构有问题，旧代码继续工作 |
| **清晰边界** | `_v2` 后缀明确标识新代码 |

## 最终清理

验证新架构稳定后：
1. 删除旧目录 (`pkg/context`, `pkg/session`)
2. 删除 `_v2` 后缀（`context_v2` → `context`）
3. 更新所有 import

---

这个方案更好吗？还是你有其他想法？
