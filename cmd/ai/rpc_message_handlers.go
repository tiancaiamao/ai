package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// --- Message and content handlers --@

func (app *rpcApp) handleCompact(args string) (any, error) {
	_ = args
	slog.Info("Received compact")
	// Reload messages from session if agent context is empty (e.g. legacy
	// session resumed in handoff mode without checkpoint structure).
	if len(app.ag.GetMessages()) == 0 && app.sess != nil {
		app.ag.GetContext().RecentMessages = app.sess.GetMessages()
	}

	beforeCount := len(app.ag.GetMessages())

	estimatedTokens := app.compactor.EstimateTokens(app.ag.GetMessages())
	keepTokens := app.compactor.KeepRecentTokens()

	if !app.sess.CanCompact(app.compactor) {
		if estimatedTokens < keepTokens {
			return nil, fmt.Errorf("all %d messages (%d tokens) fit within keep-recent budget (%d tokens); no compaction needed",
				beforeCount, estimatedTokens, keepTokens)
		}
		return nil, fmt.Errorf("no messages available for compaction (all within retention window)")
	}

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
		func(_ context.Context, span *traceevent.Span) error {
			span.AddField("before_messages", beforeCount)

			result, err := app.sess.Compact(app.compactor)
			if err != nil {
				slog.Info("Compact failed:", "value", err)
				return err
			}

			app.ag.GetContext().RecentMessages = app.sess.GetMessages()

			afterCount := len(app.ag.GetMessages())
			span.AddField("after_messages", afterCount)
			span.AddField("tokens_before", result.TokensBefore)
			span.AddField("tokens_after", result.TokensAfter)
			compactionInfo.After = afterCount

			slog.Info("Compact successful", "before", beforeCount, "after", afterCount)
			response = &rpc.CompactResult{
				FirstKeptEntryID: result.FirstKeptEntryID,
				TokensBefore:     result.TokensBefore,
				TokensAfter:      result.TokensAfter,
			}

			if app.checkpointMgr != nil && app.checkpointMgr.ShouldCheckpoint() {
				agentCtx := app.ag.GetContext()
				slog.Info("[Loop] Creating checkpoint after manual compact", "trigger", "manual_command", "turn", agentCtx.AgentState.TotalTurns)
				checkpointTurn, err := app.checkpointMgr.CreateSnapshot(agentCtx, agentCtx.LLMContext, agentCtx.AgentState.TotalTurns)
				if err != nil {
					slog.Warn("[Loop] Failed to create checkpoint after manual compact", "error", err, "turn", agentCtx.AgentState.TotalTurns)
				} else {
					slog.Info("[Loop] Checkpoint created after manual compact", "trigger", "manual_command", "checkpoint_turn", checkpointTurn)
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

func (app *rpcApp) handleHandoff(args string) (any, error) {
	_ = args

	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if streaming {
		return nil, fmt.Errorf("agent is busy")
	}

	sessionDir := app.sess.GetDir()
	if sessionDir == "" {
		return nil, fmt.Errorf("handoff requires a session directory")
	}

	// Initialize handoff checkpoint structure if current.txt does not exist.
	// This handles resumed legacy sessions that have messages but no checkpoint
	// directory. The existing messages are imported into cp_001.
	if _, err := os.Stat(filepath.Join(sessionDir, "current.txt")); err != nil {
		slog.Info("[Handoff] Initializing handoff structure for legacy session")
		entries := app.sess.GetEntries()
		var msgEntries []session.SessionEntry
		for _, e := range entries {
			if e.Type == session.EntryTypeMessage {
				cp := e
				msgEntries = append(msgEntries, cp)
			}
		}
		if err := session.InitHandoffFromExisting(sessionDir, msgEntries); err != nil {
			return nil, fmt.Errorf("init handoff session: %w", err)
		}
	}

	// Reload messages from session if agent context is empty. This can happen
	// when a legacy session was resumed in handoff mode without checkpoint
	// structure — the resume path returns empty messages in that case.
	if len(app.ag.GetMessages()) == 0 && app.sess != nil {
		app.ag.GetContext().RecentMessages = app.sess.GetMessages()
	}

	beforeCount := len(app.ag.GetMessages())
	if beforeCount == 0 {
		return nil, fmt.Errorf("no messages to handoff")
	}
	slog.Info("[Handoff] Manual handoff triggered", "before_messages", beforeCount)

	ctx := context.Background()
	if err := app.ag.Handoff(ctx); err != nil {
		return nil, fmt.Errorf("handoff failed: %w", err)
	}

	afterCount := len(app.ag.GetMessages())
	checkpoint, _ := session.GetCurrentCheckpoint(sessionDir)
	slog.Info("[Handoff] Manual handoff complete",
		"checkpoint", checkpoint,
		"before_messages", beforeCount,
		"after_messages", afterCount)

	return map[string]any{
		"status":          "ok",
		"checkpoint":      checkpoint,
		"before_messages": beforeCount,
		"after_messages":  afterCount,
	}, nil
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
	return formatMessagesForDisplay(messages, count, maxPreviewLen), nil
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

func (app *rpcApp) handleGetWorkflowStatus(args string) (any, error) {
	_ = args
	slog.Info("Received get_workflow_status")
	status, err := getWorkflowStatus(app.ws.GetCWD())
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, nil
	}
	return status, nil
}

// registerHandlers registers all RPC command handlers and slash commands on the server.

func getWorkflowStatus(cwd string) (*rpc.WorkflowState, error) {
	state := &rpc.WorkflowState{
		Phase:      "not_started",
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
	}

	workflowDir := filepath.Join(cwd, ".workflow")
	stateFile := filepath.Join(workflowDir, "state.json")

	// Read state.json if it exists
	if data, err := os.ReadFile(stateFile); err == nil {
		var stateData struct {
			Phase     string `json:"phase"`
			StartedAt string `json:"started_at"`
			TasksFile string `json:"tasks_file"`
		}
		if err := json.Unmarshal(data, &stateData); err == nil {
			state.Phase = stateData.Phase
			state.StartedAt = stateData.StartedAt
			if stateData.TasksFile != "" {
				// Handle relative or absolute path
				if filepath.IsAbs(stateData.TasksFile) {
					state.TasksFile = stateData.TasksFile
				} else {
					state.TasksFile = filepath.Join(cwd, stateData.TasksFile)
				}
			}
		}
	}

	// Read tasks.md if specified
	if state.TasksFile != "" {
		tasksData, err := os.ReadFile(state.TasksFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read tasks file %s: %w", state.TasksFile, err)
		}

		// Parse task statuses
		lines := strings.Split(string(tasksData), "\n")
		var inProgressTask *rpc.WorkflowTask

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- [") {
				continue
			}

			status := "pending"
			if strings.HasPrefix(line, "- [x]") || strings.HasPrefix(line, "- [X]") {
				status = "done"
				state.DoneTasks++
			} else if strings.HasPrefix(line, "- [-]") {
				status = "in_progress"
				state.PendingTasks++ // In-progress also counts toward pending
			} else if strings.HasPrefix(line, "- [!]") {
				status = "failed"
				state.FailedTasks++
			} else {
				state.PendingTasks++
			}

			state.TotalTasks++

			// Extract task ID and description for in-progress task
			if status == "in_progress" && inProgressTask == nil {
				// Extract task ID (e.g., TASK001, T01, etc.)
				var id string
				idMatch := regexp.MustCompile(`[A-Z]{3,}\d+|[A-Z]\d+`).FindString(line)
				if idMatch != "" {
					id = idMatch
				}

				// Extract description: remove checkbox first, then task ID
				desc := line
				// Remove checkbox
				desc = regexp.MustCompile(`^-\s*\[[xX\-\!]\]\s*`).ReplaceAllString(desc, "")
				desc = regexp.MustCompile(`^-\s*\[\s*\]\s*`).ReplaceAllString(desc, "")
				// Remove task ID (e.g., TASK002: or TASK002 )
				desc = regexp.MustCompile(`^[A-Z]{3,}\d+:?\s*`).ReplaceAllString(desc, "")
				desc = strings.TrimSpace(desc)

				inProgressTask = &rpc.WorkflowTask{
					ID:          id,
					Description: desc,
					Status:      status,
				}
			}
		}

		state.InProgressTask = inProgressTask
	}

	return state, nil
}

// formatMessagesForDisplay converts AgentMessages into a structured summary for the /messages command.
// It returns the last `count` messages with previews truncated to maxPreviewLen characters.

func formatMessagesForDisplay(messages []agentctx.AgentMessage, count int, maxPreviewLen int) rpc.MessagesResult {
	total := len(messages)

	start := total - count
	if start < 0 {
		start = 0
	}
	showing := total - start

	formatted := make([]rpc.FormattedMessage, 0, showing)
	for i := start; i < total; i++ {
		msg := messages[i]
		fm := rpc.FormattedMessage{
			Index: i,
			Role:  msg.Role,
		}

		// Build preview from text content
		preview := msg.ExtractText()
		if preview == "" {
			// Try thinking content as fallback for assistant messages
			if thinking := msg.ExtractThinking(); thinking != "" {
				preview = "(thinking) " + thinking
			}
		}
		if len(preview) > maxPreviewLen {
			preview = preview[:maxPreviewLen] + "..."
		}
		fm.Preview = preview

		// Extract tool call names for assistant messages
		toolCalls := msg.ExtractToolCalls()
		if len(toolCalls) > 0 {
			names := make([]string, 0, len(toolCalls))
			for _, tc := range toolCalls {
				names = append(names, tc.Name)
			}
			fm.ToolCalls = names
		}

		// Include tool name for tool results
		if msg.ToolName != "" {
			fm.ToolName = msg.ToolName
		}
		fm.IsError = msg.IsError

		formatted = append(formatted, fm)
	}

	return rpc.MessagesResult{
		Total:    total,
		Showing:  showing,
		Messages: formatted,
	}
}

// registerMessageHandlers registers message-related slash commands.
func (app *rpcApp) registerMessageHandlers() {
	app.server.RegisterSlash("messages", "Get formatted message summaries for the current session", func(args string) (any, error) {
		return app.handleGetMessages(args)
	})

	app.server.RegisterSlash("compact", "Compact conversation history to reduce context size", func(args string) (any, error) {
		return app.handleCompact(args)
	})

	app.server.RegisterSlash("handoff", "Manually trigger context handoff (generates handoff doc, runs Q&A, creates new checkpoint)", func(args string) (any, error) {
		return app.handleHandoff(args)
	})

	app.server.RegisterSlash("export_html", "Export the current session as HTML", func(args string) (any, error) {
		return app.handleExportHTML(args)
	})

	app.server.RegisterHiddenSlash("get_workflow_status", "Get workflow task status (internal)", func(args string) (any, error) {
		return app.handleGetWorkflowStatus(args)
	})

	app.server.RegisterHiddenSlash("get_last_assistant_text", "Get the last assistant text response (internal)", func(args string) (any, error) {
		return app.handleGetLastAssistantText(args)
	})
}
