# AGENTS.md (via CLAUDE.md)

Concise project guidance for coding agents working in this repository.

## Project

- Name: `ai`
- Language: Go (`go1.24`)
- Interface: stdin/stdout JSON-RPC server
- Model API: OpenAI-compatible (`ZAI` provider in config)

## Language Rules

IMPORTANT: Different rules apply to different contexts!

### Code (MUST be English)
- All **code** must be in English (variable names, function names, types, etc.)
- All **code comments** must be in English
- All **commit messages** must be in English
- All **documentation files** under `docs/` or `**/docs/` must be in English

### Communication (Chinese OK)
- **Chat/conversation** with the user: Use Chinese (中文)
- **Explanations** to the user: Use Chinese
- **Code reviews**: Use Chinese

### Summary
| Context | Language |
|---------|----------|
| Writing code | English only |
| Code comments | English only |
| Commit messages | English only |
| Documentation files | English only |
| Chatting with user | Chinese (中文) |
| Explaining things | Chinese (中文) |

## Most Used Commands

```bash
# install (recommended - ensures fresh binary)
go install ./cmd/ai

# run rpc mode
ai --mode rpc

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

## Debugging Guidance

### Trace File Analysis

The agent writes Perfetto-compatible trace files to `~/.ai/traces/`. These are invaluable for debugging runtime behavior:

```bash
# List recent traces
ls -lt ~/.ai/traces/ | head -5

# Check if specific event appears in traces
grep -c 'tool_output_truncated' ~/.ai/traces/<latest>.json

# Extract event details with Python
python3 -c "
import json
with open('/Users/genius/.ai/traces/<file>.json') as f:
    data = json.load(f)
for e in data['traceEvents']:
    if e.get('name') == 'tool_output_truncated':
        print(json.dumps(e, indent=2))
"
```

### Event Registration Debug

If a trace event isn't appearing:

1. Check if event is registered in `pkg/traceevent/config.go`:
   - Must be in `eventNameToBit` map
   - Should be in `defaultEnabledEvents` for default visibility
   - Or add to a selector group (e.g., `tool`, `llm`)

2. Verify `IsEventEnabled()` returns true by checking the bit flag

### Interactive Testing Pattern

For debugging event emission or runtime behavior:

1. Make code changes (e.g., add new trace event)
2. Rebuild: `go install ./cmd/ai`
3. Restart agent to pick up changes
4. Trigger the behavior (e.g., run a tool with large output)
5. Check trace file for expected events
6. Iterate

### Trace Event Categories

- `tool`: Tool execution, truncation, normalization
- `llm`: API calls, streaming, retries
- `event`: Agent lifecycle, turns, messages
- `log`: slog bridge output (info/warn/error)
