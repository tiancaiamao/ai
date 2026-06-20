package helpers

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
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

// ResolveRunID resolves the target run given an optional ID flag.
// If id is empty, it auto-selects by cwd. If id is a partial prefix,
// it uses FindByPrefix.
func ResolveRunID(baseDir, id string) (*tui.RunMeta, error) {
	if id != "" {
		// Try exact match first: look for run.json directly.
		exactPath := tui.RunMetaPath(baseDir, id)
		if meta, err := tui.LoadRunMeta(exactPath); err == nil && tui.IsRunning(meta) {
			return meta, nil
		}

		// Try prefix match.
		matches, err := tui.FindByPrefix(baseDir, id)
		if err != nil {
			return nil, fmt.Errorf("prefix match for %q: %w", id, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", id)
		}
		// FindByPrefix returns at most 1 match on success (errors on multiple).
		m := matches[0]
		if !tui.IsRunning(&m) {
			return nil, fmt.Errorf("run %s is not running (status: %s)", m.ID, m.Status)
		}
		return &m, nil
	}

	// Auto-select by cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}

	matches, err := tui.FindRunningByCwd(baseDir, cwd)
	if err != nil {
		return nil, fmt.Errorf("find running by cwd: %w", err)
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no running instances found in %s", cwd)
	case 1:
		return &matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return nil, fmt.Errorf("multiple running instances in %s (IDs: %v), use --id to disambiguate", cwd, ids)
	}
}
