# pkg/command

Shared slash command registry for the CLI agent and chat bot.

## Overview

Provides a registry for `/command` style operations. Both `ai` (the CLI agent) and `claw` (the chat bot) use this package to register and dispatch slash commands like `/model`, `/compact`, `/help`.

## Registry

```go
type Registry struct { ... }

func New() *Registry
```

### Registration

```go
func (r *Registry) Register(name, description string, handler Handler)
func (r *Registry) RegisterHidden(name, description string, handler Handler)
```

`name` should not include the `/` prefix (e.g., `"model"` not `"/model"`). Hidden commands are callable but excluded from `/help` output.

### Dispatch

```go
func (r *Registry) Dispatch(name, args string) (any, error)
```

Looks up and invokes a command handler. Returns error for unknown commands.

### Listing

```go
func (r *Registry) List() []string                // All names, sorted
func (r *Registry) ListCommands() []CommandInfo    // User-visible only
func (r *Registry) AllCommands() []CommandInfo     // Including hidden
```

## /set Subcommands

The registry also manages `/set` subcommands:

```go
func (r *Registry) RegisterSetSub(key, description string, handler Handler)
func (r *Registry) DispatchSet(key, args string) (any, error)
func (r *Registry) ListSetSubs() []SetSubcommand
```

Enables nested commands like `/set model gpt-4` or `/set thinking high`.

## Parsing

```go
func ParseSlashCommand(input string) (command string, args string, err error)
```

Splits `"/model gpt-4 o3"` into `("model", "gpt-4 o3", nil)`. Returns error if input has no `/` prefix or empty command name.

## Handler Type

```go
type Handler func(args string) (any, error)
```

`args` is the raw text after the command name.

## Key Files

| File | Description |
|------|-------------|
| `registry.go` | `Registry`, `Handler`, `CommandInfo`, `SetSubcommand`, `ParseSlashCommand` |