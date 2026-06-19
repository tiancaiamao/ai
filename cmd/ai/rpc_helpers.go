package main

import (
	"context"
	"log/slog"
	"path/filepath"
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

	if currentSess != nil {
		sessionDir := currentSess.GetDir()
		if sessionDir != "" {
			wm := agentctx.NewLLMContext(sessionDir)
			_ = wm
		}
	}

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

func (app *rpcApp) restoreLLMContextFromCompaction(sess *session.Session) {
	summary := sess.GetLastCompactionSummary()
	if summary == "" {
		slog.Info("[resume-on-branch] No compaction summary found, skipping llm context restore")
		return
	}

	sessionDir := sess.GetDir()
	if sessionDir == "" {
		slog.Warn("[resume-on-branch] No session directory, cannot restore llm context")
		return
	}

	wm := agentctx.NewLLMContext(sessionDir)
	if err := wm.WriteContent(summary); err != nil {
		slog.Warn("[resume-on-branch] Failed to restore llm context", "error", err)
	} else {
		slog.Info("[resume-on-branch] Restored llm context from compaction summary", "summary_len", len(summary))
	}
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
	}
	ctx := agentctx.NewAgentContext(app.systemPrompt)
	ctx.AgentContextPrefix = app.agentContextPrefix
	for _, tool := range app.registry.All() {
		ctx.AddTool(tool)
	}
	if app.sess != nil {
		sessionDir := app.sess.GetDir()
		if sessionDir != "" {
			ctx.LLMContext = ""
		}
		ctx.RecentMessages = app.sess.GetMessages()
		if sessionDir != "" {
			// Resume path: load the latest checkpoint and replay any
			// journal entries written AFTER the checkpoint. This is the
			// single source of truth — see pkg/agent/resume.go.
			msgs, llmCtx, agentState, err := agent.LoadResumeState(sessionDir, ctx.RecentMessages)
			if err != nil {
				slog.Warn("Resume state load failed, using session messages",
					"error", err,
					"session_messages", len(ctx.RecentMessages))
			} else {
				if len(msgs) != len(ctx.RecentMessages) {
					slog.Info("Resumed messages from checkpoint + journal replay",
						"resumed_messages", len(msgs),
						"session_messages", len(ctx.RecentMessages),
						"replayed", len(msgs)-len(ctx.RecentMessages))
				}
				ctx.RecentMessages = msgs
				if llmCtx != "" {
					ctx.LLMContext = llmCtx
				}
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

			// Legacy fallback: if LoadResumeState returned no AgentState
			// (e.g. a very old checkpoint with only agent_state.json and
			// no RecentMessages), try loading agent_state.json directly.
			if ctx.AgentState == nil {
				if cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir); err == nil && cpInfo != nil {
					cpPath := filepath.Join(sessionDir, cpInfo.Path)
					if savedState, err := agentctx.LoadCheckpointAgentState(cpPath); err == nil {
						ctx.AgentState = savedState
						if savedState.CurrentWorkingDir != "" {
							if err := app.ws.SetCWD(savedState.CurrentWorkingDir); err != nil {
								slog.Warn("Failed to restore CWD from checkpoint",
									"cwd", savedState.CurrentWorkingDir, "error", err)
							}
						}
						slog.Info("Restored agent state from checkpoint (legacy, no messages)",
							"turns", savedState.TotalTurns,
							"tokens", savedState.TokensUsed,
							"toolCallsSince", savedState.ToolCallsSinceLastTrigger,
							"cwd", savedState.CurrentWorkingDir,
						)
					}
				}
			}
		}
		ctx.OnCompactEvent = func(detail *agentctx.CompactEventDetail) error {
			return app.sess.AppendCompactEvent(detail)
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
	}
	app.server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
}

func (app *rpcApp) updateCheckpointManager() error {
	app.stateMu.Lock()
	defer app.stateMu.Unlock()

	if app.checkpointMgr != nil {
		if err := app.checkpointMgr.Close(); err != nil {
			slog.Warn("Failed to close old checkpoint manager", "error", err)
		}
	}

	if app.sess != nil {
		sessionDir := app.sess.GetDir()
		if sessionDir != "" {
			mgr, err := agent.NewAgentContextCheckpointManager(sessionDir)
			if err != nil {
				slog.Warn("Failed to create checkpoint manager", "error", err)
				app.checkpointMgr = nil
			} else {
				app.checkpointMgr = mgr
				slog.Info("Updated checkpoint manager", "sessionDir", sessionDir)
			}
		} else {
			slog.Warn("Session directory is empty, checkpoint manager not updated")
			app.checkpointMgr = nil
		}
	}

	return nil
}
