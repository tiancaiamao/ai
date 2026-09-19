# Simplification Notes

Rejected or deferred simplification candidates from [reclaim-entropy](../.agents/skills/reclaim-entropy/SKILL.md)
audits. Purpose: don't re-analyze the same candidate twice, and don't lose the
evidence for why a tempting cut was not made.

Append-only. Never delete an entry — mark it `done` (with PR link) once the
cut is implemented. Revisit a `deferred` entry only when the recorded
revisit condition is met.

## Entry format

```markdown
## <candidate name> — <status>

- **Status**: rejected | deferred | done (PR #NNN)
- **Date**: yyyy-mm-dd
- **Surface**: files / symbols / config keys
- **Evidence**: why it was rejected or deferred (consumers found, ADR backing,
  dynamic reachability, compatibility obligation)
- **Revisit when**: what new evidence would make this worth re-examining
```

## Entries

## auth no-proxy wrappers + llm parseProxyURL — deferred

- **Status**: deferred
- **Date**: 2026-09-19
- **Surface**: `pkg/auth/codex_oauth.go` (`RefreshCodexToken`, `LoadCodexCredentials` — the no-proxy variants shadowed by `*WithProxy`), `pkg/llm/openai_responses.go` (`parseProxyURL`, self-documented as "retained for compatibility with proxy configuration tests")
- **Evidence**: zero production consumers found (deadcode + full-repo grep); `pkg/auth/netutil.go` has its own live `parseProxyURL`. Deferred because they cross into auth territory (trust-boundary caution in this skill) and the llm one is test-only — should follow the "test-only API" rule (move to `_test.go`), but was outside the requested batch.
- **Revisit when**: next entropy pass on `pkg/auth`/`pkg/llm`, or when the proxy tests are reworked.