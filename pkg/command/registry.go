// Package command provides a shared command registry for slash commands.
// Both the CLI agent (ai) and the chat bot (claw) use this to register
// and dispatch /command style operations.
package command

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Handler processes a slash command.
// args is the raw text after the command name (e.g., for "/model gpt-4", args = "gpt-4").
// Returns result data (for structured responses) or an error.
type Handler func(args string) (any, error)

// CommandInfo describes a registered slash command.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Registry stores registered slash commands.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]entry
}

type entry struct {
	info    CommandInfo
	handler Handler
}

// New creates a new empty command registry.
func New() *Registry {
	return &Registry{
		handlers: make(map[string]entry),
	}
}

// Register registers a handler for the given command name.
// If a handler was already registered for the name, it is replaced.
// name should not include the "/" prefix (e.g., "model" not "/model").
func (r *Registry) Register(name, description string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = entry{
		info:    CommandInfo{Name: name, Description: description},
		handler: handler,
	}
}

// Get looks up a handler by command name.
// Returns the handler and true if found, nil and false otherwise.
func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.handlers[name]
	if !ok {
		return nil, false
	}
	return e.handler, true
}

// GetInfo looks up command info by name.
func (r *Registry) GetInfo(name string) (CommandInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.handlers[name]
	if !ok {
		return CommandInfo{}, false
	}
	return e.info, true
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

// ListCommands returns all registered command infos, sorted by name.
func (r *Registry) ListCommands() []CommandInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]CommandInfo, 0, len(r.handlers))
	for _, e := range r.handlers {
		result = append(result, e.info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Dispatch parses and executes a slash command string.
// Returns (handled, result, error). handled is false if the command is unknown.
func (r *Registry) Dispatch(input string) (bool, any, error) {
	cmdName, args, err := ParseSlashCommand(input)
	if err != nil {
		return false, nil, err
	}
	h, ok := r.Get(cmdName)
	if !ok {
		return false, nil, fmt.Errorf("unknown command: /%s", cmdName)
	}
	result, err := h(args)
	return true, result, err
}

// RegisterSetSub adds a handler for a /set subkey.
// e.g. RegisterSetSub("busy-mode", handler) makes "/set busy-mode steer" work.
func (r *Registry) RegisterSetSub(key, description string, handler Handler) {
	name := "set " + key
	r.Register(name, description, handler)
}

// DispatchSet parses "/set <key> [value]" and dispatches to the registered sub-handler.
// If args is empty, it lists available set options.
func (r *Registry) DispatchSet(args string) (any, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		// List available set options
		r.mu.RLock()
		var opts []CommandInfo
		for name, e := range r.handlers {
			if strings.HasPrefix(name, "set ") {
				opts = append(opts, CommandInfo{
					Name:        strings.TrimPrefix(name, "set "),
					Description: e.info.Description,
				})
			}
		}
		r.mu.RUnlock()
		sort.Slice(opts, func(i, j int) bool {
			return opts[i].Name < opts[j].Name
		})
		return map[string]any{"set-options": opts}, nil
	}

	fields := strings.Fields(args)
	if len(fields) == 0 {
		return nil, fmt.Errorf("usage: /set <key> [value]")
	}
	key := fields[0]
	value := strings.TrimSpace(strings.TrimPrefix(args, key))

	h, ok := r.Get("set " + key)
	if !ok {
		return nil, fmt.Errorf("unknown setting: %s", key)
	}
	return h(value)
}

// ParseSlashCommand splits a slash command string into command name and args.
// Input: "/model gpt-4 o3" → command="model", args="gpt-4 o3"
// Input: "/compact" → command="compact", args=""
// Input: "/set busy-mode steer" → command="set", args="busy-mode steer"
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