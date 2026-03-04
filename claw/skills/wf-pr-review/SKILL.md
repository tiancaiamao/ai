---
name: wf-pr-review
description: "Reconcile PR state, handle review feedback, and trigger fix passes until PR is approved or merged."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-pr-review

Manage PR and review lifecycle for a workflow item.

## Use When

- Workflow state is `pr_open`.
- Workflow state is `reviewing`.
- A tick detected new review comments or changed review decision.

## Required Inputs

- `repo` (owner/repo)
- `worktree`
- `issue_number`
- `pr_number` or `pr_url`

## State Rules

- `pr_open -> reviewing`: changes requested or unresolved review feedback exists.
- `pr_open -> done`: PR merged.
- `reviewing -> pr_open`: fixes pushed and no blocking feedback.
- `reviewing -> failed`: repeated fix attempts fail.

## Procedure

1. Resolve PR number.

- If `pr_number == 0`, try detect by branch:

```bash
cd "<worktree>" && gh pr list --head "<branch>" --json number,url,state --jq '.[0]'
```

2. Reconcile PR state from GitHub.

```bash
gh pr view <pr> --repo <owner/repo> --json state,mergeStateStatus,reviewDecision,isDraft,url
```

3. If PR is merged.

- Set workflow status to:
  - `state=done`
  - `step=merged`
  - `updated_at=now`
- Return immediately.

4. If review asks for changes or blocking comments exist.

- Set `state=reviewing`, `step=review_fix`.
- Gather feedback:

```bash
gh pr view <pr> --repo <owner/repo> --json reviews,comments
```

- Build a fix prompt that includes only unresolved actionable feedback.
- Trigger `wf-worker` style fix pass in the same worktree.

5. If no blocking feedback and PR still open.

- Keep or set `state=pr_open`, `step=awaiting_review`.

6. If auto-merge policy is enabled and review is approved.

```bash
gh pr merge <pr> --repo <owner/repo> --squash --auto
```

Then keep `pr_open` until merged is confirmed by next tick.

## Guardrails

- Never close issue here.
- Never remove worktree here.
- Do not infer review status from local git only; always query GitHub.
- Always persist last reconciliation time in `updated_at`.
