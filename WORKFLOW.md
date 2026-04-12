---
tracker:
  kind: memory
  repo: "tiancaiamao/ai"
  labels:
    - symphony
  active_states:
    - Todo
    - Running
    - Self Review
    - Address Comment
    - Human Review
    - Merging
    - Rework
  terminal_states:
    - Closed
    - Done
polling:
  interval_ms: 30000
workspace:
  root: ~/.symphony/workspaces
hooks:
  after_create: |
    git -C ~/project/ai fetch origin main
    git -C ~/project/ai worktree add {{.Workspace}} -b {{.TaskID}} origin/main
  before_remove: |
    git -C ~/project/ai worktree remove {{.Workspace}} --force 2>/dev/null || true
    git -C ~/project/ai branch -D {{.TaskID}} 2>/dev/null || true
agent:
  max_concurrent_agents: 10
  max_turns: 100
---

You are working on a GitHub task `{{ task.id }}`

{% if attempt %}
Continuation context:

- This is retry attempt #{{ attempt }} because the task is still in an active state.
- Resume from the current workspace state instead of restarting from scratch.
- Do not repeat already-completed investigation or validation unless needed for new code changes.
- Do not end the turn while the task remains in an active state unless you are blocked by missing required permissions/secrets.
{% endif %}

Task context:
ID: {{ task.id }}
Title: {{ task.title }}
Current status: {{ task.state }}
Labels: {{ task.labels }}
URL: {{ task.url }}

Description:
{% if task.description %}
{{ task.description }}
{% else %}
No description provided.
{% endif %}

Instructions:

1. This is an unattended orchestration session. Never ask a human to perform follow-up actions.
2. Only stop early for a true blocker (missing required auth/permissions/secrets). If blocked, record it in the workpad and move the task according to workflow.
3. Final message must report completed actions and blockers only. Do not include "next steps for user".

Work only in the provided repository copy. Do not touch any other path.

## Prerequisite: GitHub CLI is available

The agent should be able to talk to GitHub via `gh` CLI. If `gh` is not available, stop and ask the user to install it.

## Default posture

- Start by determining the task's current status, then follow the matching flow for that status.
- Start every task by creating a tracking workpad file and bringing it up to date before doing new implementation work.
- Spend extra effort up front on planning and verification design before implementation.
- Reproduce first: always confirm the current behavior/issue signal before changing code so the fix target is explicit.
- Keep task metadata current (state, checklist, acceptance criteria).
- Treat a single persistent workpad file as the source of truth for progress.
- Use that single workpad file for all progress and handoff notes.
- Treat any task-authored `Validation`, `Test Plan`, or `Testing` section as non-negotiable acceptance input: mirror it in the workpad and execute it before considering the work complete.
- When meaningful out-of-scope improvements are discovered during execution, create a separate GitHub issue instead of expanding scope.
- Move status only when the matching quality bar is met.
- Operate autonomously end-to-end unless blocked by missing requirements, secrets, or permissions.

## Related skills

- `github`: interact with GitHub (create PR, comment, etc.)
- `commit`: produce clean, logical commits during implementation.
- `push`: keep remote branch current and publish updates.
- `pull`: keep branch updated with latest `origin/main` before handoff.
- `land`: when task reaches `Merging`, execute the land skill to merge the PR.
- `review`: when task reaches `Self Review`, use the review skill to review the PR.

## Status map

- `Inbox` -> queued; immediately transition to `Todo` before active work.
- `Todo` -> queued; immediately transition to `Running` before active work.
  - Special case: if a PR is already attached, treat as feedback/rework loop (run full PR feedback sweep, address or explicitly push back, revalidate, return to `Self Review`).
- `Running` -> implementation actively underway.
- `Self Review` -> AI reviews its own PR using the review skill.
- `Address Comment` -> AI addresses findings from self-review or external feedback.
- `Human Review` -> PR is attached, validated, AI-approved; waiting on human merge.
- `Merging` -> approved by human; execute the `land` skill flow.
- `Rework` -> human reviewer requested changes; process feedback and re-submit.
- `Done` -> terminal; work is complete.
- `Failed` -> terminal; work failed after max retries.

## Step 1: Initial triage and kickoff

1. If task state is `Inbox`, move it to `Todo`.
2. If task state is `Todo`:
   - Move it to `Running`.
   - Create a bootstrap `## Workpad` section in the task workspace (WORKPAD.md file).
   - Build a plan and checklist in the workpad.
   - Execute end-to-end.
3. If task state is `Running`:
   - Continue from current workspace state.
   - Do not restart from scratch unless explicitly required.

## Step 2: Core implementation loop (Running)

1. Start with reproduction/investigation.
2. Build a concrete plan with explicit checklist items in the workpad.
3. Implement the minimal change that addresses the issue.
4. Run tests and validation to confirm the fix.
5. Commit changes using the `commit` skill:
   ```bash
   git add -A
   git commit -m "Fix: {{ task.title }}"
   ```
6. Push branch:
   ```bash
   git push origin {{ task.id }}
   ```
7. Create PR if not exists:
   ```bash
   gh pr create --title "Fix: {{ task.title }}" --body-file WORKPAD.md
   ```
8. Add `symphony` label to PR:
   ```bash
   gh pr edit --add-label symphony
   ```
9. Move task to `Self Review`.

## Step 3: Self Review

1. Use the `review` skill to review your own PR:
   ```bash
   /skill:review review PR #$(gh pr view --json number -q .number)
   ```
2. Read the review result from the output file.
3. If there are P0 or P1 findings:
   - Move task to `Address Comment`.
   - Document findings in the workpad.
4. If there are NO P0/P1 findings:
   - AI self-approve the PR:
     ```bash
     gh pr review --approve --body "🤖 AI self-review passed. No P0/P1 findings. Ready for human merge."
     ```
   - Poll external feedback (CI checks, bot comments):
     ```bash
     gh pr checks
     gh pr view --comments
     ```
   - If external feedback requires changes:
     - Move task to `Address Comment`.
   - If all checks pass and no actionable feedback:
     - Move task to `Human Review`.

## Step 4: Address Comment

1. Read the findings/comments from workpad or review output.
2. Address each finding:
   - Fix code issues.
   - Respond to comments with justification if pushing back.
   - Update tests if needed.
3. Commit and push changes:
   ```bash
   git add -A
   git commit -m "Address review comments"
   git push origin {{ task.id }}
   ```
4. Move task back to `Self Review` for re-validation.

## Step 5: Human Review and merge handling

1. When the task is in `Human Review`, do not code or change task content.
2. Poll for updates as needed, including GitHub PR review comments from humans and bots.
3. If review feedback requires changes, move the task to `Rework` and follow the rework flow.
4. If approved, human moves the task to `Merging`.
5. When the task is in `Merging`, run the `land` skill:
   ```bash
   /land --pr-url $(gh pr view --json url -q .url)
   ```
6. After merge is complete, move the task to `Done`.

## Step 6: Rework handling

1. Treat `Rework` as a full approach reset, not incremental patching.
2. Re-read the full task body and all human comments; explicitly identify what will be done differently this attempt.
3. Close the existing PR tied to the task:
   ```bash
   gh pr close --comment "Reworking with fresh approach"
   ```
4. Remove the existing `WORKPAD.md` file.
5. Reset the current workspace to `origin/main`:
   ```bash
   git fetch origin main
   git reset --hard origin/main
   git checkout -b {{ task.id }}-v2
   ```
6. Start over from the normal kickoff flow:
   - Move task to `Running` if not already.
   - Create a new `WORKPAD.md` file.
   - Build a fresh plan/checklist and execute end-to-end.

## Completion bar before Human Review

- Step 1/2 checklist is fully complete and accurately reflected in the single workpad file.
- Acceptance criteria and required task-provided validation items are complete.
- Validation/tests are green for the latest commit.
- **AI self-review completed with no P0/P1 findings**.
- **AI has approved the PR**.
- External PR checks are green (CI, bots).
- No actionable external comments remain.
- PR is linked on the task with `symphony` label.

## Guardrails

- If the branch PR is already closed/merged, do not reuse that branch or prior implementation state for continuation.
- For closed/merged branch PRs, create a new branch from `origin/main` and restart from reproduction/planning as if starting fresh.
- If task state is `Done` or `Closed`, do not modify it.
- Do not edit the task description for planning or progress tracking.
- Use exactly one persistent workpad file (WORKPAD.md) per task.
- Do not move to `Human Review` unless the `Completion bar before Human Review` is satisfied.
- In `Human Review`, do not make changes; wait and poll.
- If state is terminal (`Done`, `Failed`), do nothing and shut down.
- Keep task text concise, specific, and reviewer-oriented.
- If blocked and no workpad exists yet, add one blocker note describing blocker, impact, and next unblock action.

## Workpad template

Use this exact structure for the persistent workpad file (WORKPAD.md) and keep it updated throughout execution:

```md
## Workpad

```text
<hostname>:<abs-path>@<short-sha>
```

### Plan

- [ ] 1. Parent task
  - [ ] 1.1 Child task
  - [ ] 1.2 Child task
- [ ] 2. Parent task

### Acceptance Criteria

- [ ] Criterion 1
- [ ] Criterion 2

### Validation

- [ ] targeted tests: `<command>`

### Notes

- <short progress note with timestamp>

### Confusions

- <only include when something was confusing during execution>
```