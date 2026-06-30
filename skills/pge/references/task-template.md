# Task Template

写入 `.pge/tasks/task-{name}.md`：

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files (scope)
<expected files to modify/create — MUST be explicit>

## Estimated Size
<S(<100) / M(100-300) / L(300-500) / XL(>500, consider splitting)>

## Dependencies
<which tasks must complete first, if any>

## Acceptance
<how to verify this task is done — linked to spec's acceptance criteria>
```

## Task 拆解规则

- 每个任务 80-500 行（<80 合并，>500 拆分）
- 任务之间不共享文件（共享则改为串行）
- 给 WHAT（outcome），不给 HOW（实现），但包含足够上下文让 Generator 独立工作

## Delegation Tips

✅ Good: `"Implement JWT auth middleware. The handler should validate the token from the Authorization header and set user context. See spec.md acceptance criteria L1.1 and L2.1."`

❌ Bad: `"Add some auth stuff"` — too vague