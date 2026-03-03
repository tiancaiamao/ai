// Package hint provides parsing and processing of agent hints from LLM decisions.
package hint

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Hint represents a decision hint from LLM (truncate and compact).
type Hint struct {
	Truncate *TruncateHint
	Compact  *CompactHint
}

// TruncateHint represents a truncate decision.
type TruncateHint struct {
	Turn     int      // Turn index to truncate
	Section  string   // Section identifier to truncate
	ToolName string   // Tool name to truncate (e.g., "read", "grep")
	ToolIDs  []string // Tool call IDs to truncate (optional, for specific tool calls)
	Reason   string   // Reason for truncation
}

// CompactHint represents a compact decision.
type CompactHint struct {
	Confidence float64 // Confidence level (0.0-1.0)
	Reason     string  // Reason for compaction
	KeepTurns  int     // Optional: number of recent turns to keep
}

// ParseHintFile parses the hint file and returns the parsed hints.
func ParseHintFile(content string) (*Hint, error) {
	hint := &Hint{}

	lines := strings.Split(content, "\n")
	var currentSection string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for section headers
		if strings.HasPrefix(line, "## ") {
			currentSection = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}

		// Parse list items within sections
		if strings.HasPrefix(line, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "- "))

			switch strings.ToUpper(currentSection) {
			case "TRUNCATE":
				parseTruncateItem(hint, item)
			case "COMPACT":
				parseCompactItem(hint, item)
			}
		}
	}

	return hint, nil
}

func parseTruncateItem(hint *Hint, item string) {
	parts := strings.SplitN(item, ":", 2)
	if len(parts) != 2 {
		return
	}

	key := strings.TrimSpace(strings.ToLower(parts[0]))
	value := strings.TrimSpace(parts[1])

	if hint.Truncate == nil {
		hint.Truncate = &TruncateHint{}
	}

	switch key {
	case "turn":
		if turn, err := strconv.Atoi(strings.Trim(value, `"`)); err == nil {
			hint.Truncate.Turn = turn
		}
	case "section":
		hint.Truncate.Section = strings.Trim(value, `"`)
	case "tool_name":
		hint.Truncate.ToolName = strings.Trim(value, `"`)
	case "tool_ids", "toolids":
		// Parse comma-separated list of IDs
		ids := strings.Split(value, ",")
		for i, id := range ids {
			ids[i] = strings.TrimSpace(strings.Trim(id, `"`))
		}
		// Filter empty strings
		var nonEmpty []string
		for _, id := range ids {
			if id != "" {
				nonEmpty = append(nonEmpty, id)
			}
		}
		hint.Truncate.ToolIDs = nonEmpty
	case "reason":
		hint.Truncate.Reason = strings.Trim(value, `"`)
	}
}

func parseCompactItem(hint *Hint, item string) {
	parts := strings.SplitN(item, ":", 2)
	if len(parts) != 2 {
		return
	}

	key := strings.TrimSpace(strings.ToLower(parts[0]))
	value := strings.TrimSpace(parts[1])

	if hint.Compact == nil {
		hint.Compact = &CompactHint{}
	}

	switch key {
	case "confidence":
		if conf, err := strconv.ParseFloat(value, 64); err == nil {
			hint.Compact.Confidence = conf
		}
	case "reason":
		hint.Compact.Reason = strings.Trim(value, `"`)
	case "keep_last_turns", "keep_turns":
		if turns, err := strconv.Atoi(strings.Trim(value, `"`)); err == nil {
			hint.Compact.KeepTurns = turns
		}
	}
}

// LoadHintFile reads and parses the hint file from disk.
// Supports both truncate-hint.md (legacy) and truncate-compact-hint.md (new).
func LoadHintFile(sessionDir string) (*Hint, error) {
	// Try new filename first
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	content, err := os.ReadFile(hintPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Fallback to legacy filename
	if os.IsNotExist(err) {
		legacyPath := filepath.Join(sessionDir, "llm-context", "truncate-hint.md")
		content, err = os.ReadFile(legacyPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		// If legacy file exists, migrate it to new filename
		if err == nil {
			// Migrate: write to new filename
			if err := os.WriteFile(hintPath, content, 0644); err == nil {
				// Remove legacy file after successful migration
				os.Remove(legacyPath)
			}
			// Continue to parse from content (we have it already)
		} else {
			// Neither file exists
			return &Hint{}, nil
		}
	}

	return ParseHintFile(string(content))
}

// ClearHintFile removes the hint file.
// Supports both truncate-hint.md (legacy) and truncate-compact-hint.md (new).
func ClearHintFile(sessionDir string) error {
	// Remove new filename
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.Remove(hintPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Also remove legacy filename if it exists
	legacyPath := filepath.Join(sessionDir, "llm-context", "truncate-hint.md")
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// ShouldTriggerCompact decides whether to trigger compaction based on hint and current usage.
func ShouldTriggerCompact(hint *Hint, usage float64) (bool, float64, string) {
	// Force compact if usage >= 75%
	if usage >= 0.75 {
		return true, 1.0, "forced: usage exceeded 75%"
	}

	// Check compact hint
	if hint.Compact != nil && hint.Compact.Confidence > 0 {
		return true, hint.Compact.Confidence, hint.Compact.Reason
	}

	return false, 0, ""
}

// GetTruncateHintMessage returns the hint message to inject into system prompt.
func GetTruncateHintMessage(usage float64) string {
	switch {
	case usage < 0.30:
		return ""
	case usage < 0.40:
		return `<!-- TRUNCATE_HINT: Context at ~30%. Try to be concise in your responses. Consider truncating verbose explanations to save tokens. -->`
	case usage < 0.50:
		return `<!-- TRUNCATE_HINT: Context at ~45%. Please be mindful of token usage. Strongly consider truncating verbose explanations. -->`
	default:
		return `<!-- TRUNCATE_HINT: Context at ~60%+. High usage - please use truncation for verbose responses. -->`
	}
}

// GetCompactHintMessage returns the compact hint message to inject into system prompt.
func GetCompactHintMessage(usage float64) string {
	switch {
	case usage < 0.50:
		return ""
	case usage < 0.60:
		return `<!-- COMPACT_HINT: Context at ~55%. If topic has changed, consider requesting compaction. Write to truncate-compact-hint.md with COMPACT section. -->`
	case usage < 0.70:
		return `<!-- COMPACT_HINT: Context at ~65% - HIGH. Review context relevance. Consider compacting if older turns unrelated. -->`
	case usage < 0.75:
		return `<!-- COMPACT_HINT: Context at ~70% - VERY HIGH. Strongly recommend compacting old context via truncate-compact-hint.md -->`
	default:
		return `<!-- COMPACT_HINT: CRITICAL - Context at ~75%+. Compaction will be forced soon. Request now via truncate-compact-hint.md -->`
	}
}

// GetTruncateToolHintMessage returns the truncate hint message for tool output pressure.
func GetTruncateToolHintMessage(staleOutputs, largeOutputs int, largestOutputSize int) string {
	if staleOutputs == 0 && largeOutputs == 0 {
		return ""
	}

	pressure := ""
	if staleOutputs >= 10 || largeOutputs >= 10 {
		pressure = `<!-- TRUNCATE_TOOL_HINT: CRITICAL - High tool output pressure. Consider writing to truncate-compact-hint.md with TRUNCATE section to archive stale tool outputs. Use tool_name or tool_ids to specify which outputs to truncate. -->`
	} else if staleOutputs >= 5 || largeOutputs >= 5 {
		pressure = `<!-- TRUNCATE_TOOL_HINT: HIGH - Many stale or large tool outputs. Consider truncating via truncate-compact-hint.md. -->`
	} else if staleOutputs >= 3 || largeOutputs >= 3 {
		pressure = `<!-- TRUNCATE_TOOL_HINT: MODERATE - Some stale tool outputs detected. May truncate via truncate-compact-hint.md. -->`
	} else {
		pressure = `<!-- TRUNCATE_TOOL_HINT: LOW - Minor tool output pressure. Monitor. -->`
	}

	return pressure
}

// FormatHint returns a formatted hint file content for writing.
func FormatHint(h *Hint) string {
	var sb strings.Builder

	sb.WriteString("# Truncate & Compact Hint\n")
	sb.WriteString("此文件由 LLM 写入，包含对 agent 的决策提示。\n\n")

	if h.Truncate != nil {
		sb.WriteString("## TRUNCATE\n")
		if h.Truncate.Turn > 0 {
			sb.WriteString(fmt.Sprintf("- turn: %d\n", h.Truncate.Turn))
		}
		if h.Truncate.Section != "" {
			sb.WriteString(fmt.Sprintf("- section: \"%s\"\n", h.Truncate.Section))
		}
		if h.Truncate.ToolName != "" {
			sb.WriteString(fmt.Sprintf("- tool_name: \"%s\"\n", h.Truncate.ToolName))
		}
		if len(h.Truncate.ToolIDs) > 0 {
			sb.WriteString(fmt.Sprintf("- tool_ids: \"%s\"\n", strings.Join(h.Truncate.ToolIDs, ", ")))
		}
		if h.Truncate.Reason != "" {
			sb.WriteString(fmt.Sprintf("- reason: \"%s\"\n", h.Truncate.Reason))
		}
		sb.WriteString("\n")
	}

	if h.Compact != nil {
		sb.WriteString("## COMPACT\n")
		if h.Compact.Confidence > 0 {
			sb.WriteString(fmt.Sprintf("- confidence: %.2f\n", h.Compact.Confidence))
		}
		if h.Compact.Reason != "" {
			sb.WriteString(fmt.Sprintf("- reason: \"%s\"\n", h.Compact.Reason))
		}
		if h.Compact.KeepTurns > 0 {
			sb.WriteString(fmt.Sprintf("- keep_last_turns: %d\n", h.Compact.KeepTurns))
		}
	}

	return sb.String()
}

// ShouldTriggerTruncate determines if tool output should be truncated based on hint.
func ShouldTriggerTruncate(hint *Hint) bool {
	return hint.Truncate != nil
}