---
name: debate
description: Orchestrate a real-time alternating debate between two subagents using ai serve/send. Judge controls rounds by prompting each agent in turn.
---

# Debate Skill

## Overview

Run a structured debate between two agents (proposer FOR, opposer AGAINST). You are the judge — you control rounds by prompting each agent in turn. Agents write to a shared file and you read their output.

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

# Spawn proposer agent
tmux new-session -d -s "proposer" \
  "ai serve --system-prompt '@$SYSTEM_DIR/proposer-system.md' \
   --name 'proposer' --timeout 30m"
sleep 2
PROPOSER_ID=$(tmux capture-pane -t "proposer" -p | head -1 | tr -d '[:space:]')

# Spawn opposer agent
tmux new-session -d -s "opposer" \
  "ai serve --system-prompt '@$SYSTEM_DIR/opposer-system.md' \
   --name 'opposer' --timeout 30m"
sleep 2
OPPOSER_ID=$(tmux capture-pane -t "opposer" -p | head -1 | tr -d '[:space:]')
```

## Debate Loop

```bash
for round in $(seq 1 "$ROUNDS"); do
  echo "=== Round $round ==="

  # --- Proposer ---
  if [ "$round" -eq 1 ]; then
    PROPROMPT="Topic: $DEBATE_TOPIC

$DEBATE_CONTEXT

Write your R1 arguments to: $DEBATE_FILE
Ground every claim in specific code. When done, just stop — the judge will read your output."
  else
    # Inline opponent's last round from debate file
    OPPONENT_ARGS=$(tail -100 "$DEBATE_FILE")
    PROPROMPT="Round $round. Rebut the opponent's arguments:

$OPPONENT_ARGS

Append your R${round} rebuttal to: $DEBATE_FILE
Ground every claim in specific code."
  fi
  ai send --id "$PROPOSER_ID" "$PROPROMPT"
  ai watch --id "$PROPOSER_ID" --follow --pretty

  # --- Opposer ---
  if [ "$round" -eq 1 ]; then
    OPPPROMPT="Topic: $DEBATE_TOPIC

$DEBATE_CONTEXT

Read the proposer's R1 arguments from: $DEBATE_FILE
Append your R1 rebuttal to: $DEBATE_FILE
Ground every claim in specific code."
  else
    PROPONENT_ARGS=$(tail -100 "$DEBATE_FILE")
    OPPPROMPT="Round $round. Rebut the proposer's arguments:

$PROPONENT_ARGS

Append your R${round} rebuttal to: $DEBATE_FILE
Ground every claim in specific code."
  fi
  ai send --id "$OPPOSER_ID" "$OPPPROMPT"
  ai watch --id "$OPPOSER_ID" --follow --pretty
done
```

## Cleanup

```bash
ai kill --id "$PROPOSER_ID" 2>/dev/null
ai kill --id "$OPPOSER_ID" 2>/dev/null
tmux kill-session -t "proposer" 2>/dev/null
tmux kill-session -t "opposer" 2>/dev/null
```

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
| `ai serve` without tmux | Always wrap in `tmux new-session -d` |
| Send before serve is ready | `sleep 2` after tmux spawn |
| Both agents write same file simultaneously | Alternate: proposer first, then opposer |
| Agent re-reads debate file every round | Inline opponent arguments in prompt (R2+) |