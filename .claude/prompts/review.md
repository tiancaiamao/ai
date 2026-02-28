# Code Review Prompt

Use this prompt template when reviewing code changes.

## Instructions

Review the provided code changes and provide feedback in the following structure:

### Summary
Brief overview of what the changes do.

### Good 👍
Things done well:
- ...
- ...

### Bad 👎
Issues that should be fixed:
- ...
- ...

### Ugly 🤔
Questions or concerns:
- ...
- ...

## Review Checklist

### Correctness
- [ ] Logic is correct
- [ ] Edge cases handled
- [ ] Error handling is appropriate
- [ ] No off-by-one errors

### Code Quality
- [ ] Follows project conventions
- [ ] Functions are focused and small
- [ ] Meaningful variable names
- [ ] No dead code

### Testing
- [ ] Tests cover new functionality
- [ ] Tests cover edge cases
- [ ] Existing tests still pass

### Performance
- [ ] No obvious performance issues
- [ ] Appropriate data structures used
- [ ] No unnecessary allocations

### Security
- [ ] Input validation
- [ ] No injection vulnerabilities
- [ ] Sensitive data handled properly

## Example Usage

```
User: review the changes in this PR
→ Load this prompt and apply to the diff
```