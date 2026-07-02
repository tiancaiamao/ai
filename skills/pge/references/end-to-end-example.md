# End-to-End Example

以一个 task 的完整生命周期为例，展示从 spec 到 commit 的全流程。

> 以下命令遵循 `subagent` 技能的 spawn 模式。`RUN_ID` 来自 `<agent:runtime_state>` 的 `run_id` 字段。

## 场景

实现 JWT 认证中间件。spec 已确认，当前 phase 有一个 task: `add-jwt-auth`。

## Step 1: Write Task File

```bash
mkdir -p .pge/tasks
cat > .pge/tasks/task-add-jwt-auth.md << 'EOF'
## Task: Add JWT Auth Middleware
## Context
Go backend, gin framework. User model in src/models/user.go.
**Before starting, read `.pge/state.md` for context from previous tasks.**
## What to Implement
JWT auth middleware that validates token from Authorization header,
sets user context on success, returns 401 on failure.
## Files to Modify/Create
- src/middleware/auth.go (create)
- src/routes/router.go (modify — add middleware to protected routes)
## Verification
cd /home/user/myproject && go build ./... && go test ./src/middleware/...
## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. BUILD MUST PASS — 实现后必须构建成功
3. Output DONE: <file list> when complete
EOF
```

## Step 2: Spawn Generator

```bash
RUN_ID=<your run_id>
TMUX_SESSION="agent-$RUN_ID-gen-jwt"
ID_FILE="/tmp/agent-$RUN_ID-gen-jwt.id"

tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --role coder \
   --input-file .pge/tasks/task-add-jwt-auth.md \
   --name 'gen-jwt' \
   --id-file $ID_FILE \
   --timeout 10m"

# Wait for startup
sleep 3
CHILD_ID=$(cat $ID_FILE)
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

## Step 3: Watch Generator

```bash
ai watch --id "$CHILD_ID" --follow --pretty
# 等待输出中出现 "DONE: src/middleware/auth.go, src/routes/router.go"
# 不要 kill Generator — 保持活着用于潜在的修复
```

## Step 4: Spawn Evaluator

```bash
cat > /tmp/eval-add-jwt-auth.md << 'EOF'
## Task: Evaluate add-jwt-auth
You are an INDEPENDENT evaluator. You did NOT write this code.
## Spec Acceptance Criteria
- [L1] src/middleware/auth.go exists — Verify: `ls src/middleware/auth.go`
- [L1] go build passes — Verify: `go build ./...`
- [L2] Valid token sets user context — Verify: `go test ./src/middleware/...`
- [L2] Invalid token returns 401 — Verify: `go test ./src/middleware/...`
## Instructions
1. cd /home/user/myproject
2. For each criterion, run verification YOURSELF
3. READ source files for quality
4. Output ✅/❌ for EVERY criterion with EVIDENCE
5. Write to .pge/eval-add-jwt-auth.md with PASS/FAIL verdict
EOF

EVAL_TMUX="agent-$RUN_ID-eval-jwt"
EVAL_ID_FILE="/tmp/agent-$RUN_ID-eval-jwt.id"

tmux new-session -d -s "$EVAL_TMUX" \
  "ai serve --role validator \
   --input-file /tmp/eval-add-jwt-auth.md \
   --name 'eval-jwt' \
   --id-file $EVAL_ID_FILE \
   --timeout 5m"

sleep 3
EVAL_ID=$(cat $EVAL_ID_FILE)
echo "$EVAL_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

## Step 5: Watch Evaluator → Read Eval Report

```bash
ai watch --id "$EVAL_ID" --follow --pretty
# 等待 Evaluator 完成

cat .pge/eval-add-jwt-auth.md
```

### If PASS → 进入 Step 6

### If FAIL → Send Feedback to Generator

```bash
# 用 ai send 把 eval 反馈发给同一个 Generator（不 spawn 新的）
ai send --id "$CHILD_ID" --wait --timeout 5m \
  "Evaluator 报告以下问题：
$(grep '❌' .pge/eval-add-jwt-auth.md)

请修复这些问题，eval report 在 .pge/eval-add-jwt-auth.md。
修复后输出 DONE: <file list>"

# Kill 旧 Evaluator，spawn 新 Evaluator 重新验证
ai kill --id "$EVAL_ID"
# ... 重新 spawn Evaluator（同 Step 4）
# 最多 3 轮，仍 FAIL → 停下来报告用户
```

## Step 6: PASS → Update State, Kill Agents, Next Task

```bash
# 1. 更新 state.md（Orchestrator 负责，3 步）
cat > .pge/state.md << 'EOF'
# State
## Task Status
| Task | Status | Eval |
|------|--------|------|
| add-jwt-auth | ✅ PASS | eval-add-jwt-auth.md |
| add-rbac | ⏳ In Progress | |

## Next Task
add-rbac

## Key Decisions
- Using HS256 for JWT signing

## Known Issues
- (none yet)
EOF

# 2. Kill Generator + Evaluator
ai kill --id "$CHILD_ID"
ai kill --id "$EVAL_ID"

# 进入下一个 task 或 Phase 4 Review
```

## Step 7 (Phase 4): Spawn Review Agent

```bash
# 先记录 phase start commit
git rev-parse HEAD > .pge/phase-start-commit

# 确保 Phase 3 的 agent 都已 kill（Step 6 已做）
# 加载 review 技能
# find_skill(name="review", load=true)

# Spawn Review agent
REVIEW_TMUX="agent-$RUN_ID-review"
REVIEW_ID_FILE="/tmp/agent-$RUN_ID-review.id"

tmux new-session -d -s "$REVIEW_TMUX" \
  "ai serve --role coder \
   --system-prompt @~/.ai/skills/review/reviewer.md \
   --input 'Review all code changes in this phase. Run: cd /home/user/myproject && git diff \$(cat .pge/phase-start-commit)..HEAD. Write findings to .pge/review-phase1.md with P0-P3 priorities.' \
   --name 'rev-phase1' \
   --id-file $REVIEW_ID_FILE \
   --timeout 5m"

sleep 3
REVIEW_ID=$(cat $REVIEW_ID_FILE)
echo "$REVIEW_ID" >> ~/.ai/runs/$RUN_ID/subagent

# Watch Review agent
ai watch --id "$REVIEW_ID" --follow --pretty

# 读 review report，无 P1 → commit + cleanup
cat .pge/review-phase1.md
git add -A && git commit -m "feat: add JWT auth middleware"
ai kill --id "$REVIEW_ID"
```