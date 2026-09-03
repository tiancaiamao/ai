# Design: `ai history` 子命令 + session-history skill

> 状态：已实现（PR #401）。本文档是唯一设计记录，取代并删除了 `docs/plan-history-tool.md`（原生工具方案）。
> 注意：按 AGENTS.md 约定，`docs/` 最终用户文档需为英文；本设计稿暂为中文工作文档。

## 1. 现状（改之前长什么样）

### 1.1 Session 存储

- Session 目录：`~/.ai/sessions/--<escaped-cwd>--/<session-uuid>/`，一个 cwd 可对应多个 session UUID。
- 持久化：`pkg/session/session.go` `persistEntryLocked`，`messages.jsonl` 以 `O_APPEND` 追加，从不改写。
- 树结构：`SessionEntry{ID string, ParentID *string}`，`Session.byID map[string]*SessionEntry`，`Session.leafID`。支持 fork/branch。
- Entry 类型（`pkg/session/entries.go`）：`session / message / compaction / branch_summary / session_info`。
- Compaction：`SessionEntry{SnapshotRef, Summary, FirstKeptEntryID, TokensBefore}`（entries.go:59-67）。compact 之前的历史消息落入外部 snapshot 文件（`SnapshotRef` 为 session dir 内相对路径），主链只追加一条引用 entry。**compaction 语义 = summary + recent messages，本设计不做任何改动。**
- 路径重建：`buildSessionContext`（entries.go）从 leaf→root 走链，找最后一个 compaction，拼接 snapshot + kept messages。`AgentMessage.EntryID` 已挂在每条消息上（`pkg/context/message.go:104`）。
- Lazy loading：`pkg/session/lazy.go`，session 可能未全量加载（`byID` 部分可用），snapshot 在 lazy.go:191 解析。

### 1.2 Run 注册表与子命令约定

- Run 目录：`~/.ai/runs/<6-hex-id>/run.json`。
- `RunMeta`（`subcommand/run/tui/meta.go:27`）：`{ID, PID, CWD, Status(running|done|failed|killed), StartedAt, FinishedAt, Name, ParentRun, PidStartTime}`。**当前没有 session 字段。**
- 既有子命令 `send / watch / kill` 统一约定：`--id <run-id|prefix>`，省略时按 cwd 自动选择（`subcommand/helpers/helpers.go:40` `ResolveRunID`）：
  - 显式 ID → 精确匹配，再前缀匹配（多命中报错）；
  - 省略 → 按 cwd 找 running run：0 个报错、1 个选中、**多个报错并列出全部 ID 提示 `--id` 消歧**。
- Session 切换已存在：ACP `session/new` / `session/load`（`pkg/protocol/acp.go:235-239`，处理在 `pkg/app/rpc_session_handlers.go`），即 `/resume` 类操作可在**同一个 run 内**切换 session。

### 1.3 Codex 参照（`~/project/codex/codex-rs/ext/history-notes`）

- 4 个 history action：`list_windows / list_items / read_item / search_contents`，namespaced tool，`ToolExposure::DirectModelOnly`。
- 后端是远程服务（本地仓库无查询层实现）；eventually consistent；window_id 为 UUID；item_id 为短后缀；服务端注入 inline `[id: ...]` 尾标；description 带 "private model-only state, never disclose" 约束。

## 2. 为什么要改

模型要回顾已离开 live window 的历史（被 compact 的内容），当前唯一途径是**现场写脚本解析 `messages.jsonl`**：

1. **无界**：`grep`/`jq` 输出量不可控，截断是 bash 工具的"急刹车"（任意位置硬切、无总数、无翻页语义），模型在上下文压力最大的时刻（刚 compact 完）最容易因此撑爆自己的窗口。
2. **无自寻址**：消息 ID 不在模型的视野里，引用旧内容只能靠复制文本。
3. **schema 每次重学**：树结构（ParentID 链）+ 外部 snapshot 文件 + entry 类型区分，理解成本高且每次会话都要重来；写脚本的过程本身污染上下文。

目标：给模型一个**稳定、有界、自寻址**的历史读接口；存储 schema 知识沉入 Go 代码，模型只见稳定的 CLI 动作面。这是"历史永久保留 + 滑动窗口"上下文模型的第一块基础设施（后续 notes 等在此之上叠加）。

## 3. 关键设计决策和取舍

### D1 形态：原生工具 vs 纯 skill vs skill + CLI 子命令 → **skill + CLI 子命令**

- 原生工具（原 plan 方案）：确定性强、输出有界靠机制。但**工具 description 是常驻 token 成本**，大量短 session 根本不触发 compact，纯浪费；且需要新建"模型私有工具"暴露机制（仓库中无现成参照）。
- 纯 skill（jq 配方）：成本最低。但模型没有 skill 也会 jq——**增量价值趋近于零**，且 schema 耦合进 prompt，存储演化即失效。
- **skill + CLI（选定）**：schema 沉到 `pkg/session` 复用现有加载逻辑（零重复）；skill 只做触发器 + CLI 说明书（~50 行）；CLI 对 bash 工具是普通命令，天然可组合。牺牲：多一层进程启动开销（可忽略）、skill 触发依赖模型主动意识到（验证阶段可接受）。

### D2 寻址：env 注入 vs run-id → **run-id，遵循既有 `--id` 约定**

- env 注入（`AI_SESSION_ID`）被否决：与仓库既有 `ai send/watch/kill --id` 约定不一致。
- **run-id 是天然的无歧义锚点**：`ai history --id <run-id>`，复用 `ResolveRunID` 语义。模型经 context 注入得知自己的 run ID（见 §4.3），显式传参；省略时沿用"cwd 自动选择、多 run 报错列候选"的既有行为——**绝不静默猜"最近修改"**（并发多 agent 场景下那是错的）。
- **run → session 的解析在调用时进行**：`/resume`（session/load）切了 session，同一 run id 查到的历史自动跟着切。为此 `RunMeta` 需新增 `Session` 字段并在创建/切换时更新（§4.2）。
- 牺牲：要求模型知道并携带自己的 run ID（一条 context 注入的成本），换取与全家族子命令一致的寻址模型。

### D3 有界性：导航协议而非保险丝

CLI 默认输出遵循**导航协议**：分页（limit/offset）+ 总数（total_count/total_chars）+ 显式截断标记（`…[truncated, N chars total]`）+ 单条内容截断。任何一次调用的输出量可预算。另提供 `--json` 机器模式（JSONL、无截断标记、供管道加工），组合性由此保证：stdout 只出数据、诊断走 stderr、分页无状态。

### D4 检索语义：字面子串，默认大小写不敏感

对齐 Codex 的"literal substring"；差异点：默认 case-insensitive（对模型更友好，避免"大小写没对上所以没搜到"的静默失败），`--case-sensitive` 显式开关。（待用户拍板）

### D5 Compaction 语义不变

summary + recent messages 保持现状。`history` 是纯增量的读口，不改变 compact 行为。

## 4. 怎么做

### 4.1 CLI 规格

```
ai history windows --id <run-id|prefix> [--limit N] [--oldest-first] [--json]
ai history list   --id <run-id|prefix> [--window <window-id>] [--role user|assistant|tool|system|developer]
                  [--no-tool] [--entry <entry-id>] [--limit N] [--max-chars N] [--oldest-first] [--json]
ai history read   --entry <entry-id> [--offset-chars N] [--max-chars N] --id <run-id|prefix> [--json]
ai history search <query> --id <run-id|prefix> [--window <window-id>] [--role ...] [--limit N]
                  [--no-tool] [--case-sensitive] [--json]
```

所有 action 必须显式寻址：`--id <run-id|prefix>`（或 `--session <path>` 逃生门）。**没有** cwd 自动选择——agent 运行中可以切换工作目录，cwd 不再可靠标识 run；曾试加过 `--latest`（cwd 内最近 run），因同一原因移除。`--id` 同样命中 done/failed 的 run。

- **windows**：列 compaction 代际。每行：`window_id`（该代际 compaction entry ID；首个 window 用 session header）、`created_at`、`tokens_before`、`item_count`（代际内 message entry 数）、`summary_preview`（≤200 chars）。
- **list**：列 window 内 items。每行：`entry_id`、`role`、`timestamp`、`content`（截断到 `max-chars`，默认 400，上限 2000，尾部 `…[truncated, N chars total]`）、`total_chars`、`tool_name`（仅 tool result）。`--entry <id>` = 返回该 entry 及其祖先链。省略 `--window` = 当前 leaf→root 路径；指定 = 跨代际。`--no-tool` 排除 tool result。
- **read**：单条全量，字符级分页。返回 `entry_id, role, timestamp, tool_name?, content, total_chars`。`--max-chars` 默认 20000、上限 50000；offset 超界返回空 content + total_chars。
- **search**：字面子串，范围 = 所有 message entry + 所有 compaction snapshot 文件。每命中：`entry_id, role, window_id, timestamp, match`（命中 ± 上下文片段，≤400 chars）。query 长度 1..1000。返回含 `total_count`。

**全局有界**：所有 list/search `--limit` 默认 20、上限 100；单次调用输出总量上限 40000 字符（约 10k token），超出截断并标注 `…[output truncated at 40000 chars, refine your query]`。数值为实现期常量，集中定义便于调整。

### 4.2 Run → Session 挂接

- `RunMeta` 新增字段 `Session string`（session UUID 或 session dir 路径）。
- 写入点：run 启动创建 session 时（serve/run 初始化，`subcommand/run/tui/meta.go` 的写入方）；`session/load` 切换时（`pkg/app/rpc_session_handlers.go`）刷新。
- `ai history` 解析链：`--id` → `ResolveRunID` 语义（复用/扩展 `subcommand/helpers`）→ 读 `run.json` 的 `Session` → 定位 session dir。
- **调用时解析**保证 /resume 切换后历史视图自动跟随。
- 现有 `ResolveRunID` 只匹配 running run；`ai history` 需要也能命中 `done/failed` 的 run（历史查询常发生在 run 结束后），扩展时**不改** send/watch/kill 的现有语义（加参数或独立函数）。

### 4.3 Agent 得知自己的 run ID

在模型可见的 context 中注入一行 run ID（复用 runtime meta 注入通道，`pkg/agent/runtime_meta.go` `injectRuntimeMeta` 已有类似机制），形如 `run id: 00028a`。模型随后 `ai history search xxx --id 00028a`。

### 4.4 查询层（schema 封装）

- `pkg/session/history.go`（新）：`ListWindows / ListItems / ReadItem / Search`。
- 从 `buildSessionContext` 抽出可复用的 `pathToLeaf(leafID)` + `resolveSnapshot(entry)`，不复制走链逻辑。
- 查询前确保 session 全量加载（处理 lazy loading）。
- 读 `byID`/主链时持有 session 锁，保证相对一次调用的一致视图；snapshot 文件读取容忍缺失/损坏（跳过 + stderr 警告，不影响其他命中）。
- branch：默认仅当前 leaf→root 路径 + snapshot 可达内容；非当前分支显式不支持（follow-up）。

### 4.5 Skill

- 位置：`skills/session-history/SKILL.md`（进 repo，随项目分发）。
- 内容（约 50 行）：**触发条件**（需要回顾 compact 之前的内容；用户说"之前/我们说过"而 live window 没有）→ **命令表**（4 个 action 一行一个 + 常用参数）→ **示例**（search → read 两跳定位）→ **纪律提示**（先 search 拿 entry_id 和 total_count，再 read 翻页；不要盲目加大 limit）。
- **不包含** jsonl schema、snapshot 文件格式等存储细节——那些已封装在 CLI 后面。

### 4.6 涉及文件

| 文件 | 动作 | 内容 |
|---|---|---|
| `subcommand/history/history.go` | 新 | 参数解析、run 解析、分派查询层、输出格式化与有界截断 |
| `pkg/session/history.go` + `_test.go` | 新 | 只读查询层 + 测试（过滤/分页/截断/snapshot 可达性/branch 不展开） |
| `subcommand/run/tui/meta.go` | 改 | `RunMeta` 加 `Session` 字段 |
| session 创建/加载处（`pkg/app/rpc_session_handlers.go` 等） | 改 | 创建与 `/resume` 切换时更新 run.json 的 Session |
| `subcommand/helpers/helpers.go` | 改 | 扩展 resolve 以支持 done/failed run（不影响既有命令语义） |
| `pkg/agent/runtime_meta.go`（或其注入链） | 改 | context 注入 run ID |
| `cmd/ai/main.go` + `usage.go` | 改 | 注册 `history` 子命令 |
| `skills/session-history/SKILL.md` | 新 | skill |

## 5. 验收场景

### P0 Feature: 跨 compact 边界的内容恢复
**Acceptance Scenarios:**
1. 构造一个发生 过 compaction 的 session；`ai history search "<compact 前的已知短语>" --id <run>` 返回该条旧消息，`entry_id`/`role`/`snippet` 正确，且 snippet 属于 snapshot 内容。
2. `ai history read --entry <该 entry_id>` 返回全量内容，`total_chars` 与实际一致；`--offset-chars`/`--max-chars` 翻页拼接后等于全文。
3. `ai history windows` 列出 ≥2 个代际，按新到旧排序，`tokens_before`/`item_count`/`summary_preview` 与 session 内容吻合。

### P0 Feature: run-id 寻址与 session 跟随
**Acceptance Scenarios:**
4. 同一 cwd 两个并发 run A/B；`ai history windows --id A` 只反映 A 的 session；`--id B` 只反映 B 的；省略 `--id` 时明确报错提示必须显式寻址。
5. run A 内执行 `/resume` 切换 session 后，`ai history --id A` 查到的是切换后 session 的代际与内容。
6. `--id` 为不存在的 ID / 无法唯一匹配的前缀 → 非零退出码 + 明确错误；run 结束（status=done）后 `--id` 仍可解析查询。

### P0 Feature: 有界输出
**Acceptance Scenarios:**
7. search 命中 100+ 条时默认只返回 20 条 + `total_count: 100+`；`--limit 200` 被钳制到 100。
8. list 中超长消息截断到 `--max-chars` 并带 `…[truncated, N chars total]` 标记。
9. 所有诊断/警告走 stderr，stdout 在 `--json` 下为可 `jq` 的合法 JSONL；管道 `ai history search x --json --id r | jq length` 可用。

## 6. 边界条件和特殊情况

- `read --entry` 的 offset 超出 total → 空 content + `total_chars`，退出码 0（"到头了"是合法状态）。
- entry_id 不存在 → 明确错误，非零退出（不静默空返回）。
- search 无命中 → 空列表 + `total_count: 0`，退出码 0。
- snapshot 文件缺失/损坏 → stderr 警告 + 跳过该 snapshot，其余命中正常返回。
- 查询期间并发 compaction → 查询层持锁，保证单次调用的一致视图。
- lazy loading 未完成的 session → 查询前强制全量加载。
- 空 session / 从未 compact → `windows` 返回单个 window（header 代际）。
- 非 message entry（compaction/session_info/branch_summary）默认不出现在 list/search；compaction summary 可经 `--role system` 观察（可选实现，若实现成本高则 defer）。
- `--window` 传了不存在的 window_id → 明确错误。
- run.json 缺 Session 字段（旧 run）→ 明确错误提示"该 run 未记录 session，用 --session <path> 指定"（`--session` 逃生门已实现，便于人工使用）。

## 7. Deferred 与非目标（承自原 plan）

原 `plan-history-tool.md` 中未随本次实现的内容，留作后续参考：

- **inline `[id:]` 注入（方案 a）**：在 live context 每条消息末尾追加 `\n[id: <EntryID>]`（Codex 做法），让模型"看到什么就能引用什么"。已实现的是方案 b（search/list 返回 `entry_id`，模型两跳引用）。方案 a 需验证缓存影响：ID 短且固定，前缀生成后稳定，理论不影响 prefix cache，但必须对照 `pkg/compact` 的 cache_read 指标确认。
- **branch 按 `entry_id` 展开**：非当前分支的历史默认不可达，留 follow-up。
- **非目标（不变）**：`new_context`（硬重置）、`notes`（跨窗口持久工作状态）各自独立 PR；语义/embedding 检索明确不做，保持字面子串；远程/多机不支持。

## 8. 已拍板的决策

原设计稿的 Open Points 实现结果：

1. search 默认大小写不敏感 + `--case-sensitive` 开关 → **采用**。
2. `--session <path>` 逃生门 → **已实现**（人工/agent 直接指定 session dir）。
3. 单次输出上限 40000 chars、read `--max-chars` 上限 50000 → **采用**。
4. skill 名称 → **`session-history`**。
5. （实现期追加）寻址收敛为仅 run id：cwd 自动选择与 `--latest` 被移除，原因见 §4.1。
