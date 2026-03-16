---
name: review
description: Code review skill using codex-rs methodology with subagent execution
tools: [bash]
---

# Review Skill

使用 codex-rs 的 review 方法论执行代码审查。通过 subagent 运行专业的 review prompt。

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
3. **准备输出文件** - 确定结果文件路径（如 `/tmp/review-42.json`）
4. **调用 subagent** - 使用 review system prompt 执行审查
5. **读取结果** - 从文件读取 JSON 输出，格式化展示

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
1. gh pr diff 42 > /tmp/pr.diff
2. 调用 subagent with review prompt
3. 收集 JSON 结果
4. 格式化输出
```

### Example 2: Review 本地变更

```bash
# 用户输入
/skill:review review 当前未提交的代码

# 执行流程
1. git diff > /tmp/changes.diff
2. 调用 subagent with review prompt
3. 收集 JSON 结果
```

## Subagent 调用

**重要**: 调用 subagent 时请参考 `/skill:subagent` 技能的最佳实践。

### 指定工具集

必须指定 `--tools` 参数，确保 subagent 有写文件权限：

```bash
ai --mode headless \
  --system-prompt @/path/to/reviewer.md \
  --tools read,write,grep,edit,glob \
  --timeout 15m \
  "Review the PR... Write your result to /tmp/review-42.json"
```

### 输出文件约定

在 prompt 里明确告诉 subagent 输出文件路径：

```
Review the following PR diff ... Write your result to /tmp/review-42.json
```

**关键点**:
- 文件名由主 agent 决定（如 `review-{PR号}.json`）
- Subagent 使用 `write` 工具把 JSON 结果写入文件
- 主 agent 事后读取该文件获取结果
- 不要依赖 subagent 的 stdout（会混有日志）

### 示例

```bash
# 1. 获取 PR diff
gh pr diff 42 > /tmp/pr42.diff

# 2. 调用 subagent，指定输出文件
ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/review/reviewer.md \
  --tools read,write,grep,edit \
  --timeout 15m \
  "Review this PR diff. Write your result to /tmp/review-42.json

$(cat /tmp/pr42.diff)" &

# 3. 等待完成后读取结果
cat /tmp/review-42.json
```

## 关键规则

- 使用 subagent 隔离执行
- 必须等待 subagent 完成
- 解析 JSON 格式输出
- 如果无 findings，输出 "No issues found"
- overall_correctness 只能是 "patch is correct" 或 "patch is incorrect"

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| PR 不存在 | 提示用户检查 PR 链接 |
| 无变更 | 输出 "No changes to review" |
| Subagent 超时 | 增加 timeout 或简化 review 范围 |
| JSON 解析失败 | 尝试修复 JSON 或要求 subagent 重试 |