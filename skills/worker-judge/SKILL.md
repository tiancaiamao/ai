---
name: worker-judge
description: Worker-Judge 迭代循环。一个 agent 产出，另一个独立 agent 审查，循环直到通过。适用于需要质量保证的任何任务（规划、实现、审查等）。
---

# Worker-Judge Loop

Worker 产出，Judge 独立审查，循环迭代直到通过。

**灵感来源：** GAN（生成对抗网络）的 Generator-Discriminator 竞争反馈循环。Worker 是 Generator，Judge 是 Discriminator。两者的独立性是质量保证的关键。

## When to Use

- 需要独立验证质量的任务（规划审查、代码审查、设计评审）
- 单个 agent 的 self-evaluation 不可靠（Anthropic 发现 Agent 审自己的代码会自信地夸自己）
- 任务有明确的验收标准

**不要用于：** 简单任务、没有明确标准的任务

## Core Insight

来自 Anthropic 和 OpenAI 的研究发现：
1. **Self-evaluation bias** — Agent 审查自己的输出会给出不切实际的正面评价
2. **Context anxiety** — 接近上下文限制时 Agent 会提前结束。Worker 保持存活可以缓解，但 Judge 应该每轮新 spawn
3. **2-3 轮通常收敛** — 超过 5 轮说明 spec 有问题，应暂停并报告用户

## Architecture

```
┌──────────────┐     output      ┌──────────────┐
│   Worker     │ ──────────────► │    Judge     │
│ (persistent) │                 │  (fresh each │
│              │ ◄── feedback ── │    round)    │
└──────────────┘                 └──────────────┘
      │                                │
      │  if APPROVED ──────────────► done
      │  if CHANGES_REQUESTED ──► next round
      │
      └── max rounds reached ──► report to user
```

**Worker 保持跨轮次存活** — 保留完整上下文，能在先前尝试的基础上迭代改进。
**Judge 每轮重新 spawn** — 保持独立性，不受前几轮审查结果的锚定效应影响。

## Implementation with ai CLI

```bash
SESSION="wjl-$$"
WORKER_SYSTEM="@$HOME/.ai/skills/plan/prompts/planner.md"
JUDGE_SYSTEM="@$HOME/.ai/skills/plan/prompts/reviewer.md"
INPUT="Read design.md at /path/to/design.md and produce tasks.md"
MAX_ROUNDS=3

# --- Round 1: Spawn Worker ---
tmux new-session -d -s "${SESSION}-w" \
  "ai serve --system-prompt '${WORKER_SYSTEM}' \
   --input '${INPUT}' \
   --name 'worker-r1' \
   --timeout 10m"

sleep 2
WORKER_ID=$(tmux capture-pane -t "${SESSION}-w" -p | head -1 | tr -d '[:space:]')

# Wait for worker to finish round 1
ai watch --id "$WORKER_ID" --follow --pretty

# --- Get Worker Output ---
# Worker should write output to a known file (e.g., /tmp/worker-output.md)
# If not, the output is in the watch stream

for round in $(seq 2 "$MAX_ROUNDS"); do
  # --- Spawn Judge (fresh each round) ---
  JUDGE_INPUT="Review the following plan:\n\n$(cat /tmp/worker-output.md)"
  tmux new-session -d -s "${SESSION}-j-r${round}" \
    "ai serve --system-prompt '${JUDGE_SYSTEM}' \
     --input '${JUDGE_INPUT}' \
     --name 'judge-r${round}' \
     --timeout 5m"

  sleep 2
  JUDGE_ID=$(tmux capture-pane -t "${SESSION}-j-r${round}" -p | head -1 | tr -d '[:space:]')
  ai watch --id "$JUDGE_ID" --follow --pretty

  # --- Check verdict ---
  # Judge should write verdict to a known file
  if grep -qi "APPROVED\|PASS\|ACCEPT" /tmp/judge-verdict.md; then
    echo "✅ Approved in round $round"
    ai kill --id "$WORKER_ID" 2>/dev/null
    ai kill --id "$JUDGE_ID" 2>/dev/null
    exit 0
  fi

  # --- Send feedback to Worker (not respawn) ---
  FEEDBACK="Judge feedback from round $round:\n\n$(cat /tmp/judge-verdict.md)\n\nAddress the feedback and produce an improved version."
  ai send --id "$WORKER_ID" "$FEEDBACK"
  ai watch --id "$WORKER_ID" --follow --pretty

  # Cleanup judge
  ai kill --id "$JUDGE_ID" 2>/dev/null
  tmux kill-session -t "${SESSION}-j-r${round}" 2>/dev/null
done

echo "❌ Max rounds ($MAX_ROUNDS) reached without approval"
ai kill --id "$WORKER_ID" 2>/dev/null
```

## Worker-Judge Prompt Guidelines

### Worker Prompt
- 必须包含明确的输出格式要求（让 Judge 有标准可审）
- 应被告知："你的输出会被独立审查者评估"
- 输出应写入指定文件路径

### Judge Prompt
- **必须独立于 Worker** — 不能看到 Worker 的 system prompt 或输入
- 只看到 Worker 的输出 + 评估标准
- 输出结构化判定：`APPROVED` 或 `CHANGES_REQUESTED` + 具体问题列表
- 应包含明确的 pass/fail 标准（"Must Pass" blockers）

### Key Principle: Separation of Concerns
| Worker | Judge |
|--------|-------|
| 知道任务要求 | 只看到输出 |
| 知道上下文 | 不知道设计决策的背景 |
| 追求完整性 | 追求正确性 |
| 可能过度自信 | 必须严格挑剔 |

## Usage in Other Skills

| Skill | Worker | Judge |
|-------|--------|-------|
| `plan` | Planner (produces tasks.md) | Plan Reviewer (checks self-containedness) |
| `pge` | Generator (produces code) | Evaluator (checks spec compliance + quality) |
| `review` | (single pass, no loop) | — |

## Convergence Guidelines

| Rounds | Meaning |
|--------|---------|
| 1 | Excellent — output was correct first time |
| 2-3 | Normal — typical convergence |
| 4-5 | Warning — spec may be underspecified |
| >5 | Problem — stop and report to user. The spec or acceptance criteria need revision. |

## MANDATORY Self-Check

| Assertion | Trigger | Fix |
|-----------|---------|-----|
| Judge not independent | Judge shares context with Worker | Spawn Judge fresh each round |
| Worker respawned each round | Worker started from scratch each iteration | Keep Worker alive, send feedback via `ai send` |
| No convergence limit | Loop runs indefinitely | Max 5 rounds, report to user |
| Judge output not checked | Verdict ignored | Parse verdict for APPROVED/CHANGES_REQUESTED |
| Worker output not captured | Nothing to send to Judge | Worker must write to known file path |