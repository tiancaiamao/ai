---
id: security
name: Security Audit
description: Security review and remediation
phases: [assess, plan, implement, verify]
complexity: medium
estimated_tasks: 3-5
skills:
  assess: explore
  plan: plan
  implement: implement
  verify: explore
---

# Security Audit Workflow

## Overview

Systematic security review with remediation. Treats every finding seriously
until proven safe.

## Phase Sequence

```
assess (explore) → plan → implement → verify (explore)
```

### Phase 1: Assess

**Skill:** `explore`

Security scan:
1. Identify attack surface (inputs, APIs, auth boundaries)
2. Check for common vulnerabilities (OWASP Top 10)
3. Review authn/authz implementation
4. Check data handling (PII, credentials, secrets)
5. Review dependencies for known vulnerabilities

**Output:** `.workflow/artifacts/security/[name]/audit.md`

```markdown
# Security Audit: [scope]

## Findings
| ID | Severity | Category | Description | Location |
|----|----------|----------|-------------|----------|
| S01 | Critical | Injection | SQL injection in search | api/search.go:45 |
| S02 | High | Auth | Missing auth check | api/admin.go:23 |

## Attack Surface
[input points, APIs, auth boundaries]

## Recommendations
[prioritized fix list]
```

### Phase 2: Plan

**Skill:** `plan`

Create remediation plan ordered by severity:
- Critical and High first
- Each fix should be independently deployable

### Phase 3: Implement

**Skill:** `implement`

Fix vulnerabilities. Use full two-stage review — security fixes
need extra scrutiny, not less.

### Phase 4: Verify

**Skill:** `explore`

Verify each fix:
1. Original vulnerability no longer exploitable
2. No new vulnerabilities introduced by the fix
3. Tests cover the vulnerability scenario

## Security-Specific Rules

- **Every finding is real until proven otherwise** — don't dismiss as unlikely
- **Fix by severity** — critical and high first
- **Test the vulnerability** — write a test that would have caught it
- **No secrets in code** — scan for hardcoded credentials/keys
- **Document decisions** — if you accept a risk, document why