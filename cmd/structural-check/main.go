package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type StructuralTestResult struct {
	TestID       string        `json:"test_id"`
	Score        float64       `json:"score"`
	ChecksPassed int           `json:"checks_passed"`
	ChecksTotal  int           `json:"checks_total"`
	Details      []CheckDetail `json:"details"`
}

type CheckDetail struct {
	ID      string  `json:"id"`
	Passed  bool    `json:"passed"`
	Message string  `json:"message"`
	Weight  float64 `json:"weight,omitempty"`
}

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: structural-check <messages.jsonl> <trace.perfetto.json> [--from-message N]")
		os.Exit(1)
	}
	msgFile := os.Args[1]
	traceFile := os.Args[2]
	fromMessage := 0

	// Parse optional --from-message N
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--from-message" && i+1 < len(os.Args) {
			fmt.Sscanf(os.Args[i+1], "%d", &fromMessage)
			i++
		}
	}

	allMessages, err := parseMessages(msgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse messages: %v\n", err)
		os.Exit(1)
	}

	// Slice to only messages after the resume point
	var messages []message
	if fromMessage > 0 && fromMessage < len(allMessages) {
		messages = allMessages[fromMessage:]
	} else {
		messages = allMessages
	}

	traceEvents, err := parseTrace(traceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse trace: %v\n", err)
		os.Exit(1)
	}

	result := StructuralTestResult{TestID: "behavior"}

	if fromMessage > 0 {
		logf("Analyzing messages %d-%d (total %d, skipped %d warmup)", fromMessage, len(allMessages)-1, len(allMessages), fromMessage)
	}

	// --- Behavioral checks (what agent actually DID) ---

	// Core: did it manage context at all?
	result.Details = append(result.Details, checkCMCalled(messages))

	// Did it manage BEFORE being reminded?
	result.Details = append(result.Details, checkProactive(messages))

	// Did it use truncate (the most important action)?
	result.Details = append(result.Details, checkUsedTruncate(messages))

	// Did it batch truncate (efficiency)?
	result.Details = append(result.Details, checkBatchTruncate(messages))

	// Did it use compact when context was high?
	result.Details = append(result.Details, checkCompact(messages))

	// Did it complete the multi-step task? (all 3 step files + final answers)
	result.Details = append(result.Details, checkTaskCompletion(messages))

	// Did it read the big session file (step 2 actually executed)?
	result.Details = append(result.Details, checkReadBigFile(messages))

	// Trace: framework-level truncation happened
	result.Details = append(result.Details, checkTraceTruncation(traceEvents))

	// Weighted score: proactive/truncate matter most
	totalWeight := 0.0
	passedWeight := 0.0
	for _, d := range result.Details {
		w := d.Weight
		if w == 0 {
			w = 1.0
		}
		totalWeight += w
		if d.Passed {
			passedWeight += w
		}
	}
	passed := 0
	for _, d := range result.Details {
		if d.Passed {
			passed++
		}
	}
	result.ChecksPassed = passed
	result.ChecksTotal = len(result.Details)
	if totalWeight > 0 {
		result.Score = passedWeight / totalWeight
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(output))
}

// --- Data types ---

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
	Raw     string
}

type contentBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	Arguments json.RawMessage `json:"arguments"`
}

type traceEvent struct {
	Name string                 `json:"name"`
	Cat  string                 `json:"cat"`
	Args map[string]interface{} `json:"args"`
}

// --- Parsers ---

func parseMessages(path string) ([]message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs []message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		msgType := string(raw["type"])
		if !strings.Contains(msgType, "message") {
			continue
		}
		var inner struct {
			Role    string         `json:"role"`
			Content []contentBlock `json:"content"`
		}
		if err := json.Unmarshal(raw["message"], &inner); err != nil {
			continue
		}
		msgs = append(msgs, message{
			Role:    inner.Role,
			Content: inner.Content,
			Raw:     line,
		})
	}
	return msgs, nil
}

func parseTrace(path string) ([]traceEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		TraceEvents []traceEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.TraceEvents, nil
}

// --- Helpers ---

// extractToolNames returns all tool names called by assistant messages.
func extractToolNames(msgs []message) []string {
	seen := map[string]bool{}
	var names []string
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if (block.Type == "toolCall" || block.Type == "tool_use") && block.Name != "" {
				if !seen[block.Name] {
					seen[block.Name] = true
					names = append(names, block.Name)
				}
			}
		}
	}
	return names
}

// findCMCalls returns all context_management tool call blocks.
func findCMCalls(msgs []message) []contentBlock {
	var calls []contentBlock
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if (block.Type == "toolCall" || block.Type == "tool_use") &&
				strings.Contains(strings.ToLower(block.Name), "context_management") {
				calls = append(calls, block)
			}
		}
	}
	return calls
}

// extractCMDecisions returns all decision values from context_management calls.
func extractCMDecisions(msgs []message) []string {
	calls := findCMCalls(msgs)
	decRe := regexp.MustCompile(`"decision"\s*:\s*"([^"]*)"`)
	var decisions []string
	for _, call := range calls {
		args := string(call.Arguments)
		m := decRe.FindStringSubmatch(args)
		if len(m) > 1 {
			decisions = append(decisions, m[1])
		}
	}
	return decisions
}

// hasReminder returns true if any message contains a context management reminder.
func hasReminder(msgs []message) bool {
	for _, msg := range msgs {
		// Reminders come as user messages with specific patterns
		if msg.Role != "user" {
			continue
		}
		rawLower := strings.ToLower(msg.Raw)
		if (strings.Contains(rawLower, "reminder") || strings.Contains(rawLower, "context management")) &&
			strings.Contains(rawLower, "stale") {
			return true
		}
	}
	return false
}

// --- Checks ---

func checkCMCalled(msgs []message) CheckDetail {
	calls := findCMCalls(msgs)
	if len(calls) > 0 {
		decisions := extractCMDecisions(msgs)
		return CheckDetail{
			ID:      "cm_called",
			Passed:  true,
			Message: fmt.Sprintf("Agent called context_management %d time(s), decisions: %v", len(calls), decisions),
			Weight:  2.0,
		}
	}
	return CheckDetail{
		ID:      "cm_called",
		Passed:  false,
		Message: "Agent never called context_management tool",
		Weight:  2.0,
	}
}

func checkProactive(msgs []message) CheckDetail {
	cmSeen := false
	reminderSeen := false
	for _, msg := range msgs {
		if msg.Role == "user" && strings.Contains(strings.ToLower(msg.Raw), "reminder") &&
			strings.Contains(strings.ToLower(msg.Raw), "stale") {
			reminderSeen = true
			continue
		}
		if msg.Role == "assistant" {
			for _, block := range msg.Content {
				if (block.Type == "toolCall" || block.Type == "tool_use") &&
					strings.Contains(strings.ToLower(block.Name), "context_management") {
					if !reminderSeen {
						cmSeen = true
					}
				}
			}
		}
	}
	if cmSeen {
		return CheckDetail{
			ID:      "proactive_cm",
			Passed:  true,
			Message: "Agent called context_management before any reminder",
			Weight:  3.0, // Most important check
		}
	}
	return CheckDetail{
		ID:      "proactive_cm",
		Passed:  false,
		Message: "Agent did not proactively manage context (only after reminder or never)",
		Weight:  3.0,
	}
}

func checkUsedTruncate(msgs []message) CheckDetail {
	decisions := extractCMDecisions(msgs)
	for _, d := range decisions {
		if d == "truncate" {
			return CheckDetail{
				ID:      "used_truncate",
				Passed:  true,
				Message: "Agent used truncate decision",
				Weight:  2.0,
			}
		}
	}
	return CheckDetail{
		ID:      "used_truncate",
		Passed:  false,
		Message: fmt.Sprintf("Agent never used truncate (decisions: %v)", decisions),
		Weight:  2.0,
	}
}

func checkBatchTruncate(msgs []message) CheckDetail {
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type != "toolCall" && block.Type != "tool_use" {
				continue
			}
			if !strings.Contains(strings.ToLower(block.Name), "context_management") {
				continue
			}
			args := string(block.Arguments)
			if !strings.Contains(args, "truncate") {
				continue
			}
			idRe := regexp.MustCompile(`"truncate_ids"\s*:\s*"([^"]*)"`)
			m := idRe.FindStringSubmatch(args)
			if len(m) > 1 && strings.Contains(m[1], ",") {
				count := len(strings.Split(m[1], ","))
				return CheckDetail{
					ID:      "batch_truncate",
					Passed:  true,
					Message: fmt.Sprintf("Agent batch-truncated %d outputs", count),
					Weight:  1.5,
				}
			}
		}
	}
	return CheckDetail{
		ID:      "batch_truncate",
		Passed:  false,
		Message: "Agent did not batch-truncate (either no truncate or single-ID)",
		Weight:  1.5,
	}
}

func checkCompact(msgs []message) CheckDetail {
	decisions := extractCMDecisions(msgs)
	for _, d := range decisions {
		if d == "compact" {
			return CheckDetail{
				ID:      "compact_used",
				Passed:  true,
				Message: "Agent used compact decision",
				Weight:  1.0,
			}
		}
	}
	return CheckDetail{
		ID:      "compact_used",
		Passed:  true, // Neutral — compact may not be needed
		Message: "No compact used (may not have been needed)",
		Weight:  0.5,
	}
}

func checkTaskCompletion(msgs []message) CheckDetail {
	// Check if all 4 output files were written: step1_summary, step2_analysis, step3_comparison, final_answers
	files := []string{
		"step1_summary.txt",
		"step2_analysis.txt",
		"step3_comparison.txt",
		"final_answers.txt",
	}
	written := 0
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "toolCall" && block.Type != "tool_use" {
				continue
			}
			name := strings.ToLower(block.Name)
			if strings.Contains(name, "write") || strings.Contains(name, "bash") {
				args := string(block.Arguments)
				for _, f := range files {
					if strings.Contains(args, f) {
						written++
					}
				}
			}
		}
	}
	// Deduplicate
	written = min(written, len(files))
	if written >= len(files) {
		return CheckDetail{
			ID:      "task_completion",
			Passed:  true,
			Message: fmt.Sprintf("All %d output files written", len(files)),
			Weight:  1.0,
		}
	}
	return CheckDetail{
		ID:      "task_completion",
		Passed:  false,
		Message: fmt.Sprintf("Only %d/%d output files written", written, len(files)),
		Weight:  1.0,
	}
}

func checkReadBigFile(msgs []message) CheckDetail {
	// Check if agent read the 2.4MB session file (step 2)
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "toolCall" && block.Type != "tool_use" {
				continue
			}
			args := string(block.Arguments)
			if strings.Contains(args, "298df63d") && (strings.Contains(block.Name, "read") || strings.Contains(block.Name, "bash")) {
				return CheckDetail{
					ID:      "read_big_session",
					Passed:  true,
					Message: "Agent read the large session file (step 2 executed)",
					Weight:  1.0,
				}
			}
		}
	}
	return CheckDetail{
		ID:      "read_big_session",
		Passed:  false,
		Message: "Agent did not read the large session file",
		Weight:  1.0,
	}
}

func checkTraceTruncation(events []traceEvent) CheckDetail {
	count := 0
	for _, e := range events {
		if strings.Contains(e.Name, "truncat") || strings.Contains(e.Name, "context_management") {
			count++
		}
	}
	if count > 0 {
		return CheckDetail{
			ID:      "trace_truncation",
			Passed:  true,
			Message: fmt.Sprintf("Found %d truncation/context_management events in trace", count),
			Weight:  0.5,
		}
	}
	return CheckDetail{
		ID:      "trace_truncation",
		Passed:  false,
		Message: "No truncation events in trace",
		Weight:  0.5,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}