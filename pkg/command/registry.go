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

// CommandInfo describes a registered slash command.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden,omitempty"`
}

// SetSubcommand describes a subcommand available under /set.
type SetSubcommand struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// Registry stores registered slash commands.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	info     map[string]string // name -> description
	hidden   map[string]bool   // name -> hidden from user-facing listings

	setMu       sync.RWMutex
	setHandlers map[string]Handler
	setInfo     map[string]string // subkey -> description
}

// New creates a new empty command registry.
func New() *Registry {
	return &Registry{
		handlers:    make(map[string]Handler),
		info:        make(map[string]string),
		hidden:      make(map[string]bool),
		setHandlers: make(map[string]Handler),
		setInfo:     make(map[string]string),
	}
}

// Register registers a handler for the given command name with a description.
// If a handler was already registered for the name, it is replaced.
// name should not include the "/" prefix (e.g., "model" not "/model").
func (r *Registry) Register(name, description string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = handler
	r.info[name] = description
}

// RegisterHidden registers a slash command that is callable but hidden from /help listings.
func (r *Registry) RegisterHidden(name, description string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = handler
	r.info[name] = description
	r.hidden[name] = true
}

// Get looks up a handler by command name.
// Returns the handler and true if found, nil and false otherwise.
func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

// ListCommands returns user-visible commands (excluding hidden ones), sorted by name.
func (r *Registry) ListCommands() []CommandInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]CommandInfo, 0, len(r.handlers))
	for name, desc := range r.info {
		if r.hidden[name] {
			continue
		}
		result = append(result, CommandInfo{Name: name, Description: desc})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
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
