# AGENTS.md (via CLAUDE.md)

Concise project guidance for coding agents.

## Project

- `ai` — Go-based AI coding agent (`go1.24`, module `github.com/tiancaiamao/ai`)
- Interface: stdin/stdout JSON-RPC server, OpenAI-compatible API (`ZAI` provider)

## Language Rules

| Context | Language |
|---------|----------|
| Code, comments, commit messages, docs under `docs/` | English only |
| Chat / explanations / code reviews | Chinese (中文) |

### ⛔ NEVER commit to `main`

Always create a feature branch, push, and open a PR. No exceptions.

## Commands

```bash
make fmt              # format — must pass before commit
make test             # all tests (with -timeout 30s -coverprofile)
make test-ci          # CI subset (excludes slow stress/integration tests)
go install ./cmd/ai   # build & install binary
go test ./pkg/<pkg>/...  # focused tests for a single package
```

## Key Packages

Core packages (see `pkg/*/README.md` for details, `pkg/` for full list):

| Package | Role |
|---------|------|
| `cmd/ai/` | Entry point. `main.go` parses flags and calls `app.RunRPC()` |
| `pkg/app/` | RPC application — all handlers, setup, session writer (formerly `cmd/ai/rpc_*.go`) |
| `pkg/agent/` | Agent loop, tool execution, hooks, streaming, checkpoint recovery |
| `pkg/context/` | `AgentContext`, messages, `Tool` interface, `Compactor` interface |
| `pkg/compact/` | LLM-driven compaction (LLMDecide mode) |
| `pkg/session/` | JSONL session persistence, lazy loading, compaction snapshots |
| `pkg/tools/` | Built-in tools (`read`, `write`, `edit`, `bash`, `grep`, `change_workspace`, `find_skill`). Registered in `pkg/app/rpc_setup.go` |
| `pkg/skill/` | Skill loading, parsing, formatting, progressive disclosure ranking |
| `pkg/rpc/` | RPC types (`types.go`) |
| `pkg/prompt/` | System prompt builder |
| `pkg/config/` | Config, auth, model specs |
| `pkg/agentconfig/` | `agent.yaml` loading (system prompt, memory, middleware, tool filtering) |
| `pkg/middlewares/` | Hook-based guards (e.g. destructive command detection) |

## Documentation Maintenance

**Before any commit**, check if docs need updating:

1. Modified a package? → check its `pkg/*/README.md`
2. Changed architecture/RPC/session/compaction? → update `docs/*.md` (see `docs/README.md` index)
3. Added/removed/renamed a package? → update root `README.md` + `docs/architecture.md`
4. **Architecture-level change** (new feature, removed feature, design pivot)? → add `CHANGELOG.md` entry explaining **what changed and why**, not just what the commit did. Group by topic, not by date. See existing entries for style.

**Verify every file path, type name, function name in live docs against actual code.**

`docs/archive/` = historical, not maintained. `docs/adr/` = immutable.

## Guardrails

- Reuse `pkg/rpc/types.go` structs — don't duplicate types.
- Keep compatible with `pkg/session/` persistence format.

## Runtime Notes

- Sessions: `~/.ai/sessions/--<cwd>--/`
- Skills: `~/.ai/skills/` + `.ai/skills/`
- Traces: `~/.ai/traces/` (Perfetto-compatible, see `pkg/traceevent/config.go`)