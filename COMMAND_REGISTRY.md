# Command Registry

## Overview

The `ai` project now includes a command registry system for handling slash commands (e.g., `/help`, `/commands`) in user messages. This feature is designed to be reusable across multiple projects (ai, claw, etc.).

## Architecture

### Core Components

#### CommandRegistry (pkg/command/registry.go)
- Purpose: Thread-safe command storage and execution
- Methods:
  - `Register(name, description string, handler CommandHandler)` - Register a command
  - `Get(name string) (CommandHandler, bool)` - Get a command handler
  - `List() []string` - List all command names
  - `ListDescriptors() []CommandDescriptor` - List all commands with descriptions
  - `HandleCommand(ctx, name, args string, cmdCtx CommandContext) (string, error)` - Execute a command

#### CommandContext Interface
```go
type CommandContext interface {
    GetAgent() any          // Returns agent implementation (type *any)
    GetSessionKey() string  // Returns current session key
}
```
- Purpose: Decouples command handlers from specific agent implementations
- Implementation: `SimpleCommandContext` provided for basic usage

#### CommandHandler
- Type: `func(ctx context.Context, cmdCtx CommandContext, args string) (string, error)`
- Purpose: Handler function for executing commands
- Usage: Type-assert `cmdCtx.GetAgent()` to get the specific agent type

#### Agent Integration (pkg/agent/)
- Location: `pkg/agent/agent.go`
- Changes:
  - Added `commands *command.CommandRegistry` field
  - Initialized in `NewAgentFromConfigWithContext`
  - Commands registered via `registerBuiltinCommands` and `registerAdditionalCommands`

### Command Processing Flow

1. User sends message starting with `/`
2. `Agent.processPrompt` detects `/` prefix
3. `Agent.processCommand` parses command name and args
4. Creates `CommandContext` with agent instance
5. `CommandRegistry.HandleCommand` executes the handler
6. Handler type-asserts agent from context
7. Response emitted as assistant message
8. Loop ends (commands don't trigger LLM calls)

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
    func(ctx context.Context, cmdCtx command.CommandContext, args string) (string, error) {
        // Type-assert to get the specific agent type
        agent := cmdCtx.GetAgent().(*agent.Agent)
        sessionKey := cmdCtx.GetSessionKey()

        // Use agent and sessionKey...
        return "Custom response", nil
    })

// Process user message (commands are handled automatically)
agent.Prompt("/help")  // Will display help, not send to LLM
agent.Prompt("Hello")   // Normal message, sent to LLM
```

## Reusability for Other Projects

To use the command registry in another project (e.g., claw):

```go
import "github.com/tiancaiamao/ai/pkg/command"

// Define your agent type
type MyAgent struct {
    commands *command.CommandRegistry
    // ... other fields
}

// Register commands
func (a *MyAgent) RegisterCommands() {
    a.commands.Register("mycmd", "My custom command",
        func(ctx context.Context, cmdCtx command.CommandContext, args string) (string, error) {
            myAgent := cmdCtx.GetAgent().(*MyAgent)
            // ... use myAgent
            return "Response", nil
        })
}

// Handle commands in your prompt processing
func (a *MyAgent) ProcessPrompt(ctx context.Context, message string) {
    if strings.HasPrefix(message, "/") {
        cmdCtx := command.NewSimpleCommandContext(a, sessionKey)
        response, err := a.commands.HandleCommand(ctx, name, args, cmdCtx)
        // ... handle response
    }
    // ... normal processing
}
```

## Files Changed

### New Files
- `pkg/command/registry.go` - CommandRegistry core implementation (shared package)
- `pkg/command/registry_test.go` - Tests for CommandRegistry (shared package)
- `pkg/agent/command_builtin.go` - `/help`, `/commands` commands
- `pkg/agent/command_builtin_test.go` - Tests for built-in commands
- `pkg/agent/command_additional.go` - `/session`, `/clear`, `/model`, `/set_thinking_level` commands
- `pkg/agent/command_additional_test.go` - Tests for additional commands
- `pkg/agent/agent_commands.go` - Command processing logic in Agent

### Modified Files
- `pkg/agent/agent.go` - Added `commands` field and initialization

## Design Decisions

1. **Shared Package**: `pkg/command` is a standalone package that can be used by any project
2. **Interface-based Decoupling**: `CommandContext` interface allows different agent implementations
3. **Type Assertion**: Handlers use type assertion to get specific agent types (flexible but requires care)
4. **Assistant Messages**: Command responses are emitted as assistant messages (not user messages)
5. **No Session History Override**: Commands use `NewAgentEndEvent(nil)` to avoid overwriting session history