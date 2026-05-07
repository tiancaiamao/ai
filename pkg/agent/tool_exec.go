package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

func executeToolCalls(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	tools []agentctx.Tool,
	allowedTools map[string]bool,
	assistantMsg *agentctx.AgentMessage,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	executor ToolExecutor,
	_ *Metrics,
	toolOutputLimits ToolOutputLimits,
) []agentctx.AgentMessage {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return nil
	}

	type toolExecutionPlan struct {
		index      int
		normalized agentctx.ToolCallContent
		tool       agentctx.Tool
		span       *traceevent.Span
	}
	type toolExecutionOutcome struct {
		plan     toolExecutionPlan
		content  []agentctx.ContentBlock
		err      error
		duration time.Duration
	}

	resultsByIndex := make([]*agentctx.AgentMessage, len(toolCalls))
	plans := make([]toolExecutionPlan, 0, len(toolCalls))
	toolsByName := make(map[string]agentctx.Tool, len(tools))
	for _, tool := range tools {
		toolsByName[tool.Name()] = tool
	}
	availableToolNames := make([]string, 0, len(toolsByName))
	for name := range toolsByName {
		availableToolNames = append(availableToolNames, name)
	}
	sort.Strings(availableToolNames)

	for i, tc := range toolCalls {
		rawName := strings.ToLower(strings.TrimSpace(tc.Name))
		normalized := normalizeToolCall(tc)
		toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "raw_name", Value: rawName},
		)
		if normalized.Name != rawName {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_normalized",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "normalized_args", Value: normalized.Arguments},
			)
		}
		if isGenericToolName(normalized.Name) {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] unresolved tool call name",
				"rawName", rawName,
				"normalizedName", normalized.Name,
				"availableTools", availableToolNames)
		}
		args, argErr := coerceToolArguments(normalized.Name, normalized.Arguments)
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "args", Value: normalized.Arguments},
		)
		if argErr != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", argErr.Error())
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_invalid_args",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "error", Value: argErr.Error()},
			)
			errorMsg := buildInvalidToolArgsMessage(normalized.Name, argErr, assistantMsg.StopReason)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: errorMsg},
			}, true)
			stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: argErr.Error()},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		normalized.Arguments = args
		stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))

		tool := toolsByName[normalized.Name]
		if tool == nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not found")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] tool not registered",
				"tool", normalized.Name,
				"rawName", rawName,
				"availableTools", availableToolNames)
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "agentctx.Tool not found"},
			}, toolOutputLimits, normalized.Name)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not found"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		// Check if tool is allowed by whitelist
		if allowedTools != nil && !allowedTools[normalized.Name] {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not allowed")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_not_allowed",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
			)
			slog.Warn("[Loop] tool not allowed by whitelist",
				"tool", normalized.Name,
				"toolCallID", normalized.ID)
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("agentctx.Tool %q is not allowed in this context", normalized.Name)},
			}, toolOutputLimits, normalized.Name)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not allowed"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		plans = append(plans, toolExecutionPlan{
			index:      i,
			normalized: normalized,
			tool:       tool,
			span:       toolSpan,
		})
	}

	outcomes := make(chan toolExecutionOutcome, len(plans))
	var wg sync.WaitGroup
	toolExecCtx := agentctx.WithToolExecutionAgentContext(ctx, agentCtx)

	for _, plan := range plans {
		wg.Add(1)
		go func(plan toolExecutionPlan) {
			defer wg.Done()
			executionCtx := agentctx.WithToolExecutionCallID(toolExecCtx, plan.normalized.ID)

			start := time.Now()
			var content []agentctx.ContentBlock
			var err error
			if executor != nil {
				content, err = executor.Execute(executionCtx, plan.tool, plan.normalized.Arguments)
			} else {
				content, err = plan.tool.Execute(executionCtx, plan.normalized.Arguments)
			}

			outcomes <- toolExecutionOutcome{
				plan:     plan,
				content:  content,
				err:      err,
				duration: time.Since(start),
			}
		}(plan)
	}

	wg.Wait()
	close(outcomes)

	outcomeByIndex := make(map[int]toolExecutionOutcome, len(plans))
	for outcome := range outcomes {
		outcomeByIndex[outcome.plan.index] = outcome
	}

	for _, plan := range plans {
		outcome, ok := outcomeByIndex[plan.index]
		if !ok {
			continue
		}
		var result agentctx.AgentMessage
		if outcome.err != nil {
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: outcome.err.Error()},
			}, toolOutputLimits, plan.normalized.Name)
			result = agentctx.NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, true))
			plan.span.AddField("error", true)
			plan.span.AddField("error_message", outcome.err.Error())
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: outcome.err.Error()},
			)
		} else {
			content := truncateToolContent(ctx, outcome.content, toolOutputLimits, plan.normalized.Name)
			result = agentctx.NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, false)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, false))
			plan.span.AddField("error", false)
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: false},
			)
		}

		resultCopy := result
		resultsByIndex[plan.index] = &resultCopy
	}

	results := make([]agentctx.AgentMessage, 0, len(toolCalls))
	for _, result := range resultsByIndex {
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func buildInvalidToolArgsMessage(toolName string, argErr error, stopReason string) string {
	if isLikelyTruncatedToolArguments(stopReason, argErr) {
		return buildTruncatedToolArgsMessage(toolName, argErr)
	}

	errorMsg := fmt.Sprintf("Invalid tool arguments for '%s': %v\n\nCorrect format:\n", toolName, argErr)
	switch toolName {
	case "read":
		errorMsg += `<read>
  <path>file.txt</path>
</read>`
	case "write":
		errorMsg += `<write>
  <path>file.txt</path>
  <content>content here</content>
</write>`
	case "edit":
		errorMsg += `<edit>
  <path>file.txt</path>
  <oldText>old text</oldText>
  <newText>new text</newText>
</edit>`
	case "bash":
		errorMsg += `<bash>
  <command>your command here</command>
</bash>

Alternatively:
<bash>command here</bash>`
	case "grep":
		errorMsg += `<grep>
  <pattern>search pattern</pattern>
  <path>optional path</path>
</grep>`
	}
	return errorMsg
}

func isLikelyTruncatedToolArguments(stopReason string, argErr error) bool {
	if argErr == nil {
		return false
	}
	if strings.TrimSpace(stopReason) != "length" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(argErr.Error())), "missing ")
}

func buildTruncatedToolArgsMessage(toolName string, argErr error) string {
	msg := fmt.Sprintf(
		"Tool call arguments for '%s' were truncated because the assistant response hit max_tokens (stopReason=length).\n\n"+
			"This is a truncation issue, not a normal schema mistake.\n"+
			"Please resend the SAME tool call with COMPLETE arguments (all required fields) in one response.\n"+
			"Validation error after truncation: %v\n\n"+
			"Expected format:\n",
		toolName,
		argErr,
	)

	switch toolName {
	case "read":
		msg += `<read>
  <path>file.txt</path>
</read>`
	case "write":
		msg += `<write>
  <path>file.txt</path>
  <content>content here</content>
</write>`
	case "edit":
		msg += `<edit>
  <path>file.txt</path>
  <oldText>old text</oldText>
  <newText>new text</newText>
</edit>`
	case "bash":
		msg += `<bash>
  <command>your command here</command>
</bash>

Alternatively:
<bash>command here</bash>`
	case "grep":
		msg += `<grep>
  <pattern>search pattern</pattern>
  <path>optional path</path>
</grep>`
	}

	return msg
}

func hasToolResultNamed(results []agentctx.AgentMessage, toolName string) bool {
	for _, r := range results {
		if r.ToolName == toolName {
			return true
		}
	}
	return false
}
