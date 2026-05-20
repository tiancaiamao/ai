# ag Fault Tolerance Improvements

Incident date: 2026-05-14
Context: `ag task run` scheduler executing 11-task plan for opendarkeden/client-go Phase 1

## Incident Summary

### Case 1: Scheduler spawned too many `ai serve` processes (memory exhaustion)

**What happened:** `ag task run` with `MaxConcurrent=2` caused memory exhaustion. User killed all processes.

**Root cause chain:**
1. `MaxConcurrent=2` only limits **worker** count (`List(StatusRunning)`)
2. `spawnReviewer()` runs as independent goroutine, **not counted** against MaxConcurrent
3. When multiple groups finish simultaneously, multiple reviewers spawn concurrently
4. Each reviewer = 1 `ai serve` process (500MB-2.3GB each)
5. Worst case: 2 workers + N reviewers = 2+N concurrent `ai serve` processes

**Additional factor:** Old `ag` binary (May 9) had `Done(t.ID, "auto-passed: reviewer failed")` when reviewer failed — silently marking tasks as complete without actual review. Fixed in `adb64b0` (May 12) but binary wasn't rebuilt.

### Case 2: T008 failed 3× with LLM timeout, circuit breaker stopped everything

**What happened:** Task T008 (GCUpdateInfo) failed 3 consecutive retries, all timing out at 20 minutes. Circuit breaker killed the scheduler. T011 (final integration task, blocked by T008) never ran.

**Root cause chain:**
1. Run 1 (29089d): Worker was actively working (59 messages, was in `compaction_start`), but `estimated_minutes` not set → timeout fell back to `cfg.Timeout` (20min) → killed mid-compaction
2. Run 2 (c133dc): `glm-5.1` LLM backend never responded — 6 events total, no assistant message → 20min silence → timeout
3. Run 3: Same as run 2 (LLM still down)
4. After 3 retries, `Retry()` refuses with "exceeded max retries"
5. No way to recover without manually editing `task.json`
6. Circuit breaker (3 consecutive failures) stops entire scheduler — **including unrelated tasks** that could still run

### Case 3: Elapsed time display wrong after retry

**What happened:** `ag task list` showed T008 elapsed=3h33m after reset-to-pending, even though it was a fresh retry.

**Root cause:** `elapsed` in `task list` uses `task.ClaimedAt` from `task.json`, not `worker-meta.json.startedAt`. When `Retry()` resets `ClaimedAt=0`, the display shows "0" until re-claimed. But the **first scheduler run** carried over `claimedAt` from before the manual JSON hack, causing stale elapsed display.

---

## TODO: Improvements Needed

### P0: Critical — Prevent Resource Exhaustion

- [ ] **Global `ai serve` process cap:** Count ALL active `ai serve` processes (workers + reviewers), not just workers with `StatusRunning`. Implement a global semaphore or counter.
  ```
  effectiveSlots = MaxConcurrent - (active_workers + active_reviewers)
  ```
- [ ] **Reviewer queue, not parallel spawn:** When multiple groups are ready for review simultaneously, queue them and process one at a time (or with a separate `MaxReviewers` cap).
- [ ] **Pre-spawn resource check:** Before spawning any new `ai serve` process, check available system memory. If < 2GB free, wait or fail gracefully instead of spawning into OOM.

### P0: Critical — LLM Health Check & Timeout Resilience

- [ ] **LLM heartbeat before spawn:** Before spawning a worker, send a lightweight test prompt to verify the LLM backend is responsive. If it fails or times out (30s), don't spawn — mark as "LLM unavailable" and retry later.
- [ ] **Distinguish LLM silence vs. slow work:** If events.jsonl has zero assistant messages after N minutes (e.g., 5min), treat as "LLM dead" and fail fast (don't wait for full timeout). Only use full timeout if worker is actively streaming.
- [ ] **Separate timeout for "first response" vs "total task":** 
  - `firstResponseTimeout`: 3-5 min — if no assistant message appears, kill immediately
  - `totalTimeout`: current dynamic timeout — for active workers
- [ ] **Per-retry backoff:** Instead of retrying immediately, add exponential backoff (30s, 2min, 5min) between retries. If LLM is down, immediate retries just waste resources.

### P1: Important — Retry & Recovery UX

- [ ] **`ag task retry --force`:** Allow overriding max retries for stuck tasks. Currently requires manual JSON editing.
- [ ] **`ag task reset <id>`:** New command to fully reset a task (clear claimant, retryCount, error, status→pending). Current `Retry()` refuses after max retries.
- [ ] **Reset `ClaimedAt` on retry:** `Retry()` already sets `ClaimedAt=0`, but if the task was manually edited, the field persists. `spawnWorkers` should set a fresh `ClaimedAt` when it claims.
- [ ] **Elapsed time should use `worker-meta.json.startedAt`:** Not `task.ClaimedAt`. The worker start time is the authoritative source, not the claim time. This prevents stale elapsed display across retries.
- [ ] **Preserve failed run history:** When retrying, log the previous run IDs somewhere (e.g., `task.json.previousRuns[]`) so the user can inspect past failures.

### P1: Important — Circuit Breaker Granularity

- [ ] **Per-task circuit breaker, not global:** Don't stop the entire scheduler when one task fails. Only stop scheduling *that specific task*. Other tasks should continue.
- [ ] **Skip failed task and continue:** If a task exhausts retries, mark it as `done(failed)` (terminal) so `AllDone()` returns true. Don't block the scheduler forever. Let the user decide what to do with the failed task.
- [ ] **Partial completion report:** When circuit breaker fires, print which tasks completed, which failed, and which were never attempted.

### P2: Nice to Have — Observability & Debugging

- [ ] **Scheduler run log persistence:** `scheduler.log` gets overwritten on each `ag task run`. Should append or rotate so previous runs can be inspected.
- [ ] **`ag task status <id>` should show retry history:** Previous run IDs, failure reasons, timestamps.
- [ ] **Worker process resource tracking:** Log RSS/memory of each `ai serve` worker periodically. Warn if approaching system limits.
- [ ] **`ag task run --dry-run`:** Show what the scheduler would do (which tasks to spawn, in what order) without actually spawning. Helps verify the plan before committing resources.

### P2: Nice to Have — Smart Timeout

- [ ] **Use `estimated_minutes` from task metadata:** If set, timeout = 2× estimate. If not set, use `cfg.Timeout`. Currently works but tasks without `estimated_minutes` get a flat 20min which is too short for complex tasks and too long for LLM failures.
- [ ] **Default timeout should be longer:** 10min is too short. 30min would reduce false-positive timeouts. The dynamic extension already handles active workers.
- [ ] **Timeout should account for LLM latency:** If the LLM is slow (e.g., 30s per response), the timeout should scale accordingly. Track average response time in recent runs.

---

## Priority Order for Implementation

1. Global process cap (prevent OOM)
2. LLM first-response check (fail fast on dead LLM)
3. Per-task circuit breaker (don't block unrelated tasks)
4. `ag task reset` command (recovery UX)
5. Elapsed time fix (cosmetic but confusing)
6. Everything else