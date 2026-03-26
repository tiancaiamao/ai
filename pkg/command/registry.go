package command

import (
	"context"
	"fmt"
	"sync"
)

// CommandContext provides context for command execution.
// Commands can access the agent implementation and session key through this interface.
// This allows the command registry to be shared across different projects (ai, claw, etc.)
// without coupling to specific agent implementations.
type CommandContext interface {
	// GetAgent returns the underlying agent implementation (type *any).
	// Commands should type-assert to their expected agent type.
	// Example: agent := ctx.GetAgent().(*agentpkg.Agent)
	GetAgent() any

	// GetSessionKey returns the current session key.
	GetSessionKey() string
}

// SimpleCommandContext is a basic implementation of CommandContext.
type SimpleCommandContext struct {
	Agent      any
	SessionKey string
}

// GetAgent returns the agent implementation.
func (c *SimpleCommandContext) GetAgent() any {
	return c.Agent
}

// GetSessionKey returns the session key.
func (c *SimpleCommandContext) GetSessionKey() string {
	return c.SessionKey
}

// NewSimpleCommandContext creates a new SimpleCommandContext with the given agent and session key.
func NewSimpleCommandContext(agent any, sessionKey string) *SimpleCommandContext {
	return &SimpleCommandContext{
		Agent:      agent,
		SessionKey: sessionKey,
	}
}

// Handler is the function signature for command handlers.
// Commands can access the agent and session key through CommandContext.
// Use type assertion to get the specific agent type.
//
// Example for ai project:
//   handler := func(ctx context.Context, cmdCtx command.CommandContext, args string) (string, error) {
//       agent := cmdCtx.GetAgent().(*agentpkg.Agent)
//       sessionKey := cmdCtx.GetSessionKey()
//       // ... use agent and sessionKey
//   }
type Handler func(ctx context.Context, cmdCtx CommandContext, args string) (string, error)

// Descriptor describes a command for help display.
type Descriptor struct {
	Name        string
	Description string
}

// Registry stores command handlers.
type Registry struct {
	mu        sync.RWMutex
	commands  map[string]Handler
	descripts map[string]Descriptor
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands:  make(map[string]Handler),
		descripts: make(map[string]Descriptor),
	}
}

// Register registers a command with the given name, handler, and description.
// If a command with the same name already exists, it will be overwritten.
func (r *Registry) Register(name, description string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.commands[name] = handler
	r.descripts[name] = Descriptor{
		Name:        name,
		Description: description,
	}
}

// Get returns the handler for a command, or false if not found.
func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.commands[name]
	return handler, exists
}

// List returns all registered command names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	return names
}

// ListDescriptors returns all command descriptors.
func (r *Registry) ListDescriptors() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	descriptors := make([]Descriptor, 0, len(r.descripts))
	for _, desc := range r.descripts {
		descriptors = append(descriptors, desc)
	}
	return descriptors
}

// Handle executes a command and returns its response.
// Returns an error if the command is not found or execution fails.
func (r *Registry) Handle(ctx context.Context, name, args string, cmdCtx CommandContext) (string, error) {
	handler, exists := r.Get(name)
	if !exists {
		return "", fmt.Errorf("command not found: %s", name)
	}

	return handler(ctx, cmdCtx, args)
}