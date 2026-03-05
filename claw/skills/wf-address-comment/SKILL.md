---
name: wf-address-comment
description: "Fix review comments and push updates to PR. Called by wf-tick when PR has changes requested."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-address-comment

Address review comments for a pull request and push fixes.

## Use When

- A workflow item is in `reviewing` and has review comments to address.
- PR has `CHANGES_REQUESTED` status from review.

## Required Inputs

- `worktree` (absolute path)
- `pr_number` or can be detected from git

## Procedure

### 1. Fetch review comments

```bash
cd "<worktree>"
gh pr view <pr> --json reviews,comments --jq '.reviews[], .comments[]'
```

### 2. Identify actionable comments

Filter for:
- Review comments requesting changes
- Unresolved comments
- Comments from reviewers (not PR author)

### 3. Make fixes

Read the changed code, understand the feedback, and implement fixes:

```
You are addressing review comments for PR #<pr>.

Review comments to address:
<list of comments>

Your task:
1. Read the affected files
2. Understand what needs to be fixed
3. Make the necessary changes
4. Test the changes (run build and tests)
5. Commit the fixes
6. Push the fixes

Commit message format:
"fix: address review comments

- <fix 1>
- <fix 2>
"
```

### 4. Push fixes

```bash
cd "<worktree>"
git push
```

### 5. Update status

Update `.aiclaw/status.json`:
- Set `state=pr_open`
- Set `step=awaiting_review`
- Set `updated_at` to now

## Integration with wf-tick

When workflow state is `reviewing`:

```
reviewing
  → wf-tick calls wf-address-comment (or wf-worker with task_type=fix_review)
  → Fixes implemented and pushed
  → state=pr_open
  → Wait for re-review
```

## Guardrails

- Only address actionable review comments
- Don't address the author's own comments
- Always test before pushing
- Use clear commit messages