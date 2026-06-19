# pkg/config

Application configuration: model selection, compaction, concurrency, tool output, and logging.

## Overview

Loads configuration from `~/.ai/config.json` (or `AI_CONFIG_PATH`). Configuration is structured into sections for model, compaction, concurrency, tool output, and logging. Model specs are loaded separately from `~/.ai/models.json` (or `AI_MODELS_PATH`).

## Config

```go
type Config struct {
    Model         ModelConfig        `json:"model"`
    ThinkingLevel string             `json:"thinkingLevel,omitempty"` // off, minimal, low, medium, high, xhigh
    Compactor     *compact.Config    `json:"compactor,omitempty"`
    Concurrency   *ConcurrencyConfig `json:"concurrency,omitempty"`
    ToolOutput    *ToolOutputConfig  `json:"toolOutput,omitempty"`
    Log           *LogConfig         `json:"log,omitempty"`
}
```

## Model Configuration

```go
type ModelConfig struct {
    ID        string `json:"id"`        // Model ID (e.g., "glm-4.5-air")
    Provider  string `json:"provider"`  // Provider (e.g., "zai")
    BaseURL   string `json:"baseUrl"`   // API base URL
    API       string `json:"api"`       // API style: "openai-completions" or "anthropic-messages"
    MaxTokens int    `json:"maxTokens,omitempty"`
}
```

Environment overrides (take precedence over config file):
- `ZAI_MODEL` → `Model.ID`
- `ZAI_BASE_URL` → `Model.BaseURL`
- `ZAI_MAX_TOKENS` → `Model.MaxTokens`

## Model Specs

Model definitions are loaded from `~/.ai/models.json` via `ModelSpec`:

```go
type ModelSpec struct {
    ID            string
    Name          string
    Provider      string
    BaseURL       string
    API           string
    Reasoning     bool
    Input         []string
    ContextWindow int
    MaxTokens     int
}
```

## Compaction Configuration

Passed through to `pkg/compact.Config`. See `pkg/compact/README.md` for details.

## Concurrency

```go
type ConcurrencyConfig struct {
    MaxConcurrentTools int `json:"maxConcurrentTools"` // Max parallel tool executions
    ToolTimeout        int `json:"toolTimeout"`        // Per-tool timeout in seconds
    QueueTimeout       int `json:"queueTimeout"`       // Wait timeout for executor queue slot
}
```

## Tool Output

```go
type ToolOutputConfig struct {
    MaxChars int `json:"maxChars,omitempty"` // Max chars (0 = 10000 default)
}
```

## API Key Resolution

API keys are resolved by `ResolveAPIKey(provider)` in priority order (auth-first by default):

1. Auth file (`~/.ai/auth.json`) — provider entry with `key`, `apiKey`, or `token`
2. Environment variable (`<PROVIDER>_API_KEY`, e.g., `ZAI_API_KEY`)

Set `AI_API_KEY_SOURCE=env` to prefer environment over auth file.

## Key Files

| File | Description |
|------|-------------|
| `config.go` | `Config`, `ModelConfig`, `LogConfig` structs, loading, defaults, `ToLoopConfig` |
| `auth.go` | `AuthEntry`, `ResolveAPIKey`, auth file path resolution |
| `concurrency.go` | `ConcurrencyConfig`, `ResolveConcurrencyConfig` from environment |
| `models.go` | `ModelSpec`, `LoadModelSpecs` from `models.json` |

## Dependencies

- `pkg/compact` — Compaction configuration types
- `pkg/llm` — Model type
- `pkg/agent` — Agent configuration (`LoopConfig`)
- `pkg/logger` — Logger initialization
- `pkg/modelselect` — Model sorting