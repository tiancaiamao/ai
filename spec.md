# Spec: Refactor LLM Context Interaction Protocol

## Summary

移除 `overview.md` 自动注入机制，改用 tool output 留痕 + compact 后恢复的方式，让 LLM context 管理更符合 codex 的设计模式。

## Problem

当前实现存在问题：
1. LLM 用 `write` 更新 `overview.md` → tool output 留痕
2. 代码又把 `overview.md` 注入到 prompt → 信息重复
3. `llm_context_decision` 可能 truncate 掉 tool 输出 → 丢失 context

## Solution

### 1. New Tool: `llm_context_update`

创建新工具，分离自 `llm_context_decision`：

- **参数**: `{"content": "自由文本 markdown"}`
- **返回值**: `"Context updated."`
- **副作用**: 双写
  - Tool output 留在上下文窗口
  - 同时写入 `overview.md` 文件

### 2. Behavior Changes

| 场景 | 当前行为 | 新行为 |
|------|---------|--------|
| 正常对话 | 代码注入 `overview.md` | 不注入，依赖 tool output 留痕 |
| LLM 更新 context | `write` 写文件 | `llm_context_update` 双写 |
| compact 后 | 代码注入 `overview.md` | 代码注入 `overview.md`（恢复记忆） |
| truncate | 可能删掉 tool output | 保护最近 `llm_context_update` 不标 stale |

### 3. File Changes

| 文件 | 改动 |
|------|------|
| `pkg/prompt/llm_context.md` | 最小改动 |
| `pkg/context/llm_context.go` | 新增 `llm_context_update` 工具 |
| truncate 逻辑 | 保护最近 `llm_context_update` |
| prompt 注入逻辑 | 移除自动注入，添加 compact 后注入 |

## Acceptance Criteria

- [ ] New tool `llm_context_update` implemented
- [ ] Tool parameter: `content` (free-form markdown)
- [ ] Tool return: `"Context updated."`
- [ ] Tool performs dual-write: tool output + `overview.md` file
- [ ] Auto-injection of `overview.md` removed from normal request flow
- [ ] `overview.md` injected after compact for recovery
- [ ] Truncate logic protects latest `llm_context_update` from stale marking
- [ ] `llm_context.md` prompt file updated with minimal changes
- [ ] All existing tests pass
- [ ] New functionality tested

## Out of Scope

- Changing the structure/template of `overview.md` content
- Modifying `llm_context_decision` tool behavior
- Changing compact algorithm