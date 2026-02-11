package agent

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var toolCallSeq uint64

func normalizeToolCall(tc ToolCallContent) ToolCallContent {
	normalized := tc
	normalized.Name = normalizeToolCallName(tc.Name)
	normalized.ID = ensureToolCallID(tc.ID)
	if normalized.Arguments == nil {
		normalized.Arguments = map[string]any{}
	}
	return normalized
}

func normalizeToolCallName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "readfile":
		return "read"
	case "write_file", "writefile":
		return "write"
	case "edit_file", "editfile":
		return "edit"
	case "shell", "sh", "bash_command", "command":
		return "bash"
	case "search", "ripgrep":
		return "grep"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func ensureToolCallID(id string) string {
	clean := sanitizeToolCallID(id)
	if clean != "" {
		return clean
	}
	seq := atomic.AddUint64(&toolCallSeq, 1)
	return fmt.Sprintf("tool_%d_%d", time.Now().UnixNano(), seq)
}

func sanitizeToolCallID(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	clean := strings.Trim(b.String(), "_-")
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

func coerceToolArguments(toolName string, args map[string]any) (map[string]any, error) {
	name := normalizeToolCallName(toolName)
	if args == nil {
		args = map[string]any{}
	}

	switch name {
	case "read":
		path := getStringArg(args, "path", "file")
		if path == "" {
			return nil, fmt.Errorf("missing path")
		}
		return map[string]any{"path": path}, nil
	case "write":
		path := getStringArg(args, "path", "file")
		content := getStringArg(args, "content", "text")
		if path == "" || content == "" {
			return nil, fmt.Errorf("missing path/content")
		}
		return map[string]any{"path": path, "content": content}, nil
	case "edit":
		path := getStringArg(args, "path", "file")
		oldText := getStringArg(args, "oldText", "old_text", "old")
		newText := getStringArg(args, "newText", "new_text", "new")
		if path == "" || oldText == "" || newText == "" {
			return nil, fmt.Errorf("missing path/oldText/newText")
		}
		return map[string]any{
			"path":    path,
			"oldText": oldText,
			"newText": newText,
		}, nil
	case "bash":
		command := getStringArg(args, "command", "cmd")
		if command == "" {
			return nil, fmt.Errorf("missing command")
		}
		return map[string]any{"command": command}, nil
	case "grep":
		pattern := getStringArg(args, "pattern", "query")
		if pattern == "" {
			return nil, fmt.Errorf("missing pattern")
		}
		result := map[string]any{"pattern": pattern}
		if path := getStringArg(args, "path"); path != "" {
			result["path"] = path
		}
		if filePattern := getStringArg(args, "filePattern", "file_pattern"); filePattern != "" {
			result["filePattern"] = filePattern
		}
		return result, nil
	default:
		return args, nil
	}
}

func getStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		default:
			coerced := strings.TrimSpace(fmt.Sprint(v))
			if coerced != "" && coerced != "<nil>" {
				return coerced
			}
		}
	}
	return ""
}
