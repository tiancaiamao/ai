---
name: debate
description: Orchestrate a real-time alternating debate between two subagents. Judge controls rounds via prompt, agents signal completion via channel.
---

# Debate Skill

## Overview

Run a structured debate between two agents (proposer FOR, opposer AGAINST). You are the judge — you control rounds by prompting each agent in turn. Agents write to a shared file and signal completion via `ag send`. You block on `ag recv --wait`.

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

# Clean previous
ag agent rm proposer --force 2>/dev/null
ag agent rm opposer --force 2>/dev/null
ag channel rm judge 2>/dev/null

# Create judge channel
ag channel create judge

# Spawn agents (non-blocking, returns in ~20ms)
ag agent spawn proposer --system "@$SYSTEM_DIR/proposer-system.md"
ag agent spawn opposer --system "@$SYSTEM_DIR/opposer-system.md"
```

## Debate Loop

```bash
# ── Round 1: Proposer opens (full context, reads source code) ──
ag agent prompt proposer "Working directory: /path/to/project

DEBATE TOPIC: $DEBATE_TOPIC

Context:
$DEBATE_CONTEXT

Write your opening argument FOR feasibility to $DEBATE_FILE.
Format:

## Round 1 — Proposer

Ground every claim in specific code. When done, run: ag send judge proposer-r1-done"

ag recv judge --wait --timeout 600

# ── Round 1: Opposer counters (reads debate file for proposer argument + source code) ──
ag agent prompt opposer "Working directory: /path/to/project

DEBATE TOPIC: $DEBATE_TOPIC

Context:
$DEBATE_CONTEXT

Read $DEBATE_FILE for the proposer's opening argument.
Then append your counter-argument AGAINST feasibility.
Format:

## Round 1 — Opposer

Address the proposer's specific claims. When done, run: ag send judge opposer-r1-done"

ag recv judge --wait --timeout 600

# ── Rounds 2+: Inline opponent argument, no file re-read ──
for ROUND in $(seq 2 $ROUNDS); do
  # Extract opponent's latest argument from debate file
  OPPONENT_ARG=$(sed -n "/^## Round $((ROUND-1)) — Opposer/,/^## Round/p" $DEBATE_FILE | head -n -1)

  ag agent prompt proposer "Working directory: /path/to/project

Here is the opposer's Round $((ROUND-1)) argument:

$OPPONENT_ARG

---

Rebut directly. Use your earlier code analysis — do NOT re-read source files.
Append to $DEBATE_FILE:

## Round $ROUND — Proposer

When done, run: ag send judge proposer-r${ROUND}-done"

  ag recv judge --wait --timeout 600

  if [ "$ROUND" -lt "$ROUNDS" ]; then
    MY_ARG=$(sed -n "/^## Round $ROUND — Proposer/,/^## Round/p" $DEBATE_FILE | head -n -1)

    ag agent prompt opposer "Working directory: /path/to/project

Here is the proposer's Round $ROUND argument:

$MY_ARG

---

Counter directly. Use your earlier code analysis — do NOT re-read source files.
Append to $DEBATE_FILE:

## Round $ROUND — Opposer

When done, run: ag send judge opposer-r${ROUND}-done"

    ag recv judge --wait --timeout 600
  fi
done

# ── Read result & cleanup ──
cat $DEBATE_FILE
ag agent rm proposer --force
ag agent rm opposer --force
ag channel rm judge
```

## How It Works

```
Judge                              Proposer              Opposer
  │                                   │                      │
  ├── prompt (full ctx + topic) ─────►│                      │
  │                                   ├── read source (once) │
  │                                   ├── write R1 to file   │
  │   recv judge ◄───────────────────┤── ag send "done"     │
  │                                   │                      │
  ├── prompt (full ctx + R1 file) ──────────────────────────►│
  │                                   │                      ├── read R1 + source (once)
  │                                   │                      ├── append R1 to file
  │   recv judge ◄──────────────────────────────────────────┤── ag send "done"
  │                                   │                      │
  ├── prompt (inline R1 opponent) ──►│                      │
  │                                   ├── rebut directly     │
  │   recv judge ◄───────────────────┤── ag send "done"     │
  │                                   │                      │
  ├── prompt (inline R2 opponent) ──────────────────────────►│
  │  ... no file re-read after R1 ... │                      │
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