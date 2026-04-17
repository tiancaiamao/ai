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

**Gate:** Audit complete with findings documented.

**Output:** `audit.md` in artifact directory

### Phase 2: Plan

**Skill:** `plan`

Create remediation plan ordered by severity:
- Critical and High first
- Each fix should be independently deployable

**Gate:** Plan approved.

**Output:** `PLAN.yml` + `PLAN.md` in artifact directory

### Phase 3: Implement

**Skill:** `implement`

Fix vulnerabilities. Use full two-stage review — security fixes
need extra scrutiny, not less.

**Output:** Git commits

### Phase 4: Verify

**Skill:** `explore`

Verify each fix:
1. Original vulnerability no longer exploitable
2. No new vulnerabilities introduced by the fix
3. Tests cover the vulnerability scenario

**Gate:** All findings verified as remediated.

**Output:** `verification-report.md` in artifact directory

## Security-Specific Rules

- **Every finding is real until proven otherwise** — don't dismiss as unlikely
- **Fix by severity** — critical and high first
- **Test the vulnerability** — write a test that would have caught it
- **No secrets in code** — scan for hardcoded credentials/keys
- **Document decisions** — if you accept a risk, document why

## Commit Convention

```
fix(security): [vulnerability description]
```