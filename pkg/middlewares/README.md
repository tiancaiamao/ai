# pkg/middlewares

Middleware registry for agent hooks. Provides a plugin system for pre/post-processing of model calls, tool executions, and agent lifecycle events.

## Architecture

Middlewares are registered globally via `Register()`. Each `MiddlewareSpec` can provide up to three hook factories:

- **BeforeModelHook** — runs before each LLM API call
- **AfterToolHook** — runs after each tool execution
- **AfterAgentHook** — runs after the agent completes

Middlewares are configured in `agent.yaml` and resolved by `pkg/agentconfig`.

## Built-in Middlewares

| Name | Hook Type | Description |
|------|-----------|-------------|
| `destructive_guard` | AfterTool | Detects destructive shell commands (rm -rf, kill -9, etc.) in bash output and appends warnings |

## Usage

```go
// Register a custom middleware
middlewares.Register(middlewares.MiddlewareSpec{
    Name: "my-middleware",
    AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
        return myHook, nil
    },
})

// Build hooks from config entries
hooks, err := middlewares.BuildHooks([]middlewares.MiddlewareEntry{
    {Name: "destructive_guard", Params: map[string]any{}},
})
```

## Key Types

| Type | Description |
|------|-------------|
| `MiddlewareSpec` | Describes a registered middleware (name + hook factories) |
| `MiddlewareEntry` | Config-level middleware reference (name + params) |
| `BeforeModelFactory` | Creates `agent.BeforeModelHook` from params |
| `AfterToolFactory` | Creates `agent.AfterToolHook` from params |
| `AfterAgentFactory` | Creates `agent.AfterAgentHook` from params |

## Key Files

| File | Description |
|------|-------------|
| `registry.go` | Global registry, `Register()`, `Lookup()`, `BuildHooks()` |
| `destructive_guard.go` | Built-in destructive command detection middleware |