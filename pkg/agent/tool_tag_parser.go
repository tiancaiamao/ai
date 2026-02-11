package agent

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

type toolTagCall struct {
	name     string
	args     map[string]any
	start    int
	end      int
	consumed bool
}

var (
	toolTagRegex   = regexp.MustCompile(`(?is)<(read_file|read|write|edit|bash|grep)>\s*(.*?)\s*</(read_file|read|write|edit|bash|grep)>`)
	argKeyValRegex = regexp.MustCompile(`(?is)<arg_key>\s*(.*?)\s*</arg_key>\s*<arg_value>\s*(.*?)\s*</arg_value>`)
	thinkTagRegex  = regexp.MustCompile(`(?is)</?think>`)
)

func injectToolCallsFromTaggedText(msg AgentMessage) (AgentMessage, bool) {
	if msg.Role != "assistant" {
		return msg, false
	}
	if len(msg.ExtractToolCalls()) > 0 {
		return msg, false
	}

	text := msg.ExtractText()
	if text == "" || !strings.Contains(text, "<") {
		return msg, false
	}

	normalized := thinkTagRegex.ReplaceAllString(text, "")
	if call, ok := parseArgKeyValueToolCall(normalized); ok {
		return buildToolCallMessage(msg, normalized, []toolTagCall{call}), true
	}
	matches := toolTagRegex.FindAllStringSubmatchIndex(normalized, -1)
	if len(matches) == 0 {
		return msg, false
	}

	calls := make([]toolTagCall, 0, len(matches))
	for _, match := range matches {
		if len(match) < 8 {
			continue
		}
		openName := strings.ToLower(normalized[match[2]:match[3]])
		body := normalized[match[4]:match[5]]
		closeName := strings.ToLower(normalized[match[6]:match[7]])
		if openName != closeName {
			continue
		}
		toolName, args, ok := parseToolTag(openName, body)
		if !ok {
			continue
		}
		calls = append(calls, toolTagCall{
			name:     toolName,
			args:     args,
			start:    match[0],
			end:      match[1],
			consumed: true,
		})
	}

	if len(calls) == 0 {
		return msg, false
	}
	return buildToolCallMessage(msg, normalized, calls), true
}

func buildToolCallMessage(msg AgentMessage, normalized string, calls []toolTagCall) AgentMessage {
	remaining := stripConsumedRanges(normalized, calls)
	remaining = strings.TrimSpace(remaining)

	content := make([]ContentBlock, 0, 1+len(calls))
	if remaining != "" {
		content = append(content, TextContent{
			Type: "text",
			Text: remaining,
		})
	}

	for i, call := range calls {
		content = append(content, ToolCallContent{
			ID:        fmt.Sprintf("tool_%d_%d", time.Now().UnixNano(), i),
			Type:      "toolCall",
			Name:      call.name,
			Arguments: call.args,
		})
	}

	msg.Content = content
	slog.Debug("[Loop] injected tool calls from tagged text", "count", len(calls))
	return msg
}

func stripConsumedRanges(text string, calls []toolTagCall) string {
	if len(calls) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, call := range calls {
		if !call.consumed {
			continue
		}
		if call.start > last {
			b.WriteString(text[last:call.start])
		}
		last = call.end
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return b.String()
}

func parseToolTag(tagName, body string) (string, map[string]any, bool) {
	switch tagName {
	case "read", "read_file":
		path := firstTagValue(body, "path", "file")
		if path == "" {
			return "", nil, false
		}
		return "read", map[string]any{"path": path}, true
	case "write":
		path := firstTagValue(body, "path", "file")
		content := firstTagValue(body, "content", "text")
		if path == "" || content == "" {
			return "", nil, false
		}
		return "write", map[string]any{"path": path, "content": content}, true
	case "edit":
		path := firstTagValue(body, "path", "file")
		oldText := firstTagValue(body, "oldText", "old_text", "old")
		newText := firstTagValue(body, "newText", "new_text", "new")
		if path == "" || oldText == "" || newText == "" {
			return "", nil, false
		}
		return "edit", map[string]any{
			"path":    path,
			"oldText": oldText,
			"newText": newText,
		}, true
	case "bash":
		command := firstTagValue(body, "command", "cmd")
		if command == "" {
			command = strings.TrimSpace(body)
		}
		if command == "" {
			return "", nil, false
		}
		return "bash", map[string]any{"command": command}, true
	case "grep":
		pattern := firstTagValue(body, "pattern", "query")
		if pattern == "" {
			return "", nil, false
		}
		args := map[string]any{"pattern": pattern}
		if path := firstTagValue(body, "path"); path != "" {
			args["path"] = path
		}
		if filePattern := firstTagValue(body, "filePattern", "file_pattern"); filePattern != "" {
			args["filePattern"] = filePattern
		}
		return "grep", args, true
	default:
		return "", nil, false
	}
}

func parseArgKeyValueToolCall(text string) (toolTagCall, bool) {
	matches := argKeyValRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return toolTagCall{}, false
	}

	first := matches[0]
	toolStart := strings.LastIndex(text[:first[0]], "<")
	if toolStart < 0 {
		return toolTagCall{}, false
	}
	toolName := extractToolName(text[toolStart+1 : first[0]])
	if toolName == "" {
		return toolTagCall{}, false
	}

	args := make(map[string]any)
	lastEnd := matches[len(matches)-1][1]
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		key := strings.TrimSpace(text[match[2]:match[3]])
		value := strings.TrimSpace(text[match[4]:match[5]])
		if key == "" {
			continue
		}
		args[key] = value
		if match[1] > lastEnd {
			lastEnd = match[1]
		}
	}

	end := lastEnd
	closeTags := []string{
		"</tool_call>",
		"</tool>",
		fmt.Sprintf("</%s>", toolName),
	}
	for _, tag := range closeTags {
		if idx := strings.Index(strings.ToLower(text[end:]), tag); idx >= 0 {
			end = end + idx + len(tag)
			break
		}
	}

	normalizedName := normalizeToolCallName(toolName)
	normalizedArgs, err := coerceToolArguments(normalizedName, args)
	if err != nil {
		return toolTagCall{}, false
	}

	return toolTagCall{
		name:     normalizedName,
		args:     normalizedArgs,
		start:    toolStart,
		end:      end,
		consumed: true,
	}, true
}

func extractToolName(fragment string) string {
	name := strings.Builder{}
	for _, r := range fragment {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' {
			name.WriteRune(r)
			continue
		}
		break
	}
	value := strings.ToLower(strings.TrimSpace(name.String()))
	if value == "" {
		return ""
	}
	return value
}

func firstTagValue(body string, names ...string) string {
	for _, name := range names {
		value := tagValue(body, name)
		if value != "" {
			return value
		}
	}
	return ""
}

func tagValue(body, name string) string {
	re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(name) + `>\s*(.*?)\s*</` + regexp.QuoteMeta(name) + `>`)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
