---
name: review
description: Code review skill using codex-rs methodology with ai CLI
tools: [bash]
---

# Review Skill

使用 codex-rs 的 review 方法论执行代码审查。通过 `ai serve` + `ai send` + `ai watch` 运行独立的 review agent。

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
2. **确定项目目录和变更范围** - 解析出项目路径和需要 review 的变更描述
3. **启动 review agent** - 用 `tmux` + `ai serve` 启动独立 agent，传入 reviewer system prompt 和任务
4. **发送任务** - 用 `ai send` 发送 review 指令
5. **等待完成** - 用 `ai watch --follow --pretty` 实时观察直到 agent_end
6. **读取结果** - agent 将 JSON 输出写到指定文件，读取并格式化展示

### 为什么不生成 diff 文件？

Agent 足够智能，给它项目目录和变更说明，它可以自己执行 `git diff`、读取源文件、
理解上下文。预生成 diff 文件会带来问题：
- 大 diff 可能超出 read tool 的限制，导致 agent 被迫多次 grep/read
- 多次读取导致 context 膨胀，触发 compaction 浪费大量时间
- Agent 自己 `git diff` 可以灵活控制范围，按需读取相关源文件

## Review System Prompt

审查使用的 system prompt 位于 `~/.ai/skills/review/reviewer.md`，基于 codex-rs review_prompt.md，包含：

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

### Review PR

```bash
PR_NUM=42
REVIEW_ID="reviewer-$PR_NUM"
REVIEW_OUT="/tmp/review-${PR_NUM}.json"
SESSION="rev-$PR_NUM"

# 1. 在 tmux 里启动独立 agent，加载 reviewer system prompt
tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt '@$HOME/.ai/skills/review/reviewer.md' --input 'You are in the project directory. Review PR #${PR_NUM}. Steps: 1) Run gh pr diff ${PR_NUM} to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to ${REVIEW_OUT} following the output format in your system prompt.'"

# 2. 等待完成 — 用 watch --follow --pretty 观察输出
timeout 600 ai watch --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')" --follow --pretty

# 3. 读取结果
cat "$REVIEW_OUT"

# 4. 清理
ai kill --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')"
tmux kill-session -t "$SESSION"
```

### Review uncommitted changes

```bash
SESSION="rev-local"
REVIEW_OUT="/tmp/review-local.json"

tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt '@$HOME/.ai/skills/review/reviewer.md' --input 'You are in the project directory. Review the current uncommitted changes. Steps: 1) Run git diff and/or git diff --cached to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to ${REVIEW_OUT} following the output format in your system prompt.'"

timeout 600 ai watch --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')" --follow --pretty
cat "$REVIEW_OUT"
ai kill --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')"
tmux kill-session -t "$SESSION"
```

### Review a specific commit

```bash
COMMIT=abc123
SESSION="rev-$COMMIT"
REVIEW_OUT="/tmp/review-${COMMIT}.json"

tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt '@$HOME/.ai/skills/review/reviewer.md' --input 'You are in the project directory. Review commit ${COMMIT}. Steps: 1) Run git show ${COMMIT} to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to ${REVIEW_OUT} following the output format in your system prompt.'"

timeout 600 ai watch --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')" --follow --pretty
cat "$REVIEW_OUT"
ai kill --id "$(tmux capture-pane -t "$SESSION" -p | tr -d ' ')"
tmux kill-session -t "$SESSION"
```

## ⚠️ 常见错误

```bash
# ai serve 是阻塞的，必须用 tmux
❌ ai serve --input "..." && echo done     # 永远不会到 echo
✅ tmux new-session -d -s rev "ai serve --input '...'"

# 获取 run ID — ai serve 输出的第一行就是 ID
❌ ai serve --input "..." | head -1        # 会断开 serve 的 stdin
✅ tmux capture-pane -t SESSION -p | tr -d ' '  # 从 tmux pane 读取

# 必须等 serve 启动后再 send
❌ ai serve ... & ai send --id xxx "..."   # 竞态，serve 还没启动
✅ tmux new-session -d ... && sleep 2 && ai send --id $ID "..."  # 等 serve 就绪

# agent 失败后不能自己代做
❌ agent 失败后自己手动执行检查命令（严格禁止）
✅ 停下来向用户汇报，等待指示
```

## 关键规则

- **用 `ai serve` 启动独立 agent** — 配合 tmux，不用 ag 基础设施
- **用 `ai send` 发送任务** — 不需要 spawn 子命令
- **用 `ai watch --follow --pretty` 观察结果** — 实时流式输出
- **用 `ai kill` 清理** — review 完成后杀掉 agent
- **传递项目目录而非 diff 文件** — 让 agent 自己 `git diff` 和读取源文件
- **解析 JSON 格式输出** — 从指定文件读取 JSON 结果
- **如果没有 findings** — 输出 "No issues found"
- **overall_correctness** — 只能是 "patch is correct" 或 "patch is incorrect"

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| PR 不存在 | 提示用户检查 PR 链接 |
| 无变更 | 输出 "No changes to review" |
| Agent 超时 | 增加 `timeout` 值或简化 review 范围 |
| JSON 解析失败 | 尝试修复 JSON 或要求 agent 重试 |
| tmux session 已存在 | 先 `tmux kill-session -t NAME` 再重试 |

## 参考文档

- `~/.ai/skills/review/reviewer.md` - Reviewer system prompt（codex-rs 方法论）