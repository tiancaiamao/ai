---
name: review
description: Code review skill using codex-rs methodology with ag CLI
tools: [bash]
---

# Review Skill

使用 codex-rs 的 review 方法论执行代码审查。通过 ag CLI 运行专业的 review prompt。

## ⚠️ 常见错误（开始前必读！）

### 错误 1: CLI 参数语法错误

```bash
❌ 错误:   ag spawn --id reviewer ...        # --id 不是 flag
❌ 错误:   ag spawn reviewer --timeout 15m   # spawn 没有 --timeout
✅ 正确:   ag spawn reviewer --input "..."    # id 是位置参数
          ag wait reviewer --timeout 600      # timeout 只在 wait 上

原因: ag spawn <id> 的 id 是位置参数，不是 flag。
     --timeout 只在 ag wait 上有效，spawn 不接受。
```

### 错误 2: 不使用 ag wait

```bash
❌ 错误:   ag spawn reviewer ... && sleep 30
✅ 正确:   ag spawn reviewer ... && ag wait reviewer --timeout 600

原因: sleep 会浪费时间（如果 agent 3秒就完成，你等了27秒）。
     ag wait 通过 .done marker 立即检测完成。
```

### 错误 3: 串行执行而非并行

```bash
❌ 错误:   串行执行（慢）
  ag spawn reviewer --system @reviewer.md --input "review code"
  ag wait reviewer --timeout 600
  # Agent 运行时做其他工作，这里应该并行

✅ 正确:   并行执行（快）
  ag spawn reviewer --system @reviewer.md --input "review code"
  gh pr view $PR --json statusCheckRollup  # 立即做其他工作
  ag wait reviewer --timeout 600           # 只在这里等待

节省时间: 如果 agent 需要 60s + 其他工作 5s
- 串行: 65s (60s 等待 + 5s 工作)
- 并行: 60s (同时运行)
```

### 错误 4: agent 失败后私自执行 review

```bash
❌ 错误:   agent 失败后自己手动执行检查命令
  # 这是被严格禁止的！违反了技能的约束。

✅ 正确:   agent 失败后向用户报告问题，提供选项
  1) 增加超时时间重试
  2) 简化 review 范围
  3) 手动执行 review（需要用户明确授权）
  4) 放弃 review

参考技能文档中的 "Agent 失败处理" 章节。
```

## 使用方式

```
/skill:review 帮我 review 这个 pr https://github.com/tiancaiamao/ai/pull/42
/skill:review review 当前未提交的代码
/skill:review review commit abc123
```

## 支持的 Review 模式

| 模式 | 命令示例 | 说明 |
|------|----------|------|
| PR Review | `/skill:review review PR #42` | Review GitHub PR |
| Uncommitted | `/skill:review review 当前未提交的代码` | Review 本地未提交更改 |
| Commit | `/skill:review review commit abc123` | Review 指定 commit |
| Base Branch | `/skill:review review against main` | Review 相对于 base branch 的变更 |

## 执行流程

1. **解析用户输入** - 识别 review 模式（PR/commit/uncommitted）
2. **获取代码变更** - 通过 gh pr diff 或 git 命令获取变更
3. **准备任务文件** - 将大内容（如 diff）写入文件，准备任务描述
4. **启动 agent** - 使用 `ag spawn` + review system prompt
5. **等待完成** - 使用 `ag wait` 等待 agent 执行完毕
6. **读取结果** - 从文件读取 JSON 输出，格式化展示

## Review System Prompt

审查使用的 system prompt 基于 codex-rs review_prompt.md，包含：

- **审查标准**: 影响准确性/性能/安全性/可维护性的问题
- **优先级**: P0 (阻塞) / P1 (紧急) / P2 (普通) / P3 (建议)
- **评论规范**:
  - 不超过 1 段落
  - 代码块不超过 3 行
  - 使用 suggestion block 提供修复建议
  - 保持客观、不带恭维

## 输出格式

```json
{
  "findings": [
    {
      "title": "[P1] Fix null pointer risk in auth handler",
      "body": "在 user.go:45 处，user 可能是 nil...",
      "confidence_score": 0.9,
      "priority": 1,
      "code_location": {
        "absolute_file_path": "/path/to/user.go",
        "line_range": {"start": 44, "end": 46}
      }
    }
  ],
  "overall_correctness": "patch is correct",
  "overall_explanation": "代码质量良好，无阻塞问题",
  "overall_confidence_score": 0.85
}
```

## 示例

### Example 1: Review PR (codex backend)

```bash
export AG_BIN=~/.ai/skills/ag/ag

# ⛔ MANDATORY: Codex requires proxy in restricted network
export HTTP_PROXY=http://127.0.0.1:8119
export HTTPS_PROXY=http://127.0.0.1:8119

# ⛠️ Setup: backends.yaml must be in CWD for ag to find it
cp /Users/genius/.ai/skills/ag/backends.yaml /tmp/

PR_NUM=42
cd /tmp
gh pr diff $PR_NUM > /tmp/pr${PR_NUM}.diff

# 启动 reviewer agent (codex backend)
$AG_BIN agent spawn reviewer-$PR_NUM \
  --backend codex \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read the diff from /tmp/pr${PR_NUM}.diff and review it. Write result to /tmp/review-${PR_NUM}.json"

# 等待完成
$AG_BIN agent wait reviewer-$PR_NUM --timeout 600

# 读取结果
cat /tmp/review-${PR_NUM}.json

# 清理
$AG_BIN agent rm reviewer-$PR_NUM
```

### Example 2: Review PR (default ai backend)

```bash
export AG_BIN=~/.ai/skills/ag/ag

# 用户输入
/skill:review 帮我 review 这个 pr https://github.com/tiancaiamao/ai/pull/42

# 执行流程
PR_NUM=42
gh pr diff $PR_NUM > /tmp/pr${PR_NUM}.diff

# 启动 reviewer agent (default ai backend)
$AG_BIN agent spawn reviewer-$PR_NUM \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read the diff from /tmp/pr${PR_NUM}.diff and review it. Write result to /tmp/review-${PR_NUM}.json"

# 等待完成
$AG_BIN agent wait reviewer-$PR_NUM --timeout 600

# 读取结果
cat /tmp/review-${PR_NUM}.json

# 清理
$AG_BIN agent rm reviewer-$PR_NUM
```

### Example 3: Review 本地变更

```bash
git diff > /tmp/changes.diff

$AG_BIN agent spawn review-uncommitted \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read the diff from /tmp/changes.diff and review it. Write result to /tmp/review-uncommitted.json"

$AG_BIN agent wait review-uncommitted --timeout 900
cat /tmp/review-uncommitted.json
$AG_BIN agent rm review-uncommitted
```

### Example 4: Review 特定 Commit

```bash
COMMIT="abc1234"
git show $COMMIT > /tmp/commit-${COMMIT}.diff

$AG_BIN agent spawn review-${COMMIT} \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read the commit diff from /tmp/commit-${COMMIT}.diff and review it. Write result to /tmp/review-${COMMIT}.json"

$AG_BIN agent wait review-${COMMIT} --timeout 900
cat /tmp/review-${COMMIT}.json
$AG_BIN agent rm review-${COMMIT}
```

## 使用 ag 执行 Review

**⚠️ 重要：使用 `ag` CLI 而不是 subagent**

`subagent` skill 已经被废弃，统一使用 `ag` CLI。

### 标准调用模式

```bash
export AG_BIN=~/.ai/skills/ag/ag

# 1. 准备输入（diff）
gh pr diff 42 > /tmp/pr42.diff

# 2. 启动 reviewer agent
$AG_BIN agent spawn reviewer-42 \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read diff from /tmp/pr42.diff and review it. Write result to /tmp/review-42.json"

# 3. 等待完成
$AG_BIN agent wait reviewer-42 --timeout 900

# 4. 读取结果
cat /tmp/review-42.json

# 5. 清理
$AG_BIN agent rm reviewer-42
```

### ag vs subagent 对比

| 功能 | subagent (旧) | ag (新) |
|------|---------------|----------|
| **启动** | `start_subagent_tmux.sh` | `ag spawn --id ...` |
| **等待** | `tmux_wait.sh` | `ag wait ...` |
| **获取输出** | `cat output.txt` | `ag output ...` |
| **清理** | 手动 | `ag rm ...` |
| **状态检查** | 手动 `tmux ls` | `ag status/ls` |

### 迁移指南

**旧方式（subagent）：**
```bash
SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @reviewer.md \
  "Read diff from /tmp/pr42.diff")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" /tmp/subagent-output.txt 900
OUTPUT=$(cat /tmp/subagent-output.txt)
```

**新方式（ag）：**
```bash
$AG_BIN agent spawn reviewer-42 \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read diff from /tmp/pr42.diff and review it. Write result to /tmp/review-42.json"

$AG_BIN agent wait reviewer-42 --timeout 900
OUTPUT=$($AG_BIN agent output reviewer-42)
$AG_BIN agent rm reviewer-42
```

详细迁移指南请参考：`~/.ai/skills/MIGRATION-subagent-to-ag.md`

## Worker-Judge Loop（完整 Review 流程）

单轮 Worker review 容易产生误报。Worker-Judge Loop 通过两轮独立评估提高 review 质量：

```
Worker (codex) → findings JSON → Judge (codex) → evaluated findings → 主 agent 决策
```

### 流程

#### Step 1: 准备

```bash
# ⛔ MANDATORY: Codex 需要代理才能访问 API
export HTTP_PROXY=http://127.0.0.1:8119
export HTTPS_PROXY=http://127.0.0.1:8119

# backends.yaml 必须在 CWD（ag 的 FindBackendsFile 在当前目录查找）
cp /Users/genius/.ai/skills/ag/backends.yaml /tmp/
cd /tmp

# 准备 diff 文件
cd /path/to/repo && git diff main...HEAD > /tmp/pr-review.diff
```

#### Step 2: Worker — 执行 Review

```bash
$AG_BIN agent spawn worker-review \
  --backend codex \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Read the diff from /tmp/pr-review.diff and review it. Write your findings as JSON to /tmp/review-findings.json"

$AG_BIN agent wait worker-review --timeout 600
$AG_BIN agent rm worker-review
```

#### Step 3: Judge — 评估 Worker Findings

```bash
# Judge 使用专门的 judge prompt（见下方 Judge Prompt 模板）
$AG_BIN agent spawn judge-review \
  --backend codex \
  --system @/tmp/judge-prompt.md \
  --input "Read the diff from /tmp/pr-review.diff and the review findings from /tmp/review-findings.json. Evaluate each finding as true_positive, false_positive, or partially_valid. Write your evaluation as JSON to /tmp/judge-result.json"

$AG_BIN agent wait judge-review --timeout 600
$AG_BIN agent rm judge-review
```

#### Step 4: 主 Agent 决策

读取 `/tmp/judge-result.json`，根据 judge 评估结果决定行动：

| Judge 结果 | 行动 |
|------------|------|
| 全部 false_positive | 无需修改，PR 可以合并 |
| 有 true_positive | 针对性修复 judge 确认的问题 |
| 有 partially_valid | 进一步验证（如对比 wire format、检查外部消费者） |
| 有 investigate_further | 可能需要第二轮 worker 补充 review |

### Judge Prompt 模板

创建 `/tmp/judge-prompt.md`：

```markdown
# Review Judge

You are a **judge** evaluating code review findings produced by a reviewer agent.

## Your Task

1. Read the diff from the file specified in your input
2. Read the review findings JSON from the file specified in your input
3. For **each finding**, determine whether it is a **true positive** or a **false positive**
4. Write your evaluation as JSON

## Output Format

Write JSON (no markdown fences):

{
  "findings_evaluated": [
    {
      "original_title": "<from worker>",
      "verdict": "true_positive" | "false_positive" | "partially_valid",
      "reasoning": "<1-3 sentences>",
      "suggested_action": "fix" | "skip" | "investigate_further"
    }
  ],
  "summary": {
    "true_positives": <int>,
    "false_positives": <int>,
    "partially_valid": <int>,
    "needs_follow_up": <bool>,
    "overall_assessment": "<1-2 sentences>"
  }
}

## Rules
- Be rigorous. A finding is only true positive if you can point to specific code that proves it.
- If unsure, mark as "investigate_further" rather than guessing.
- Focus on the diff, not on code outside the changes.
- Write the result to the output file using the write tool.
```

### Codex Backend 使用须知

| 要点 | 说明 |
|------|------|
| **代理** | ⛔ 必须设置 `HTTP_PROXY=http://127.0.0.1:8119` 和 `HTTPS_PROXY`，否则 codex 无法连接 API |
| **backends.yaml** | 必须在 CWD。ag 的 `FindBackendsFile` 在当前目录查找，找不到则回退到 `ai` backend |
| **环境变量继承** | `exec.Command` 不设置 `cmd.Env`，因此 spawn 的 codex 子进程会继承 shell 的环境变量 |
| **--skip-git-repo-check** | codex 在非 git 目录运行时需要此 flag（已在 backends.yaml 中配置） |
| **raw protocol** | codex 使用 `raw` 协议，不支持 steer/abort/prompt，只能等它自己完成 |
| **超时** | codex 不能被中途取消，务必设置合理的 `ag wait --timeout` |

## 关键规则

- **完全依赖 ag CLI** - 使用 `ag spawn` 启动 agent，不要直接调用 `ai`
- **必须等待完成** - 使用 `ag wait` 确保执行完毕再读取结果
- **大内容通过文件传递** - Diff 等大内容写入文件，通过文件路径传递给 agent
- **解析 JSON 格式输出** - 从指定文件读取 JSON 结果
- **如果没有 findings** - 输出 "No issues found"
- **overall_correctness** - 只能是 "patch is correct" 或 "patch is incorrect"

## 参考文档

详细的使用规则和最佳实践请参见：
- `~/.ai/skills/ag/SKILL.md` - Agent 编排 CLI 完整文档
- `~/.ai/skills/MIGRATION-subagent-to-ag.md` - 从 subagent 迁移到 ag 的详细指南

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| PR 不存在 | 提示用户检查 PR 链接 |
| 无变更 | 输出 "No changes to review" |
| Agent 超时 | 增加 `ag wait --timeout` 或简化 review 范围 |
| JSON 解析失败 | 尝试修复 JSON 或要求 agent 重试 |
| Codex 连接超时 | ⛔ 检查 `HTTP_PROXY` / `HTTPS_PROXY` 是否已 export（每次新 shell 都要设） |
| `unknown backend "codex"` | `backends.yaml` 不在 CWD。`cp ~/.ai/skills/ag/backends.yaml ./` |
| `ag ls` 显示 backend 为 `ai` | 你可能在另一个目录运行的 `ag ls`，回到 spawn 时的 CWD 查看 |
| Codex agent 无活动 | 检查 `stderr`：`cat .ag/agents/<id>/stderr`，常见原因是代理未设置 |
| `--id` flag 报错 | `ag spawn <id>` 的 id 是位置参数，不是 flag |
| `--timeout` flag 报错 | `--timeout` 只在 `ag wait` 上，`spawn` 不接受 |