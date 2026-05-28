---
name: debate
description: Orchestrate a real-time alternating debate between two subagents using ai serve/send. Judge controls rounds by prompting each agent in turn.
---

# Debate Skill

## Overview

Run a structured debate between two agents (proposer FOR, opposer AGAINST). You are the judge — you control rounds by prompting each agent in turn. Agents write to a shared file and you read their output.

**子 agent 生命周期遵循 `subagent` 技能：** spawn → watch → cleanup。辩论结束后必须 `ai kill` 清理双方 agent。

**⚠️ MUST：在执行任何子 agent 操作前，确认 `subagent` 技能已加载到当前上下文。如果未加载，先调用 `find_skill` 工具（参数 `name="subagent"`, `load=true`）加载它。**

**Key optimization:** R1 gives full context (working directory, file paths, reference links) so the agent doesn't waste time discovering what you already know. R2+ inlines the opponent's argument in the prompt so the agent doesn't re-read the debate file.

## Setup

```bash
DEBATE_TOPIC="YOUR TOPIC HERE"
DEBATE_CONTEXT="- Working directory: ...
- Key file paths: ...
- Reference links: ..."
ROUNDS=3
DEBATE_FILE="/tmp/debate-$(date +%s).md"
SYSTEM_DIR="/Users/genius/.ai/skills/debate/references"

# Create empty debate file
touch "$DEBATE_FILE"
```

用 `subagent` 技能 spawn 2 个子 agent（并行），参数：

| Agent | system-prompt | name | timeout |
|-------|---------------|------|---------|
| Proposer | `@$SYSTEM_DIR/proposer-system.md` | `proposer` | `30m` |
| Opposer | `@$SYSTEM_DIR/opposer-system.md` | `opposer` | `30m` |

> 完整 spawn 代码（tmux + `--id-file`）见 `subagent` 技能 Spawn 阶段。

## Debate Loop

遵循 `subagent` 技能 Multi-Turn 模式（`ai send` + `ai watch` 循环）。

每轮的 prompt 构造逻辑：

**Proposer prompt（R1）：**
```
Topic: $DEBATE_TOPIC

$DEBATE_CONTEXT

Write your R1 arguments to: $DEBATE_FILE
Ground every claim in specific code. When done, just stop — the judge will read your output.
```

**Proposer prompt（R2+）：**
```
Round $round. Rebut the opponent's arguments:

$(tail -100 "$DEBATE_FILE")

Append your R${round} rebuttal to: $DEBATE_FILE
Ground every claim in specific code.
```

**Opposer prompt（R1）：**
```
Topic: $DEBATE_TOPIC

$DEBATE_CONTEXT

Read the proposer's R1 arguments from: $DEBATE_FILE
Append your R1 rebuttal to: $DEBATE_FILE
Ground every claim in specific code.
```

**Opposer prompt（R2+）：**
```
Round $round. Rebut the proposer's arguments:

$(tail -100 "$DEBATE_FILE")

Append your R${round} rebuttal to: $DEBATE_FILE
Ground every claim in specific code.
```

每轮顺序：`ai send` 给 Proposer → `ai send --wait` 等待完成 → `ai send` 给 Opposer → `ai send --wait` 等待完成。

## Cleanup

遵循 `subagent` 技能 Cleanup 阶段（`ai kill` + `rm -f $ID_FILE`），清理 Proposer 和 Opposer。

## Flow Diagram

```
Judge (you)                Proposer              Opposer
   │                          │                      │
   ├── send R1 context ──────►│                      │
   │  (working dir, files)    │ reads source,        │
   │                          │ writes to debate file│
   │◄── watch done ───────────│                      │
   │                          │                      │
   ├── send R1 rebuttal ────────────────────────────►│
   │  (inline proposer R1)    │                      │
   │                          │  reads source,       │
   │                          │  appends rebuttal    │
   │◄── watch done ──────────────────────────────────│
   │                          │                      │
   ├── send R2 (inline opponent) ──►│                 │
   │  ... no file re-read after R1 ...│               │
```

**R1:** Each agent reads source code once.
**R2+:** Opponent argument inlined in prompt. No file re-read. Fast rebuttal.

## Verdict

After the debate, read the transcript and synthesize:

```markdown
## Debate Verdict

**Topic:** <topic>
**Conclusion:** <feasible | not feasible | feasible with conditions>

### Key Arguments

| Round | Proposer (正方) | Opposer (反方) |
|-------|----------------|----------------|
| 1     | <key point>    | <key rebuttal> |
| ...   | ...            | ...            |

### Decisive Factor
<what tipped the balance>
```

## File Assets

- `references/proposer-system.md` — "You argue FOR feasibility"
- `references/opposer-system.md` — "You argue AGAINST feasibility"

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve` in foreground | 遵循 `subagent` 技能 spawn 模式 |
| Send before serve is ready | `sleep 1` after spawn, then `cat $ID_FILE` |
| Both agents write same file simultaneously | Alternate: proposer first, then opposer |
| Agent re-reads debate file every round | Inline opponent arguments in prompt (R2+) |