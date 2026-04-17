---
name: implement
description: 对话式执行实现阶段。用户给出“开始/继续实现”意图，agent 在后台完成任务分发、实现、评审与收尾。
---

# Implement — Conversation Interface

Implement skill 面向用户的交互是自然语言，不是 shell 命令。

## User Contract (对用户)

用户可以这样说：

- "开始实现这个 plan"
- "继续 implement 阶段"
- "汇报剩余任务和阻塞依赖"
- "先暂停，等我确认后再继续"
- "把失败任务重试一轮"

用户不需要手工运行 `ag` 或脚本。

## Agent Contract (对 agent)

agent 必须在后台完成以下流程：

1. 读取 `PLAN.yml` / `PLAN.md`，检查可执行性。
2. 选择执行模式：
   1. 小任务可直接执行。
   2. 中/大任务默认 team mode（依赖感知并行）。
3. 对每个任务执行：
   1. 实现
   2. 规格符合性评审（spec compliance）
   3. 代码质量评审（quality）
4. 评审失败时进行受限重试（最多 N 轮）。
5. 任务完成后更新状态并持续汇报总体进度。
6. 全部完成后输出 `impl-report.md` 与测试结论。

## Team Mode (internal, hidden from user)

team mode 可使用 `ag team` + `ag task` 实现：

- plan 导入任务与依赖
- worker 自动领取下一可执行任务
- 依赖阻塞自动生效
- 阶段结束后收尾/清理

这些属于内部实现细节，不应转嫁给用户手工执行。

## Progress Reporting

每轮关键动作后向用户汇报：

1. 已完成任务数 / 总任务数
2. 当前活跃任务
3. 阻塞任务与依赖原因
4. 失败/重试情况
5. 下一步计划

## Conversation-First Rules

1. 不把 CLI 参数当作用户主接口。
2. 不要求用户自己执行命令来推进任务。
3. 出现失败时，agent 先自恢复（重试、降并行、回退），再向用户汇报决策。
4. 仅当用户明确要求时，才展示底层命令与脚本细节。
