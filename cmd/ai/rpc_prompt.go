package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/skill"
)

// handlePrompt processes a prompt command: the main user-input handler.
// It handles slash commands, skill expansion, streaming behavior dispatch,
// and normal agent prompts.
func (c *RPCCore) handlePrompt(cmd rpc.RPCCommand) (any, error) {
	// Parse prompt data
	var data struct {
		Message           string            `json:"message"`
		StreamingBehavior string            `json:"streamingBehavior"`
		Images            []json.RawMessage `json:"images"`
	}
	if len(cmd.Data) > 0 {
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
	}
	message := cmd.Message
	if message == "" {
		message = data.Message
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty prompt message")
	}
	if len(data.Images) > 0 {
		return nil, fmt.Errorf("images are not supported in this RPC implementation")
	}

	// Expand /skill:name commands BEFORE generic slash dispatch.
	// Skill commands like /skill:name are not registered as slash handlers;
	// they are expanded into full prompts and processed normally.
	if skill.IsSkillCommand(message) {
		expandedMessage := c.ExpandSkillCommands(message)
		slog.Info("Expanded skill command", "original", message, "skill", skill.ExtractSkillName(message))

		c.StateMu.Lock()
		streaming := c.IsStreaming
		c.StateMu.Unlock()

		if streaming {
			// During streaming, treat expanded skill prompt as a steer
			c.StateMu.Lock()
			c.PendingSteer = true
			c.StateMu.Unlock()
			c.Ag.Steer(expandedMessage)
			return nil, nil
		}

		c.CompactionCtrl.MaybeCompact("pre_request_prompt", c.Sess)
		return nil, c.Ag.Prompt(expandedMessage)
	}

	// Intercept slash commands — execute synchronously without agent.
	// Only non-skill slash commands (e.g. /get_state, /compact) reach here.
	if message[0] == '/' {
		cmdName, args, err := command.ParseSlashCommand(message)
		if err != nil {
			return nil, fmt.Errorf("invalid slash command: %w", err)
		}
		handler, ok := c.Server.GetSlashHandler(cmdName)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", cmdName)
		}
		return handler(args)
	}

	c.StateMu.Lock()
	streaming := c.IsStreaming
	mode := c.SteeringMode
	followMode := c.FollowUpMode
	pending := c.PendingSteer
	c.StateMu.Unlock()

	if streaming {
		behavior := strings.TrimSpace(data.StreamingBehavior)
		if behavior == "" {
			return nil, fmt.Errorf("agent is streaming; specify streamingBehavior")
		}
		switch behavior {
		case "steer":
			if mode == "one-at-a-time" && pending {
				return nil, fmt.Errorf("steer already pending")
			}
			c.StateMu.Lock()
			c.PendingSteer = true
			c.StateMu.Unlock()
			c.Ag.Steer(message)
			return nil, nil
		case "followUp", "follow_up":
			if followMode == "one-at-a-time" && c.Ag.GetPendingFollowUps() > 0 {
				return nil, fmt.Errorf("follow-up queue already has a pending message")
			}
			return nil, c.Ag.FollowUp(message)
		default:
			return nil, fmt.Errorf("invalid streamingBehavior: %s", behavior)
		}
	}

	c.CompactionCtrl.MaybeCompact("pre_request_prompt", c.Sess)
	return nil, c.Ag.Prompt(message)
}

// handleSteer processes a mid-turn steering command.
func (c *RPCCore) handleSteer(cmd rpc.RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received steer:", "value", message)

	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty steer message")
	}

	// Expand /skill:name commands
	expandedMessage := c.ExpandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in steer", "original", message, "skill", skill.ExtractSkillName(message))
	}

	c.StateMu.Lock()
	mode := c.SteeringMode
	pending := c.PendingSteer
	streaming := c.IsStreaming
	c.StateMu.Unlock()
	if mode == "one-at-a-time" && pending {
		return nil, fmt.Errorf("steer already pending")
	}
	if !streaming {
		c.CompactionCtrl.MaybeCompact("pre_request_steer", c.Sess)
	}
	c.StateMu.Lock()
	c.PendingSteer = true
	c.StateMu.Unlock()
	c.Ag.Steer(expandedMessage)
	return nil, nil
}

// handleAbort processes an abort command.
func (c *RPCCore) handleAbort(_ rpc.RPCCommand) (any, error) {
	slog.Info("Received abort")
	c.Ag.Abort()
	return nil, nil
}

// handleFollowUp processes a follow-up message command.
func (c *RPCCore) handleFollowUp(cmd rpc.RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received follow_up:", "value", message)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty follow-up message")
	}

	// Expand /skill:name commands
	expandedMessage := c.ExpandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in follow_up", "original", message, "skill", skill.ExtractSkillName(message))
	}

	c.StateMu.Lock()
	mode := c.FollowUpMode
	c.StateMu.Unlock()
	if mode == "one-at-a-time" && c.Ag.GetPendingFollowUps() > 0 {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}
	return nil, c.Ag.FollowUp(expandedMessage)
}
