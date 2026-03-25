package agent

import (
	"context"
	"fmt"
	"sync"
)

// CommandHandler is the function signature for command handlers.
// Commands can access the agent and session key to read/modify state.
type CommandHandler func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error)

// CommandDescriptor describes a command for help display.
type CommandDescriptor struct {
	Name        string
	Description string
}

// CommandRegistry stores command handlers.
type CommandRegistry struct {
	mu        sync.RWMutex
	commands  map[string]CommandHandler
	descripts map[string]CommandDescriptor
}

// NewCommandRegistry creates a new command registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands:  make(map[string]CommandHandler),
		descripts: make(map[string]CommandDescriptor),
	}
}

// Register registers a command with the given name, handler, and description.
// If a command with the same name already exists, it will be overwritten.
func (r *CommandRegistry) Register(name, description string, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.commands[name] = handler
	r.descripts[name] = CommandDescriptor{
		Name:        name,
		Description: description,
	}
}

// Get returns the handler for a command, or false if not found.
func (r *CommandRegistry) Get(name string) (CommandHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.commands[name]
	return handler, exists
}

// List returns all registered command names.
func (r *CommandRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	return names
}

// ListDescriptors returns all command descriptors.
func (r *CommandRegistry) ListDescriptors() []CommandDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	descriptors := make([]CommandDescriptor, 0, len(r.descripts))
	for _, desc := range r.descripts {
		descriptors = append(descriptors, desc)
	}
	return descriptors
}

// HandleCommand executes a command and returns its response.
// Returns an error if the command is not found or execution fails.
func (r *CommandRegistry) HandleCommand(ctx context.Context, name, args string, agent *Agent, sessionKey string) (string, error) {
	handler, exists := r.Get(name)
	if !exists {
		return "", fmt.Errorf("command not found: %s", name)
	}

	return handler(ctx, agent, sessionKey, args)
}