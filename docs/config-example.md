# ai Configuration

## Configuration File Location

The default configuration file is located at:
```
~/.ai/config.json
```

## Configuration Options

### Example Configuration

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
    "keepRecent": 5,
    "keepRecentTokens": 20000,
    "reserveTokens": 16384,
    "toolCallCutoff": 10,
    "toolSummaryStrategy": "llm",
    "autoCompact": true
  },
  "toolOutput": {
    "maxLines": 2000,
    "maxBytes": 51200,
    "maxChars": 204800,
    "largeOutputThreshold": 204800,
    "truncateMode": "head_tail"
  },
  "concurrency": {
    "maxConcurrentTools": 3,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai.log",
    "prefix": "[ai] "
  }
}
```

### Fields

#### model
- `id` (string): Model identifier (e.g., "glm-4.5-air", "gpt-4")
- `provider` (string): Provider name (e.g., "zai", "openai")
- `baseUrl` (string): API endpoint URL
- `api` (string): API type (e.g., "openai-completions")

#### compactor
- `maxMessages` (int): Maximum messages before auto-compaction (default: 50)
- `maxTokens` (int): Approximate token limit before auto-compaction (default: 8000)
- `keepRecent` (int): Number of recent messages to keep uncompressed (default: 5)
- `keepRecentTokens` (int): Token budget to keep from the most recent messages (default: 20000)
- `reserveTokens` (int): Reserved response tokens used when calculating effective token limit from model context window (default: 16384)
- `toolCallCutoff` (int): Maximum number of agent-visible tool result messages before oldest outputs are summarized (default: 10; `0` disables this behavior)
- `toolSummaryStrategy` (string): Strategy for tool-output summarization: `llm`, `heuristic`, `off` (default: `llm`)
- `autoCompact` (bool): Enable/disable automatic compression (default: true)

When `keepRecentTokens` is set to a positive value, compaction keeps recent context by token budget and `keepRecent` is used only as a fallback.

#### toolOutput
- `maxLines` (int): Maximum lines to keep in tool output (default: 2000)
- `maxBytes` (int): Maximum bytes to keep in tool output (default: 51200)
- `maxChars` (int): Maximum runes to keep in tool output (default: 204800)
- `largeOutputThreshold` (int): Spill very large tool output to temp files above this rune threshold (default: 204800)
- `truncateMode` (string): Truncation mode: `head` or `head_tail` (default: `head_tail`)

#### concurrency
- `maxConcurrentTools` (int): Maximum number of tools running concurrently (default: 3)
- `toolTimeout` (int): Tool execution timeout in seconds (default: 30)
- `queueTimeout` (int): Maximum wait time for available execution slot in seconds (default: 60)

**Note**: The concurrency control prevents resource exhaustion by limiting simultaneous tool execution. Adjust based on your system capabilities.

#### log
- `level` (string): Log level - "debug", "info", "warn", or "error" (default: "info")
- `file` (string): Path to log file (default: "~/.ai/ai.log"). Set to empty string to disable file logging.
- `prefix` (string): Prefix for all log messages (default: "[ai] ")

**Log Levels**:
- `debug`: Detailed debugging messages for troubleshooting
- `info`: General informational messages (default)
- `warn`: Warning messages for potential issues
- `error`: Error messages only

**Logging Output**: By default, logs are written to both console (stdout) and file. The file is rotated when it exceeds the configured size limit.

## Auth File

API keys can be stored in `~/.ai/auth.json` (env vars take precedence). Example:

```json
{
  "zai": {
    "type": "api_key",
    "key": "your-zai-api-key"
  }
}
```

## Environment Variables

Environment variables take precedence over configuration file values:

- `ZAI_API_KEY`: API key (required unless present in `~/.ai/auth.json`)
- `ZAI_MODEL`: Model identifier (overrides config file)
- `ZAI_BASE_URL`: API base URL (overrides config file)

## Default Configuration

If no configuration file exists, the following defaults are used:

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
    "keepRecent": 5,
    "keepRecentTokens": 20000,
    "reserveTokens": 16384,
    "toolCallCutoff": 10,
    "toolSummaryStrategy": "llm",
    "autoCompact": true
  },
  "toolOutput": {
    "maxLines": 2000,
    "maxBytes": 51200,
    "maxChars": 204800,
    "largeOutputThreshold": 204800,
    "truncateMode": "head_tail"
  },
  "concurrency": {
    "maxConcurrentTools": 3,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai.log",
    "prefix": "[ai] "
  }
}
```

## Creating a Configuration File

To create a configuration file with default values:

```bash
mkdir -p ~/.ai
cat > ~/.ai/config.json <<'EOF'
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
    "keepRecent": 5,
    "keepRecentTokens": 20000,
    "reserveTokens": 16384,
    "toolCallCutoff": 10,
    "toolSummaryStrategy": "llm",
    "autoCompact": true
  },
  "toolOutput": {
    "maxLines": 2000,
    "maxBytes": 51200,
    "maxChars": 204800,
    "largeOutputThreshold": 204800,
    "truncateMode": "head_tail"
  },
  "concurrency": {
    "maxConcurrentTools": 3,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai.log",
    "prefix": "[ai] "
  }
}
EOF
```

## Log File Management

Log files are automatically created in the `~/.ai` directory. To manage log files:

**View recent logs**:
```bash
tail -f ~/.ai/ai.log
```

**Clear log file**:
```bash
> ~/.ai/ai.log
```

**Disable file logging** (console only):
```json
{
  "log": {
    "level": "info",
    "file": "",
    "prefix": "[ai] "
  }
}
```
