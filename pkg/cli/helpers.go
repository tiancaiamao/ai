package cli

import (
	"log/slog"
	"os"
	"strings"
)

// ParseSystemPrompt parses the --system-prompt flag.
// If the value starts with '@', it reads the file content.
// Otherwise, it returns the value as-is.
func ParseSystemPrompt(systemPromptFlag string) string {
	if systemPromptFlag == "" {
		return ""
	}
	if strings.HasPrefix(systemPromptFlag, "@") {
		filePath := strings.TrimPrefix(systemPromptFlag, "@")
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			slog.Warn("empty file path after '@' in --system-prompt flag")
			return ""
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("failed to read system-prompt file", "path", filePath, "error", err)
			return ""
		}
		if len(content) > 64*1024 {
			slog.Warn("system-prompt file too large, truncating to 64KB", "size", len(content))
			content = content[:64*1024]
		}
		return string(content)
	}
	return systemPromptFlag
}
