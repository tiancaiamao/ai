---
name: wf-pr-review
description: "Automated PR review: wait for CI, review code changes, add LGTM or comment issues. Called by wf-tick when auto_review=true."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-pr-review

Automated code review for pull requests. Reviews code quality, waits for CI, and adds LGTM or comments issues.

## Use When

- A workflow item is in `pr_open` state with `step=awaiting_review`.
- `auto_review=true` in workflow config.
- Called by wf-tick during reconciliation.

## Required Inputs

- `worktree` (absolute path)
- `pr_number` (or can be detected from git)
- `repo` (e.g., "owner/repo")

## Procedure

### 1. Fetch PR details

```bash
cd "<worktree>"

# Get PR info
PR_INFO=$(gh pr view <pr_number> --repo <repo> --json \
  title,body,headRefName,baseRefName,additions,deletions,changedFiles,files,commits)

# Get changed files
CHANGED_FILES=$(gh pr diff <pr_number> --repo <repo>)
```

### 2. Wait for CI (optional, configurable)

```bash
# Check CI status
CI_STATUS=$(gh pr checks <pr_number> --repo <repo> --json status --jq '.status')

if [ "$CI_STATUS" = "pending" ]; then
  echo "CI still running, waiting..."
  # Option 1: Wait with timeout
  # Option 2: Return and retry next tick
  # Default: Skip CI wait, proceed with review
fi
```

### 3. Review code changes

Review task prompt:

```
You are reviewing PR #<pr_number>: <title>

Changed files:
<list of changed files with additions/deletions>

Diff:
<diff content>

Review checklist:
1. Code quality: Is the code clean, readable, and follows project conventions?
2. Logic correctness: Does the implementation match the requirements?
3. Error handling: Are edge cases and errors handled properly?
4. Tests: Are there adequate tests for the changes?
5. Documentation: Is the code properly documented?
6. Security: Are there any security concerns?
7. Performance: Are there any performance issues?

Your task:
1. Read each changed file
2. Analyze the implementation
3. Check for issues or improvements
4. If no issues found: Respond with "LGTM" and a brief summary
5. If issues found: List each issue with file:line and suggested fix

Response format (LGTM):
LGTM
Summary: <brief summary of changes>
Confidence: <high/medium/low>

Response format (Issues found):
ISSUES_FOUND
1. <file>:<line> - <issue description>
   Suggested fix: <fix>
2. ...
```

### 4. Post review result

**If LGTM**:
```bash
# Add LGTM review
gh pr review <pr_number> --repo <repo> --approve --body "🤖 Automated Review: LGTM

Summary: <summary>
Confidence: <confidence>

Ready for human review."
```

**If issues found**:
```bash
# Add review comments
gh pr review <pr_number> --repo <repo> --request-changes --body "🤖 Automated Review: Issues found

<list of issues>

Please address these issues before merging."
```

### 5. Update workflow status

Update `.aiclaw/status.json`:

**If LGTM**:
```json
{
  "state": "pr_open",
  "step": "ready_to_merge",
  "review_result": "approved",
  "review_summary": "<summary>",
  "review_confidence": "<confidence>",
  "updated_at": "<now>"
}
```

**If issues found**:
```json
{
  "state": "reviewing",
  "step": "review_fix_needed",
  "review_result": "changes_requested",
  "review_issues": ["<issue1>", "<issue2>"],
  "updated_at": "<now>"
}
```

## Integration with wf-tick

```
pr_open (step=awaiting_review)
  → wf-tick checks auto_review=true
  → calls wf-pr-review
  ↓
  ├─ LGTM → state=pr_open, step=ready_to_merge (wait for human merge)
  └─ Issues → state=reviewing (wf-address-comment handles fixes)
```

## Configuration

Review behavior can be configured in `~/.aiclaw/workflows/config.json`:

```json
{
  "review": {
    "wait_for_ci": false,
    "ci_timeout_minutes": 10,
    "confidence_threshold": "medium",
    "skip_patterns": ["*.md", "*.txt"],
    "max_diff_size": 10000
  }
}
```

## Idempotency

- Check if review already posted before posting again
- Use consistent review body format for detection
- Don't duplicate reviews on re-run

```bash
# Check for existing automated review
EXISTING_REVIEW=$(gh pr view <pr> --repo <repo> --json reviews --jq \
  '.reviews[] | select(.body | contains("🤖 Automated Review"))')

if [ -n "$EXISTING_REVIEW" ]; then
  echo "Automated review already exists, skipping"
  exit 0
fi
```

## Examples

### Example 1: Simple LGTM

```bash
$ wf-pr-review worktree=/path/to/worktree pr_number=48 repo=tiancaiamao/ai

Fetching PR #48 details...
Changed files: 3 (+150, -20)

Reviewing code changes...
- Makefile: ✓ Clean, follows conventions
- .github/workflows/test.yml: ✓ Correct CI configuration
- README.md: ✓ Documentation updated

Result: LGTM
Summary: Adds Makefile with build/test targets and updates CI to use make commands
Confidence: high

Posting review...
✓ Posted LGTM review to PR #48
✓ Updated status to ready_to_merge
```

### Example 2: Issues Found

```bash
$ wf-pr-review worktree=/path/to/worktree pr_number=49 repo=tiancaiamao/ai

Fetching PR #49 details...
Changed files: 2 (+80, -5)

Reviewing code changes...
- config.go: ⚠ Missing error handling on line 42
- main.go: ⚠ Hardcoded value should be configurable

Result: ISSUES_FOUND
1. config.go:42 - Error return value not checked
   Suggested fix: Add if err != nil { return err }
2. main.go:78 - Hardcoded timeout value
   Suggested fix: Make configurable via flag or config

Posting review...
✓ Posted changes_requested review to PR #49
✓ Updated status to reviewing
```

## Guardrails

- Never auto-merge PRs
- Always wait for human final approval
- Don't review files matching skip_patterns
- Fail gracefully on large diffs (skip with warning)
- Don't post duplicate reviews