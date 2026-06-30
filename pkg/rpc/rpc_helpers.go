package rpc

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func (app *rpcApp) buildSystemPrompt(currentSess *session.Session) string {
	// Agent config overrides the default system prompt.
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
	if app.customSystemPrompt != "" {
		slog.Info("Using custom system prompt", "length", len(app.customSystemPrompt))
		return app.customSystemPrompt
	}
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

func (app *rpcApp) compactBeforeRequest(trigger string) {
	if app.compactor == nil {
		return
	}

	agentCtx := app.ag.GetContext()
	if !app.compactor.ShouldCompact(context.Background(), agentCtx) {
		return
	}

	beforeCount := len(agentCtx.RecentMessages)
	compactionInfo := agent.CompactionInfo{
		Auto:    true,
		Before:  beforeCount,
		Trigger: trigger,
	}

	app.stateMu.Lock()
	app.isCompacting = true
	app.stateMu.Unlock()
	app.server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))

	err := runDetachedTraceSpan(
		"compaction",
		traceevent.CategoryEvent,
		[]traceevent.Field{
			{Key: "source", Value: "pre_request"},
			{Key: "auto", Value: true},
			{Key: "trigger", Value: trigger},
			{Key: "before_messages", Value: beforeCount},
		},
		func(ctx context.Context, span *traceevent.Span) error {
			result, err := app.compactor.Compact(ctx, agentCtx)
			if err != nil {
				return err
			}

			afterCount := len(agentCtx.RecentMessages)
			compactionInfo.After = afterCount

			span.AddField("after_messages", afterCount)
			if result != nil {
				span.AddField("tokens_before", result.TokensBefore)
				span.AddField("tokens_after", result.TokensAfter)
			}

			// Persist compaction: save snapshot + append compaction entry.
			// messages.jsonl stays append-only.
			if result != nil && app.sess != nil {
				if _, err := app.sess.AppendCompaction(
					result.Summary, agentCtx.RecentMessages,
				); err != nil {
					slog.Error("Failed to persist pre-request compaction", "error", err)
				}
			}
			return nil
		},
	)

	app.stateMu.Lock()
	app.isCompacting = false
	app.stateMu.Unlock()

	if err != nil {
		compactionInfo.Error = err.Error()
		slog.Error("Pre-request compaction failed", "trigger", trigger, "error", err)

		// Nuclear fallback: after consecutive compaction failures, force-truncate
		// oldest messages without LLM summary. This prevents permanent session death
		// when the summarization model itself cannot handle the conversation size.
		const maxConsecutiveFailures = 3
		app.stateMu.Lock()
		app.consecutiveCompactionFailures++
		failures := app.consecutiveCompactionFailures
		app.stateMu.Unlock()

		if failures >= maxConsecutiveFailures {
			slog.Warn("[Compact] Nuclear fallback: force-truncating oldest messages after consecutive compaction failures",
				"failures", failures,
				"trigger", trigger)
			app.nuclearTruncate()
		}
	} else {
		app.stateMu.Lock()
		app.consecutiveCompactionFailures = 0
		app.stateMu.Unlock()

		// Inject a post-compaction hint so the LLM knows to reload
		// skills and design docs that were lost during compaction.
		injectCompactionHint(agentCtx)
	}
	app.server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
}

// injectCompactionHint inserts an ephemeral user message at the beginning of
// RecentMessages (right after the compaction summary) to remind the agent that
// compaction just occurred. Skills and design docs loaded earlier are now lost
// — the agent should reload them if needed before proceeding.
func injectCompactionHint(agentCtx *agentctx.AgentContext) {
	const hint = `<agent:hint>
Context was just compacted. The summary above lists skills that were loaded — their full content is now LOST from context. If you need to use any of those skills (e.g. pge, subagent, grill-me), reload them via find_skill(name="<skill>", load=true) BEFORE acting. Similarly, re-read any design docs or important files you were working with. Don't proceed on stale memory.
</agent:hint>`

	msg := agentctx.NewUserMessage(hint).
		WithKind("compaction_hint").
		WithVisibility(true, false)

	// Insert at the beginning so it appears right after the compaction summary.
	agentCtx.RecentMessages = append([]agentctx.AgentMessage{msg}, agentCtx.RecentMessages...)
}
