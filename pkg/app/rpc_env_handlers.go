package app

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const envCommandUsage = "usage: /env set NAME value | get NAME | list | unset NAME"

// registerEnvHandlers registers the operator-only environment command. The
// handler edits the injection table used by the bash tool; it never touches
// the agent process environment or agent context.
func (app *App) registerEnvHandlers() {
	app.commands.Register("env", "Set or inspect environment variables injected into bash", func(args string) (any, error) {
		return app.handleEnv(args)
	})
}

// handleEnv implements /env set|get|list|unset. Values are returned only to the
// human client through the slash-command channel; they are never sent to the
// model. Do not route this handler through the agent prompt path.
func (app *App) handleEnv(args string) (any, error) {
	parts := strings.SplitN(args, " ", 2)
	action := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}

	switch action {
	case "set":
		name, value, ok := strings.Cut(rest, " ")
		if !ok || value == "" {
			return nil, errors.New("usage: /env set NAME value")
		}
		if !validEnvName(name) {
			return nil, fmt.Errorf("invalid environment variable name %q", name)
		}
		app.envInjector.Set(name, value)
		slog.Info("Received env set", "name", name)
		return "set " + name, nil

	case "get":
		name := strings.TrimSpace(rest)
		if !validEnvName(name) {
			return nil, errors.New("usage: /env get NAME")
		}
		value, ok := app.envInjector.Lookup(name)
		if !ok {
			return name + " is not set", nil
		}
		if value == "" {
			return name + " is set to an empty value", nil
		}
		slog.Info("Received env get", "name", name)
		return value, nil

	case "list":
		return strings.Join(app.envInjector.Environ(), "\n"), nil

	case "unset":
		name := strings.TrimSpace(rest)
		if !validEnvName(name) {
			return nil, errors.New("usage: /env unset NAME")
		}
		app.envInjector.Unset(name)
		slog.Info("Received env unset", "name", name)
		return "unset " + name, nil

	default:
		return nil, errors.New(envCommandUsage)
	}
}

// validEnvName accepts the conventional shell identifier form. The injection
// table accepts any name exec can pass, but a strict check turns typos into
// clear errors.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}
