# Eval Report Format

Evaluator **必须**将验证结果写入 `.pge/eval-{task}.md`：

```markdown
# Eval Report: {task-name}

**Result: PASS / FAIL**

## Criteria Verification

### L1 — Structural
- [✅/❌] <criterion> — Evidence: <actual output or observation>

### L2 — Behavioral
- [✅/❌] <criterion> — Evidence: <actual output or observation>

## Issues Found (if any)
- <description of each failure, with enough detail for Generator to fix>
```