package context

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// TruncateCompactHint handles llm-context/truncate-compact-hint.md.
type TruncateCompactHint struct {
	compactor Compactor
}

// HintProcessResult describes what was applied from one hint file.
type HintProcessResult struct {
	TruncatedCount   int
	CompactPerformed bool
}

// NewTruncateCompactHint creates a new hint processor.
func NewTruncateCompactHint(compactor Compactor) *TruncateCompactHint {
	return &TruncateCompactHint{compactor: compactor}
}

// Process reads and applies truncate-compact-hint.md once.
func (t *TruncateCompactHint) Process(ctx context.Context, agentCtx *AgentContext) (HintProcessResult, error) {
	traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_start",
		traceevent.Field{Key: "agent_ctx_nil", Value: agentCtx == nil},
		traceevent.Field{Key: "llm_context_nil", Value: agentCtx == nil || agentCtx.LLMContext == nil},
	)

	if agentCtx == nil || agentCtx.LLMContext == nil {
		traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_skip",
			traceevent.Field{Key: "reason", Value: "agent_ctx_or_llm_context_is_nil"},
		)
		return HintProcessResult{}, nil
	}

	hintPath := filepath.Join(agentCtx.LLMContext.GetSessionDir(), "llm-context", "truncate-compact-hint.md")
	content, err := os.ReadFile(hintPath)

	traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_read_attempt",
		traceevent.Field{Key: "hint_path", Value: hintPath},
		traceevent.Field{Key: "error", Value: err},
	)

	if err != nil {
		if os.IsNotExist(err) {
			traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_skip",
				traceevent.Field{Key: "reason", Value: "hint_file_not_exists"},
			)
			return HintProcessResult{}, nil
		}
		return HintProcessResult{}, fmt.Errorf("read truncate-compact-hint.md: %w", err)
	}

	traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_read",
		traceevent.Field{Key: "hint_path", Value: hintPath},
		traceevent.Field{Key: "hint_file_chars", Value: len(content)},
		traceevent.Field{Key: "hint_file_content", Value: string(content)},
	)

	defer func() {
		if removeErr := os.Remove(hintPath); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("[TruncateCompactHint] Failed to delete hint file", "path", hintPath, "error", removeErr)
		}
	}()

	sections, err := t.parseSections(string(content))
	if err != nil {
		return HintProcessResult{}, fmt.Errorf("parse truncate-compact-hint.md: %w", err)
	}

	var processErr error
	result := HintProcessResult{}

	truncatedCount, truncateErr := t.processTruncateSection(ctx, agentCtx, sections.Truncate)
	if truncateErr != nil {
		processErr = errors.Join(processErr, truncateErr)
	}
	result.TruncatedCount = truncatedCount

	compactPerformed, compactErr := t.processCompactSection(ctx, agentCtx, sections.Compact)
	if compactErr != nil {
		processErr = errors.Join(processErr, compactErr)
	}
	result.CompactPerformed = compactPerformed

	traceevent.Log(ctx, traceevent.CategoryTool, "truncate_compact_hint_processed",
		traceevent.Field{Key: "truncated_count", Value: result.TruncatedCount},
		traceevent.Field{Key: "compact_performed", Value: result.CompactPerformed},
		traceevent.Field{Key: "has_truncate_section", Value: len(sections.Truncate) > 0},
		traceevent.Field{Key: "has_compact_section", Value: sections.Compact != nil},
	)

	return result, processErr
}

// HintSections contains parsed TRUNCATE and COMPACT sections.
type HintSections struct {
	Truncate []string
	Compact  *CompactHintSpec
}

// CompactHintSpec contains COMPACT section config.
type CompactHintSpec struct {
	Target        string
	Strategy      string
	KeepRecent    int
	ArchiveTo     string
	ConfidenceMin float64
	ConfidenceMax float64
}

func (t *TruncateCompactHint) parseSections(content string) (*HintSections, error) {
	sections := &HintSections{Truncate: make([]string, 0)}
	scanner := bufio.NewScanner(strings.NewReader(content))
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "##") || strings.HasPrefix(line, "#") {
			sectionName := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "##"), "#"))
			currentSection = strings.ToUpper(sectionName)
			continue
		}

		switch currentSection {
		case "TRUNCATE":
			parts := strings.Split(line, ",")
			for _, part := range parts {
				id := strings.TrimSpace(part)
				if id != "" {
					sections.Truncate = append(sections.Truncate, id)
				}
			}
		case "COMPACT":
			idx := strings.Index(line, ":")
			if idx <= 0 {
				continue
			}
			key := strings.ToUpper(strings.TrimSpace(line[:idx]))
			value := strings.TrimSpace(line[idx+1:])
			if sections.Compact == nil {
				sections.Compact = &CompactHintSpec{
					ConfidenceMin: -1,
					ConfidenceMax: -1,
				}
			}
			switch key {
			case "TARGET":
				sections.Compact.Target = value
			case "STRATEGY":
				sections.Compact.Strategy = value
			case "KEEP_RECENT":
				if n, err := strconv.Atoi(value); err == nil {
					sections.Compact.KeepRecent = n
				}
			case "ARCHIVE_TO":
				sections.Compact.ArchiveTo = value
			case "CONFIDENCE":
				if min, max, ok := parseCompactConfidenceRange(value); ok {
					sections.Compact.ConfidenceMin = min
					sections.Compact.ConfidenceMax = max
				} else if p, ok := parseCompactConfidence(value); ok {
					sections.Compact.ConfidenceMin = p
					sections.Compact.ConfidenceMax = p
				}
			case "CONFIDENCE_MIN":
				if p, ok := parseCompactConfidence(value); ok {
					sections.Compact.ConfidenceMin = p
				}
			case "CONFIDENCE_MAX":
				if p, ok := parseCompactConfidence(value); ok {
					sections.Compact.ConfidenceMax = p
				}
			}
		}
	}

	return sections, scanner.Err()
}

func (t *TruncateCompactHint) processTruncateSection(
	ctx context.Context,
	agentCtx *AgentContext,
	idsToTruncate []string,
) (int, error) {
	if len(idsToTruncate) == 0 {
		return 0, nil
	}

	truncatedCount := 0
	for i := range agentCtx.Messages {
		msg := agentCtx.Messages[i]
		if msg.Role != "toolResult" {
			continue
		}
		if !shouldTruncate(msg.ToolCallID, idsToTruncate) {
			continue
		}
		if IsTruncatedAgentToolTag(msg.ExtractText()) {
			continue
		}

		originalSize := len(msg.ExtractText())
		if n, ok := ParseCharsFromAgentToolTag(msg.ExtractText()); ok {
			originalSize = n
		}

		agentCtx.Messages[i] = NewToolResultMessage(
			msg.ToolCallID,
			msg.ToolName,
			[]ContentBlock{
				TextContent{
					Type: "text",
					Text: fmt.Sprintf(
						`<agent:tool id="%s" name="%s" chars="%d" truncated="true" />`,
						msg.ToolCallID,
						msg.ToolName,
						originalSize,
					),
				},
			},
			msg.IsError,
		)

		truncatedCount++
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_output_truncated_via_hint",
			traceevent.Field{Key: "tool_call_id", Value: msg.ToolCallID},
			traceevent.Field{Key: "tool_name", Value: msg.ToolName},
			traceevent.Field{Key: "original_chars", Value: originalSize},
		)
	}

	return truncatedCount, nil
}

func (t *TruncateCompactHint) processCompactSection(
	ctx context.Context,
	agentCtx *AgentContext,
	spec *CompactHintSpec,
) (bool, error) {
	if spec == nil {
		return false, nil
	}
	if t.compactor == nil {
		return false, nil
	}

	confidence := compactConfidenceProbability(spec)
	roll := rand.Float64()
	if roll > confidence {
		traceevent.Log(ctx, traceevent.CategoryTool, "compact_skipped_via_hint_confidence",
			traceevent.Field{Key: "confidence", Value: confidence},
			traceevent.Field{Key: "roll", Value: roll},
			traceevent.Field{Key: "target", Value: spec.Target},
			traceevent.Field{Key: "strategy", Value: spec.Strategy},
		)
		return false, nil
	}

	before := len(agentCtx.Messages)
	compacted, err := t.compactor.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
	if err != nil {
		return false, fmt.Errorf("compact via hint: %w", err)
	}

	after := before
	if compacted != nil {
		agentCtx.Messages = compacted.Messages
		agentCtx.LastCompactionSummary = compacted.Summary
		after = len(compacted.Messages)
	}

	traceevent.Log(ctx, traceevent.CategoryTool, "compact_performed_via_hint",
		traceevent.Field{Key: "target", Value: spec.Target},
		traceevent.Field{Key: "strategy", Value: spec.Strategy},
		traceevent.Field{Key: "keep_recent", Value: spec.KeepRecent},
		traceevent.Field{Key: "archive_to", Value: spec.ArchiveTo},
		traceevent.Field{Key: "confidence", Value: confidence},
		traceevent.Field{Key: "roll", Value: roll},
		traceevent.Field{Key: "before_messages", Value: before},
		traceevent.Field{Key: "after_messages", Value: after},
	)

	return true, nil
}

// shouldTruncate checks whether a tool_call_id is listed in TRUNCATE.
func shouldTruncate(toolCallID string, idsToTruncate []string) bool {
	for _, id := range idsToTruncate {
		if strings.EqualFold(toolCallID, id) {
			return true
		}
	}
	return false
}

func compactConfidenceProbability(spec *CompactHintSpec) float64 {
	if spec == nil {
		return 1.0
	}
	minV, maxV := normalizeConfidenceRange(spec.ConfidenceMin, spec.ConfidenceMax)
	return (minV + maxV) / 2.0
}

func normalizeConfidenceRange(minV, maxV float64) (float64, float64) {
	if minV < 0 && maxV < 0 {
		return 1.0, 1.0
	}
	if minV < 0 && maxV >= 0 {
		minV = maxV
	}
	if maxV < 0 && minV >= 0 {
		maxV = minV
	}
	minV = clamp01(minV)
	maxV = clamp01(maxV)
	if minV > maxV {
		minV, maxV = maxV, minV
	}
	return minV, maxV
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func parseCompactConfidence(raw string) (float64, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false
	}
	isPercent := strings.HasSuffix(v, "%")
	v = strings.TrimSuffix(v, "%")
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	if isPercent {
		n = n / 100.0
	} else if n > 1 {
		n = n / 100.0
	}
	return clamp01(n), true
}

func parseCompactConfidenceRange(raw string) (float64, float64, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, 0, false
	}
	sep := "-"
	if strings.Contains(v, "~") {
		sep = "~"
	}
	parts := strings.Split(v, sep)
	if len(parts) != 2 {
		return 0, 0, false
	}
	minV, ok1 := parseCompactConfidence(parts[0])
	maxV, ok2 := parseCompactConfidence(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	minV, maxV = normalizeConfidenceRange(minV, maxV)
	return minV, maxV, true
}