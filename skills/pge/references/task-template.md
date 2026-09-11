# Task Template

写入 `.pge/tasks/task-{name}.md`：

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files
### Read (context, do not modify)
<files the Generator can read but must NOT change — e.g. existing types, interfaces>

### Write (expected changes)
<files the Generator is expected to modify/create>
**Kitchen Sink check will compare `git status --porcelain --untracked-files=all` against this list.**

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
<how to verify this task is done — linked to spec's acceptance criteria>
```

拆解规则（80-500 行、不共享文件、给 WHAT 不给 HOW）见 SKILL.md Phase 2；委派措辞示例见 orchestrator system prompt 的 Delegation Rules。