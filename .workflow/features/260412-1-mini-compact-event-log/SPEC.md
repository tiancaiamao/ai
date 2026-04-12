# SPEC: Mini Compact Event-Log 模型

## Summary

将 mini compact 的行为从"修改 messages.jsonl"改为"追加 compact event"，与 WAL 日志模型对齐。
messages.jsonl 是不可变日志（只追加），内存 snapshot 可以自由 apply compact 操作。

## Motivation

当前 mini compact 存在三个设计问题：

1. **违反日志不可变原则** — truncate_messages 通过 `SaveMessages` 全量覆盖 messages.jsonl，破坏了只追加语义
2. **compact 效果差** — 只能截断内容不能删除消息，687 → 687 条，token 节省极少
3. **checkpoint 等于全量** — 因为消息条数不变，checkpoint 存的和 messages.jsonl 一样，恢复时没有加速

根本原因：compact 操作被设计为"直接修改已有数据"，而非"追加操作记录 + 内存 apply"。

## User Stories

- 作为开发者，我希望 messages.jsonl 是不可变的 append-only 日志，这样可以用标准工具（grep/jq）审计所有历史
- 作为 mini compact LLM，我希望可以 drop 消息（不只是 truncate），这样能更有效地回收 token
- 作为 session 恢复机制，我希望 checkpoint 是 compact 后的 snapshot（比 messages.jsonl 更小），这样恢复更快
- 作为调试者，我希望从 checkpoint + messages.jsonl 的 compact events 可以重建任意时刻的 snapshot

## Requirements

### R1: Compact Event 追加到 messages.jsonl

- compact 操作（truncate / drop）作为新的 entry type 追加到 messages.jsonl
- 不修改、不覆盖已有的 messages.jsonl 条目
- entry 包含足够的信息让 snapshot 从上一个 checkpoint 的基础上 apply

### R2: 内存 Snapshot Apply

- compact event 在追加后，立即在内存 RecentMessages 上 apply
- truncate：替换目标消息的 Content，标记 Truncated=true
- drop：从 RecentMessages 数组中移除目标消息，或标记 Visibility=false

### R3: 去掉 SaveMessages 全量覆盖

- agent_end 不再 Replace（全量覆盖 messages.jsonl）
- OnMessagesChanged 不再 SaveMessages
- 所有写入都是 Append

### R4: 新增 drop_messages 工具

- mini compact 可以调用 drop_messages(ids=[...])
- 效果：消息从 LLM 视角消失（不再计入 token）
- drop 后的消息仍保留在 messages.jsonl（只是被 event 标记为 dropped）

### R5: Checkpoint 只存 Snapshot

- checkpoint 存的是 apply compact 后的 RecentMessages（可能比 messages.jsonl 少很多条）
- checkpoint 是 snapshot 缓存，不是唯一真相
- 恢复时优先用 checkpoint，fallback 到 replay messages.jsonl

### R6: 向后兼容

- 没有 compact event 的老 messages.jsonl 正常加载
- 老的 checkpoint 正常恢复
- 新代码可以读老格式

## Out of Scope

- full compact 的改造（本次只改 mini compact）
- compact event 的可视化/审计工具
- replay 性能优化（可以从 checkpoint 恢复就行）

## Success Criteria

- [ ] mini compact truncate 后，messages.jsonl 条目数增加（追加了 event），不是被覆盖
- [ ] mini compact drop 后，内存 RecentMessages 条数减少
- [ ] agent_end 不再调用 SaveMessages（全量覆盖）
- [ ] checkpoint 的消息条数 ≤ messages.jsonl 的条数
- [ ] 现有 test 全部通过
- [ ] 用真实 session 做 mini compact 验证