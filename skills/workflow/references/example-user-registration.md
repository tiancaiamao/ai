# Reference: User Registration Feature Example

This is a worked example of how a feature workflow might look for a
"user registration" feature. **This is not a template** — it is a concrete
illustration for reference only.

## Phase 1: Spec — Sample Conversation

```
User: I want to add user registration.

Agent: Got it. Let me clarify a few things:
- Email-based registration?
- Do you need email verification?
- Any password requirements?

User: Email registration with email activation. Password must be 8+ chars, mixed case.

Agent: What fields are required at registration time?
- Email, password, confirm password — anything else?

User: Just those three.

Agent: Here's my understanding:

**Requirements:**
- Registration endpoint: POST /api/users/register
- Fields: email, password, password_confirm
- Password rules: min 8 chars, at least one uppercase, one lowercase
- Send verification email after registration
- Email contains activation link
- Clicking link activates the account

**Current system (from codebase exploration):**
- Existing model: User{Email, PasswordHash, Active}
- Existing table: users(email, password_hash, active)
- Existing endpoints: GET /api/users, PUT /api/users/:id
- Missing: registration endpoint, email service, verification tokens

Shall I write the SPEC?

User: Yes, go ahead.
```

## Phase 2: Plan — Sample PLAN.yml

```yaml
version: "1.0"
metadata:
  spec_file: "SPEC.md"
  created_at: "2024-04-13T10:00:00Z"

tasks:
  - id: "T001"
    title: "Add password validation function"
    description: "Implement password strength validation (min 8 chars, mixed case)"
    priority: high
    estimated_hours: 1
    dependencies: []
    file: "pkg/auth/validation.go"

  - id: "T002"
    title: "Create verification token model and store"
    description: "Token struct, DB table, generate/validate functions"
    priority: high
    estimated_hours: 2
    dependencies: []
    file: "pkg/auth/token.go"

  - id: "T003"
    title: "Update database schema"
    description: "Add verification_tokens table migration"
    priority: high
    estimated_hours: 1
    dependencies: ["T002"]
    file: "migrations/"

  - id: "T004"
    title: "Implement SMTP email client"
    description: "Email sender using standard library SMTP"
    priority: medium
    estimated_hours: 2
    dependencies: []
    file: "pkg/email/smtp.go"

  - id: "T005"
    title: "Create email templates"
    description: "Verification email HTML template with activation link"
    priority: medium
    estimated_hours: 1
    dependencies: ["T004"]
    file: "pkg/email/templates/"

  - id: "T006"
    title: "Implement registration API endpoint"
    description: "POST /api/users/register with validation and email dispatch"
    priority: high
    estimated_hours: 3
    dependencies: ["T001", "T003", "T005"]
    file: "pkg/api/users.go"

  - id: "T007"
    title: "Implement activation API endpoint"
    description: "GET /api/users/activate?token=xxx to verify and activate account"
    priority: high
    estimated_hours: 2
    dependencies: ["T003"]
    file: "pkg/api/users.go"

  - id: "T008"
    title: "Unit tests for auth utilities"
    description: "Test password validation and token generation"
    priority: medium
    estimated_hours: 1
    dependencies: ["T001", "T002"]
    file: "pkg/auth/validation_test.go"

  - id: "T009"
    title: "Integration tests for registration flow"
    description: "End-to-end: register → receive email → activate"
    priority: medium
    estimated_hours: 2
    dependencies: ["T006", "T007"]
    file: "pkg/api/users_test.go"

groups:
  - name: "infrastructure"
    title: "Infrastructure"
    description: "Core utilities: validation, tokens, DB schema"
    tasks: ["T001", "T002", "T003"]
    commit_message: "feat(auth): add password validation and verification tokens"

  - name: "email"
    title: "Email Service"
    description: "SMTP client and templates"
    tasks: ["T004", "T005"]
    commit_message: "feat(email): add SMTP client and verification template"

  - name: "api"
    title: "Registration and Activation APIs"
    description: "Registration and activation endpoints"
    tasks: ["T006", "T007"]
    commit_message: "feat(api): add user registration and activation endpoints"

  - name: "testing"
    title: "Tests"
    description: "Unit and integration tests"
    tasks: ["T008", "T009"]
    commit_message: "test(auth): add registration and validation tests"

group_order: ["infrastructure", "email", "api", "testing"]

risks:
  - area: "Email Service"
    risk: "SMTP configuration may be complex in different environments"
    mitigation: "Use standard library SMTP with configurable host/port from env vars"
  - area: "Token Security"
    risk: "Predictable tokens could allow unauthorized activation"
    mitigation: "Use crypto/rand for token generation with minimum 32 bytes"
  - area: "Race Conditions"
    risk: "Concurrent registration with same email"
    mitigation: "Database unique constraint on email column"
```

## Phase 3: Implement — Sample Group Execution

For the "infrastructure" group (T001, T002, T003):
- T001 and T002 have no dependencies → run in parallel
- T003 depends on T002 → run after T002 completes
- Review via pair.sh (max 3 rounds)
- Commit: `feat(auth): add password validation and verification tokens`

For the "api" group (T006, T007):
- T006 depends on infrastructure + email → wait for those groups
- T007 depends on T003 → wait for infrastructure
- T006 and T007 can run in parallel (different logical units in same file —
  decide based on actual conflict risk)
- Review via pair.sh
- Commit: `feat(api): add user registration and activation endpoints`