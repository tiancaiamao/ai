package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/rpc"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// --- Message and content handlers --@

func (app *rpcApp) handleCompact(args string) (any, error) {
	_ = args
	slog.Info("Received compact")
	agentCtx := app.ag.GetContext()
	beforeCount := len(agentCtx.RecentMessages)

	compactionInfo := agent.CompactionInfo{
		Auto:    false,
		Before:  beforeCount,
		Trigger: "manual_command",
	}
	app.stateMu.Lock()
	app.isCompacting = true
	app.stateMu.Unlock()
	app.server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))
	defer func() {
		app.stateMu.Lock()
		app.isCompacting = false
		app.stateMu.Unlock()
		app.server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
	}()

	var response *rpc.CompactResult
	err := runDetachedTraceSpan(
		"compaction",
		traceevent.CategoryEvent,
		[]traceevent.Field{{Key: "source", Value: "manual"}},
		func(ctx context.Context, span *traceevent.Span) error {
			span.AddField("before_messages", beforeCount)

			result, err := app.compactor.Compact(ctx, agentCtx)
			if err != nil {
				slog.Info("Compact failed:", "value", err)
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

			slog.Info("Compact successful", "before", beforeCount, "after", afterCount)
			response = &rpc.CompactResult{
				TokensBefore: result.TokensBefore,
				TokensAfter:  result.TokensAfter,
			}

			// Persist compaction: save snapshot + append compaction entry.
			// messages.jsonl stays append-only.
			if app.sess != nil {
				if _, err := app.sess.AppendCompaction(
					result.Summary, agentCtx.RecentMessages,
				); err != nil {
					slog.Error("Failed to persist manual compaction", "error", err)
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

func (app *rpcApp) handleGetMessages(args string) (any, error) {
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
	return rpc.FormatMessagesForDisplay(messages, count, maxPreviewLen), nil
}

func (app *rpcApp) handleGetLastAssistantText(args string) (any, error) {
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

func (app *rpcApp) handleExportHTML(args string) (any, error) {
	slog.Info("Received export_html", "outputPath", args)
	return "", fmt.Errorf("export_html is not supported")
}

// registerMessageHandlers registers message-related slash commands.
func (app *rpcApp) registerMessageHandlers() {
	app.server.RegisterSlash("messages", "Get formatted message summaries for the current session", func(args string) (any, error) {
		return app.handleGetMessages(args)
	})

	app.server.RegisterSlash("compact", "Compact conversation history to reduce context size", func(args string) (any, error) {
		return app.handleCompact(args)
	})

	app.server.RegisterSlash("export_html", "Export the current session as HTML", func(args string) (any, error) {
		return app.handleExportHTML(args)
	})

	app.server.RegisterHiddenSlash("get_last_assistant_text", "Get the last assistant text response (internal)", func(args string) (any, error) {
		return app.handleGetLastAssistantText(args)
	})
}
