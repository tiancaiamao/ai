# pkg/agentconfig

Loads agent YAML configuration (`agent.yaml`) — system prompt, memory, middleware list, and tool filtering — and resolves it into runtime structures.

## Usage

```go
cfg, err := agentconfig.Load("agent.yaml")
if err != nil {
    log.Fatal(err)
}

// Resolve system prompt (supports @file references)
prompt, err := cfg.ResolveSystemPrompt()

// Get enabled tools whitelist (nil = all tools enabled)
tools := cfg.GetEnabledTools()

// Build agent hooks from middleware config
hooks := cfg.BuildHooks()
```

## agent.yaml Format

```yaml
version: 1
system_prompt: "@prompts/coder.md"
memory: "project context and conventions"
middlewares:
  - name: "example"
    enabled: true
    params:
      key: "value"
tools:
  - name: "bash"
    enabled: true
  - name: "edit"
    enabled: false
```

## Key Types

| Type | Description |
|------|-------------|
| `AgentConfig` | Parsed agent.yaml configuration |
| `ToolEntry` | Single tool reference with enable flag and params |
| `MiddlewareEntry` | Single middleware reference with enable flag and params |

## Key Files

| File | Description |
|------|-------------|
| `config.go` | `AgentConfig` struct, `Load()`, `ResolveSystemPrompt()`, `GetEnabledTools()` |
| `hooks.go` | `BuildHooks()` — creates `agent.HookRegistry` from middleware config |