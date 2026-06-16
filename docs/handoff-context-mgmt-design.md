# Handoff-Based Context Management Design

> Status: Design specification. All decisions confirmed through iterative
> review (3 rounds). References code paths on `main` (commit `f6dd3d5`).
>
> This document supersedes the LLM-driven compaction model described in
> [`context-management.md`](./context-management.md) for the *proactive* context
> management path. The reactive recovery path is retained.

---

## 1. Problem Statement

### 1.1 The two governing principles

The maintainer stated two non-negotiable principles:

1. **"Only incremental is viable."** Near-full compaction is too late — by the
   time the window is 75% full, context quality has already degraded. Percentage
   thresholds scale incorrectly when windows grow (200K→1M: the same percentage
   represents a vastly different absolute token count and cost). The only viable
   strategy is incremental management.

2. **"Compaction always breaks cache, so the decision must weigh cost-effectiveness
   AND context quality."** Any compaction that **edits or replaces** existing
   messages invalidates the provider's prefix cache from the edit point onward —
   the previously cached prefix no longer matches. Note the asymmetry:
   **appending** messages (like Q&A turns during handoff) extends the prefix but
   does not invalidate the already-cached portion — the cache hit applies to the
   unchanged prefix, only the new suffix is a miss. This is why Q&A turns are
   cache-warm for the new checkpoint (§3.2 Phase B) while compaction edits are
   cache-hostile. Therefore the trigger cannot be purely mechanical (token count).
   It must consider both the economic tradeoff (cache miss cost vs. token savings)
   and context quality.

### 1.2 Why existing approaches fail

| Approach | Violates | Reason |
|----------|----------|--------|
| Percentage threshold (75%) | Principle 1 | Scales wrong with window growth; triggers too late |
| Near-full compaction | Principle 1 | Context already rot by the time it fires |
| Delta compaction (blind summary) | Principle 2 | LLM writes a summary that no one verifies; errors frozen into context |
| Existing ContextManager | Both | LLM-driven but unverified: a separate LLM call decides which tool (`truncate_messages`, `compact`, `no_action`) to invoke, but no fresh agent verifies the result — the summary is accepted on faith. Token-threshold trigger still scales wrong (Principle 1). |

### 1.3 Why handoff is different

Handoff replaces blind compression with **verified context transfer**. A fresh
context (seeded with a handoff document) reads the document, explores the
codebase, and asks questions about anything unclear. The Q&A verification loop
makes information loss *discoverable and correctable* — the critical capability
that all compaction approaches lack.

An experiment validated this: a fresh agent reading a handoff document
identified 11 gaps including factual errors that a Q&A loop caught and corrected.

---

## 2. Core Concept

### 2.1 Two checkpoint systems, selected by mode

The codebase **already has** a checkpoint system for crash recovery:
`pkg/context/checkpoint.go` creates `checkpoints/checkpoint_%05d/` directories,
each holding an `AgentContext` snapshot (`agent_state.json`, `llm_context.txt`,
snapshot `messages.jsonl`). A `current` symlink and `checkpoint_index.json`
track the latest. This is a snapshot+WAL recovery model — the session's
`messages.jsonl` is the write-ahead log; resume loads the snapshot then replays
journal entries after it. See `pkg/context/README.md`.

Handoff introduces a **second** checkpoint concept: context-segment boundaries.
These are fundamentally different purposes:

| | Legacy checkpoint (`checkpoint_%05d`) | Handoff checkpoint (`cp_NNN`) |
|--|--|--|
| Purpose | Crash recovery (snapshot + WAL replay) | Context segmentation (handoff boundary) |
| Trigger | Pre/post-compaction, context-limit recovery | Economic threshold + quality signals |
| Contents | `AgentContext` memory snapshot | A segment of conversation history |
| Mutability | Frozen snapshot, WAL appends after | Frozen segment, new segment created |

**The two systems never interact.** A `contextManagement.mode` field in
`SessionMeta` selects which system a session uses. On resume, the mode is read
first; each mode has its own checkpoint directory layout, loading code, and
lifecycle. Legacy sessions keep their existing layout unchanged. Handoff
sessions use the layout below.

### 2.2 Handoff checkpoint layout (handoff mode only)

A handoff-mode session is a chain of context-segment checkpoints, each with its
own `messages.jsonl`:

```
~/.ai/sessions/--<cwd>--/
  meta.json                         # SessionMeta, includes contextManagement.mode
  current.txt                       # "cp_003" — points to the active segment
  checkpoints/                      # Handoff segments
    cp_001/
      messages.jsonl                # Frozen after handoff. Never modified again.
    cp_002/
      handoff.md                    # cp_001→cp_002 handoff document
      messages.jsonl                # Frozen. Contains: [handoff doc seed + Q&A turns]
    cp_003/
      handoff.md                    # cp_002→cp_003 handoff document
      messages.jsonl                # Currently active
```

Each checkpoint's `messages.jsonl` uses the **existing SessionEntry format**
(`pkg/session/entries.go`). No new file format is invented. The handoff document
is the first `EntryTypeMessage` (role=user); Q&A turns are subsequent
`EntryTypeMessage` entries. The `SessionHeader` gains a `ParentCheckpoint`
field pointing to the parent segment's path, enabling full history recovery.

**Crash recovery in handoff mode:** Handoff mode does not use the legacy
snapshot+WAL system. Instead, crash recovery relies on: (1) each segment's
`messages.jsonl` is append-only and frozen after handoff, so it is always
consistent; (2) `current.txt` is updated atomically (write temp + rename) only
after the new segment is fully written (§3.4). A crash mid-handoff leaves
`current.txt` pointing to the old segment; the half-written new segment is
ignored. No journal replay needed.

### 2.2 What handoff does NOT replace

Handoff replaces **proactive** context management. It does **not** replace the
**reactive** safety net at `loop.go:214`:

```go
if llm.IsContextLengthExceeded(err) && len(config.Compactors) > 0 && state.compactionRecs < maxCompactionRecoveries {
    recoveryResult, recoveryErr := state.performCompaction(ctx, "context_limit_recovery", false, true)
```

This path fires when the LLM API itself rejects a request for exceeding the
context window. It needs an *immediate synchronous compression*, not a
multi-minute handoff. It stays as-is, operating **within** the current
checkpoint's `messages.jsonl` via the existing `EntryTypeCompaction` mechanism.

**Two levels, independent:**

| Level | Mechanism | Trigger | Scope |
|-------|-----------|---------|-------|
| Inter-checkpoint | Handoff (new checkpoint) | Quality signals + economic model | Full context reset |
| Intra-checkpoint | Reactive compaction (existing) | `context_limit_recovery` | Single checkpoint file |

### 2.4 Economic model

The decision to handoff is fundamentally economic:

```
Cost of staying:       N × oldTokens × cacheHitRate        (cheap per-call, but tokens are large)
Cost of switching:     handoffMissCost + N × newTokens × cacheHitRate
                       (one-time cache miss + cheaper per-call afterwards)

Switch when:           handoffMissCost < N × (oldTokens - newTokens) × cacheHitRate
                       i.e., payback period < N remaining rounds
```

Where:
- `handoffMissCost ≈ newTokens × 50` (cache-miss input rate is ~50× hit rate)
- `N` = remaining rounds (unknowable; assume a conservative lower bound, e.g., ≥5)
- `oldTokens` = current context size
- `newTokens` = handoff document + Q&A size (typically 1-5% of oldTokens)

**Example:** 100K context → 1K handoff, 50× cache miss penalty:
```
Payback = (1K × 50) / (100K - 1K) ≈ 0.5 rounds  → pays back in ONE round
```

This is why a 1M-window model can trigger at 100K — not because 100K is "10%",
but because the compression ratio makes it economically justified. The threshold
is **payback-period-based**, not percentage-based.

---

## 3. Implementation: Loop-Driven (Not Subagent)

### 3.1 Why loop-driven, not subagent

> **Note:** The shipped skill (`.ai/skills/handoff/SKILL.md`) describes a
> **subagent-driven** handoff (spawn `ai serve`, Q&A via `ai send --wait`,
> subagent takes over). This design **supersedes** that approach. The skill
> documents the original interactive concept; this section explains why the
> production implementation moves the mechanism into the loop. Once the
> loop-driven implementation lands, the skill will be updated to describe the
> new mechanism (and reduced to the operator-facing parts: when to trigger,
> how to write a good handoff doc).

Handoff is a **base feature** of the RPC layer, not a user-level tool. Spawning
a subagent (`ai serve`) would create a bottom-layer-depends-on-top-layer
inversion: `pkg/agent` (the loop) would depend on `cmd/ai` (the CLI that spawns
subagents) — a layering violation. The loop-driven approach keeps handoff in
`pkg/agent` / `pkg/compact`, parallel to `performCompaction`.

The implementation is structurally identical to the existing compaction path.
The `GenerateSummaryWithPrevious` function (`pkg/compact/compact_summary.go:27`)
already uses `llm.StreamLLM(ctx, model, llmCtx, apiKey, timeout)` — a low-level
primitive independent of the loop's streaming mechanism. Handoff reuses this
same primitive.

```
performCompaction:                  performHandoff:
├─ GenerateSummaryWithPrevious      ├─ GenerateHandoffDoc       (1× llm.StreamLLM)
│  (1× llm.StreamLLM)               ├─ Q&A loop (≤3 rounds)     (2× llm.StreamLLM per round)
├─ Replace RecentMessages           ├─ Write new checkpoint
└─ Return                           ├─ Switch current.txt
                                    └─ Replace RecentMessages
```

### 3.2 The handoff process (4 phases)

#### Phase A: Main agent writes handoff document

The main agent (whose context is degrading but not yet critical) writes the
handoff document using the handoff skill template
(`.ai/skills/handoff/references/handoff-template.md`). This is one
`llm.StreamLLM` call with a prompt that requests the structured document.

**Why the main agent writes it (not a subagent):** The main agent's context is
still cache-warm. Writing the document as an LLM call reuses the cached prefix,
minimizing cost. The main agent's context has NOT fully rotted yet — handoff
triggers early (see §4), while the agent is still coherent enough to write an
accurate document.

#### Phase B: Q&A verification loop

The core innovation. The main agent's loop drives a bounded Q&A process between
two LLM call targets:

```go
// Pseudocode for the Q&A loop
newCtx := []AgentMessage{handoffDocSeed}  // Fresh context = handoff doc only

for round := 0; round < 3; round++ {
    // New context asks questions (1× llm.StreamLLM on newCtx)
    questions := streamLLM(systemPrompt_qa, newCtx)
    if questions signals "READY" {
        break
    }
    newCtx = append(newCtx, userMsg(questions))

    // Old context answers (1× llm.StreamLLM on oldMessages + question)
    answers := streamLLM(systemPrompt_answer, append(oldMessages, userMsg(questions)))
    newCtx = append(newCtx, assistantMsg(answers))
}

// After loop: newCtx contains [handoff doc + Q1 + A1 + ... + READY]
```

**Key points:**
- The "old context answering" call appends the question to oldMessages and makes
  one `llm.StreamLLM` call. The result is NOT persisted to oldMessages — it is a
  read-only query on the old context. The old checkpoint's `messages.jsonl` is
  frozen; it never grows during handoff.
- The "new context asking" call operates on `newCtx` (handoff doc + accumulated
  Q&A), which grows each round.
- Maximum 3 rounds, but often 0-1 rounds suffice if the handoff document is
  complete (the subagent says "READY" immediately).
- Each round = 2 `llm.StreamLLM` calls. Total handoff cost: 1 (doc) + 2×3 (QA) =
  up to 7 LLM calls.

**The Q&A process IS the context of the new checkpoint.** After completion,
`newCtx` (handoff doc + all Q&A turns) becomes the seed for the new
checkpoint's `messages.jsonl`. This is intentional: the Q&A turns build a
cache-warm prefix for the new checkpoint, so its first real working turn gets a
cache hit.

#### Phase C: Checkpoint creation

1. Create `checkpoints/cp_NNN/` directory (next sequential number).
2. Write `handoff.md` (the handoff document).
3. Write `messages.jsonl` using the existing `SessionEntry` format:
   - `SessionHeader` with `ParentCheckpoint` = parent checkpoint path.
   - `EntryTypeMessage` entries for: handoff doc (role=user), each Q&A turn.
4. Update `current.txt` to `cp_NNN` (atomic write — this is the commit point).

#### Phase D: Context switch

After `current.txt` is updated, the loop reloads `agentCtx` from the new
checkpoint's `messages.jsonl`. The old `agentCtx` is discarded. The next LLM
turn runs with the fresh, Q&A-verified context.

The switch is **silent** — no system message telling the LLM "you were
handoff'd." The handoff document + Q&A is a complete, self-contained context.

### 3.3 LLM decision protocol

The LLM decides whether to execute handoff, but does **not** use a tool call.
The flow is:

1. Loop detects conditions → injects `<context_management>` reminder as a user
   message into `agentCtx.RecentMessages` before the next LLM call.
2. The reminder describes the handoff skill (or references it) and includes
   current context metrics (token count, quality signals, cache stats).
3. The LLM either:
   - **Ignores it** and continues working (handoff not warranted for this task).
   - **Executes handoff**: writes the handoff document as its text output, then
     emits a `<handoff_complete>` marker.
4. The loop detects `<handoff_complete>` in the LLM response, extracts the
   handoff document, and enters Phase B (Q&A loop) using `llm.StreamLLM`.

No new tool is registered. The protocol is a combination of injected reminders
and output markers — simpler than a tool call, and keeps all handoff logic in
the loop layer.

### 3.4 Failure and rollback

Handoff failure is safe by construction:

- **During Phase A/B (writing doc + Q&A):** The main session's `current.txt`
  has not changed. The old checkpoint (`cp_NNN`) is untouched. If anything fails
  (LLM error, timeout, user cancellation), discard the in-progress work. The
  loop continues from the current checkpoint as if nothing happened.

- **During Phase C (checkpoint creation):** Files are written before
  `current.txt` is updated. If the process crashes mid-write, `current.txt`
  still points to the old checkpoint. On resume, the old checkpoint loads
  normally. The half-written `cp_NNN+1` is ignored (can be GC'd or manually
  deleted).

- **After Phase D (switch complete):** No rollback. The old checkpoint is
  frozen but accessible via the `ParentCheckpoint` pointer. If the new context
  turns out to be wrong, the user can fork from a parent checkpoint.

**Dead-loop prevention:** If handoff fails due to OOM (`context_length_exceeded`
during Phase A/B), the reactive compaction path (`loop.go:213`) fires as a
fallback, compressing within the current checkpoint. This breaks the
"trigger→fail→retrigger" cycle because reactive compaction reduces the context
enough for the next handoff attempt to succeed.

---

## 4. Trigger Design

### 4.1 Two-layer trigger

| Layer | Mechanism | Purpose |
|-------|-----------|---------|
| **Soft trigger** | Quality/economic signals → inject reminder → LLM decides | Normal operation |
| **Hard底线** | Absolute token threshold → force handoff (override LLM) | Safety net against LLM refusal |

### 4.2 Soft trigger: economic threshold

The soft trigger fires based on the payback-period economic model (§2.4).
Rather than computing the exact formula at runtime (N is unknowable), we use
**absolute token-count thresholds** that approximate the payback crossover:

| Context window | Soft trigger starts at | Rationale |
|----------------|----------------------|-----------|
| 200K | 40K tokens used (20%) | At 40K, a handoff to ~2K pays back in ~1 round (50× miss penalty) |
| 1M | 100K tokens used (10%) | At 100K, a handoff to ~2K pays back in ~0.5 rounds |

**Reconciliation with Principle 1 (§1.1):** Principle 1 rejects *percentage*
thresholds ("75% full") because they scale wrong with window growth and trigger
too late. The thresholds here are **absolute token counts** derived from the
payback model, not percentages. They scale with window size because the payback
crossover does (larger windows can afford more context before handoff pays off).
This is economically grounded, not "purely mechanical." The ideal trigger (v2)
adds quality signals on top; v1 starts with the economic floor.

**These are starting points, not final values.** The constants are provisional
and should be tuned from real session traces. The design commits to the
*payback-period model*, not to exact threshold numbers.

The soft trigger does NOT immediately execute handoff. It injects a
`<context_management>` reminder and lets the LLM decide.

### 4.3 Soft trigger: injection frequency

Once the soft threshold is crossed, reminders are injected periodically:

- **First injection:** Immediately when threshold is crossed.
- **Subsequent injections:** Every N tool calls, where N is **dynamic** —
  the more context is used, the more frequent the reminders.

```
N = max(1, baseInterval - (currentTokens / softThreshold) × decayFactor)
```

Where `baseInterval` (e.g., 10) decreases as context usage approaches the hard
底线. At 2× the soft threshold, reminders may come every 2-3 tool calls. This
reuses the existing `ToolCallsSinceLastTrigger` counter
(`loop_state.go:314`).

### 4.4 Soft trigger: quality signals (future enhancement)

The economic threshold is the **initial** trigger. Quality signals — detecting
*context degradation* independent of token count — are a future enhancement
layered on top. Candidates identified during design:

| Signal | Source | Status |
|--------|--------|--------|
| Repeated-read (same file read >K times in W turns) | `toolLoopGuard` observation point (`tool_guard.go:113`) | Proposed, not implemented |
| Loop-guard feedback frequency | `toolLoopGuard.Observe()` return value | Existing infrastructure |
| Tool-output truncation density | `tool_output_truncated` trace event (bit 41) | Existing infrastructure |

These reuse existing data paths. They are **not required for the initial
implementation** — the economic threshold alone is sufficient to make handoff
functional. Quality signals make it *smarter*.

### 4.5 Hard底线 (safety net)

The hard底线 forces handoff (bypassing LLM decision) when remaining context is
insufficient for the handoff process itself (~20K tokens):

| Context window | Hard底线 |
|----------------|---------|
| 200K | 150K used (75%) — leaves 50K, enough for handoff |
| 1M | 200K used — absolute cap, because 1M windows fill slowly but handoff cost is fixed |

> **Note:** The 150K value for 200K windows coincidentally equals 75%. This is
> incidental — 150K is an **absolute token floor** derived from the handoff
> process's ~20K working budget, not a percentage threshold. The §1.1 objection
> to "75% full" targets *percentage-based* triggers that scale wrong; absolute
> token floors do not have that problem.

At the hard底线, the loop injects an urgent reminder. If the LLM still does not
emit `<handoff_complete>` within 2 turns, the loop executes handoff
automatically (Phase A uses a minimal prompt, skipping the LLM's judgment).

### 4.6 Where triggers check in the loop

The trigger check replaces the existing `performCompaction` call at
`loop.go:198`:

```
// Current (legacy mode):
state.savePreCompactionCheckpoint("pre_llm_threshold")
compacted, _ := state.performCompaction(ctx, "pre_llm_threshold", true, false)

// Handoff mode:
state.maybeInjectHandoffReminder(agentCtx)  // checks soft/hard thresholds,
                                            // injects <context_management> if needed
```

And in the LLM response processing, detect `<handoff_complete>` in the
assistant message:

```
if hasHandoffMarker(msg) {
    handoffDoc := extractHandoffDoc(msg)
    state.performHandoff(ctx, handoffDoc)  // Phase B + C + D
    continue  // next loop iteration uses fresh context
}
```

---

## 5. Session/Checkpoint Operations

### 5.1 Resume

Read `meta.json` → check `contextManagement.mode`. If `legacy`, use existing
resume path (`pkg/agent/resume.go:LoadResumeState` — checkpoint snapshot + WAL
replay). If `handoff`, read `current.txt` → load
`checkpoints/<current>/messages.jsonl`. No parent-entry traversal needed.

### 5.2 Fork

Copy the entire session directory to a new session ID. The new session inherits
all checkpoints, `current.txt`, `meta.json` (including mode), and the full
history. This is a **new filesystem-level implementation** that replaces the
existing message-level `Branch()` / `ForkSessionFrom` tree-walk for handoff-mode
sessions. (Legacy-mode sessions continue to use `ForkSessionFrom` unchanged.)

### 5.3 Rewind

Operates **within the current checkpoint only**. The existing `leafID`
tree-walk mechanism (`session.go:226`, `Branch()`) works unchanged within a
single `messages.jsonl`. Cross-checkpoint rewind (going from `cp_003` back to
`cp_002`) is **not supported** — the handoff document is the verified
replacement for that history.

### 5.4 /messages

Displays messages from the **current checkpoint only**. Earlier checkpoints'
content is accessible via `handoff.md` files (the verified summary). Users lose
the ability to scroll through raw pre-handoff messages — this is intentional:
pre-handoff context was degrading and unreliable; the handoff document is a
*better* representation.

### 5.5 Full history recovery

Every checkpoint stores a `ParentCheckpoint` pointer in its `SessionHeader`.
Following this chain (`cp_003 → cp_002 → cp_001`) reaches the original
`messages.jsonl` with complete, unmodified conversation history. Nothing is ever
lost. This satisfies the non-negotiable requirement: "regardless of how many
handoffs occur, the full conversation history is always recoverable."

---

## 6. Legacy Code Disposition

### 6.1 Configuration switch

A new config field controls the mode:

```json
{
  "contextManagement": {
    "mode": "handoff"    // "legacy" | "handoff" (default: "handoff")
  }
}
```

- `legacy`: Current behavior — proactive compaction via `CompactionController`
  (`pkg/agent/compaction_controller.go`) + `ContextManager`
  (`pkg/compact/context_management.go`) + `context_mgmt` tools
  (`pkg/tools/context_mgmt/`). LLM decides whether to compact; summary is
  accepted without verification. Plus `checkpoint_%05d` crash-recovery.
- `handoff`: All proactive compaction **disabled** (ContextManager,
  CompactionController, context_mgmt tools not wired). Only reactive
  `context_limit_recovery` remains. Handoff trigger + Q&A loop active.

**The mode is persisted in `SessionMeta`** (`pkg/session/manager.go:17`) as a
new `ContextManagementMode` field. This ensures resume knows which checkpoint
system and loading code to use — the two layouts are never mixed within a
session:

```go
type SessionMeta struct {
    // ... existing fields ...
    ContextManagementMode string `json:"contextManagementMode,omitempty"` // "legacy" | "handoff"
}
```

### 6.2 Disposition table

The table below covers **handoff-mode** behavior. In legacy mode, all components
remain active and unchanged — handoff code is simply not wired.

| Component | Location | Disposition (handoff mode) | Reason |
|-----------|----------|---------------------------|--------|
| `context_limit_recovery` | `loop.go:214` | **Retain** | Handoff cannot be reactive; safety net required |
| `performCompaction` (method) | `pkg/agent/loop_state.go:129` | **Retain** (reactive only) | Backs the `context_limit_recovery` path |
| `ContextManager` | `pkg/compact/context_management.go` | **Remove** from proactive path | Philosophy overlaps with handoff; unverified summary |
| `context_mgmt` tools | `pkg/tools/context_mgmt/` | **Remove** from proactive path | Tied to ContextManager |
| `CompactionController` | `pkg/agent/compaction_controller.go` | **Remove** from proactive path | Proactive trigger; superseded by handoff trigger |
| `ShouldCompact` (percentage) | `pkg/compact/compact.go:460` | **Remove** from proactive path | Retain for reactive path check |
| **Crash-recovery checkpoint system** | `pkg/context/checkpoint*.go`, `pkg/agent/checkpoint_manager.go`, `pkg/agent/resume.go` | **Inactive** (legacy only) | Snapshot+WAL recovery belongs to legacy mode; handoff mode uses frozen per-segment `messages.jsonl` + atomic `current.txt` switch for crash recovery. |
| **`ForkSessionFrom` / `Branch()`** | `pkg/session/manager.go`, `pkg/session/session.go:226` | **Inactive** (legacy only) | Handoff mode uses directory-copy fork (§5.2) |
| `toolLoopGuard` | `pkg/agent/tool_guard.go` | **Retain** | Loop safety + future quality signal source |
| `performCompaction` (pre_llm call) | `loop.go:198` | **Replace** with `maybeInjectHandoffReminder` | Handoff trigger replaces blind compaction |

> **Deletion timeline:** Legacy-mode components are retained until handoff mode
> is validated in production. Once confirmed stable, legacy code
> (`ContextManager`, `context_mgmt` tools, `checkpoint_%05d` system,
> `ForkSessionFrom`) is deleted in a single cleanup commit.

### 6.3 Migration

- The `mode` switch defaults to `handoff` (for validation).
- Existing sessions (flat `messages.jsonl`, no `checkpoints/` directory) are
  loaded in a compatibility mode: treated as a single implicit checkpoint
  (`cp_001`).
- No automatic migration of old sessions. New sessions use the checkpoint
  structure from creation.

---

## 7. Cache Verification (Implementation Gate)

### 7.1 The critical assumption

The economic model (§2.4) assumes **prefix cache is content-addressed (shared
across sequential LLM calls in the same process)**, not per-call-scoped. If
cache does not carry over between the Q&A turns and the first working turn of
the new checkpoint, the cache-inheritance benefit disappears and thresholds must
shift later.

**⚠️ This is a gate, not a footnote.** The thresholds in §4.2 (40K/100K) are
derived from the economic model, which depends on this assumption. If the
assumption fails, the thresholds are invalid. The verification below must run
**before** Phase 1 commits to concrete threshold numbers.

### 7.2 What is already known

- **`pkg/llm` response types have no cache-hit field.** Verified by grep: no
  `cache_hit_tokens`, `cached_tokens`, `CacheHit`, or `prompt_cache` field
  exists. A field must be added to capture this data.
- **The handoff path is in-process sequential calls** (§3.1, loop-driven). The
  cache scope question is specifically: does the provider's prefix cache persist
  across sequential `llm.StreamLLM` calls within one process/connection? Not
  cross-process.

### 7.3 Verification plan

1. Add a `CacheHitTokens` field to the LLM response type in `pkg/llm`.
2. In a single `ai serve` process, make two sequential `llm.StreamLLM` calls
   with an identical prefix (system prompt + same messages).
3. Check whether the second call's response reports cache-hit tokens.

If yes → cache persists across sequential calls → the economic model holds.
If no → cache is per-call → the Q&A cache-inheritance benefit disappears, and
the economic model must be recalculated (handoff becomes more expensive,
thresholds shift later, or the Q&A cache-warmth argument is dropped from the
justification entirely).

---

## 8. Open Questions Carried Forward

1. **Threshold constants** (soft trigger 40K/100K, hard底线 150K/200K,
   injection interval base/decay): all provisional. Require empirical tuning
   from real traces once the counters exist.

2. **Q&A round count** (currently 3): provisional. Real sessions may show that
   1-2 rounds suffice for well-written handoff documents. Tune from data.

3. **Session continuity across handoff**: the subagent (new checkpoint) is a
   logically separate context. The `ParentCheckpoint` pointer preserves data,
   but there is no "session merge" — if a user wants to reference a specific
   pre-handoff message, they must follow the pointer chain manually. Future
   enhancement: cross-checkpoint search.

4. **Cache scope** (§7): must be verified before the economic model is trusted.

5. **Quality signals** (§4.4): proposed but not implemented in the initial
   version. The economic threshold is sufficient for v1.

---

## Appendix A: Implementation Phases

### Phase 1: Config switch + disable proactive compaction
- Add `contextManagement.mode` to `pkg/config/config.go`.
- Add `ContextManagementMode` field to `SessionMeta` (`pkg/session/manager.go:17`).
- In `rpc_setup.go:createCompactors`, skip ContextManager/CompactionController
  when mode=handoff.
- In `loop.go:198`, skip `performCompaction("pre_llm_threshold")` when
  mode=handoff.
- **Verify:** `go build` passes; handoff mode triggers no proactive compaction.

### Phase 2: Checkpoint directory structure
- Session directory: `checkpoints/cp_NNN/messages.jsonl` + `current.txt`.
- `SessionHeader`: add `ParentCheckpoint` field.
- Resume: read `current.txt` → load checkpoint.
- Compatibility: old sessions (no `checkpoints/`) load as implicit `cp_001`.
- **Verify:** new sessions use checkpoint structure; old sessions resume.

### Phase 3: Reminder injection
- `maybeInjectHandoffReminder()` replacing `performCompaction` at `loop.go:198`.
- Soft threshold logic (40K/100K based on window size).
- Dynamic injection interval (based on `ToolCallsSinceLastTrigger` + context usage).
- Hard底线 check (urgent reminder + auto-execute after 2 turns).
- **Verify:** reminders appear in context at the right thresholds.

### Phase 4: performHandoff
- Detect `<handoff_complete>` in LLM response.
- Extract handoff document from LLM output.
- Q&A loop (≤3 rounds) using `llm.StreamLLM`.
- Write new checkpoint (`messages.jsonl` + `handoff.md`).
- Update `current.txt`, reload `agentCtx`.
- **Verify:** handoff produces new checkpoint; Q&A works; cache hits on first
  post-handoff turn.

### Phase 5: Integration cleanup
- Fork = copy session directory (new implementation for handoff mode).
- Rewind/messages scoped to current checkpoint.
- Remove legacy proactive code (ContextManager, context_mgmt tools,
  CompactionController) once handoff is validated. Remove legacy checkpoint
  infrastructure (`checkpoint_%05d` system, `ForkSessionFrom`/`Branch()`)
  at the same time.
- Retain reactive `performCompaction` method and `context_limit_recovery`.
- **Verify:** fork/rewind/messages work; `go test ./...` passes.