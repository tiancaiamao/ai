# Command Registry

## Overview

The `ai` project now includes a command registry system for handling slash commands (e.g., `/help`, `/commands`) in user messages. This feature is inspired by the `claw/cmd/aiclaw` pattern and makes the agent run loop more reusable as an SDK.

## Architecture

### Core Components

#### CommandRegistry
- Location: `pkg/agent/command_registry.go`
- Purpose: Thread-safe command storage and execution
- Methods:
  - `Register(name, description string, handler CommandHandler)` - Register a command
  - `Get(name string) (CommandHandler, bool)` - Get a command handler
  - `List() []string` - List all command names
  - `ListDescriptors() []CommandDescriptor` - List all commands with descriptions
  - `HandleCommand(ctx, name, args string, agent *Agent, sessionKey string) (string, error)` - Execute a command

#### CommandHandler
- Type: `func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error)`
- Purpose: Handler function for executing commands

#### Agent Integration
- Location: `pkg/agent/agent.go`
- Changes:
  - Added `commands *CommandRegistry` field
  - Initialized in `NewAgentFromConfigWithContext`
  - Commands registered via `registerBuiltinCommands` and `registerAdditionalCommands`

### Command Processing Flow

1. User sends message starting with `/`
2. `Agent.processPrompt` detects `/` prefix
3. `Agent.processCommand` parses command name and args
4. `CommandRegistry.HandleCommand` executes the handler
5. Response emitted as system message
6. Loop ends (commands don't trigger LLM calls)

### Built-in Commands

| Command | Description |
|----------|-------------|
| `/help` | Display help information for all available commands |
| `/commands` | List all available commands |
| `/session` | Display current session information |
| `/clear` | Clear the conversation context |
| `/model` | Display or set the current model |
| `/set_thinking_level` | Set the thinking level (off, minimal, low, medium, high, xhigh) |

## Usage Example

```go
// Create agent (automatically registers built-in commands)
agent := agent.NewAgent(model, apiKey, systemPrompt)

// Register custom command
agent.commands.Register("custom", "Custom command description",
    func(ctx context.Context, agent *agent.Agent, sessionKey string, args string) (string, error) {
        return "Custom response", nil
    })

// Process user message (commands are handled automatically)
agent.Prompt("/help")  // Will display help, not send to LLM
agent.Prompt("Hello")   // Normal message, sent to LLM
```

## Files Changed

- `pkg/agent/command_registry.go` - New file
- `pkg/agent/command_registry_test.go` - New file
- `pkg/agent/command_builtin.go` - New file
- `pkg/agent/command_builtin_test.go` - New file
- `pkg/agent/command_additional.go` - New file
- `pkg/agent/command_additional_test.go` - New file
- `pkg/agent/agent_commands.go` - New file
- `pkg/agent/agent.go` - Modified (added `commands` field, initialization)