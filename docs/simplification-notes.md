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

(none yet)