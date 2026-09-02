# Plan: `history` 只读检索工具

> 状态：设计待实现。目标读者：Coder agent。
> 参考：Codex `ext/history-notes` 的 `history` 工具（PR #39827），但本实现是**本地、强一致、零网络**版本。

## 1. 目的

给模型一个**有界、自寻址、渐进披露**的只读工具，用来检索**已经离开 live window 的历史**（被 compact 掉的、被重置丢掉的消息）。

动机：当前模型要回顾旧历史时，只能**自己写脚本解析 `messages.jsonl`**——无界、无自寻址、每次重新摸 schema、且写脚本本身污染上下文。本工具把这件事封装成确定性的有界调用。

**这是"上下文管理朝 Codex 方向迁移"的第一步**（后续 `new_context`、`notes` 见 §11，各自独立 PR）。

## 2. 已经存在的 substrate（不要重建）

| 能力 | 位置 | 说明 |
|------|------|------|
| append-only 存储 | `pkg/session/session.go` `persistEntryLocked` | `messages.jsonl` 用 `O_APPEND`，从不改写 |
| 树结构 | `SessionEntry{ID, ParentID *string}`，`Session.byID map[string]*SessionEntry`，`Session.leafID` | 支持 fork/branch |
| 自寻址 ID 已挂在消息上 | `pkg/context/message.go:104` `AgentMessage.EntryID`；`buildSessionContext` 里 `msg.EntryID = entry.ID` | **每条消息已带自己的 entry ID** |
| 路径重建 | `pkg/session/entries.go` `buildSessionContext` | leaf→root 走链，找**最后一个 compaction**，拼接 snapshot + kept |
| compaction 快照 | `SessionEntry{SnapshotRef, Summary, FirstKeptEntryID, TokensBefore}` | compact 前消息落到外部 snapshot 文件（`SnapshotRef` 相对路径），主线 append 一条引用 entry |
| entry 类型 | `entries.go`：`session/message/compaction/branch_summary/session_info` | |
| 工具接口 | `pkg/tools/*.go`（如 `read.go`） | `Name()/Description()/Parameters() map[string]any /Execute(ctx, args map[string]any) ([]agentctx.ContentBlock, error)`，构造 `NewXxxTool(ws *Workspace)` |

**结论**：`history` 工具 ≈ 在 `Session` 之上加一个**只读、有界的查询层**，复用 `byID` + snapshot 解析 + 已有的路径走链逻辑。

## 3. 数据模型映射

- **window** = 一次上下文代际。边界 = `EntryTypeCompaction` entry（也可用 `SessionHeader.LastCompactionID` / `TokensBefore`）。`list_windows` 就是列这些边界 + 代际 token 规模。
- **item** = 一条 `EntryTypeMessage` entry（`Message *agentctx.AgentMessage`），寻址用其 `ID`（= `AgentMessage.EntryID`）。
- **默认视图**：当前 `leafID` → root 的线性路径（= `buildSessionContext` 现在做的"当前 window"）。
- **分支**：可选，按 `EntryID` 显式进入非当前分支（默认不展开，保持简单）。
- **被 compact 掉的内容**：在 `SnapshotRef` 指向的 snapshot 文件里，按其中的 entry ID 可达——**存储层已免费保留**，工具只需在 search/read 时把 snapshot 纳入范围。

## 4. 工具契约

工具名：`history`（单工具 + `action` 参数，或 4 个子工具——**建议单工具 + action**，与 Codex 一致，减少工具面）。暴露方式：`DirectModelOnly`（不对用户可见，模型私有状态）。

### 4.1 `action=list_windows`

列上下文代际（compaction 边界）。

- 参数：
  - `limit` (int, 1..100, 默认 20)
  - `recent_first` (bool, 默认 true)
- 返回：每个 window 一行
  - `window_id` (string) — 该代际的 compaction entry ID（首个 window 用 session header ID）
  - `created_at` (timestamp)
  - `tokens_before` (int, 来自 `SessionEntry.TokensBefore`)
  - `item_count` (int, 该代际内 message entry 数)
  - `summary_preview` (string, ≤200 chars, 来自 `SessionEntry.Summary`)

### 4.2 `action=list_items`

列某 window（或当前路径）内的 items，**返回截断内容**（渐进披露第一层）。

- 参数：
  - `window_id` (string, 可选；省略 = 当前 leaf→root 路径)
  - `limit` (int, 1..20, 默认 20)
  - `recent_first` (bool, 默认 true)
  - `role` (enum `user|assistant|tool|system|developer`, 可选)
  - `include_tool` (bool, 默认 true；false 则排除 tool result)
  - `entry_id` (string, 可选) — 只返回该 entry 及其**祖先链**（用于"看这条及其上下文"）
  - `max_chars_per_item` (int, 1..2000, 默认 400)
- 返回：每个 item
  - `entry_id` (string)
  - `role` (string)
  - `timestamp`
  - `content` (string, 截断到 `max_chars_per_item`，结尾加 `…[truncated, N chars total]`)
  - `total_chars` (int)
  - `tool_name` (string, 仅 tool result)

### 4.3 `action=read_item`

读单条 item 全量（字符级分页，渐进披露第二层）。

- 参数：
  - `entry_id` (string, **必填**)
  - `offset_chars` (int ≥0, 默认 0)
  - `limit_chars` (int, 1..20000, 默认 20000)
- 返回：
  - `entry_id`, `role`, `timestamp`, `tool_name`(可选)
  - `content` (string, 按 offset/limit 切片)
  - `total_chars` (int) — 让模型知道是否还有剩余可翻页

### 4.4 `action=search`

**字面子串**搜索（大小写敏感，非正则、非语义），范围 = `messages.jsonl` **所有 message entry + 所有 compaction snapshot 文件**。

- 参数：
  - `query` (string, 1..1000, **必填**)
  - `limit` (int, 1..20, 默认 20)
  - `recent_first` (bool, 默认 true)
  - `role` (enum, 可选)
- 返回：每个命中
  - `entry_id`, `role`, `window_id`, `timestamp`
  - `match` (string, 命中行 ± 少量上下文的截断片段, ≤400 chars)

### 全局有界

- **单次响应硬上限**：`MAX_RESULT_TOKENS = 10_000`（超出截断并在末尾标注 `…[result truncated at ~10k tokens]`）。
- 所有 list/search 的 `limit` 有上界；单 item 内容截断；分页靠 offset/limit。

## 5. 自寻址（inline ID 注入）

**方案 b（先做，最小）**：不额外注入，模型通过 `list_items` 拿到 `entry_id` 再 `read_item`/`search`。零改动成本，先跑通。

**方案 a（后做，吃收益）**：在 live context 里每条 message 内容后追加 `\n[id: <EntryID>]`（Codex 做法），让"模型看到什么就能引用什么"。
- 实现点：`pkg/context` 把 message 转成 prompt 的那层（`buildSessionContext` 产出的 `AgentMessage` 已带 `EntryID`，在其 text block 末尾拼接即可）。
- **缓存对齐注意**：`[id:]` 追加在**每条消息内容末尾**会改变该消息的字节 → 影响该消息之后的前缀缓存。评估：ID 短（~10 字符）、每条固定，前缀一旦生成即稳定，缓存仍可命中（同一 session 内 ID 不变）。**必须验证**：加 ID 后 `GenerateSummary`/`askLLM` 的 cache-hit 不显著下降（对照 `pkg/compact` 里 cache_read 的 trace 指标）。

## 6. 需要动的文件

1. `pkg/session/history.go`（新）— 只读查询层：`ListWindows / ListItems / ReadItem / Search`，复用 `byID`、`leafID`、snapshot 解析（把 `buildSessionContext` 的走链逻辑抽成可复用的 `pathToLeaf(leafID)` + `resolveSnapshot(entry)`）。
   - search 需要能读 `SnapshotRef` 指向的 snapshot 文件（session dir 内相对路径）。
2. `pkg/tools/history.go`（新）— `HistoryTool`，实现 `Name/Description/Parameters/Execute`，`Execute` 按 `action` 分派到 `pkg/session` 查询层，负责参数校验 + 有界截断。构造 `NewHistoryTool(sess *session.Session)`（或注入一个只读 interface，便于测试）。
3. `pkg/tools` 注册处 — 把 `HistoryTool` 加入工具集（`DirectModelOnly` 暴露；对齐 `new_context` 的暴露方式，参考 `pkg/compact` / spec_plan 里 DirectModelOnly 的注册模式）。
4. `pkg/context`（仅方案 a）— message→prompt 层追加 `[id:]`。
5. 测试：
   - `pkg/session/history_test.go` — 各 action 的过滤/分页/截断/有界；**compaction snapshot 可达性**（compact 前的 entry 能被 search/read 到）；branch 默认不展开。
   - `pkg/tools/history_test.go` — 参数边界（limit 上界、query 长度、offset/limit 越界）、`MAX_RESULT_TOKENS` 截断、错误返回。

## 7. 边界与错误

- `entry_id` 不存在 → 明确错误（不要静默空返回）。
- `read_item` offset 超出 total → 返回空 content + `total_chars`（让模型知道到头了）。
- search 无命中 → 返回空列表 + 计数 0。
- 非 message entry（compaction/session_info）在 `list_items`/`search` 里默认排除，除非显式 `role=system`（compaction summary 作为 system 可见，可选）。
- snapshot 文件缺失/损坏 → 记 error，跳过该 snapshot，不影响其它命中。

## 8. 非目标（本 PR 不做）

- `new_context`（硬重置）— 独立 PR，用 subagent + 现有持久化。
- `notes`（跨窗口持久工作状态）— 独立 PR，基于现有 `update_llm_context` 提升为一等 store + 工具层 schema 校验。
- 语义/embedding 检索 — **明确不做**，保持字面 grep（见 §9 决策 D1）。
- 远程/多机 — 本地单 session dir。

## 9. 需要用户拍板的决策

- **D1**：search 保持**字面子串**（非语义、非正则）？（建议：是，对齐 Codex，失败模式在"模型不知道该检索"而非"检索排错序"）
- **D2**：单工具 + `action` 参数 vs 4 个独立工具？（建议：单工具 + action，减少工具面）
- **D3**：`MAX_RESULT_TOKENS = 10_000`、per-item 400/2000/20000 这些界，是否符合你的 1M 场景？
- **D4**：自寻址先做**方案 b**（list 返回 ID）还是直接上**方案 a**（inline `[id:]`）？（建议：先 b 落地，a 作为 follow-up 并验证缓存影响）
- **D5**：`list_items` 默认范围 = 当前 leaf→root 路径，是否需要一开始就支持 `window_id` 跨代际 + branch 按 `entry_id`？（建议：window_id 一起上，branch 留 follow-up）

## 10. 验收标准（Definition of Done）

- 模型在**不写任何脚本**的前提下，能通过 `history` 工具：列出代际 → 列出当前/指定 window 的 items（截断）→ 读单条全量（可翻页）→ 字面搜索命中**被 compact 掉**的旧内容。
- 所有返回受 `MAX_RESULT_TOKENS` 与 per-action 界约束；越界/非法参数返回明确错误。
- 测试覆盖 §6.5 全部点，`go test ./pkg/session/... ./pkg/tools/...` 通过。
- （方案 a 时）`pkg/compact` 的 cache_read 指标在加 ID 前后无显著回退。