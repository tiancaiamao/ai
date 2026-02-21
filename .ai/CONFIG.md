# Configuration Reference

This file documents all configuration options for the `ai` agent.

## Configuration Hierarchy

Configuration is loaded in the following priority order (highest to lowest):

1. **Environment Variables** - Override everything
2. **Config File** (`~/.ai/config.json`)
3. **Auth File** (`~/.ai/auth.json`)
4. **Defaults** - Built-in default values

## Environment Variables

### API Configuration
```bash
# ZAI API (primary)
export ZAI_API_KEY="your-api-key"              # Required
export ZAI_BASE_URL="https://api.z.ai/api/coding/paas/v4"
export ZAI_MODEL="glm-4.5-air"

# Debug
export AI_LOG_STREAM_EVENTS="true"             # Enable verbose event logging
```

## Config File (`~/.ai/config.json`)

### Complete Example

```json
{
  "model": {
    "id": "glm-4.5-air",
    "provider": "zai",
    "baseUrl": "https://api.z.ai/api/coding/paas/v4",
    "api": "openai-completions"
  },
  "compactor": {
    "maxMessages": 50,
    "maxTokens": 8000,
    "keepRecent": 10,
    "keepRecentTokens": 2000,
    "reserveTokens": 1000,
    "autoCompact": true,
    "toolCallCutoff": 10,
    "toolSummaryStrategy": "llm"
  },
  "concurrency": {
    "maxConcurrentTools": 3,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "toolOutput": {
    "maxLines": 2000,
    "maxBytes": 51200,
    "maxChars": 100000,
    "largeOutputThreshold": 10000,
    "truncateMode": "middle"
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai.log",
    "prefix": "[ai] "
  }
}
```

### Model Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | `glm-4.5-air` | Model identifier |
| `provider` | string | `zai` | API provider name |
| `baseUrl` | string | ZAI API | API endpoint URL |
| `api` | string | `openai-completions` | API protocol type |

### Compactor Configuration

Controls automatic context compression to prevent token limit errors.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `maxMessages` | int | `50` | Trigger compaction after this many messages |
| `maxTokens` | int | `8000` | Trigger compaction after this many tokens (estimated) |
| `keepRecent` | int | `10` | Always keep this many recent messages |
| `keepRecentTokens` | int | `2000` | Always keep this many tokens of recent messages |
| `reserveTokens` | int | `1000` | Reserve space for response |
| `autoCompact` | bool | `true` | Enable automatic compaction |
| `toolCallCutoff` | int | `10` | Summarize tool outputs when visible count exceeds this |
| `toolSummaryStrategy` | string | `llm` | How to summarize: `llm`, `heuristic`, `off` |

**Tool Summary Strategies:**
- `llm` - Use LLM to generate summaries (highest quality, slower)
- `heuristic` - Use rule-based extraction (faster, simpler)
- `off` - Disable automatic summarization

### Concurrency Configuration

Controls parallel tool execution.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `maxConcurrentTools` | int | `3` | Maximum tools running simultaneously |
| `toolTimeout` | int | `30` | Seconds before tool execution times out |
| `queueTimeout` | int | `60` | Seconds before tool queue times out |

### Tool Output Configuration

Controls truncation of tool outputs to prevent token bloat.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `maxLines` | int | `2000` | Maximum lines to keep from tool output |
| `maxBytes` | int | `51200` | Maximum bytes to keep from tool output |
| `maxChars` | int | `100000` | Maximum characters to keep |
| `largeOutputThreshold` | int | `10000` | Threshold for "large" output classification |
| `truncateMode` | string | `middle` | How to truncate: `head`, `middle`, `tail` |

**Truncate Modes:**
- `head` - Keep beginning of output
- `middle` - Keep beginning and end with ellipsis (default)
- `tail` - Keep end of output

### Log Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `level` | string | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `file` | string | `~/.ai/ai.log` | Log file path |
| `prefix` | string | `[ai] ` | Log message prefix |

## Auth File (`~/.ai/auth.json`)

Stores API keys separately from config (for security).

```json
{
  "zai": {
    "type": "api_key",
    "key": "your-zai-api-key"
  }
}
```

### Provider Types

**ZAI:**
```json
{
  "zai": {
    "type": "api_key",
    "key": "sk-..."
  }
}
```

## Session Storage

Sessions are stored in `~/.ai/sessions/` with the following structure:

```
~/.ai/sessions/
└── --<working-directory-hash>--/
    └── <session-id>.jsonl
```

**Session Isolation:** Each working directory has separate sessions.

**Session File Format:** JSONL (one JSON object per line)

**Entry Types:**
- `session` - Session header
- `message` - Message (user/assistant/tool)
- `compaction` - Compression event
- `branch_summary` - Branch information
- `session_info` - Metadata update

## Skills Configuration

Skills are loaded from two locations:

1. **Global:** `~/.ai/skills/`
2. **Project:** `.ai/skills/` (in your project directory)

### Skill File Format

Skills are Markdown files with YAML frontmatter:

```markdown
---
name: skill-name
description: Brief description of what this skill does
disable-model-invocation: false
---

# Skill Title

Detailed instructions for the skill...

## Usage Examples

Example of how to use this skill...
```

### Skill Loading

- **Auto-inclusion:** Skills with `disable-model-invocation: false` are added to system prompt (max 24)
- **Manual invocation:** All skills can be invoked via `/skill:name` command
- **Priority:** Project skills override global skills with same name

### Skill Limits

- **Max skills in prompt:** 24 (configurable in `pkg/skill/formatter.go`)
- **Max description length:** 220 runes per skill

## Runtime Configuration

Some settings can be changed at runtime via RPC commands:

| Setting | Command | Notes |
|---------|---------|-------|
| Model | `set_model` | Requires provider and modelId |
| Auto-compaction | `set_auto_compaction` | Enable/disable |
| Tool cutoff | `set_tool_call_cutoff` | Adjust summarization threshold |
| Summary strategy | `set_tool_summary_strategy` | llm/heuristic/off |
| Thinking level | `set_thinking_level` | off/minimal/low/medium/high/xhigh |
| Trace events | `set_trace_events` | Configure event logging |

## Defaults

### Built-in Defaults

When no config is provided, the following defaults apply:

```go
Model:              glm-4.5-air
Provider:           zai
MaxMessages:        50
MaxTokens:          8000
AutoCompact:        true
MaxConcurrentTools: 3
ToolTimeout:        30s
ToolCallCutoff:     10
ToolSummaryStrategy: llm
ThinkingLevel:      high
```

### Token Estimation

Token usage is estimated using the following heuristic:

```
tokens ≈ characters / 4
```

This is approximate. Actual token counts may vary based on model tokenization.

## Performance Tuning

### For Faster Responses

1. **Reduce maxConcurrentTools** - Fewer parallel tool executions
2. **Lower toolTimeout** - Fail fast on slow tools
3. **Use heuristic summarization** - Faster than LLM-based

### For Better Context Retention

1. **Increase maxMessages** - Allow more messages before compaction
2. **Increase maxTokens** - Higher token threshold
3. **Disable autoCompact** - Manual control only

### For Lower Costs

1. **Lower toolCallCutoff** - More aggressive summarization
2. **Use heuristic summarization** - Fewer LLM calls
3. **Reduce maxLines/maxBytes** - Less tool output retained
