---
name: review
description: Code review skill using codex-rs methodology with subagent execution
tools: [bash]
---

# Review Skill

使用 codex-rs 的 review 方法论执行代码审查。通过 subagent 运行专业的 review prompt。

## ⚠️ 常见错误（开始前必读！）

### 错误 1: tmux_wait.sh 参数错误

```bash
❌ 错误:   tmux_wait.sh "$SESSION_NAME" 900
✅ 正确:   tmux_wait.sh "$SESSION_NAME" /tmp/subagent-output.txt 900

原因: tmux_wait.sh 的第二个参数是 output-file（必需），不是 timeout。
     现在 tmux_wait.sh 会检测这个常见错误并给出明确提示。
```

### 错误 2: 使用 sleep 等待

```bash
❌ 错误:   sleep 30 && tmux capture-pane ...
✅ 正确:   tmux_wait.sh "$SESSION_NAME" /tmp/output.txt 30

原因: sleep 会浪费时间（如果 subagent 3秒就完成，你等了27秒）。
     tmux_wait.sh 通过 .done marker 立即检测完成。
```

### 错误 3: 串行执行而非并行

```bash
❌ 错误:   串行执行（慢）
  SESSION=$(start_subagent_tmux.sh /tmp/out.txt 15m @reviewer.md "task")
  SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
  tmux_wait.sh "$SESSION_NAME" /tmp/out.txt 900
  # Subagent 需要时间
  gh pr view $PR  # 本可以并行运行！

✅ 正确:   并行执行（快）
  SESSION=$(start_subagent_tmux.sh /tmp/out.txt 15m @reviewer.md "task")
  SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
  # 在 subagent 运行时，立即做独立的工作
  CI_STATUS=$(gh pr view $PR --json statusCheckRollup)
  ERROR_LOG=$(gh run view $RUN_ID --log-failed)
  tmux_wait.sh "$SESSION_NAME" /tmp/out.txt 900  # 只在这里等待

节省时间: 如果 subagent 需要 60s + 其他工作 5s
- 串行: 65s (60s 等待 + 5s 工作)
- 并行: 60s (同时运行)
```

### 错误 4: subagent 失败后私自执行 review

```bash
❌ 错误:   subagent 失败后自己手动执行检查命令
  # 这是被严格禁止的！违反了技能的约束。

✅ 正确:   subagent 失败后向用户报告问题，提供选项
  1) 增加超时时间重试
  2) 简化 review 范围
  3) 手动执行 review（需要用户明确授权）
  4) 放弃 review

参考技能文档中的 "Subagent 失败处理" 章节。
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
4. **启动 subagent** - 使用 `start_subagent_tmux.sh` + review system prompt
5. **等待完成** - 使用 `tmux_wait.sh` 等待 subagent 执行完毕
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

### Example 1: Review PR

```bash
# 用户输入
/skill:review 帮我 review 这个 pr https://github.com/tiancaiamao/ai/pull/42

# 执行流程
PR_NUM=42
gh pr diff $PR_NUM > /tmp/pr${PR_NUM}.diff

cat > /tmp/task.txt << 'EOF'
Read the diff from /tmp/pr42.diff and review it thoroughly.
Output your result to /tmp/review-42.json in JSON format using the write tool.
EOF

SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @/Users/genius/.ai/skills/review/reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
/Users/genius/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 900

cat /tmp/review-42.json
```

### Example 2: Review 本地变更

```bash
# 用户输入
/skill:review review 当前未提交的代码

# 执行流程
git diff > /tmp/changes.diff

cat > /tmp/task.txt << 'EOF'
Read the diff from /tmp/changes.diff and review it thoroughly.
Output your result to /tmp/review-uncommitted.json in JSON format using the write tool.
EOF

SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @/Users/genius/.ai/skills/review/reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
/Users/genius/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 900

cat /tmp/review-uncommitted.json
```

### Example 3: Review 特定 Commit

```bash
COMMIT="abc1234"
git show $COMMIT > /tmp/commit-${COMMIT}.diff

cat > /tmp/task.txt << EOF
Read the commit diff from /tmp/commit-${COMMIT}.diff and review it thoroughly.
Output your result to /tmp/review-${COMMIT}.json in JSON format using the write tool.
EOF

SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @/Users/genius/.ai/skills/review/reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
/Users/genius/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 900

cat /tmp/review-${COMMIT}.json
```

## Subagent 调用

**重要**: 本技能完全依赖 `/skill:subagent` 技能。请遵循 subagent 技能的最佳实践。

### 核心规则（来自 subagent 技能）

1. **必须使用 `start_subagent_tmux.sh` 脚本** - 不要直接调用 `ai --mode headless`
2. **大内容必须通过文件传递** - 对于超过 200 字符的内容，写入文件并传递文件路径
3. **必须使用 `tmux_wait.sh` 等待完成** - 不要依赖 `&` 后台运行
4. **指定 system prompt 和工具集**

### 标准调用模式

```bash
# 1. 获取 PR diff（大内容）
gh pr diff 42 > /tmp/pr42.diff

# 2. 准备任务描述（小内容，嵌入命令行）
cat > /tmp/task.txt << 'EOF'
Read the diff from /tmp/pr42.diff and review it.
Write your result to /tmp/review-42.json in JSON format.
EOF

# 3. 启动 subagent
SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @/Users/genius/.ai/skills/review/reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

# 4. 等待 subagent 完成
SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
/Users/genius/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 900

# 5. 读取结果
cat /tmp/review-42.json
```

### 输出文件约定

- **文件名由主 agent 决定**（如 `review-{PR号}.json`、`review-{commit}.json`）
- **在任务描述中明确告知 subagent 输出路径**
- **Subagent 使用 `write` 工具写入结果**
- **主 agent 事后读取该文件**

### 为什么使用文件传递大内容？

subagent 技能明确说明：
> For long task descriptions (>200 characters), write to file first, pass file path. Don't embed large content directly in the command.

原因：
- 命令行参数有长度限制
- Shell `$()` 替换可能导致 `file already closed` 错误
- 文件传递更可靠，支持任意大小

### 小内容 vs 大内容

**小内容（<200 字符）** - 可以直接嵌入：
```bash
SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/output.txt \
  10m \
  @reviewer.md \
  "Review the single file at /tmp/one-file.go")
```

**大内容（>200 字符或 diff）** - 必须通过文件：
```bash
cat > /tmp/task.txt << 'EOF'
Read the diff from /tmp/explore-skill.diff (6507 lines, 204KB)
Review thoroughly. Output to /tmp/review-explore-skill.json
EOF
```

### 环境变量引用

如需引用环境变量，使用 `--env` 参数（subagent 技能支持）：
```bash
SESSION=$(/Users/genius/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/output.txt \
  10m \
  --env GITHUB_TOKEN \
  @reviewer.md \
  "Read task from /tmp/task.txt")
```

## 关键规则

- **完全依赖 subagent 技能** - 使用 `start_subagent_tmux.sh` 脚本，不要直接调用 `ai`
- **必须等待完成** - 使用 `tmux_wait.sh` 确保执行完毕再读取结果
- **大内容通过文件传递** - Diff 等大内容写入文件，通过文件路径传递给 subagent
- **解析 JSON 格式输出** - 从指定文件读取 JSON 结果
- **如果没有 findings** - 输出 "No issues found"
- **overall_correctness** - 只能是 "patch is correct" 或 "patch is incorrect"

## 参考文档

详细的使用规则和最佳实践请参见：
- `/skill:subagent` - Subagent 技能文档
- `/skill:tmux` - Tmux 技能文档（含 tmux_wait.sh 说明）

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| PR 不存在 | 提示用户检查 PR 链接 |
| 无变更 | 输出 "No changes to review" |
| Subagent 超时 | 增加 timeout 或简化 review 范围 |
| JSON 解析失败 | 尝试修复 JSON 或要求 subagent 重试 |