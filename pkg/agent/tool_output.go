package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"fmt"
	"strings"
)

// ============================================================================
// Legacy Types - kept for backward compatibility
// ============================================================================

// ToolOutputLimits defines truncation limits for tool output (legacy).
// Deprecated: Use ToolOutputProcessor with OutputPolicy instead.
type ToolOutputLimits struct {
	MaxLines             int
	MaxBytes             int
	MaxChars             int
	LargeOutputThreshold int
	TruncateMode         string
}

// DefaultToolOutputLimits returns default truncation limits (legacy).
// Deprecated: Use NewToolOutputProcessor instead.
func DefaultToolOutputLimits() ToolOutputLimits {
	return ToolOutputLimits{
		MaxLines:             2000,
		MaxBytes:             51200,
		MaxChars:             51200,
		LargeOutputThreshold: 10000,
		TruncateMode:         "middle",
	}
}

// truncateToolContent truncates tool content based on limits (legacy).
// This is kept for backward compatibility and delegates to ToolOutputProcessor.
func truncateToolContent(content []agentctx.ContentBlock, limits ToolOutputLimits) []agentctx.ContentBlock {
	if len(content) == 0 {
		return content
	}

	// Create a processor with default policies
	processor := NewToolOutputProcessor(nil, limits.MaxChars)

	// Process each content block
	result := make([]agentctx.ContentBlock, 0, len(content))
	for _, block := range content {
		switch b := block.(type) {
		case agentctx.TextContent:
			// Apply truncation to text content
			processed := processor.ProcessOutput("", b.Text, false)
			result = append(result, agentctx.TextContent{
				Type: "text",
				Text: processed,
			})
		default:
			result = append(result, block)
		}
	}

	return result
}

// minInt returns the minimum of two integers (helper for legacy code).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================================
// New agentctx.Tool Output Processing System
// ============================================================================

// OutputStrategy defines how to process tool output.
type OutputStrategy int

const (
	// StrategyFull keeps the complete output (for critical tools like read)
	StrategyFull OutputStrategy = iota
	// StrategyTruncate keeps head and tail with truncation marker
	StrategyTruncate
	// StrategyDigest extracts key information (for bash commands)
	StrategyDigest
	// StrategyExtract extracts specific patterns (for grep)
	StrategyExtract
)

// OutputPolicy defines how to process a tool's output.
type OutputPolicy struct {
	ToolName   string
	MaxTokens  int
	Strategy   OutputStrategy
	KeepTail   int    // For StrategyTruncate/Digest - keep last N lines
	KeepHead   int    // For StrategyTruncate - keep first N lines
	DetectLang string // For code blocks - try to detect language
}

// DefaultToolPolicies returns the default output processing policies.
func DefaultToolPolicies() map[string]OutputPolicy {
	return map[string]OutputPolicy{
		"read": {
			ToolName:  "read",
			MaxTokens: 8000,
			Strategy:  StrategyFull,
		},
		"write": {
			ToolName:  "write",
			MaxTokens: 1000,
			Strategy:  StrategyDigest,
			KeepTail:  5,
		},
		"edit": {
			ToolName:  "edit",
			MaxTokens: 1000,
			Strategy:  StrategyDigest,
			KeepTail:  10,
		},
		"bash": {
			ToolName:  "bash",
			MaxTokens: 2000,
			Strategy:  StrategyDigest,
			KeepTail:  50,
		},
		"grep": {
			ToolName:  "grep",
			MaxTokens: 3000,
			Strategy:  StrategyExtract,
		},
	}
}

// ToolOutputProcessor processes tool outputs according to policies.
type ToolOutputProcessor struct {
	policies      map[string]OutputPolicy
	defaultPolicy OutputPolicy
	maxChars      int // Fallback max chars when policy not found
}

// NewToolOutputProcessor creates a new processor with given policies.
func NewToolOutputProcessor(policies map[string]OutputPolicy, maxChars int) *ToolOutputProcessor {
	if policies == nil {
		policies = DefaultToolPolicies()
	}

	return &ToolOutputProcessor{
		policies: policies,
		defaultPolicy: OutputPolicy{
			MaxTokens: 2000,
			Strategy:  StrategyTruncate,
			KeepTail:  30,
		},
		maxChars: maxChars,
	}
}

// ProcessOutput processes a tool output according to its policy.
func (p *ToolOutputProcessor) ProcessOutput(toolName, output string, isError bool) string {
	if output == "" {
		return output
	}

	policy, hasPolicy := p.policies[toolName]
	if !hasPolicy {
		policy = p.defaultPolicy
	}

	// Convert token limit to char limit (rough: 1 token ≈ 4 chars)
	maxChars := policy.MaxTokens * 4
	if maxChars <= 0 {
		maxChars = p.maxChars
	}

	// If output fits, return as-is
	if len(output) <= maxChars {
		return output
	}

	switch policy.Strategy {
	case StrategyFull:
		// Even full strategy needs a cap
		if len(output) > maxChars {
			return output[:maxChars] + "\n... (output truncated due to size limit)"
		}
		return output

	case StrategyDigest:
		return p.digestOutput(output, maxChars, policy.KeepTail, isError)

	case StrategyExtract:
		return p.extractOutput(output, maxChars)

	case StrategyTruncate:
		fallthrough
	default:
		return p.truncateOutput(output, maxChars, policy.KeepHead, policy.KeepTail)
	}
}

// digestOutput extracts key information from command output.
func (p *ToolOutputProcessor) digestOutput(output string, maxChars int, keepTail int, isError bool) string {
	lines := strings.Split(output, "\n")

	// For errors, preserve more context
	if isError {
		keepTail = keepTail * 2
	}

	var result []string
	var errorLines []string
	var importantLines []string

	// Scan for important information
	for _, line := range lines {
		lowerLine := strings.ToLower(line)

		// Error patterns
		if containsErrorPattern(lowerLine) {
			errorLines = append(errorLines, line)
			continue
		}

		// Important patterns (warnings, success indicators, etc.)
		if containsImportantPattern(lowerLine) {
			importantLines = append(importantLines, line)
		}
	}

	// Build digest
	maxDigestLines := maxChars / 40 // Rough estimate: 40 chars per line

	// Add error lines first (most important)
	if len(errorLines) > 0 {
		result = append(result, "=== ERRORS ===")
		for i, line := range errorLines {
			if i >= maxDigestLines/3 {
				result = append(result, fmt.Sprintf("... (%d more error lines)", len(errorLines)-i))
				break
			}
			result = append(result, line)
		}
	}

	// Add important lines
	if len(importantLines) > 0 && len(result) < maxDigestLines/2 {
		result = append(result, "=== IMPORTANT ===")
		for i, line := range importantLines {
			if i >= maxDigestLines/3 {
				break
			}
			result = append(result, line)
		}
	}

	// Always keep tail (most recent output)
	if keepTail > 0 && len(lines) > keepTail {
		result = append(result, fmt.Sprintf("=== LAST %d LINES ===", keepTail))
		tailStart := len(lines) - keepTail
		result = append(result, lines[tailStart:]...)
	} else if len(lines) <= keepTail {
		// Output is small enough, just return it
		return output
	}

	digest := strings.Join(result, "\n")

	// Final safety check
	if len(digest) > maxChars {
		digest = digest[:maxChars] + "\n... (digest truncated)"
	}

	return digest
}

// extractOutput extracts structured information from grep-like output.
func (p *ToolOutputProcessor) extractOutput(output string, maxChars int) string {
	lines := strings.Split(output, "\n")

	// Track matches per file for deduplication
	fileMatches := make(map[string][]string)
	maxPerFile := 10
	totalLimit := maxChars / 30 // Rough: 30 chars per line

	totalLines := 0
	for _, line := range lines {
		if line == "" {
			continue
		}

		// Extract file path (before first colon or at start)
		filePath := extractFilePath(line)
		if filePath == "" {
			filePath = "(general)"
		}

		// Limit matches per file
		if len(fileMatches[filePath]) >= maxPerFile {
			continue
		}

		fileMatches[filePath] = append(fileMatches[filePath], line)
		totalLines++

		if totalLines >= totalLimit {
			break
		}
	}

	// Build result
	var result []string
	for file, matches := range fileMatches {
		if len(matches) > 0 {
			if len(fileMatches) > 1 {
				result = append(result, fmt.Sprintf("[%s]", file))
			}
			result = append(result, matches...)
			if len(matches) >= maxPerFile {
				result = append(result, fmt.Sprintf("  ... (%d more matches in this file)",
					countFileMatches(lines, file)-maxPerFile))
			}
		}
	}

	extracted := strings.Join(result, "\n")

	// Safety check
	if len(extracted) > maxChars {
		extracted = extracted[:maxChars] + "\n... (output truncated)"
	}

	return extracted
}

// truncateOutput keeps head and tail with truncation marker.
func (p *ToolOutputProcessor) truncateOutput(output string, maxChars int, keepHead, keepTail int) string {
	// First check: if output fits within maxChars, return as-is
	if len(output) <= maxChars {
		return output
	}

	if keepHead == 0 {
		keepHead = 10
	}
	if keepTail == 0 {
		keepTail = 30
	}

	lines := strings.Split(output, "\n")

	// If few lines but large content (e.g., one very long JSON line),
	// truncate by characters instead of lines
	if len(lines) <= keepHead+keepTail {
		// Keep what we can, with truncation marker
		if len(output) > maxChars {
			headChars := maxChars / 2
			tailChars := maxChars - headChars - 50 // reserve space for marker
			if tailChars < 0 {
				tailChars = 0
			}
			result := output[:headChars]
			result += fmt.Sprintf("\n\n... (%d chars truncated, output was %d chars in %d lines) ...\n\n", len(output)-headChars-tailChars, len(output), len(lines))
			if tailChars > 0 {
				result += output[len(output)-tailChars:]
			}
			return result
		}
		return output
	}

	head := lines[:keepHead]
	tail := lines[len(lines)-keepTail:]

	result := strings.Join(head, "\n")
	result += fmt.Sprintf("\n\n... (%d lines truncated) ...\n\n", len(lines)-keepHead-keepTail)
	result += strings.Join(tail, "\n")

	if len(result) > maxChars {
		// Reduce tail if still too long
		result = result[:maxChars] + "\n... (further truncated)"
	}

	return result
}

// Helper functions

func containsErrorPattern(line string) bool {
	errorPatterns := []string{
		"error:", "error:", "failed", "exception",
		"cannot", "unable to", "not found",
		"exit code", "fatal", "panic:",
		"undefined", "unexpected", "invalid",
	}

	for _, pattern := range errorPatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

func containsImportantPattern(line string) bool {
	importantPatterns := []string{
		"warning:", "warn:", "deprecated",
		"success", "completed", "created",
		"updated", "deleted", "installed",
		"built", "finished", "done",
	}

	for _, pattern := range importantPatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// extractFilePath tries to extract a file path from a line.
func extractFilePath(line string) string {
	// Pattern: "path/to/file:line:col: content"
	// or "path/to/file: content"

	// Try to find the first colon-separated segment that looks like a path
	parts := strings.SplitN(line, ":", 3)
	if len(parts) >= 2 {
		candidate := parts[0]
		// Check if it looks like a file path
		if strings.Contains(candidate, "/") ||
			strings.Contains(candidate, "\\") ||
			strings.HasSuffix(candidate, ".go") ||
			strings.HasSuffix(candidate, ".js") ||
			strings.HasSuffix(candidate, ".ts") ||
			strings.HasSuffix(candidate, ".py") ||
			strings.HasSuffix(candidate, ".rs") {
			return candidate
		}
	}

	return ""
}

func countFileMatches(lines []string, file string) int {
	count := 0
	for _, line := range lines {
		if extractFilePath(line) == file {
			count++
		}
	}
	return count
}

// Simple max helper
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
