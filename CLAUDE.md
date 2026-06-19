# AGENTS.md (via CLAUDE.md)

Concise project guidance for coding agents working in this repository.

## Project

- Name: `ai` — Go-based AI coding agent (`go1.24`)
- Interface: stdin/stdout JSON-RPC server
- Model API: OpenAI-compatible (`ZAI` provider)

## Language Rules

| Context | Language |
|---------|----------|
| Code, comments, commit messages, docs under `docs/` | English only |
| Chat / explanations / code reviews | Chinese (中文) |

## Commands

```bash
make fmt              # format (must be clean before commit)
go install ./cmd/ai   # build & install
ai rpc                # run RPC mode (stdin/stdout JSON-RPC)
go test ./pkg/agent -v  # focused tests
```

## Key Packages

| Package | Role |
|---------|------|
| `cmd/ai/` | RPC server, CLI entry, handler wiring (`rpc_app.go` is the hub) |
| `pkg/agent/` | Agent loop, tool execution, hooks, streaming, checkpoint |
| `pkg/context/` | `AgentContext`, messages, `Compactor` interface |
| `pkg/compact/` | LLM-driven compaction (LLMDecide mode) |
| `pkg/session/` | JSONL session persistence, lazy loading |
| `pkg/rpc/` | RPC types (`types.go`) |
| `pkg/config/` | Config, auth, model specs |
| `pkg/agentconfig/` | `agent.yaml` loading (system prompt, memory, middleware) |
| `pkg/middlewares/` | Hook-based guards (e.g. destructive command detection) |

See `pkg/*/README.md` for package details.

## Documentation Maintenance

**Before any commit**, check if docs need updating:

1. Modified a package? → check its `pkg/*/README.md`
2. Changed architecture/RPC/session/compaction? → update `docs/*.md` (see `docs/README.md` index)
3. Added/removed/renamed a package? → update root `README.md` + `docs/architecture.md`
4. **Functional code change** (not docs-only)? → add `CHANGELOG.md` entry under today's date with commit hash, grouped by Added/Changed/Fixed/Removed

**Verify every file path, type name, function name in live docs against actual code.**

`docs/archive/` = historical, not maintained. `docs/adr/` = immutable.

## Guardrails

- Reuse `pkg/rpc/types.go` structs — don't duplicate types.
- Respect context cancellation through loop/tool paths.
- Keep compatible with `pkg/session/` persistence format.
- Minimal, targeted changes; no broad refactors unless asked.

### ⛔ NEVER commit to `main`

Always create a feature branch, worktree, push, and open a PR. No exceptions (features, fixes, docs, typos).

## Runtime Notes

- Sessions: `~/.ai/sessions/--<cwd>--/`
- Skills: `~/.ai/skills/` + `.ai/skills/`
- Traces: `~/.ai/traces/` (Perfetto-compatible, see `pkg/traceevent/config.go`)

## Testing

- Run focused package tests for touched code first.
- Broader tests when changes affect `pkg/agent`, `pkg/session`, `pkg/rpc`, `pkg/prompt`.
- Stress/integration tests can be slow; still run them for loop/session changes.
