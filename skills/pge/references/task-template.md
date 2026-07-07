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

### Files Section Note

Split files into **Read** (现有的接口/类型，只读不改) 和 **Write** (本次需要改/创建的文件)。Write 列表是 Kitchen Sink 检查的比对依据——Generator 若修改了 Write 列表以外的文件，会被检测到并回滚。这防止 long run 中 scope 无声膨胀。