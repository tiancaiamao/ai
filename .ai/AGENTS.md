# Agent Behavior Guidelines

This file provides core behavioral guidance for the AI agent working on this codebase.

## Project Identity

`ai` is a Go-based RPC-first Agent Core for editor integration via stdin/stdout JSON-RPC protocol.

**Key Characteristics:**
- **Language:** Go 1.24.0
- **API:** ZAI API (OpenAI-compatible)
- **Architecture:** Event-driven streaming with concurrent tool execution
- **Session Storage:** JSONL format isolated by working directory

## Core Behavioral Rules

1. **Prefer existing patterns** - Check `pkg/rpc/types.go` for shared types before creating new ones
2. **No Makefile** - Build directly with `go build`
3. **Session isolation** - Each working directory has separate sessions
4. **Context cancellation** - Use `context.Background()` for new operations, not agent context
5. **Concurrent tools** - Tools execute in parallel (default max 3)

## Quick Reference

### Build Commands
```bash
go build -o bin/ai ./cmd/ai && ./bin/ai --mode rpc
```

### Test Commands
```bash
go test ./pkg/agent -v        # Test specific package
go test ./... -cover          # All tests with coverage
```

### Key Locations
- **RPC handlers:** `cmd/ai/rpc_handlers.go` (920 lines)
- **Agent loop:** `pkg/agent/loop.go`
- **Shared types:** `pkg/rpc/types.go`
- **Session format:** `~/.ai/sessions/--<cwd>--/*.jsonl`

## When Context is Insufficient

For detailed information on specific topics:
- **Tools:** See `TOOLS.md` for built-in tools and usage
- **Architecture:** See `ARCHITECTURE.md` for package structure
- **Commands:** See `COMMANDS.md` for RPC command reference
- **Configuration:** See `CONFIG.md` for config options

Use the `read` tool to load these files when needed.
