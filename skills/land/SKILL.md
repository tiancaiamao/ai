---
name: land
description: |
  Merge a PR automatically with safety checks.
  Use when task is in "Merging" state and PR is approved.
  Handles merge conflicts, failing checks, and cleanup.
---

# Land Skill - 自动 Merge PR

## When to Use

- Task is in `Merging` state
- PR has been approved by human
- All checks are passing (or explicitly waived)

## Prerequisites

- GitHub CLI (`gh`) installed and authenticated
- PR URL or PR number available
- Repository has merge permissions

## Steps

### 1. Check PR Status

```bash
gh pr view <pr-url-or-number> --json \
  reviewDecision,\
  statusCheckRollup,\
  mergeable,\
  state
```

**Expected:**
- `reviewDecision`: APPROVED
- `state`: OPEN
- `mergeable`: MERGEABLE or CONFLICTING

### 2. Handle Merge Conflicts

If `mergeable` is `CONFLICTING`:

```bash
# Fetch latest main
git fetch origin main

# Rebase onto main
git rebase origin/main

# If conflicts occur, AI should resolve them
# Then force push
git push --force-with-lease

# Move task back to "Rework"
exit 1
```

### 3. Wait for Checks to Pass

```bash
# Poll until all checks complete
while true; do
  STATUS=$(gh pr view --json statusCheckRollup -q '.statusCheckRollup[] | select(.conclusion == "FAILURE") | .conclusion')
  
  if [ -n "$STATUS" ]; then
    echo "Checks failed!"
    # Move task back to "Rework"
    exit 1
  fi
  
  # Check if all checks completed
  PENDING=$(gh pr view --json statusCheckRollup -q '[.statusCheckRollup[] | select(.status == "IN_PROGRESS" or .status == "QUEUED")] | length')
  
  if [ "$PENDING" -eq 0 ]; then
    echo "All checks passed!"
    break
  fi
  
  echo "Waiting for checks... ($PENDING pending)"
  sleep 10
done
```

### 4. Squash and Merge

```bash
# Merge with squash
gh pr merge --squash --delete-branch --subject "Fix: {{ task.title }}"
```

**Options:**
- `--squash`: Squash commits into one
- `--delete-branch`: Delete branch after merge
- `--subject`: Commit message

### 5. Update Task State

```bash
# Mark task as done
curl -X PUT http://localhost:8080/api/tasks/{{ task.id }} \
  -H "Content-Type: application/json" \
  -d '{"state": "done"}'
```

### 6. Notify (Optional)

```bash
# Send notification
if [ -n "$SLACK_WEBHOOK" ]; then
  curl -X POST "$SLACK_WEBHOOK" \
    -d "{\"text\": \"✅ Merged: {{ task.title }}\nPR: $(gh pr view --json url -q .url)\"}"
fi
```

## Error Handling

### Checks Failed

```bash
# Move task to Rework
curl -X PUT http://localhost:8080/api/tasks/{{ task.id }} \
  -d '{"state": "running"}'  # Back to running to fix

# Add comment to PR
gh pr comment --body "❌ Checks failed. Addressing issues..."
```

### Merge Conflict

```bash
# Move task to Rework
curl -X PUT http://localhost:8080/api/tasks/{{ task.id }} \
  -d '{"state": "running"}'

# Add comment to PR
gh pr comment --body "⚠️ Merge conflicts detected. Rebasing and resolving..."
```

### Not Approved

```bash
# Move task back to Human Review
curl -X PUT http://localhost:8080/api/tasks/{{ task.id }} \
  -d '{"state": "todo"}'  # Back to todo

# Add comment
gh pr comment --body "⏸️ PR not yet approved. Waiting for review."
```

## Complete Example

```bash
#!/bin/bash
set -e

PR_URL=$1
TASK_ID=$2

echo "🚀 Starting land process for PR: $PR_URL"

# 1. Check PR status
echo "📋 Checking PR status..."
PR_STATUS=$(gh pr view "$PR_URL" --json reviewDecision,state,mergeable)

REVIEW_DECISION=$(echo "$PR_STATUS" | jq -r .reviewDecision)
STATE=$(echo "$PR_STATUS" | jq -r .state)
MERGEABLE=$(echo "$PR_STATUS" | jq -r .mergeable)

echo "  Review: $REVIEW_DECISION"
echo "  State: $STATE"
echo "  Mergeable: $MERGEABLE"

# 2. Validate
if [ "$STATE" != "OPEN" ]; then
  echo "❌ PR is not open"
  exit 1
fi

if [ "$REVIEW_DECISION" != "APPROVED" ]; then
  echo "❌ PR not approved"
  curl -X PUT "http://localhost:8080/api/tasks/$TASK_ID" \
    -d '{"state": "todo"}'
  exit 1
fi

# 3. Handle conflicts
if [ "$MERGEABLE" == "CONFLICTING" ]; then
  echo "⚠️ Merge conflicts detected"
  git fetch origin main
  git rebase origin/main || {
    echo "❌ Could not resolve conflicts automatically"
    curl -X PUT "http://localhost:8080/api/tasks/$TASK_ID" \
      -d '{"state": "running"}'
    exit 1
  }
  git push --force-with-lease
  echo "✅ Conflicts resolved, rebased"
fi

# 4. Wait for checks
echo "🔍 Waiting for checks..."
while true; do
  FAILURES=$(gh pr view "$PR_URL" --json statusCheckRollup -q '[.[] | select(.conclusion == "FAILURE")] | length')
  PENDING=$(gh pr view "$PR_URL" --json statusCheckRollup -q '[.[] | select(.status == "IN_PROGRESS" or .status == "QUEUED")] | length')
  
  if [ "$FAILURES" -gt 0 ]; then
    echo "❌ Checks failed"
    gh pr comment --body "❌ Checks failed. Addressing issues..."
    curl -X PUT "http://localhost:8080/api/tasks/$TASK_ID" \
      -d '{"state": "running"}'
    exit 1
  fi
  
  if [ "$PENDING" -eq 0 ]; then
    echo "✅ All checks passed"
    break
  fi
  
  echo "  Waiting... ($PENDING checks pending)"
  sleep 10
done

# 5. Merge
echo "🔀 Merging PR..."
gh pr merge "$PR_URL" --squash --delete-branch

# 6. Update task
echo "✅ Marking task as done"
curl -X PUT "http://localhost:8080/api/tasks/$TASK_ID" \
  -d '{"state": "done"}'

# 7. Notify
if [ -n "$SLACK_WEBHOOK" ]; then
  curl -X POST "$SLACK_WEBHOOK" \
    -d "{\"text\": \"✅ Merged PR: $PR_URL\"}"
fi

echo "🎉 Land complete!"
```

## Integration with Symphony

In WORKFLOW.md:

```markdown
## Step 3: Human Review and merge handling

5. When the task is in `Merging`, run the `land` skill:
   ```bash
   /land --pr-url $(gh pr view --json url -q .url)
   ```
```

## Safety Features

1. **Never force merge** - Wait for approval
2. **Check CI status** - Never merge red PRs
3. **Handle conflicts** - Rebase and retry
4. **Atomic updates** - Update task state only after merge
5. **Cleanup** - Delete branch after merge
6. **Notifications** - Inform team of merge

## Anti-Patterns

❌ Don't call `gh pr merge` directly - use land skill
❌ Don't merge without approval - wait for review
❌ Don't merge failing checks - fix first
❌ Don't ignore conflicts - resolve or escalate
❌ Don't skip task state update - keep Symphony in sync