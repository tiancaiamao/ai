---
name: review
description: Code review skill using codex-rs methodology with ag CLI
tools: [bash]
---

# Review Skill

使用 codex-rs 的 review 方法论执行代码审查。通过 ag CLI 运行专业的 review prompt。

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
3. **启动 agent** - 使用 `ag agent spawn` + review system prompt，传入项目目录和变更说明
4. **等待完成** - 使用 `ag agent wait` 等待 agent 执行完毕
5. **读取结果** - 从文件读取 JSON 输出，格式化展示

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
# 让 agent 在项目目录中自己执行 git diff，直接读源文件
ag agent spawn reviewer-$PR_NUM \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "You are in the project directory. Review PR #${PR_NUM}. Steps: 1) Run 'gh pr diff ${PR_NUM}' to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to /tmp/review-${PR_NUM}.json following the output format in your system prompt."

ag agent wait reviewer-$PR_NUM --timeout 600
cat /tmp/review-${PR_NUM}.json
ag agent rm reviewer-$PR_NUM
```

### Review uncommitted changes

```bash
ag agent spawn reviewer-local \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "You are in the project directory. Review the current uncommitted changes. Steps: 1) Run 'git diff' and/or 'git diff --cached' to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to /tmp/review-local.json following the output format in your system prompt."

ag agent wait reviewer-local --timeout 600
cat /tmp/review-local.json
ag agent rm reviewer-local
```

### Review a specific commit

```bash
COMMIT=abc123
ag agent spawn reviewer-$COMMIT \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "You are in the project directory. Review commit ${COMMIT}. Steps: 1) Run 'git show ${COMMIT}' to see the changes. 2) Read relevant source files for context. 3) Write your review findings as JSON to /tmp/review-${COMMIT}.json following the output format in your system prompt."

ag agent wait reviewer-$COMMIT --timeout 600
cat /tmp/review-${COMMIT}.json
ag agent rm reviewer-$COMMIT
```

## ⚠️ 常见错误

### ag CLI 常见错误

```bash
# spawn 语法
❌ ag agent spawn --id reviewer ...        # --id 不是 flag
❌ ag agent spawn reviewer --timeout 15m   # spawn 没有 --timeout
✅ ag agent spawn reviewer --input "..."    # id 是位置参数

# 必须用 wait
❌ ag agent spawn reviewer ... && sleep 30
✅ ag agent spawn reviewer ... && ag agent wait reviewer --timeout 600

# agent 失败后不能自己代做
❌ agent 失败后自己手动执行检查命令（严格禁止）
✅ 停下来向用户汇报，等待指示
✅ 向用户报告问题，提供选项：
   1) 增加超时时间重试
   2) 简化 review 范围
   3) 手动执行 review（需要用户明确授权）
```

## 关键规则

- **完全依赖 ag CLI** - 使用 `ag agent spawn` 启动 agent，不要直接调用 `ai`
- **必须等待完成** - 使用 `ag agent wait` 确保执行完毕再读取结果
- **传递项目目录而非 diff 文件** - 让 agent 自己 `git diff` 和读取源文件，避免大文件读取问题
- **解析 JSON 格式输出** - 从指定文件读取 JSON 结果
- **如果没有 findings** - 输出 "No issues found"
- **overall_correctness** - 只能是 "patch is correct" 或 "patch is incorrect"

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| PR 不存在 | 提示用户检查 PR 链接 |
| 无变更 | 输出 "No changes to review" |
| Agent 超时 | 增加 `ag agent wait --timeout` 或简化 review 范围 |
| JSON 解析失败 | 尝试修复 JSON 或要求 agent 重试 |
| `ag ls` 显示 backend 为 `ai` | 回到 spawn 时的 CWD 查看 |

## 参考文档

- `~/.ai/skills/ag/SKILL.md` - Agent 编排 CLI 完整文档