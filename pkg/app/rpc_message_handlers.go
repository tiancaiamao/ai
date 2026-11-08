package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// --- Message and content handlers --@

func (app *App) handleCompact(args string) (any, error) {
	_ = args
	slog.Info("Received compact")

	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if streaming {
		app.ag.RequestCompaction()
		return map[string]any{"status": "queued"}, nil
	}

	return app.compactNow("manual_command", false)
}

// compactNow runs one compaction outside the agent loop and persists it.
// trigger identifies the caller in events and traces ("manual_command", "idle").
// On error the agent context is left untouched: compact.Compactor.Compact only
// mutates it after the summary has been generated, so the next request still
// sends the pre-compaction context.
func (app *App) compactNow(trigger string, auto bool) (*CompactResult, error) {
	agentCtx := app.ag.GetContext()
	beforeCount := len(agentCtx.RecentMessages)

	compactionInfo := agent.CompactionInfo{
		Auto:    auto,
		Before:  beforeCount,
		Trigger: trigger,
	}
	app.stateMu.Lock()
	app.isCompacting = true
	app.stateMu.Unlock()
	app.emitCompactionEvent(agent.NewCompactionStartEvent(compactionInfo))
	defer func() {
		app.stateMu.Lock()
		app.isCompacting = false
		app.stateMu.Unlock()
		app.emitCompactionEvent(agent.NewCompactionEndEvent(compactionInfo))
	}()

	var response *CompactResult
	err := runDetachedTraceSpan(
		"compaction",
		traceevent.CategoryEvent,
		[]traceevent.Field{{Key: "source", Value: trigger}},
		func(ctx context.Context, span *traceevent.Span) error {
			span.AddField("before_messages", beforeCount)

			result, err := app.compactor.Compact(ctx, agentCtx)
			if err != nil {
				slog.Info("Compact failed:", "trigger", trigger, "value", err)
				return err
			}
			if result == nil {
				return fmt.Errorf("compactor returned nil result")
			}

			afterCount := len(agentCtx.RecentMessages)
			span.AddField("after_messages", afterCount)
			span.AddField("tokens_before", result.TokensBefore)
			span.AddField("tokens_after", result.TokensAfter)
			compactionInfo.After = afterCount

			slog.Info("Compact successful", "trigger", trigger, "before", beforeCount, "after", afterCount)
			response = &CompactResult{
				TokensBefore: result.TokensBefore,
				TokensAfter:  result.TokensAfter,
			}

			// Persist compaction: save snapshot + append compaction entry.
			// messages.jsonl stays append-only.
			if app.sess != nil {
				if _, err := app.sess.AppendCompaction(
					result.Summary, agentCtx.RecentMessages,
				); err != nil {
					slog.Error("Failed to persist compaction", "trigger", trigger, "error", err)
				}
			}

			return nil
		},
	)
	if err != nil {
		compactionInfo.Error = err.Error()
		return nil, err
	}
	return response, nil
}

// emitCompactionEvent forwards a compaction event to the protocol sink when one
// is wired. Detached compaction paths also run before an emitter exists.
func (app *App) emitCompactionEvent(event agent.AgentEvent) {
	if app.emitEvent != nil {
		app.emitEvent(event)
	}
}

func (app *App) handleGetMessages(args string) (any, error) {
	slog.Info("Received get_messages", "args", args)
	const defaultCount = 20
	const maxPreviewLen = 200

	count := defaultCount
	args = strings.TrimSpace(args)
	if args != "" {
		if n, err := strconv.Atoi(args); err == nil && n > 0 {
			count = n
		}
	}

	messages := app.ag.GetMessages()
	return FormatMessagesForDisplay(messages, count, maxPreviewLen), nil
}

func (app *App) handleGetLastAssistantText(args string) (any, error) {
	_ = args
	slog.Info("Received get_last_assistant_text")
	messages := app.ag.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return map[string]any{"text": messages[i].ExtractText()}, nil
		}
	}
	return "", nil
}

func (app *App) handleExportHTML(args string) (any, error) {
	slog.Info("Received export_html", "outputPath", args)
	return "", fmt.Errorf("export_html is not supported")
}

// registerMessageHandlers registers message-related slash commands.
func (app *App) registerMessageHandlers() {
	app.commands.Register("messages", "Get formatted message summaries for the current session", func(args string) (any, error) {
		return app.handleGetMessages(args)
	})

	app.commands.Register("compact", "Compact conversation history to reduce context size", func(args string) (any, error) {
		return app.handleCompact(args)
	})

	app.commands.Register("export_html", "Export the current session as HTML", func(args string) (any, error) {
		return app.handleExportHTML(args)
	})

	app.commands.RegisterHidden("get_last_assistant_text", "Get the last assistant text response (internal)", func(args string) (any, error) {
		return app.handleGetLastAssistantText(args)
	})
}
