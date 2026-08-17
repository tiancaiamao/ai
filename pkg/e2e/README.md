# pkg/e2e

Black-box end-to-end tests for the `ai` agent, driven over the real RPC
boundary. Each test spawns the actual `ai rpc` binary (built from `cmd/ai`)
as a subprocess and talks to it over stdin/stdout JSON-RPC — the same
interface an editor or `ai serve` client uses.

Unlike `pkg/testutil` (mock LLM server, in-process harness), these tests
exercise the **whole application** against a **real OpenAI-compatible model
endpoint**: agent loop → streaming LLM client → tool execution → multi-turn
state → session persistence. They are the only tests that catch protocol
drift between the code and an actual model server.

## Running

Opt-in via the `e2e` build tag — **not** part of `make test` / CI, because
they require a reachable endpoint and a live model:

```bash
make e2e                                  # all tests, 20m timeout
# or, focused:
go test -tags e2e ./pkg/e2e/ -run TestE2E_SessionLifecycle -v
```

By default the suite connects to the first `ollama/*` model from
`~/.ai/models.json` (prefers `laguna`). Endpoint/model/key can be overridden:

| Env | Purpose |
|-----|---------|
| `E2E_BASE_URL` | Endpoint base URL (e.g. `http://localhost:11434`) |
| `E2E_MODEL` | Model ID (e.g. `ollama/laguna`) |
| `E2E_API_KEY` | API key for the endpoint |

Tests **skip** when no endpoint is reachable or no model is configured,
so a machine without a running model simply reports `SKIP`.

## Isolation

Every test gets a fresh, isolated HOME (sessions, skills, auth, run state)
and an isolated working directory, so runs are side-effect free and
deterministic. Stray state from a previous run cannot leak in.

## Coverage

The binary is built with `-cover` in `TestMain`, so every spawned `ai rpc`
subprocess records coverage of the **whole application** to `GOCOVERDIR`
(`-coverpkg=./...`). At the end of the run the profiles are merged
(`go tool covdata`) and the total is printed, e.g.:

```
=== E2E coverage (whole app via `ai rpc` subprocess) ===
total: (statements) 50.2%
```

This covers `pkg/rpc`, `pkg/session`, `pkg/skill`, `cmd/ai`, etc. through
the same entry point a user invokes — something agent-level tests with a
mock server cannot do.

## Tests

| Test | What It Verifies |
|------|------------------|
| `TestLRRepro_SameBatchEvents` | Fast, deterministic log-replay regression repro (no model needed) |
| `TestE2E_StreamingCompletion` | Basic prompt → SSE streaming → assistant reply |
| `TestE2E_MultiTurnContext` | Conversation state survives across prompts on one server |
| `TestE2E_ToolExecution` | Real tool call → subprocess executes `read` → result fed back |
| `TestE2E_RPCCommands` | Slash commands (`model`/`session`/`help`/`context`) |
| `TestE2E_SessionLifecycle` | `/new`, `resume` list/index/errors, tree, fork, rewind |
| `TestE2E_BusyAndAbort` | Streaming-time policies (`reject`/`cancel`/`submit`), abort |
| `TestE2E_TimeoutWatchdog` | Stall watchdog terminates the agent |
| `TestE2E_FlagsAndRoles` | CLI flags (`-max-turns`/`-session`) and `--role` wiring |
| `TestE2E_Subcommands` | `ai serve` / `ls` / `send` / `kill` lifecycle + dead-run reconcile |
| `TestE2E_Compaction` | Manual compaction of a tool-call-heavy + large context (compact_tools digest) |
| `TestE2E_ToolVariety` | bash / write / grep / edit / change_workspace on one server |
| `TestE2E_DestructiveGuard` | `--role guard` destructive-command middleware reacts to `rm -rf` |
| `TestE2E_Skills` | Skill discovery from `~/.ai/skills` via `find_skill` |
| `TestE2E_CompactionAtScale` | Large prompts cross the LLMDecide soft threshold repeatedly |ionAtScale` | Large prompts cross the LLMDecide soft threshold repeatedly |