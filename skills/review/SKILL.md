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
3. **调用 subagent** - 使用 review system prompt 执行审查
4. **收集反馈** - 解析 JSON 输出，格式化展示

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

关键点：
- 使用 reviewer.md 作为 system prompt
- 设置 timeout 10-15 分钟（复杂 review 可能需要更长时间）
- **必须后台运行并收集结果**（bash 工具有 30s 超时限制）
- 监控 session 文件获取进度

Reviewer persona 路径: `/Users/genius/.ai/skills/review/reviewer.md`

详细调试方法见 subagent 技能的 **Debugging & Monitoring** 章节。

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