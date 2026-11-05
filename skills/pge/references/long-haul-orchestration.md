# Long-Haul Orchestration (PGE at week scale)

PGE's SKILL.md covers one focused deliverable. This file covers the case where a single session runs for
days or weeks, survives several context compactions, and ships dozens of PRs. Everything below is written
from observed failures, including the orchestrator's own.

## 1. state.md and progress.md are the memory, not the context

After a compaction the transcript is gone; `.pge/state.md` and `.pge/progress.md` are all that remains. The
split from SKILL.md still holds — state.md is the snapshot the orchestrator edits, progress.md is the
append-only WAL — but at week scale both carry far more weight than in a single-phase run:

- append to progress.md after **every** worker report or merge, not at the end of the day;
- record the decision and the evidence side by side in state.md (`merged 3f7842a49 after rebase proved the
  red was a stats-tick flake, not this diff`);
- keep a running baseline block (commit shas, pass/fail counts) so a later run can be compared, not just
  described;
- record your own mistakes in it. A corrected wrong diagnosis is more valuable to a successor than a clean
  narrative.

Corollary: never answer "where are we" from memory when these files exist — read them.

## 2. Task files: what prevents hangs is scope, not section names

Three workers died in silent hangs one night. All three had broad scope ("fix the A14 DDL cases"). None of
the single-file, single-outcome tasks ever hung.

Keep [`task-template.md`](task-template.md) as the format — the Kitchen Sink check depends on its `Scope` /
Write list, so do not invent a competing layout. What long-haul runs must **add** on top of it:

- **`STATUS:` as the first line**, kept current by the worker — a 2-second `head -1` tells you whether a
  week-old task is still in flight.
- **Time box** and "single bash ≤ 300 s" under `Constraints` — long single commands are what look like
  hangs.
- **Explicit non-goals**: "only this file", "do not touch corpus", "do not kill other processes".
- **Concurrency facts** — which ports/clusters/worktrees another worker holds. Without this, two workers
  fight and both report fake failures.
- **Delivery + notification line** under `Acceptance` (commit, push, PR base, the `ai send` that reports
  back) so completion never depends on the orchestrator polling.

## 3. A watchdog is not optional

A poller (90 s) that watches in-flight CI statuses and messages the orchestrator on transitions removes
dead-waiting entirely and survives compaction. Keep its state in a JSON file so restarts do not re-notify.

## 4. Verify writes by their receipt

Real incident: four issue-close comments were "posted" with `>/dev/null`; the command had failed
(`tea api -X POST ... --body-file` is unsupported and printed usage), and hours passed before anyone
noticed the state had changed but the comment had not.

- Print and keep the returned id/URL for every write.
- If the tool cannot prove the write, write a small `urllib` script against the API and print the id.
- A successful PATCH does not imply a successful comment — they are different calls.

## 5. Diagnose auth failures with a triangle, not a guess

Symptom: `repos/<org>/<repo>/*` returned **404** while `user`, `user/repos`, and `search` worked. Two
successive diagnoses were wrong ("token scope lost", "transient outage"). The cause was **2FA enforcement
on the account**: until 2FA is configured, Gitea answers 404 for all repo routes while the identity
endpoints keep working.

Triangle to run before blaming a token:

1. `GET /user` — is the token alive?
2. `GET /user/repos` — is the repo visible to the identity?
3. `GET /repos/<org>/<repo>` — is the route authorized?

`user` OK + `user/repos` lists it + `repos/*` 404 ⇒ account-level policy, not token scope.

## 6. Reviewer separation needs pointed questions

"Please review this PR" returns an approving summary. What produced real findings was enumerating the
plausible self-deceptions of the author:

- Did the assertion actually change? (list every changed assertion line)
- Is the new wait on the same path as the bug, or a **placebo** on a path that never produces the state?
- Are the expected values derived from the fixture, or copied from current implementation output?
- Does the test-only trigger leak into other cases in the same process?
- Is the reproduction proof real, or 30 green rounds plus optimism?

Each question came from a specific failure seen earlier. Keep a per-domain question bank and reuse it.

## 7. Reverse validation is the only proof

The opposite of "it passes now" is "I saw it fail for the stated reason". Require it in the task file,
and accept an honest "natural reproduction did not occur in 30 rounds; here is the mechanism demonstrated
deterministically" — that is a better artifact than a fabricated failing log, and it tells you the real
frequency (rare).

## 8. Do not contaminate your own measurement

A long verification run (254 integration cases) is sensitive to load: a 2 s read timeout in a test helper
fired twice and killed two shards *because* another worker was compiling. Rule: while a canonical batch is
running, do not start heavy workers; queue the flake-fix work instead of running it. Serialize by default,
parallelize only light/read-only work.

## 9. Merge discipline

- Merge only on green plus `mergeable=true`; a green PR that cannot merge is not done.
- Rebase/merge `main` into the branch before merging; a green measured on a stale base does not transfer.
- When a red is on the PR head but the same head was green elsewhere, prove the flake before merging, and
  merge on the **second** green — never on the retry alone.
- Squash-merge via the API; record the resulting main sha in the state file immediately.

## 10. Workspace and disk hygiene

Worktrees in this repo carry `target/` and reach 1–4 GB each; 43 of them reached 195 GB and started
blocking builds.

- One worktree per task, removed in the same cycle the PR merges.
- Never delete branches on a vibe. Prove a branch's content is in `main` by comparing the blob hash of
  **every file the branch touched**; squash merges break ancestry checks, blob comparison does not.
- Archive small evidence (logs, experiment patches, acceptance lists) to a `*-keep/` directory first.
- 17 branches were kept precisely because their content differed and the API could not confirm merge
  status; refs cost nothing, unmerged work costs a rewrite.

## 11. Honest reporting is a feature

Twice this session the orchestrator published a confident wrong diagnosis and had to retract it in the
state file and to the user. Report process failures with the same specificity as technical findings —
including failed writes, wrong attribution, and self-inflicted load. The user's trust in the remaining
numbers depends on it.