---
name: wf-pr-review
description: "Wait for CI to pass, add LGTM review, and flag for human merge (no auto-merge)."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-pr-review

Wait for CI to pass on a PR, then add LGTM review and comment that the PR is ready for human merge.

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

### 2. Check CI status

```bash
# Get CI checks status
gh pr checks <pr_number> --repo <repo> --json name,conclusion,status --jq '.'
```

**If CI is not passing:**
- Still running or pending: return early, will retry on next tick
- Failed: add comment noting CI failure, return early

**If CI is passing:**
- Proceed to review step

### 3. AI Self-Review (Required)

**CRITICAL: You must review the code changes yourself before adding LGTM.**

```bash
# Get PR diff for review
gh pr view <pr_number> --repo <repo> --json files --jq '.files[].path' | head -20
```

Then use `read` tool to review key files:
- Check logic correctness
- Look for potential bugs
- Verify error handling
- Check testing coverage

**If you find issues:**
- Add a comment describing the issues clearly
- Do NOT add LGTM
- Update status to `reviewing`, step to `review_fix_needed`
- Return, waiting for next tick to handle fixes

**If code looks good:**
- Proceed to step 4 (add LGTM)

### 4. Check if already reviewed

Check if an LGTM review already exists from the AI bot:

```bash
gh api repos/<repo>/pulls/<pr_number>/reviews --jq '.[] | select(.user.login == "ai-claw[bot]" or .user.login == "ai-claw") | .state'
```

If `APPROVED` review already exists, skip adding another one.

### 4. Add LGTM review and comment

**Only add LGTM if you have reviewed the code and found no issues!**

```bash
# Approve the PR
gh pr review <pr_number> --repo <repo> --approve --body "LGTM 🎉

CI passed. Ready for human merge."

# Also add a comment for visibility
gh pr comment <pr_number> --repo <repo> --body "CI passed, LGTM added. Ready for human merge."
```

### 5. Update status (if integrated with workflow)

If called from `wf-tick` for a workflow item:
- Update `.aiclaw/status.json` with review result
- Set `state=pr_open`, `step=ready_to_merge`

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
- Never auto-merge - only add review and comment
- Only add review when CI is passing
- Be specific in comments about CI status

## Output

Review is posted directly to GitHub as an approval with LGTM comment. The PR remains open for human merge decision.