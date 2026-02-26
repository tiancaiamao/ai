# AGENTS.md (via CLAUDE.md)

Concise project guidance for coding agents working in this repository.

## Project

- Name: `ai`
- Language: Go (`go1.24`)
- Interface: stdin/stdout JSON-RPC server
- Model API: OpenAI-compatible (`ZAI` provider in config)

## Most Used Commands

```bash
# build
go build -o bin/ai ./cmd/ai

# run rpc mode
./bin/ai --mode rpc

# run all tests
go test ./... -v

# focused tests
go test ./pkg/agent -v
go test ./pkg/rpc -v
```

No `Makefile` is used in this repo.

## High-Value Code Paths

- RPC entrypoint: `cmd/ai/rpc_handlers.go`
- Agent loop: `pkg/agent/loop.go`
- Agent context/model wiring: `pkg/agent/agent.go`
- Shared RPC types: `pkg/rpc/types.go`
- Session storage/loading: `pkg/session/`
- Prompt building: `pkg/prompt/builder.go`
- Tool implementations: `pkg/tools/`

## Guardrails

- Reuse shared RPC structs in `pkg/rpc/types.go` instead of duplicating types.
- Respect context cancellation through loop/tool execution paths.
- Keep behavior compatible with session persistence format in `pkg/session/`.
- Prefer minimal, targeted changes; avoid broad refactors unless requested.

## Runtime/Storage Notes

- Sessions are isolated by working directory.
- Session files live under `~/.ai/sessions/--<cwd>--/`.
- Skills load from:
  - `~/.ai/skills/`
  - `.ai/skills/`

## Testing Guidance

- Run focused package tests for touched code first.
- Then run broader tests when changes affect shared paths (`pkg/agent`, `pkg/session`, `pkg/rpc`, `pkg/prompt`).
- Existing stress/integration tests can be slow; still run them when touching loop/session behavior.
