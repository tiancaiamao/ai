---
id: feature
name: Feature Development
description: Develop a new feature from spec to ship
phases: [spec, plan, implement, test, ship]
complexity: medium
estimated_tasks: 4-8
---

# Feature Development Workflow

## Overview

Feature workflow is **agent-driven execution**. Main agent orchestrates phases,
interacts with user, spawns sub-agents for work execution, and advances
phases via `workflow-ctl advance`.

## Phase 1: Spec（对话+探索+确认）

### Goals
- 通过对话了解需求细节
- 探索代码库理解现有系统
- 确认需求后撰写 SPEC.md
- 用户确认后进入下一 phase

### Execution: Agent 主导

#### Step 1.1: 对话了解需求

```
Agent: 我想确认一下，你想做的 xxx 功能具体是什么样的？

User: 我想要用户注册功能，支持邮箱注册。

Agent: 了解。注册时需要哪些信息？

User: 需要邮箱、密码、确认密码。

... (多轮对话，收集完整信息)
```

**Agent 动作：** 自主对话，不调用 ag spawn

#### Step 1.2: 探索代码库

Agent 决定需要探索：

```bash
# Agent 读取 templates/feature.md → "Phase 1: Spec"
# Agent 决定需要探索用户相关代码

# Agent 调用:
ag spawn \
  --id "spec-explorer-users" \
  --system "探索用户相关的代码：用户模型、数据库schema、现有API" \
  --cwd "$PWD" \
  --timeout 5m

ag wait "spec-explorer-users" --timeout 300
EXPLORER_OUTPUT=$(ag output "spec-explorer-users")
ag rm "spec-explorer-users"
```

**Agent 动作：** 读取探索结果，更新理解

#### Step 1.3: 总结并确认

```
Agent: 基于你的需求和代码探索，我总结如下：

**需求：**
- 用户注册功能，支持邮箱注册
- 注册信息：邮箱、密码（至少8位，包含大小写）、确认密码
- 注册后发送验证邮件激活账号

**当前系统现状：**
- 已有用户模型：User{Email, PasswordHash, Active}
- 已有用户表：users(email, password_hash, active)
- 已有API：GET /api/users, PUT /api/users/:id
- 没有注册API、没有邮件功能

**需要做：**
- 添加注册API：POST /api/users/register
- 实现密码验证逻辑
- 实现邮件发送功能（需要邮件服务配置）

确认吗？可以开始写 SPEC.md 了吗？

User: 确认，开始写。
```

**Agent 动作：** 等待用户确认

#### Step 1.4: 撰写 SPEC.md

**Agent 动作：** 自己撰写，不调用 ag spawn

```
# Agent 写入:
cat > .workflow/artifacts/features/SPEC.md <<'EOF'
# Feature: User Registration

## Summary
Allow users to register via email with password verification.

## Motivation
Enable new user sign-up for website access.

## User Stories
- As a new user, I want to register with email so that I can access site
- As a new user, I want to set a password (8+ chars, mixed case) for security
- As a new user, I want to receive verification email to activate my account

## Requirements
- [ ] Registration API: POST /api/users/register
- [ ] Input fields: email, password, password_confirm
- [ ] Password validation: min 8 chars, mixed case
- [ ] Send verification email after registration
- [ ] Email contains verification link
- [ ] Clicking link activates account

## Success Criteria
- [ ] Users can register with valid email/password
- [ ] Invalid passwords are rejected
- [ ] Verification email is sent
- [ ] Account is activated after clicking email link
EOF
```

#### Step 1.5: 用户确认

```
Agent: SPEC.md 已写好：

[展示 SPEC.md 内容]

需要修改吗？

User: 加一点，密码不能包含用户名。

Agent: 好的，加上密码不能包含用户名的规则。

# Agent 直接修改文件（不需要 spawn）
```

```
Agent: 更新完成。还有其他需要改的吗？

User: 没有了，可以了。

Agent: 好的，SPEC phase 完成。
```

#### Step 1.6: Advance

```bash
# Agent 调用:
workflow-ctl advance
```

**STATE.json 更新：** `{currentPhase: 1}` (plan)

---

## Phase 2: Plan（分析+拆解+review）

### Goals
- 分析 SPEC.md
- 探索代码库（如需要）
- 拆解成具体任务
- 写 PLAN.md
- Review loop（worker-reviewer）
- 确认后进入下一 phase

### Execution: Agent 主导

#### Step 2.1: 分析 SPEC.md

**Agent 动作：** 读取并分析

#### Step 2.2: 探索代码库（如需要）

```bash
# Agent 决定需要探索邮件服务

ag spawn \
  --id "plan-explorer-email" \
  --system "探索邮件服务：是否有邮件发送功能？如何配置？" \
  --cwd "$PWD" \
  --timeout 5m

ag wait "plan-explorer-email" --timeout 300
EMAIL_OUTPUT=$(ag output "plan-explorer-email")
ag rm "plan-explorer-email"
```

#### Step 2.3: 拆解任务

**Agent 动作：** 自己思考，不需要 spawn

```
# Agent 决定任务列表:
1. 实现密码验证函数
2. 创建注册API handler
3. 实现邮件服务（SMTP）
4. 生成验证码/token
5. 发送验证邮件
6. 实现激活API：GET /api/users/activate?token=xxx
7. 更新数据库schema（需要activation_token字段）
8. 写单元测试
9. 写集成测试
```

#### Step 2.4: 撰写 PLAN.md

**Agent 动作：** 自己撰写

```
# Agent 写入:
cat > .workflow/artifacts/features/PLAN.md <<'EOF'
# Plan: User Registration

## Sub-phases

### Sub-phase 2.1: 基础设施搭建
- Task-1: Password Validation
- Task-2: Activation Token
- Task-3: Database Schema

### Sub-phase 2.2: 核心功能
- Task-4: Email Service
- Task-5: Registration API

### Sub-phase 2.3: 激活流程
- Task-6: Send Verification Email
- Task-7: Activation API

### Sub-phase 2.4: 测试
- Task-8: Unit Tests
- Task-9: Integration Tests

## Tasks Detail

### Task-1: Password Validation
**File:** pkg/user/password.go
**Description:** Implement password validation: min 8 chars, mixed case, no username.
**Estimated:** 1 hour
**Dependencies:** none

### Task-2: Activation Token
**File:** pkg/user/token.go
**Description:** Generate and validate activation tokens. 24h expiry.
**Estimated:** 1 hour
**Dependencies:** none

... (其他任务)

## Dependencies
- Task-1, Task-2, Task-3 可以并行
- Task-4, Task-5 可以并行
- Task-6, Task-7 可以并行
- Task-8, Task-9 依赖其他所有任务
EOF
```

#### Step 2.5: Review Loop（worker-reviewer）

```
Agent: PLAN.md 已写好，我需要 review 一下。

# Agent 决定使用 pair pattern

## Round 1:

# Agent spawns reviewer
ag spawn \
  --id "plan-reviewer-r1" \
  --system "你正在 review 一个 implementation plan。检查任务完整性、依赖关系正确性、估算是否合理。" \
  --input .workflow/artifacts/features/PLAN.md \
  --cwd "$PWD" \
  --timeout 10m

ag wait "plan-reviewer-r1" --timeout 600
REVIEW_OUTPUT=$(ag output "plan-reviewer-r1")
ag rm "plan-reviewer-r1"

# REVIEW_OUTPUT:
# Verdict: CHANGES_REQUESTED
# Issues:
# - Task-4 (Email Service) 应该拆解：SMTP client + email templates
# - 缺少任务：配置 SMTP settings
# - Task-9 (Integration Tests) 依赖 Task-7 (Activation API)，但这不对，应该测试完整的注册流程
```

**Agent 动作：** 读取反馈，更新 PLAN.md（直接修改）

```
## Round 2:

# Agent spawns reviewer again
ag spawn \
  --id "plan-reviewer-r2" \
  --system "你正在再次 review plan。检查问题是否都已解决。" \
  --input .workflow/artifacts/features/PLAN.md \
  --cwd "$PWD" \
  --timeout 10m

ag wait "plan-reviewer-r2" --timeout 600
REVIEW_OUTPUT=$(ag output "plan-reviewer-r2")
ag rm "plan-reviewer-r2"

# REVIEW_OUTPUT:
# Verdict: APPROVED
```

```
Agent: Plan 已 approved，继续下一个 phase。

workflow-ctl advance
```

---

## Phase 3: Implement（sub-phases + review loop + commit）

### Goals
- 组织任务成 sub-phases
- 每个 sub-phase: 并行执行任务 → review loop → commit
- 所有 sub-phases 完成后进入下一 phase

### Execution: Agent 主导

#### Step 3.1: 组织任务成 sub-phases

**Agent 动作：** 读取 PLAN.md，自主决策

```
Agent: 基于任务依赖和逻辑，我组织成 4 个 sub-phases：

## Sub-phase 3.1: 基础设施搭建
- Task-1: Password Validation
- Task-2: Activation Token
- Task-3: Database Schema

## Sub-phase 3.2: 核心功能
- Task-4: SMTP Client
- Task-5: Email Templates
- Task-6: Registration API

## Sub-phase 3.3: 激活流程
- Task-7: Send Verification Email
- Task-8: Activation API

## Sub-phase 3.4: 测试
- Task-9: Unit Tests
- Task-10: Integration Tests

每个 sub-phase 完成后，review，确认后 commit，再进入下个 sub-phase。
```

#### Sub-phase 3.1: 基础设施搭建

**Step 3.1.1: Spawn workers 并行执行**

```bash
# Agent spawns workers
ag spawn \
  --id "impl-3.1-task1" \
  --system "实现密码验证在 pkg/user/password.go。要求：min 8 chars, mixed case, no username。" \
  --input <(cat .workflow/artifacts/features/PLAN.md | grep -A10 "Task-1") \
  --cwd "$PWD" \
  --timeout 2h

ag spawn \
  --id "impl-3.1-task2" \
  --system "实现 activation token 在 pkg/user/token.go。生成随机 token，验证 token (24h 过期)。" \
  --input <(cat .workflow/artifacts/features/PLAN.md | grep -A10 "Task-2") \
  --cwd "$PWD" \
  --timeout 2h

ag spawn \
  --id "impl-3.1-task3" \
  --system "更新数据库 schema：添加 activation_token 字段到 users 表。" \
  --input <(cat .workflow/artifacts/features/PLAN.md | grep -A10 "Task-3") \
  --cwd "$PWD" \
  --timeout 2h

# Agent waits for all 3 workers
ag wait "impl-3.1-task1" --timeout 7200
ag wait "impl-3.1-task2" --timeout 7200
ag wait "impl-3.1-task3" --timeout 7200

# 捕获输出
TASK1_OUTPUT=$(ag output "impl-3.1-task1")
TASK2_OUTPUT=$(ag output "impl-3.1-task2")
TASK3_OUTPUT=$(ag output "impl-3.1-task3")

# Cleanup
ag rm "impl-3.1-task1" "impl-3.1-task2" "impl-3.1-task3"
```

**Step 3.1.2: Review Loop（pair.sh pattern）**

```bash
WORKER_PROMPT="你修复基础设施代码中的问题。基于 reviewer 的反馈，修正代码。"
REVIEWER_PROMPT="你正在审查基础设施代码。检查：密码验证逻辑、token 生成逻辑、数据库 schema 变更。"

./ag/patterns/pair.sh \
  "$WORKER_PROMPT" \
  "$REVIEWER_PROMPT" \
  <(cat <<'EOF'
Sub-phase 3.1: 基础设施搭建

Task-1: Password Validation
$TASK1_OUTPUT

Task-2: Activation Token
$TASK2_OUTPUT

Task-3: Database Schema
$TASK3_OUTPUT
EOF
) \
  3  # max rounds
```

**pair.sh 输出：**
```
[pair] Round 2: APPROVED
[pair] ✅ Complete
```

**Step 3.1.3: Commit sub-phase**

```bash
# Agent 调用 git
git add pkg/user/password.go pkg/user/token.go db/schema.sql
git commit -m "feat(feature): implement 基础设施-密码验证/token生成/数据库schema"

# 输出:
# [main abc1234] feat(feature): implement 基础设施-密码验证/token生成/数据库schema
#  3 files changed, 45 insertions(+)
```

#### Sub-phase 3.2: 核心功能

同样流程：
1. Spawn workers (Task-4, Task-5, Task-6) 并行
2. Review loop（pair.sh）
3. Commit

```bash
git commit -m "feat(feature): implement 核心功能-SMTP客户端/邮件模板/注册API"
```

#### Sub-phase 3.3: 激活流程

同样流程：
1. Spawn workers (Task-7, Task-8) 并行
2. Review loop（pair.sh）
3. Commit

```bash
git commit -m "feat(feature): implement 激活流程-验证邮件/激活API"
```

#### Sub-phase 3.4: 测试

同样流程：
1. Spawn workers (Task-9, Task-10) 并行
2. Review loop（pair.sh）
3. Commit

```bash
git commit -m "test(feature): 测试-单元测试/集成测试"
```

#### Step 3.2: Implement phase 完成

```
Agent: 所有 sub-phases 完成，Implement phase 结束。

workflow-ctl advance
```

**STATE.json 更新：** `{currentPhase: 3}` (test)

---

## Phase 4: Test（执行测试+review）

### Goals
- 运行测试套件
- 验证所有需求满足

### Execution: Agent 主导

#### Step 4.1: Spawn test agent

```bash
# Agent spawns worker
ag spawn \
  --id "feature-test-worker" \
  --system "你正在运行测试并验证 feature。执行 go test ./... 并报告结果。" \
  --input <(cat .workflow/artifacts/features/SPEC.md) \
  --cwd "$PWD" \
  --timeout 15m

ag wait "feature-test-worker" --timeout 900
ag output "feature-test-worker" > .workflow/artifacts/features/test-results.md
ag rm "feature-test-worker"
```

#### Step 4.2: Review Loop（pair.sh）

```bash
WORKER_PROMPT="你修复测试中的问题。"
REVIEWER_PROMPT="你正在 review 测试结果。检查所有测试通过、覆盖率是否充足。"

./ag/patterns/pair.sh \
  "$WORKER_PROMPT" \
  "$REVIEWER_PROMPT" \
  .workflow/artifacts/features/test-results.md \
  2
```

#### Step 4.3: Advance

```
workflow-ctl advance
```

---

## Phase 5: Ship（打包提交）

### Goals
- 清理 commit 历史
- 创建 PR（如适用）
- 更新文档

### Execution: Agent 主导

#### Step 5.1: Spawn ship agent

```bash
ag spawn \
  --id "feature-ship-worker" \
  --system "你正在准备 ship feature。Squash commits，写 PR 描述，更新文档。" \
  --input <(echo "Feature: $FEATURE_DESC") \
  --cwd "$PWD" \
  --timeout 10m

ag wait "feature-ship-worker" --timeout 600
ag output "feature-ship-worker" > .workflow/artifacts/features/ship-summary.md
ag rm "feature-ship-worker"
```

#### Step 5.2: Review Loop

```bash
WORKER_PROMPT="你完善 shipping package。"
REVIEWER_PROMPT="你正在 review 最终 shipping package。检查 PR 描述、commit 历史。"

./ag/patterns/pair.sh \
  "$WORKER_PROMPT" \
  "$REVIEWER_PROMPT" \
  .workflow/artifacts/features/ship-summary.md \
  2
```

#### Step 5.3: Final commit

```bash
git commit -m "ship(feature): ship user registration feature"
```

---

## Summary: Agent Autonomy

| Phase | Agent 动作 | ag spawn 用途 |
|-------|-------------|---------------|
| Spec | 对话用户 → 探索代码库（spawn explorers）→ 撰写 SPEC.md → 确认 | Spawn explorers for code search |
| Plan | 分析 SPEC → 探索代码库（spawn explorers）→ 撰写 PLAN.md → review（spawn reviewers loop） | Spawn explorers + reviewers |
| Implement | 组织 sub-phases → 每个 sub-phase: spawn workers 并行执行 → review（pair.sh）→ commit | Spawn workers + reviewers (pair.sh) |
| Test | Spawn test worker → review（pair.sh） | Spawn worker + reviewers |
| Ship | Spawn ship worker → review（pair.sh） | Spawn worker + reviewers |

**关键原则：**
- Main agent 是 **coordinator**，不写代码
- Main agent 决定什么时候 spawn sub-agents
- Main agent 决定什么时候调用 `workflow-ctl advance`
- 状态更新通过 `workflow-ctl`，保证确定性
- 每个 sub-phase 都有完整的：执行 → review loop → commit