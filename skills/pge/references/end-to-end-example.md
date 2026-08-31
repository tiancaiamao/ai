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
## Files
### Read (context, do not modify)
- src/models/user.go
### Write (expected changes)
- src/middleware/auth.go (create)
- src/routes/router.go (modify — add middleware to protected routes)
## Verification
cd /home/user/myproject && go build ./... && go test ./src/middleware/...
## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. MODIFY ONLY WRITE FILES — 不要改动 Write 列表之外的文件
3. BUILD MUST PASS — 实现后必须构建成功
4. Output DONE: <file list> when complete
EOF

# 写 progress.md
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Task file task-add-jwt-auth.md 已创建" >> .pge/progress.md
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

# 写 progress.md
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Spawn gen-jwt (coder)" >> .pge/progress.md
```

## Step 3: Watch Generator

```bash
ai watch --id "$CHILD_ID" --follow --pretty
# 等待输出中出现 "DONE: src/middleware/auth.go, src/routes/router.go"
# Generator DONE 后，它会写 progress.md（在 task 实现代码中执行）
# 不要 kill Generator — 保持活着用于潜在的修复
```

## Step 4: Kitchen Sink Check

Generator DONE 后，Orchestrator 先检查 scope 合规性（不 spawn agent）：

```bash
# 列出本次修改的所有文件（含新增/暂存/未暂存）
git status --porcelain --untracked-files=all

# 对比 task 的 Write 列表：
# Write: src/middleware/auth.go, src/routes/router.go
# 如果出现任何其他文件，说明 scope 越界
```

如有越界文件 → 写 progress.md，用 `ai send` 让 Generator 回滚：

```bash
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Kitchen Sink 越界" >> .pge/progress.md

ai send --id "$CHILD_ID" --wait --timeout 5m \
  "Kitchen Sink detected. The following files are outside task Write scope:
  $(git status --porcelain --untracked-files=all | sed 's/^...//' | grep -v 'src/middleware/auth.go' | grep -v 'src/routes/router.go')
  Please revert these changes and only modify files in Write scope.
  Output DONE: <fixed file list> when complete."
# 回到 Step 3 watch
```

无越界 → 写 progress.md，继续 Step 5。

```bash
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Kitchen Sink 检查通过，未超出范围" >> .pge/progress.md
```

## Step 5: Spawn Evaluator

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

# 写 progress.md
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Spawn eval-jwt (validator)" >> .pge/progress.md
```

## Step 6: Watch Evaluator → Read Eval Report

```bash
ai watch --id "$EVAL_ID" --follow --pretty
# 等待 Evaluator 完成（Evaluator 会在写完 eval report 后写 progress.md）

cat .pge/eval-add-jwt-auth.md
```

### If PASS → 进入 Step 7

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
# ... 重新 spawn Evaluator（同 Step 5）
# 最多 3 轮，仍 FAIL → 停下来报告用户
```

## Step 7: PASS → Update State, Progress, Commit, Kill Agents, Next Task

```bash
# 1. 更新 state.md（Orchestrator 负责）
# ⚠️ 首次创建用 cat >，后续 task 用 edit 逐字段更新（不要 cat > 覆盖，会丢失历史）
# 见 SKILL.md State Tracking 章节

# 首次创建：
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

## Attempt Log
- (none for this task)

## Known Issues
- (none yet)

## Phase Log
EOF

# ⚠️ 后续 task PASS 后，用 edit 逐字段更新（不要 cat > 覆盖）：
# edit Task Status 表格行（标记当前 task ✅，下一 task ⏳）
# edit Next Task（改为下一 task 名）
# edit Attempt Log（如有放弃的方案）


# 2. 写 progress.md
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | task-add-jwt-auth ✅ state.md 已更新" >> .pge/progress.md


# 3. 可选：task 级 commit（每个 task 通过 eval 后可独立提交）
# git add -A && git commit -m "task-add-jwt-auth: JWT auth middleware"
# echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | task-add-jwt-auth ✅ commit: $(git rev-parse HEAD)" >> .pge/progress.md


# 4. Kill Generator + Evaluator
ai kill --id "$CHILD_ID"
ai kill --id "$EVAL_ID"


# 5. 进入下一个 task 或 Phase 4 Review
```

## Step 8 (Phase 4): Spawn Review Agent

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
   --input 'Review all code changes in this phase. Run: cd /home/user/myproject && git diff \$(cat .pge/phase-start-commit)..HEAD (this shows the cumulative diff of all task commits in this phase against the baseline). Write findings to .pge/review-phase1.md with P0-P3 priorities.' \
   --name 'rev-phase1' \
   --id-file $REVIEW_ID_FILE \
   --timeout 5m"

sleep 3
REVIEW_ID=$(cat $REVIEW_ID_FILE)
echo "$REVIEW_ID" >> ~/.ai/runs/$RUN_ID/subagent

# 写 progress.md
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Spawn rev-phase1 (coder)" >> .pge/progress.md

# Watch Review agent
ai watch --id "$REVIEW_ID" --follow --pretty

# 读 review report，无 P1 → Phase 合入 + update Phase Log + cleanup
cat .pge/review-phase1.md

# 5. Phase 合入
# 若 Phase 3 各 task 已独立 commit，确认分支已就绪即可
# 若未独立 commit，执行最终 commit：
# git add -A && git commit -m "phase1: JWT auth middleware

# Tasks: add-jwt-auth
# Review: review-phase1.md"

# 写 progress.md
COMMIT_HASH=$(git rev-parse HEAD)
echo "[$(date '+%Y-%m-%d %H:%M:%S')] ORCHESTRATOR | Phase 1 合入. Review: $(cat .pge/review-phase1.md | grep -c 'P0\|P1' || echo 'clean')" >> .pge/progress.md

# 6. Update state.md Phase Log
# 找到 state.md 的 Phase Log 段落，在其下追加一行：
# edit 命令：将 "## Phase Log\n" 替换为 "## Phase Log\n- Phase 1: commit $COMMIT_HASH — 1 task, all PASS, review clean\n"

ai kill --id "$REVIEW_ID"
```