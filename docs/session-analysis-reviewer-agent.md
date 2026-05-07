# Session Analysis: Reviewer Agent Infinite Thinking Loop

**Session**: `09559edf-d216-4353-93e9-953802aeca14`
**时间**: 2026-05-07 19:11:23 → 19:30:38 (~19 min, killed)
**CWD**: `/Users/genius/project/ai/.worktrees/arch-deepen`
**分析模式**: full (tools + flow + prompt + context-mgmt)

## 摘要
- **用户意图**：让 reviewer agent 分析 tasks.yml 的 task 边界正确性，输出 JSON review 结果
- **agent 执行**：正常探索了 15 个 turns（31 次工具调用），但从未产出文本输出或结果文件。在 turn 12-13 陷入 thinking 爆炸（各 78-80 秒），在 turn 16 进入无止境的 thinking，最终被 kill。
- **发现问题**：5 个（1 Critical, 2 Medium, 2 Suggestion）

---

## 🔴 Critical Issues

### Issue 1: 模型在 Turn 16 进入无限 Thinking 循环

**位置**：Trace file 1 末尾，`message_start` event with `stop_reason: ""`

**问题描述**：
Agent 在完成 15 个 turns 的工具调用后，开始生成第 16 轮响应。Trace 最后一帧是 `message_start`（stop_reason=""），表明模型开始 streaming 但从未完成。此后的 **15 分 17 秒**没有任何 trace 事件，agent 处于无限 thinking 状态，直到被 kill。

**具体证据**：
```
Trace 最后一帧:
  name: message_start
  ts: 1778152521.5 (agent start + 238.5s = 19:15:21)
  stop_reason: ""  ← 没有停止

Agent 被 kill: 19:30:38
Gap: 917s (15m17s) 无 trace 输出
```

**Timeline 重建**：
```
0:00  Turn 1-2: 读取 input/tasks.yml/design.md (正常)
0:17  Turn 3-11: 代码探索，31 次 grep/sed/read (正常)
1:00  Turn 12: 78s thinking 爆炸，2874 output tokens
2:20  Turn 13: 80s thinking 爆炸，3566 output tokens
4:00  Turn 14-15: 2 次小工具调用 (正常)
4:00  Turn 16 开始 → 无限 thinking → 被 kill (19:30)
```

**为什么这是个问题**：
Agent 没有终止条件约束。模型认为需要更多思考才能给出结论，但 thinking 机制没有 token 预算上限。每轮 thinking 产出越多，下一轮的 context 越大（因为 tool results 被追加），形成正反馈循环。

**根因分析**：
1. **No `--max-turns` limit**：`ag agent spawn` 没有限制最大 turn 数
2. **No `--max-thinking-tokens` limit**：thinking 输出没有预算上限
3. **Prompt 缺乏终止条件**：input 没有告诉 agent "最多 N 轮后必须输出"
4. **模型自身问题**：`glm-5.1` 模型在复杂分析任务中，可能不会主动收敛 thinking

**改进建议**：
```go
// ag CLI: 添加 turn 和 thinking 限制
ag agent spawn reviewer \
  --max-turns 10 \
  --max-thinking-tokens 8000 \
  --input @/path/to/input.md

// 或在 input prompt 中加入：
"你最多可以使用 8 次工具调用，然后必须输出结论到 /tmp/result.json"
```

---

### Issue 2: Turn 12-13 的 Thinking 突然爆炸（10x-30x 增长）

**位置**：Trace `llm_call` events, turn 12 (ts=1778152427.3) 和 turn 13 (ts=1778152508.0)

**问题描述**：
Turn 1-11 的 output tokens 稳定在 40-255 范围（avg ~120）。Turn 12 突然跳到 2874 tokens（23x），turn 13 进一步到 3566 tokens（30x）。LLM 调用时长也从 ~6s 跳到 78-80s。

**具体证据**：
```
Turn  | Output Tokens | LLM Duration | Output/Input%
------|---------------|------------- |--------------
1-11  | 40-255 (avg 120) | 3-10s      | 0.6-2.7%
12    | 2874          | 78.1s        | 18.4%  ← 爆炸
13    | 3566          | 80.6s        | 22.0%  ← 更大
14-15 | 41-97         | 3-7s         | 0.2-0.6% ← 恢复正常
```

**为什么这是个问题**：
Thinking 爆炸把 tool results 的 context 从 15210 tokens 推到 16772 tokens（+10%），但产出仍然是 tool calls（没有 text）。模型花了 160 秒思考但没有产出任何可见输出。这不是正常的 "extended thinking"——是模型在分析-探索循环中失控。

**根因分析**：
Turn 11 的最后一个工具调用是 `grep -n 'func.*Agent.*Set\|func.*Agent.*Get' pkg/agent/agent.go`，探索 Agent 的接口方法。这可能触发了模型对 "整个 codebase 架构应该如何重构" 的深度推理——远远超出了 review tasks.yml 边界的任务范围。模型不是在做 "review"，而是在做 "design"。

**改进建议**：
在 input prompt 中明确限制 scope：
```
"你的任务是 REVIEW 已有的 tasks.yml，不是重新设计架构。
只检查 task 边界是否合理，不要提出新的设计方案。"
```

---

## 🟡 Medium Issues

### Issue 3: `@file` 引用未被解析

**位置**：Session `messages.jsonl` 第 1 条 user message

**问题描述**：
`--input @/tmp/plan-review-input.md` 传递给 agent 后，内容是字面字符串 `@/tmp/plan-review-input.md`，而非文件内容。

**具体证据**：
```json
{"role": "user", "content": [{"type": "text", "text": "@/tmp/plan-review-input.md"}]}
```

**为什么这是个问题**：
Agent 需要 extra turn 来 read 这个文件（Turn 1），浪费了一个 turn。更重要的是，如果文件路径不存在或 agent 无法 read，任务直接失败。主 agent（对话模式）中 `@file` 引用会被展开，但 `ag agent spawn` 没有。

**改进建议**：
在 `ag agent spawn` 中实现 `@file` 展开：
```go
// Spawn input handling: resolve @file references before sending
if strings.HasPrefix(input, "@") {
    content, err := os.ReadFile(input[1:])
    // use content as input
}
```

### Issue 4: Thinking 内容未持久化，无法复盘

**位置**：Session `messages.jsonl`

**问题描述**：
Turn 12-13 共产出 6440 tokens 的 thinking，但 `messages.jsonl` 中的 assistant message 只有 `toolCall` blocks，没有任何 thinking 内容。482MB 的 `events.jsonl`（已被清理）是唯一的 thinking 记录。

**具体证据**：
```
MSG 12 (Turn 12):
  Content blocks: 2
  Block 0: type=toolCall name=bash
  Block 1: type=toolCall name=bash
  # ← 0 thinking blocks, 0 text blocks, 但 LLM 实际产出了 2874 tokens
```

**为什么这是个问题**：
复盘 agent 行为时，无法知道 "模型当时在想什么"。Thinking 内容是调试 agent 行为的关键数据。当前行为：thinking 在 streaming 中产生 → 写入 events.jsonl → session persistence 只保存 tool calls/text。

**改进建议**：
考虑在 session messages.jsonl 中保存 thinking 摘要（前 N 字符或全部），或在 `meta.json` 中记录 thinking token 统计：
```json
{"thinking_tokens": 2874, "text_tokens": 0}
```

---

## 🟢 Suggestions

### Suggestion 1: 增加 Agent 超时 heartbeat

**观察**：
Agent 在 19:15 进入无限 thinking，到 19:30 被 kill（手动）。中间 15 分钟没有任何输出。对于自动化场景（cron 触发 review），需要自动检测和终止。

**优化方案**：
在 ag runtime 中添加 heartbeat 检测：如果 N 分钟内没有 tool call 或 text output，自动 kill agent 并标记为 `timeout`。

### Suggestion 2: 添加 trace 文件轮转

**观察**：
Agent 只生成了 2 个 trace 文件（`.0.perfetto.json` 和 `.1.perfetto.json`）。第二个文件在 turn 16 的 `message_start` 后停止记录。15 分钟的空白期没有任何 trace。

**优化方案**：
确保 trace 文件在长 thinking 阶段也能持续写入，或在 `message_start` 后设置 flush interval。

---

## 总结

### 优先修复

1. **[Critical]** `ag agent spawn` 添加 `--max-turns` 和 `--max-thinking-tokens` 参数
2. **[Critical]** Spawn input 中明确终止条件和 scope 限制
3. **[Medium]** `@file` 引用在 spawn 时展开

### 可选优化

4. **[Suggestion]** 添加 agent heartbeat/超时检测
5. **[Medium]** Thinking 内容持久化或至少记录统计

### 下次关注点

- 修复 `--max-turns` 后，验证 reviewer agent 是否能在限制内产出结果
- 关注 `glm-5.1` 模型在其他复杂分析任务中的 thinking 稳定性
- 对比其他模型（如 Claude）在相同任务中的 thinking 行为差异