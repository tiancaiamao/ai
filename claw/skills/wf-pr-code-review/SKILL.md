---
name: wf-pr-code-review
description: "Perform AI code review for a pull request. Post comments if issues found, or approve and comment LGTM if everything looks good."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-pr-code-review

Perform AI code review for a pull request using GitHub API.

## Use When

- A workflow item is in `pr_open` and needs automated review.
- User requests: "Review PR #17" or "Review https://github.com/owner/repo/pull/17"
- Integrated into `wf-tick` for automated review cycle.

## Required Inputs

At least one of:

- `pr_url` (e.g., "https://github.com/tiancaiamao/ai/pull/17")
- `pr_number` + `repo` (e.g., 17 + "tiancaiamao/ai")
- Just run in worktree and auto-detect from git

## Procedure

### 1. Resolve PR details

If only `pr_url` provided:
```bash
# Extract owner/repo and pr_number
# e.g., https://github.com/tiancaiamao/ai/pull/17 → repo=tiancaiamao/ai, pr=17
```

If in worktree:
```bash
cd "<worktree>"
repo=$(gh repo view --json owner,name -q '.owner.login + "/" + .name')
pr=$(gh pr list --head "$(git branch --show-current)" --json number --jq '.[0].number')
```

### 2. Fetch PR information

```bash
gh pr view <pr_number> --repo <repo> --json title,body,files,additions,deletions,headRefName,baseRefName
gh pr diff <pr_number> --repo <repo>
```

### 3. Perform code review

Read the diff and review the changes:

```
You are performing code review for PR #<pr_number> in <repo>.

Title: <title>
Description: <body>
Branch: <head_ref> → <base_ref>
Stats: +<additions> -<deletions> changed <changed_files> files

Your task:
1. Review the code changes for:
   - Security vulnerabilities
   - Performance issues
   - Best practices violations
   - Documentation gaps
   - Potential bugs
   - Code clarity

2. For each issue found, note:
   - File path and line number
   - What's wrong
   - How to fix it

3. Make a decision:
   - If issues found: prepare review comments
   - If no issues: approve and comment "LGTM"
```

### 4. Post review to GitHub

**If issues found:**
```bash
# Request changes with comments
gh pr review <pr_number> --repo <repo> --request-changes --body "<review summary>

Comments:
- <file>:<line> - <issue>
- <file>:<line> - <issue>
..."
```

**If no issues (LGTM):**
```bash
# Approve and comment LGTM
gh pr review <pr_number> --repo <repo> --approve --body "LGTM 🎉"
```

**Optional: Post inline comments**
```bash
# For specific line comments
gh pr review <pr_number> --repo <repo> --comment --body "<review comments>"
```

### 5. Update status (if integrated with workflow)

If called from `wf-tick` for a workflow item:
- Update `.aiclaw/status.json` with review result
- Set `step` based on review outcome:
  - If approved: `step=awaiting_merge`
  - If changes requested: `reviewing`

## Example Usage

### Simple usage (auto-detect)
```bash
# User asks: "Review the current PR"
# Agent detects PR from git/gh and performs review
```

### With PR URL
```bash
# User asks: "Review https://github.com/tiancaiamao/ai/pull/17"
# Agent extracts info and reviews
```

### With PR number and repo
```bash
# User asks: "Review PR #17 in tiancaiamao/ai"
# Agent reviews the specified PR
```

## Guardrails

- Always check if review already exists (idempotent)
- Don't post duplicate reviews
- Be specific and actionable with feedback
- Don't be overly nitpicky unless explicitly requested

## Output

Review is posted directly to GitHub as a review comment with approval or request for changes.