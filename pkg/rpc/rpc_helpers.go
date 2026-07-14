package rpc

import (
	"log/slog"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
)

func (app *rpcApp) buildSystemPrompt(currentSess *session.Session) string {
	// --system-prompt overrides everything (even role config).
	if app.customSystemPrompt != "" {
		slog.Info("Using custom system prompt", "length", len(app.customSystemPrompt))
		return app.customSystemPrompt
	}
	// Role config system prompt (from ~/.ai/roles/<name>/agent.yaml) is next.
	if app.agentConfig != nil {
		sp, err := app.agentConfig.ResolveSystemPrompt()
		if err != nil {
			slog.Error("Failed to resolve agent config system prompt", "error", err)
			// Fall through to default logic
		} else {
			slog.Info("Using agent config system prompt", "length", len(sp))
			return sp
		}
	}
	// Default: embedded coder prompt.
	promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)
	promptBuilder.SetTools(app.registry.All()).SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats)

	return promptBuilder.Build()
}

func (app *rpcApp) buildAgentContextPrefix() string {
	var parts []string

	promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)

	// Skills section
	promptBuilderForSkills := prompt.NewBuilderWithWorkspace("", app.ws)
	promptBuilderForSkills.SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats)
	if skills := promptBuilderForSkills.BuildSkillsMessage(); skills != "" {
		parts = append(parts, skills)
	}

	// Instructions section (AGENTS.md)
	if instructions := promptBuilder.BuildInstructionsMessage(); instructions != "" {
		parts = append(parts, instructions)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func (app *rpcApp) createBaseContext() *agentctx.AgentContext {
	app.systemPrompt = app.buildSystemPrompt(app.sess)
	app.agentContextPrefix = app.buildAgentContextPrefix()
	// Keep loopCfg in sync if it has been constructed (createBaseContext
	// may be re-invoked on session resume while loopCfg already exists).
	if app.loopCfg != nil {
		app.loopCfg.AgentContextPrefix = app.agentContextPrefix
	}
	// Sync prefix to compactor: AgentContext.AgentContextPrefix has json:"-"
	// and is lost on checkpoint/restore, so the compactor stores its own copy.
	if app.compactor != nil {
		app.compactor.SetAgentContextPrefix(app.agentContextPrefix)
		app.compactor.SetThinkingLevel(app.currentThinkingLevel)
	}
	ctx := agentctx.NewAgentContext(app.systemPrompt)
	ctx.AgentContextPrefix = app.agentContextPrefix
	for _, tool := range app.registry.All() {
		ctx.AddTool(tool)
	}
	if app.sess != nil {
		sessionDir := app.sess.GetDir()
		ctx.RecentMessages = app.sess.GetMessages()
		if sessionDir != "" {
			// Resume path: load AgentState from agent_state.json.
			// Messages come from sess.GetMessages() (source of truth).
			msgs, agentState, err := agent.LoadResumeState(sessionDir, ctx.RecentMessages)
			if err != nil {
				slog.Warn("Resume state load failed, using session messages",
					"error", err,
					"session_messages", len(ctx.RecentMessages))
			} else {
				ctx.RecentMessages = msgs
				if agentState != nil {
					ctx.AgentState = agentState
					if agentState.CurrentWorkingDir != "" {
						if err := app.ws.SetCWD(agentState.CurrentWorkingDir); err != nil {
							slog.Warn("Failed to restore CWD from checkpoint",
								"cwd", agentState.CurrentWorkingDir, "error", err)
						}
					}
					slog.Info("Restored agent state from checkpoint",
						"turns", agentState.TotalTurns,
						"tokens", agentState.TokensUsed,
						"toolCallsSince", agentState.ToolCallsSinceLastTrigger,
						"cwd", agentState.CurrentWorkingDir,
					)
				}
			}
		}
	}
	return ctx
}

func (app *rpcApp) setAgentContext(ctx *agentctx.AgentContext) {
	app.ag.SetContext(ctx)
}

func (app *rpcApp) expandSkillCommands(text string) string {
	if app.skillResult == nil || app.skillStats == nil {
		return text
	}
	expanded := skill.ExpandCommand(text, app.skillResult.Skills)
	if skill.IsSkillCommand(text) {
		skillName := skill.ExtractSkillName(text)
		app.skillStats.RecordUsage(skillName)
		if err := app.skillStats.Save(); err != nil {
			slog.Error("Failed to save skill stats", "skill", skillName, "error", err)
		}
	}
	return expanded
}

// appendCompactionHint is defined in pkg/agent/loop_state.go.
