# Spec Template

写入 `.pge/spec.md`：

```markdown
# Spec: <title>

## Goal
<one sentence>

## Acceptance Criteria

### L1 — Structural (must pass before L2)
- [ ] <criterion> — Verify: `<executable command>`

### L2 — Behavioral (validates correctness, not just existence)
- [ ] <criterion> — Verify: `<test command or manual check>`

## Constraints
- <technical constraints>

## Phases
1. <phase-1 name> — <简述>
2. <phase-2 name> — <简述>

## Out of Scope
- <explicitly excluded>
```

## L1 vs L2

- **L1 (Structural)**: 构建/编译通过、文件存在、签名正确
- **L2 (Behavioral)**: 单元测试通过、行为正确

**Rule: Unverifiable criterion = 不合格 criterion.** 如果写不出验证命令，说明 criterion 太模糊。