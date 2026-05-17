# pkg/config

Application configuration: model selection, compaction, concurrency, and tool output settings.

## Overview

Loads configuration from `~/.ai/config.json` (or `AI_CONFIG_PATH`). Configuration is structured into sections for model, compaction, concurrency, tool output, and logging.

## Config

```go
type Config struct {
    Model        ModelConfig        `json:"model"`
    Compactor    *compact.Config    `json:"compactor,omitempty"`
    Concurrency  *ConcurrencyConfig `json:"concurrency,omitempty"`
    ToolOutput   *ToolOutputConfig  `json:"toolOutput,omitempty"`
    Edit         *EditConfig        `json:"edit,omitempty"`
    Log          *LogConfig         `json:"log,omitempty"`
    Workspace    string             `json:"workspace,omitempty"`
}
```

## Model Configuration

```go
type ModelConfig struct {
    Default string `json:"default"` // Model ID (e.g., "claude-sonnet-4-20250514")
    APIKey  string `json:"apiKey"`  // API key or env var reference (${ENV_VAR})
}
```

### Model Spec Format

The `Default` field supports extended notation:

```
provider/model-id    → Model{Provider: "provider", ID: "model-id"}
model-id             → Model{Provider: "", ID: "model-id"}
```

## Compaction Configuration

Passed through to `pkg/compact.Config`. See `pkg/compact/README.md` for details.

## Concurrency

```go
type ConcurrencyConfig struct {
    MaxConcurrentTools int  // Max parallel tool executions
    ToolTimeout        int  // Per-tool timeout in seconds
    QueueTimeout       int  // Wait timeout for executor queue slot
}
```

## Tool Output

```go
type ToolOutputConfig struct {
    MaxChars  int  `json:"maxChars,omitempty"`  // Max chars (0 = 10000 default)
    HashLines bool `json:"hashLines,omitempty"` // Enable hashline verification
}
```

## Edit

```go
type EditConfig struct {
    Mode string `json:"mode,omitempty"` // "replace" (default) or "hashline"
}
```

## API Key Resolution

API keys are resolved in order:
1. Config file value (supports `${ENV_VAR}` references)
2. Environment variable (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.)
3. Auth file (`~/.ai/auth.json`)

## Key Files

| File | Description |
|------|-------------|
| `config.go` | Config struct, loading, model resolution, defaults |
| `auth.go` | API key resolution from environment and auth files |

## Dependencies

- `pkg/compact` — Compaction configuration types
- `pkg/llm` — Model type
- `pkg/agent` — Agent configuration
- `pkg/logger` — Logger initialization