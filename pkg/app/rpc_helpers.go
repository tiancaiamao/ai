package app

import (
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
)

func (app *App) buildSystemPrompt(currentSess *session.Session) string {
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

func (app *App) buildAgentContextPrefix() string {
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

func (app *App) createBaseContext() *agentctx.AgentContext {
	// Resume path: restore the workspace CWD recorded in the session's
	// meta.json (persisted on agent_end), before the system prompt is built
	// (the prompt embeds the current working directory).
	if app.sess != nil && app.sessionMgr != nil {
		if meta, err := app.sessionMgr.GetMeta(app.sessionID); err == nil && meta.CurrentWorkdir != "" {
			if err := app.ws.SetCWD(meta.CurrentWorkdir); err != nil {
				slog.Warn("Failed to restore session workdir",
					"cwd", meta.CurrentWorkdir, "error", err)
			} else {
				slog.Info("Restored session workdir", "cwd", meta.CurrentWorkdir)
			}
		}
	}
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
		ctx.RecentMessages = app.sess.GetMessages()
	}
	return ctx
}

func (app *App) setAgentContext(ctx *agentctx.AgentContext) {
	app.ag.SetContext(ctx)
}

func (app *App) expandSkillCommands(text string) string {
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
