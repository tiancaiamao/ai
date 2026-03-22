# Session Reader Reference

从 session-reader 迁移的核心内容，用于 session-analyzer 的会话读取阶段。

## 快速会话概览

在深度分析前，先用 overview 模式快速了解会话：

```bash
# 获取会话概览（元数据、轮次、工具调用）
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> --mode overview
```

输出包括：
- Session 元数据（model、项目、成本）
- Turn 计数
- 每个轮次的总结（时间戳、工具调用）

## 按需读取内容

| 目标 | 命令 |
|------|---------|
| 对话内容 | `--mode conversation` |
| 完整内容（含工具 I/O） | `--mode full` |
| 工具调用和结果 | `--mode tools` |
| Token 使用和成本 | `--mode costs` |
| Subagent 会话详情 | `--mode subagents` |

## 输出控制

### 分页（大型会话）

```bash
# 跳过前 3 个用户轮次，显示接下来的 5 个
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> \
  --mode conversation --offset 3 --limit 5
```

### 内容截断控制

```bash
# 显示完整工具输出（不截断）
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> \
  --mode full --max-content 0

# 较短预览（每块 500 字符）
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> \
  --mode full --max-content 500
```

## Subagent 会话分析

当主会话包含 subagent 调用时，`--mode subagents` 会显示每个 subagent 的路径。

### 读取 Subagent 会话

```bash
# 持久化 artifact 副本（始终可用）
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py \
  ~/.ai/sessions/<project>/subagent-artifacts/<hash>_worker.jsonl \
  --mode overview

# 临时会话文件（可能被清理）
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py \
  $TMPDIR/ai-subagent-session-<id>/run-0/<timestamp>.jsonl \
  --mode overview
```

Subagent 会话使用相同的 JSONL 格式。overview 和 full 模式都支持 subagent 数据。

## 报告框架（分析时使用）

在分析 session 时，按照以下框架总结：

1. **目标** - 用户意图（第一条用户消息）
2. **过程** - 关键步骤、工具使用、决策
3. **结果** - 是否成功？最终状态？
4. **问题** - 错误、重试、变通、浪费
5. **成本** - 总花费和 token 使用

## Session 格式参考

JSONL 格式说明（如需自定义解析）：

**关键点**：消息内容嵌套在 `line.message.content`，**不是** `line.content`。Content 是类型化对象数组（`text`、`toolCall`、`thinking`）。工具结果是单独的消息条目，`role: "toolResult"`。

完整格式参考：`${CLAUDE_SKILL_ROOT}/references/session-format.md`