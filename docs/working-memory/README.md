# Working Memory - 文档集

本文件夹包含 Working Memory 功能的完整设计、实施和总结文档。

---

## 文档结构

```
working-memory/
├── README.md                   # 本文件 - 文档导航和快速入口
├── spec.md                     # 功能规格说明 - Level 3 自主上下文管理
├── plan.md                     # 实施计划 - Phase 1 和 Phase 2 的详细计划
├── tasks.md                    # 任务清单 - 已完成的所有任务和 Bug 修复
├── design.md                   # 设计文档 - Working Memory 核心设计理念
└── IMPLEMENTATION_SUMMARY.md   # 实施总结 - 完整的实施历程和成果
```

---

## 快速导航

### 我是新手，想了解这是什么？
👉 阅读 [IMPLEMENTATION_SUMMARY.md](./IMPLEMENTATION_SUMMARY.md) 的 **概述** 部分

### 我想了解核心设计原理
👉 阅读 [design.md](./design.md) 的 **设计目的** 和 **核心机制**

### 我想查看详细的实施步骤
👉 阅读 [plan.md](./plan.md) 和 [tasks.md](./tasks.md)

### 我想知道如何使用和维护 Working Memory
👉 阅读 [IMPLEMENTATION_SUMMARY.md](./IMPLEMENTATION_SUMMARY.md) 的 **使用指南** 部分

### 我想了解验证测试结果
👉 阅读 [IMPLEMENTATION_SUMMARY.md](./IMPLEMENTATION_SUMMARY_SUMMARY.md) 的 **验证和测试** 部分

---

## 核心概念速览

### What is Working Memory?

Working Memory 是 Agent 的外部记忆系统，让 LLM 自主管理上下文，不再依赖自动规则。

### 核心架构层级

```
Level 0: 线性对话（无压缩）
Level 1: 规则驱动压缩（原实现）
Level 2: LLM 辅助压缩（Working Memory + 兜底）
Level 3: LLM 完全自主（当前实现） ✅
Level 4: 抽象化记忆（未来方向）
```

### 关键特性

| 特性 | 说明 |
|------|------|
| **自主管理** | LLM 通过 `write`/`read` 工具自主维护记忆 |
| **工具压缩** | LLM 主动调用 `compact_history` 压缩上下文 |
| **状态监控** | `context_meta` 实时显示 token 使用情况 |
| **75% 兜底** | 安全网机制，防止上下文溢出 |

---

## 文件存储结构

```
~/.ai/sessions/--<cwd>--/<session-id>/
├── messages.jsonl              # 对话历史（JSONL 格式）
└── working-memory/
    ├── overview.md              # LLM 维护的核心记忆（当前状态）
    └── detail/                  # 详细归档
        ├── design-discussion.md
        ├── debug-session.md
        └── ...
```

---

## 实施状态

### Phase 1: 基础设施 ✅

- ✅ Working Memory 目录结构
- ✅ Prompt 自动注入机制
- ✅ 向后兼容性支持
- ✅ 模板自动生成

### Phase 2: 自主管理 ✅

- ✅ `compact_history` 工具
- ✅ 移除自动压缩触发
- ✅ `context_meta` 监控
- ✅ System prompt 强化
- ✅ Bug 修复（8 个）

### 未来方向 📋

- ⏳ Resume 惰性加载（已设计）
- ⏳ Level 4 抽象化记忆
- ⏳ Memory 版本控制

---

## 关键代码文件

| 文件 | 职责 |
|------|------|
| `pkg/session/session.go` | Session 管理，Working Memory 目录创建 |
| `pkg/tools/compact_history.go` | 压缩工具实现 |
| `pkg/prompt/builder.go` | Prompt 构建，Working Memory 注入 |
| `pkg/agent/loop.go` | Agent 主循环，context_meta 注入 |
| `pkg/compact/compact.go` | 压缩逻辑 |

---

## 性能提升

| 指标 | Phase 1 (规则驱动) | Phase 2 (LLM 自主) | 改善 |
|------|-------------------|-------------------|------|
| 长对话稳定性 | 不稳定 | 稳定 | ✅ |
| Token 使用 | 线性增长 | 波动后稳定 | -60% |
| Prompt 延迟 | ~100ms | < 50ms | -50% |
| LLM 自主性 | 被动触发 | 主动管理 | ✅ |

---

## 相关链接

- **项目指南**: `../../AGENTS.md`
- **架构文档**: `../../ARCHITECTURE.md`
- **命令参考**: `../../COMMANDS.md`
- **工具文档**: `../../TOOLS.md`
- **Lazy Load 设计**: `../lazy-load-design.md`

---

## 更新日志

| 日期 | 文档 | 变更内容 |
|------|------|---------|
| 2025-01-15 | 所有 | 初始版本，Phase 1 文档 |
| 2025-02-26 | 所有 | Phase 2 完成更新，添加实施总结 |
| 2025-02-26 | README | 创建文档导航和快速入口 |

---

**最后更新**: 2025-02-26