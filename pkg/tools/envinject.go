package tools

import (
	"os"
	"sort"
	"strings"
	"sync"
)

// EnvInjector is an operator-managed view of the environment given to bash
// tool children. It never calls os.Setenv: the agent process environment is
// left untouched.
type EnvInjector struct {
	mu      sync.RWMutex
	set     map[string]string
	deleted map[string]bool
}

// NewEnvInjector returns an empty injection table.
func NewEnvInjector() *EnvInjector {
	return &EnvInjector{
		set:     make(map[string]string),
		deleted: make(map[string]bool),
	}
}

// Set overrides NAME for future bash children. It also clears any earlier
// deletion of NAME.
func (e *EnvInjector) Set(name, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.set[name] = value
	delete(e.deleted, name)
}

// Unset hides NAME from future bash children, even if the agent process was
// started with NAME in its own environment.
func (e *EnvInjector) Unset(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.set, name)
	e.deleted[name] = true
}

// Lookup returns the value future bash children will see for NAME.
func (e *EnvInjector) Lookup(name string) (string, bool) {
	e.mu.RLock()
	value, ok := e.set[name]
	removed := e.deleted[name]
	e.mu.RUnlock()
	if ok {
		return value, true
	}
	if removed {
		return "", false
	}
	return os.LookupEnv(name)
}

// ManagedEntry is a variable the operator explicitly changed through /env.
type ManagedEntry struct {
	Name    string
	Value   string
	Deleted bool
}

// ManagedEntries returns only the variables the operator has set or unset,
// sorted by name. Variables merely inherited from the agent process are not
// included, so this is the view /env list reports.
func (e *EnvInjector) ManagedEntries() []ManagedEntry {
	e.mu.RLock()
	entries := make([]ManagedEntry, 0, len(e.set)+len(e.deleted))
	for name, value := range e.set {
		entries = append(entries, ManagedEntry{Name: name, Value: value})
	}
	for name := range e.deleted {
		entries = append(entries, ManagedEntry{Name: name, Deleted: true})
	}
	e.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// Environ builds the effective child environment: the agent process
// environment with injected overrides applied and removed names filtered out.
func (e *EnvInjector) Environ() []string {
	e.mu.RLock()
	overrides := make(map[string]string, len(e.set))
	for name, value := range e.set {
		overrides[name] = value
	}
	deleted := make(map[string]bool, len(e.deleted))
	for name := range e.deleted {
		deleted[name] = true
	}
	e.mu.RUnlock()

	effective := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && !deleted[name] {
			effective[name] = value
		}
	}
	for name, value := range overrides {
		effective[name] = value
	}

	names := make([]string, 0, len(effective))
	for name := range effective {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+effective[name])
	}
	return entries
}
