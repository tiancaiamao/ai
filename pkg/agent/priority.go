package agent

import (
	"regexp"
	"strings"
)

// PriorityCalculator calculates message priority for compaction decisions.
// Instead of keyword matching, it uses multi-dimensional heuristic scoring
// based on message attributes and structural features.
type PriorityCalculator struct {
	rules []PriorityRule
}

// PriorityRule defines a single priority scoring rule.
type PriorityRule struct {
	Name      string
	Evaluator func(msg AgentMessage, ctx *PriorityContext) float64
	Weight    float64
}

// PriorityContext provides context for priority calculation.
type PriorityContext struct {
	MessageIndex  int
	TotalMessages int
	RecentErrors  []string
	ModifiedFiles map[string]bool
	seenErrors    map[string]bool
}

// NewPriorityCalculator creates a calculator with default rules.
func NewPriorityCalculator() *PriorityCalculator {
	return &PriorityCalculator{
		rules: []PriorityRule{
			// Rule 1: Position weight - recent and early messages are more important
			{
				Name:   "position",
				Weight: 0.15,
				Evaluator: func(msg AgentMessage, ctx *PriorityContext) float64 {
					if ctx.TotalMessages == 0 {
						return 0.5
					}

					// Most recent 20%: high score (0.9-1.0)
					recentThreshold := int(float64(ctx.TotalMessages) * 0.8)
					if ctx.MessageIndex >= recentThreshold {
						return 1.0 - float64(ctx.TotalMessages-ctx.MessageIndex)*0.05
					}

					// First user message: high score (original goal)
					if ctx.MessageIndex == 0 {
						return 0.9
					}

					// Middle messages: low score (candidates for compression)
					return 0.3
				},
			},

			// Rule 2: Role weight - user messages > tool results > assistant messages
			{
				Name:   "role",
				Weight: 0.20,
				Evaluator: func(msg AgentMessage, ctx *PriorityContext) float64 {
					switch msg.Role {
					case "user":
						return 0.9 // User intent is most important
					case "toolResult":
						// Score based on tool type
						return getToolImportance(msg.ToolName, msg.IsError)
					case "assistant":
						return 0.5
					default:
						return 0.4
					}
				},
			},

			// Rule 3: Content features - paths/errors/code are important
			{
				Name:   "content_features",
				Weight: 0.35,
				Evaluator: func(msg AgentMessage, ctx *PriorityContext) float64 {
					text := msg.ExtractText()
					score := 0.0

					// Contains file paths (strong signal for coding context)
					if hasFilePath(text) {
						score += 0.3
					}

					// Contains error information
					if msg.IsError || containsErrorPattern(text) {
						score += 0.4 // Errors are very important
						// Track seen errors for context
						if ctx.seenErrors == nil {
							ctx.seenErrors = make(map[string]bool)
						}
						ctx.seenErrors[msg.ToolName] = true
					}

					// Contains code blocks
					if strings.Contains(text, "```") {
						score += 0.2
					}

					// Tool calls (more important than plain text)
					if len(msg.ExtractToolCalls()) > 0 {
						score += 0.25
					}

					if score > 1.0 {
						score = 1.0
					}
					return score
				},
			},

			// Rule 4: Error context - success after failure is important
			{
				Name:   "error_context",
				Weight: 0.20,
				Evaluator: func(msg AgentMessage, ctx *PriorityContext) float64 {
					// If there were previous errors, successful execution is important
					if msg.Role == "toolResult" && !msg.IsError {
						if ctx.seenErrors != nil && ctx.seenErrors[msg.ToolName] {
							return 0.8 // Success after failure
						}
					}
					return 0.4
				},
			},

			// Rule 5: File tracking - remember which files were accessed
			{
				Name:   "file_tracking",
				Weight: 0.10,
				Evaluator: func(msg AgentMessage, ctx *PriorityContext) float64 {
					if msg.Role == "toolResult" {
						filePath := extractFilePathFromMessage(msg)
						if filePath != "" {
							// Initialize map if needed
							if ctx.ModifiedFiles == nil {
								ctx.ModifiedFiles = make(map[string]bool)
							}

							// Frequently accessed files are more important
							if ctx.ModifiedFiles[filePath] {
								return 0.7
							}
							ctx.ModifiedFiles[filePath] = true
							return 0.5
						}
					}
					return 0.3
				},
			},
		},
	}
}

// Calculate computes priority score for a message.
func (pc *PriorityCalculator) Calculate(msg AgentMessage, ctx *PriorityContext) float64 {
	totalScore := 0.0
	totalWeight := 0.0

	for _, rule := range pc.rules {
		score := rule.Evaluator(msg, ctx)
		totalScore += score * rule.Weight
		totalWeight += rule.Weight
	}

	if totalWeight == 0 {
		return 0.5
	}

	return totalScore / totalWeight
}

// NewPriorityContext creates a new priority context.
func NewPriorityContext(totalMessages int) *PriorityContext {
	return &PriorityContext{
		TotalMessages: totalMessages,
		RecentErrors:  make([]string, 0),
		ModifiedFiles: make(map[string]bool),
		seenErrors:    make(map[string]bool),
	}
}

// getToolImportance returns importance score based on tool type.
// Write operations > read operations > query operations
func getToolImportance(toolName string, isError bool) float64 {
	if isError {
		return 0.9 // Errors are always high priority
	}

	// Write operations are important (state changes)
	switch toolName {
	case "write", "edit", "bash":
		return 0.8
	case "read":
		return 0.6
	case "grep":
		return 0.4
	default:
		return 0.5
	}
}

// hasFilePath detects file path patterns in text.
// Uses structural features, not specific paths.
func hasFilePath(text string) bool {
	// Common directory patterns
	dirPatterns := []string{
		"pkg/", "cmd/", "internal/", "src/", "lib/",
		"test/", "tests/", "spec/", "docs/", "config/",
	}

	for _, p := range dirPatterns {
		if strings.Contains(text, p) {
			return true
		}
	}

	// Common file extensions
	extPatterns := []string{
		".go", ".js", ".ts", ".py", ".rs", ".java",
		".cpp", ".c", ".h", ".json", ".yaml", ".yml",
		".md", ".txt", ".sh", ".env",
	}

	for _, p := range extPatterns {
		if strings.Contains(text, p) {
			return true
		}
	}

	// Path patterns with line numbers: "path/to/file:123"
	pathLinePattern := regexp.MustCompile(`\b\w+[/\\]\w+:\d+\b`)
	if pathLinePattern.MatchString(text) {
		return true
	}

	// Absolute paths
	if strings.Contains(text, "/Users/") ||
		strings.Contains(text, "/home/") ||
		strings.Contains(text, "C:\\") {
		return true
	}

	return false
}

// Note: containsErrorPattern is defined in tool_output.go to avoid duplication.

// extractFilePathFromMessage extracts file path from tool parameters.
func extractFilePathFromMessage(msg AgentMessage) string {
	// Only for tools that work with files
	if msg.ToolName != "read" && msg.ToolName != "write" && msg.ToolName != "edit" {
		return ""
	}

	// Extract from tool call content
	for _, block := range msg.Content {
		if tc, ok := block.(ToolCallContent); ok {
			if path, ok := tc.Arguments["path"].(string); ok {
				return path
			}
		}
	}

	return ""
}

// ScoredMessage pairs a message with its priority score.
type ScoredMessage struct {
	Msg   AgentMessage
	Score float64
	Index int
}

// CalculateMessagePriorities computes priority scores for all messages.
// Returns messages categorized by priority level.
func CalculateMessagePriorities(messages []AgentMessage) (highPriority, normal []ScoredMessage) {
	if len(messages) == 0 {
		return nil, nil
	}

	calculator := NewPriorityCalculator()
	ctx := NewPriorityContext(len(messages))

	scored := make([]ScoredMessage, len(messages))
	for i, msg := range messages {
		ctx.MessageIndex = i
		score := calculator.Calculate(msg, ctx)
		scored[i] = ScoredMessage{
			Msg:   msg,
			Score: score,
			Index: i,
		}
	}

	// Categorize by threshold
	highThreshold := 0.7
	for _, sm := range scored {
		if sm.Score >= highThreshold {
			highPriority = append(highPriority, sm)
		} else {
			normal = append(normal, sm)
		}
	}

	return highPriority, normal
}
