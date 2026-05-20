package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// --- Help and miscellaneous handlers ---

func (app *rpcApp) handleSteerSlash(args string) (any, error) {
	message := strings.TrimSpace(args)
	if message == "" {
		return nil, fmt.Errorf("usage: /steer <message>")
	}

	slog.Info("Received steer slash command", "value", message)

	expandedMessage := app.expandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in steer", "original", message, "skill", skill.ExtractSkillName(message))
	}

	app.stateMu.Lock()
	mode := app.steeringMode
	pending := app.pendingSteer
	streaming := app.isStreaming
	app.stateMu.Unlock()

	if mode == "one-at-a-time" && pending {
		return nil, fmt.Errorf("steer already pending")
	}
	if !streaming {
		app.compactBeforeRequest("pre_request_steer")
	}

	app.stateMu.Lock()
	app.pendingSteer = true
	app.stateMu.Unlock()
	app.ag.Steer(expandedMessage)
	return map[string]any{"status": "steered"}, nil
}

func (app *rpcApp) handleFollowUpSlash(args string) (any, error) {
	message := strings.TrimSpace(args)
	if message == "" {
		return nil, fmt.Errorf("usage: /follow-up <message>")
	}

	slog.Info("Received follow-up command")
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not busy")
	}

	if app.followUpMode != "one-at-a-time" && app.followUpMode != "queue" {
		return nil, fmt.Errorf("follow-up mode is '%s', not enabled", app.followUpMode)
	}

	if len(app.followUpQueue) > 0 && app.followUpMode == "one-at-a-time" {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}

	expandedMessage := app.expandSkillCommands(message)
	app.followUpQueue = append(app.followUpQueue, expandedMessage)
	return map[string]any{"status": "queued", "message": expandedMessage}, nil
}

func (app *rpcApp) handleAbortSlash(args string) (any, error) {
	_ = args
	slog.Info("Received abort command")
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not streaming")
	}

	app.ag.Abort()
	return map[string]any{"status": "aborting"}, nil
}

// registerHelpHandlers registers help and miscellaneous slash commands.
func (app *rpcApp) registerHelpHandlers() {
	// /help
	app.server.RegisterSlash("help", "Show available slash commands", func(args string) (any, error) {
		_ = args
		commands := app.server.ListSlashCommands()
		return map[string]any{"commands": commands}, nil
	})

	// /skills
	app.server.RegisterSlash("skills", "List available skills", func(args string) (any, error) {
		_ = args
		slog.Info("Received skills")
		return map[string]any{"commands": app.skillCommands}, nil
	})

	// /quit
	app.server.RegisterSlash("quit", "Exit the application", func(args string) (any, error) {
		_ = args
		slog.Info("Received quit command, exiting application")
		os.Exit(0)
		return nil, nil
	})

	// /steer
	app.server.RegisterSlash("steer", "Inject mid-conversation guidance", func(args string) (any, error) {
		return app.handleSteerSlash(args)
	})

	// /abort
	app.server.RegisterSlash("abort", "Abort the current agent execution", func(args string) (any, error) {
		return app.handleAbortSlash(args)
	})

	// /follow-up
	app.server.RegisterSlash("follow-up", "Add a follow-up message when agent is busy", func(args string) (any, error) {
		return app.handleFollowUpSlash(args)
	})

	// Hidden aliases — help-related
	app.registerHiddenAlias("get_messages", "Get session messages (internal)", "messages")
	app.registerHiddenAlias("get_state", "Get agent state (internal)", "session")
	app.registerHiddenAlias("get_commands", "List commands (internal)", "skills")
	app.registerHiddenAlias("new_session", "Create new session (internal)", "new")
	app.registerHiddenAlias("list_sessions", "List sessions (internal)", "resume")
	app.registerHiddenAlias("switch_session", "Switch session (internal)", "resume")
}