---
id: security
name: Security Audit
description: Security review and remediation
phases: [scan, analyze, remediate, verify, document]
complexity: medium
estimated_tasks: 3-5
---

# Security Audit Workflow

## Overview

Security workflow for vulnerability assessment and remediation.

## Core Principle

> **When in doubt, report it. False positives are cheaper than missed vulnerabilities.**

## Phase 1: Scan

### Goals
- Identify potential vulnerabilities
- Cover common attack vectors
- Document findings

### Actions

1. Automated scanning
   ```bash
   # Dependency audit
   npm audit / go mod verify
   
   # Static analysis
   semgrep --config=security ...
   
   # Secret scanning
   git-secrets --scan
   
   # OWASP checks
   ```
   
2. Manual review
   - Authentication/authorization
   - Input validation
   - Data handling
   - Error handling
   - Logging

### Output

Create `scan-results.md`:

```markdown
# Security Scan: [Target]

## Automated Findings

| Severity | Type | Location | Status |
|----------|------|----------|--------|
| Critical | [type] | [file:line] | TODO |
| High | [type] | [file:line] | TODO |

## Manual Review Findings
...

## False Positives (Confirmed)
- [finding] → reason
```

### Severity Levels
- **Critical**: RCE, SQL injection, auth bypass
- **High**: XSS, CSRF, sensitive data exposure
- **Medium**: Information disclosure, DoS
- **Low**: Best practice violations

---

## Phase 2: Analyze

### Goals
- Prioritize findings
- Assess exploitability
- Plan remediation

### Actions

1. For each finding:
   - Is it exploitable?
   - What's the impact?
   - How hard to exploit?

2. Risk assessment
   - Likelihood × Impact
   - Prioritize accordingly

### Output

Update `scan-results.md`:

```markdown
## Risk Assessment

| Finding | Risk Score | Priority | Remediation |
|---------|-----------|----------|-------------|
| [finding] | High (9.0) | P1 | [fix] |

## Exploitability Analysis
[For critical/high findings]
```

---

## Phase 3: Remediate

### Goals
- Fix vulnerabilities
- Follow secure coding practices
- Add tests

### Actions

1. Fix in priority order
2. Follow secure patterns
3. Add regression tests

### Secure Coding Patterns

**SQL Injection:**
```go
// ❌ Bad
query := "SELECT * FROM users WHERE id = " + id

// ✅ Good
query := "SELECT * FROM users WHERE id = ?"
row := db.QueryRow(query, id)
```

**XSS:**
```go
// ❌ Bad
html := "<div>" + userInput + "</div>"

// ✅ Good
html := template.HTMLEscapeString(userInput)
```

**Authentication:**
```go
// ❌ Bad
if user.IsAdmin { ... }

// ✅ Good
if authz.HasPermission(ctx, user, "admin:read") { ... }
```

---

## Phase 4: Verify

### Goals
- Confirm fixes work
- Re-scan to ensure no new issues
- Verify no bypass

### Actions

1. Re-run automated scans
2. Manual verification
3. Penetration testing if critical

---

## Phase 5: Document

### Goals
- Create security report
- Update security documentation
- Plan ongoing monitoring

### Output

Create `SECURITY.md`:

```markdown
# Security Report: [Date]

## Summary
[Executive summary]

## Vulnerabilities Found
| ID | Severity | Type | Status |
|----|----------|------|--------|
| SEC-001 | Critical | SQL Injection | Fixed |
| SEC-002 | High | XSS | Fixed |

## Remediation
[What was fixed]

## Verification
[How we verified fixes]

## Recommendations
1. [recommendation 1]
2. [recommendation 2]

## Next Audit
[Scheduled date]
```