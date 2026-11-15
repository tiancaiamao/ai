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

// ensureAgentContextPrefix returns the agent-context prefix for the current
// session and working directory, building it only when that key changes.
//
// The prefix must stay frozen for the whole session: buildAgentContextPrefix
// advances the skills exploration rotation (and persists it), so rebuilding
// returns a different block every time. The prefix is message #0 of every
// request, so regenerating it mid-session throws away the provider prefix cache
// for the entire conversation and makes the compaction ask diverge from normal
// turns (which forces a full re-prefill on every ask).
func (app *App) ensureAgentContextPrefix() string {
	cwd := ""
	if app.ws != nil {
		cwd = app.ws.GetCWD()
	}
	key := app.sessionID + "\x00" + cwd
	if app.agentContextPrefixKey != key {
		app.agentContextPrefix = app.buildAgentContextPrefix()
		app.agentContextPrefixKey = key
	}
	return app.agentContextPrefix
}

func (app *App) buildAgentContextPrefix() string {
	var parts []string

	promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)

	// Skills section
	promptBuilderForSkills := prompt.NewBuilderWithWorkspace("", app.ws)
	promptBuilderForSkills.SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats).
		SetSkillWarnings(skillLoadWarnings(app.skillResult))
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

// skillLoadWarnings extracts user-actionable skill loading warnings from the
// loader diagnostics — currently the legacy .ai/skills deprecation notice —
// so they reach the agent via the <agent:skills> block, not just the log.
func skillLoadWarnings(result *skill.LoadResult) []string {
	if result == nil {
		return nil
	}
	var warnings []string
	for _, d := range result.Diagnostics {
		if d.Type == "warning" && d.Code == skill.DiagnosticCodeLegacyProjectSkills {
			warnings = append(warnings, d.Message)
		}
	}
	return warnings
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
	app.agentContextPrefix = app.ensureAgentContextPrefix()
	// Keep loopCfg in sync if it has been constructed (createBaseContext
	// may be re-invoked on session resume while loopCfg already exists).
	if app.loopCfg != nil {
		app.loopCfg.AgentContextPrefix = app.agentContextPrefix
	}
	// The running agent embeds its own copy of LoopConfig, so it needs the
	// prefix pushed explicitly or it keeps sending the one from construction.
	if app.ag != nil {
		app.ag.SetAgentContextPrefix(app.agentContextPrefix)
	}
	// Sync prefix to compactor: AgentContext.AgentContextPrefix has json:"-"
	// and is lost on checkpoint/restore, so the compactor stores its own copy.
	if app.compactor != nil {
		app.compactor.SetRunID(app.runID)
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
