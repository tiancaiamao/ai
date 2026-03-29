# Code Quality Review Template

Review code quality after spec compliance is confirmed.

## Files to Review

{{FILES_TO_REVIEW}}

## Review Dimensions

### Correctness
- [ ] Logic is sound
- [ ] Edge cases handled
- [ ] No obvious bugs
- [ ] Error handling appropriate

### Maintainability
- [ ] Clear naming
- [ ] Well-structured code
- [ ] No duplication
- [ ] Comments where needed

### Security
- [ ] No security vulnerabilities
- [ ] Input validation
- [ ] Proper escaping
- [ ] No secrets exposed

### Performance
- [ ] No obvious performance issues
- [ ] Appropriate algorithms
- [ ] Efficient data structures

### Testing
- [ ] Adequate test coverage
- [ ] Tests are meaningful
- [ ] Edge cases covered

## Output Format

```
=== CODE QUALITY REVIEW ===
VERDICT: [APPROVED | REQUEST_CHANGES]

CRITICAL ISSUES: [must fix before proceeding]
- [issue description and fix]

IMPORTANT ISSUES: [should fix]
- [issue description and suggestion]

MINOR ISSUES: [optional to fix]
- [nice-to-have improvements]

STRENGTHS:
- [what's done well]
===
```

## Decision Rules

- **APPROVED**: No critical or important issues
- **REQUEST_CHANGES**: Has unresolved critical/important issues
  - Must fix before proceeding
  - Re-review after fixes
