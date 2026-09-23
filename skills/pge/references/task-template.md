# Task Template

任务 spec 是自包含文件——Generator 无法看到任何会话上下文，它的全部依据只有这份 task 文件。格式：

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Scope
### Read (context, do not modify)
<files the Generator may read but must NOT change — e.g. existing types, interfaces>
### Write (expected changes)
<files the Generator is expected to modify/create — anything outside this list is out of scope>
**The Kitchen Sink check compares `git status --porcelain --untracked-files=all` against this list.**

## Estimated Size
<S(<100) / M(100-300) / L(300-500) / XL(>500, consider splitting)>

## Dependencies
<which tasks must complete first, if any>

## Constraints
<operation boundaries beyond files — e.g. no commit / no dependency install / no CI config changes>

## Stop Conditions
<when to BLOCKED instead of guessing: spec 与代码现实冲突 / 依赖的输入或 API 不存在 /
需要修改 Write 列表以外的文件。遇到即输出 `BLOCKED: <reason>`。>

## Acceptance
<how to verify this task is done — the exact commands/tests, each linked to a spec acceptance criterion>
```

拆解规则（80-500 行、不共享文件、给 WHAT 不给 HOW）见 SKILL.md Step 2；委派措辞示例见 orchestrator system prompt 的 Delegation Rules。