# Spec Compliance Review Template

Verify that an implementation matches the original specification exactly.

## Original Specification

{{SPECIFICATION}}

## Implementation to Review

Files: {{FILES_TO_REVIEW}}

## Review Checklist

- [ ] All requirements from spec implemented?
- [ ] File paths match spec?
- [ ] Function signatures match spec?
- [ ] Data structures match spec?
- [ ] Behavior matches expected?
- [ ] Nothing extra added (scope creep)?
- [ ] Nothing missing (incomplete)?

## Output Format

```
=== SPEC COMPLIANCE REVIEW ===
VERDICT: [PASS | FAIL]

COMPLIANT ITEMS:
- [list what matches]

NON-COMPLIANT ITEMS:
- [list what doesn't match, with specifics]

GAPS FOUND:
- [what's missing]

RECOMMENDATIONS:
- [how to fix gaps]
===
```

## Decision Rules

- **PASS**: All spec items implemented correctly
- **FAIL**: Any spec item missing or incorrect
  - Must fix before proceeding
  - Re-review after fixes
