// Package command provides a shared command registry for slash commands.
// Both the CLI agent (ai) and the chat bot (claw) use this to register
// and dispatch /command style operations.
package command

import (
	"fmt"
	"sort"
	"sync"
)

// Handler processes a slash command.
// args is the raw text after the command name (e.g., for "/model gpt-4", args = "gpt-4").
// Returns result data (for structured responses) or an error.
type Handler func(args string) (any, error)

// Registry stores registered slash commands.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// New creates a new empty command registry.
func New() *Registry {
	return &Registry{
		handlers: make(map[string]Handler),
	}
}

// Register registers a handler for the given command name.
// If a handler was already registered for the name, it is replaced.
// name should not include the "/" prefix (e.g., "model" not "/model").
func (r *Registry) Register(name string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = handler
}

// Get looks up a handler by command name.
// Returns the handler and true if found, nil and false otherwise.
func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

// List returns all registered command names, sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseSlashCommand splits a slash command string into command name and args.
// Input: "/model gpt-4 o3" → command="model", args="gpt-4 o3"
// Input: "/compact" → command="compact", args=""
// Returns an error if the input is not a slash command (no "/" prefix).
func ParseSlashCommand(input string) (command string, args string, err error) {
	if len(input) == 0 || input[0] != '/' {
		return "", "", fmt.Errorf("not a slash command: %q", input)
	}
	// Remove the "/" prefix
	rest := input[1:]
	// Split into command and args
	for i := 0; i < len(rest); i++ {
		if rest[i] == ' ' || rest[i] == '\t' {
			cmd := rest[:i]
			if cmd == "" {
				return "", "", fmt.Errorf("empty command name")
			}
			return cmd, trimLeftSpace(rest[i+1:]), nil
		}
	}
	if rest == "" {
		return "", "", fmt.Errorf("empty command name")
	}
	return rest, "", nil
}

func trimLeftSpace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[i:]
		}
	}
	return ""
}