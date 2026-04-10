package main

import "github.com/tiancaiamao/ai/internal/evolvemini"

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// runSnapshotExtract extracts diverse snapshots from session JSONL files.
func runSnapshotExtract(sessionsDir, outputDir string) error {
	fmt.Printf("Scanning sessions in %s ...\n", sessionsDir)

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return fmt.Errorf("read sessions dir: %w", err)
	}

	type sessionInfo struct {
		path      string
		id        string
		size      int64
		msgs      int
		firstUser string
	}
	var candidates []sessionInfo

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		msgFile := filepath.Join(sessionsDir, e.Name(), "messages.jsonl")
		info, err := os.Stat(msgFile)
		if err != nil {
			continue
		}
		if info.Size() < 50_000 { // skip small sessions (< 50KB)
			continue
		}

		// Count messages and find first user message
		msgCount, firstUser, err := scanSession(msgFile)
		if err != nil {
			continue
		}
		if msgCount < 30 {
			continue
		}

		candidates = append(candidates, sessionInfo{
			path:      msgFile,
			id:        e.Name(),
			size:      info.Size(),
			msgs:      msgCount,
			firstUser: firstUser,
		})
	}

	fmt.Printf("Found %d candidate sessions (30+ msgs, 50KB+)\n", len(candidates))

	// Sort by size descending — larger sessions tend to have richer contexts
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].size > candidates[j].size
	})

	// Limit to top 40 sessions to avoid processing too many
	if len(candidates) > 40 {
		candidates = candidates[:40]
	}

	// Extract snapshots from candidates
	var allSnapshots []evolvemini.Snapshot
	seen := make(map[string]bool) // dedup by session

	for _, s := range candidates {
		if seen[s.id] {
			continue
		}
		seen[s.id] = true

		snaps, err := extractSnapshotsFromSession(s.path, s.id, s.firstUser)
		if err != nil {
			fmt.Printf("  Warning: failed to extract from %s: %v\n", s.id, err)
			continue
		}
		allSnapshots = append(allSnapshots, snaps...)

		if len(allSnapshots) >= 25 {
			break
		}
	}

	// Select top 15-20 for diversity
	if len(allSnapshots) > 20 {
		allSnapshots = selectDiverse(allSnapshots, 20)
	}

	fmt.Printf("Extracted %d snapshots\n", len(allSnapshots))

	// Save
	suite := &evolvemini.SnapshotSuite{Snapshots: allSnapshots}
	if err := suite.Save(outputDir); err != nil {
		return fmt.Errorf("save suite: %w", err)
	}
	fmt.Printf("Saved to %s\n", outputDir)

	return nil
}

// scanSession counts messages and finds the first user message text.
func scanSession(msgFile string) (int, string, error) {
	f, err := os.Open(msgFile)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	count := 0
	firstUser := ""

	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}

		var entry struct {
			Type    string `json:"type"`
			Message *struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		count++

		if entry.Message.Role == "user" && firstUser == "" {
			for _, bl := range entry.Message.Content {
				if bl.Type == "text" && bl.Text != "" {
					firstUser = bl.Text
					break
				}
			}
		}
	}
	return count, firstUser, nil
}

// msgEntry holds a raw session entry and its parsed AgentMessage.
type msgEntry struct {
	Entry   json.RawMessage
	Message agentctx.AgentMessage
}

// extractSnapshotsFromSession extracts 1-2 snapshots from a single session.
func extractSnapshotsFromSession(msgFile, sessionID, firstUser string) ([]evolvemini.Snapshot, error) {
	// Try to load existing LLMContext from the session directory
	llmContextDir := filepath.Dir(msgFile) + "/llm-context"
	llmContextFile := filepath.Join(llmContextDir, "overview.md")
	var llmContext string
	if data, err := os.ReadFile(llmContextFile); err == nil {
		llmContext = string(data)
	}

	// Read all message entries
	f, err := os.Open(msgFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []msgEntry

	dec := json.NewDecoder(f)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}

		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Type != "message" {
			continue
		}

		var msg agentctx.AgentMessage
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			continue
		}
		messages = append(messages, msgEntry{Entry: raw, Message: msg})
	}

	if len(messages) < 30 {
		return nil, nil
	}

	// Find slice points where cumulative text size exceeds threshold
	type slicePoint struct {
		index           int
		cumulativeChars int
		tags            []string
	}

	cumChars := 0
	var points []slicePoint
	// We want snapshot at around 30KB, 60KB, 100KB of text
	thresholds := []int{30_000, 60_000, 100_000}
	thresholdIdx := 0

	toolNames := make(map[string]int)
	largeOutputs := 0

	for i, me := range messages {
		msg := me.Message
		// Estimate text size
		for _, bl := range msg.Content {
			switch b := bl.(type) {
			case agentctx.TextContent:
				cumChars += len(b.Text)
			case agentctx.ToolCallContent:
				cumChars += len(b.Name) + 50
				toolNames[b.Name]++
				if b.Arguments != nil {
					if aj, err := json.Marshal(b.Arguments); err == nil {
						cumChars += len(aj)
					}
				}
			case agentctx.ThinkingContent:
				cumChars += len(b.Thinking) / 2 // thinking is usually large, count half
			}
		}

		if msg.Role == "toolResult" {
			text := msg.ExtractText()
			cumChars += len(text)
			if len(text) > 10_000 {
				largeOutputs++
			}
		}

		// Check if we hit a threshold
		if thresholdIdx < len(thresholds) && cumChars >= thresholds[thresholdIdx] && i >= 20 {
			// Build partial for classification
			partials := make([]msgEntryPartial, i+1)
			for j := 0; j <= i; j++ {
				partials[j].Role = messages[j].Message.Role
				partials[j].Texts = extractTexts(messages[j].Message)
			}
			snapTags := classifySnapshot(partials, toolNames, largeOutputs)
			points = append(points, slicePoint{
				index:           i + 1,
				cumulativeChars: cumChars,
				tags:            snapTags,
			})
			thresholdIdx++
		}
	}

	// Limit to 2 snapshots per session
	if len(points) > 2 {
		points = points[:2]
	}

	var snapshots []evolvemini.Snapshot
	for _, p := range points {
		if p.index >= len(messages)-3 {
			continue // need at least 3 messages after for follow-up
		}

		recentMsgs := make([]agentctx.AgentMessage, p.index)
		for i := 0; i < p.index; i++ {
			recentMsgs[i] = messages[i].Message
		}

		// Extract follow-up from messages after the slice point
		followUpTask, followUpAnswer := extractFollowUp(messages[p.index:])

		desc := firstUser
		if len(desc) > 100 {
			desc = desc[:100] + "..."
		}

		snap := evolvemini.Snapshot{
			ID:             fmt.Sprintf("%s_%d", sessionID[:8], p.index),
			Description:    desc,
			Tags:           p.tags,
			RecentMessages: recentMsgs,
			LLMContext:     llmContext,
			AgentState: &agentctx.AgentState{
				TokensLimit: 200_000,
				TotalTurns:  p.index / 2,
			},
			ContextWindow:  128_000,
			FollowUpTask:   followUpTask,
			FollowUpAnswer: followUpAnswer,
			SourceSession:  sessionID,
			ExtractedAt:    time.Now().Format(time.RFC3339),
		}
		snapshots = append(snapshots, snap)
	}

	return snapshots, nil
}

// classifySnapshot assigns diversity tags based on message content.
func classifySnapshot(msgs []msgEntryPartial, toolNames map[string]int, largeOutputs int) []string {
	var tags []string

	if largeOutputs >= 3 {
		tags = append(tags, "large_tool_output")
	}
	if len(msgs) >= 80 {
		tags = append(tags, "long_conversation")
	} else if len(msgs) >= 50 {
		tags = append(tags, "multi_turn")
	}

	// Check for exploration-heavy sessions
	exploreCount := toolNames["bash"] + toolNames["read"]
	if exploreCount >= 10 {
		tags = append(tags, "exploration_heavy")
	}

	// Check for code review (contains "diff", "PR", "review" in messages)
	for _, m := range msgs {
		if m.Role == "user" {
			for _, t := range m.Texts {
				lower := strings.ToLower(t)
				if strings.Contains(lower, "review") || strings.Contains(lower, "diff") || strings.Contains(lower, "pr") {
					tags = append(tags, "code_review")
					return tags
				}
			}
		}
	}

	// Check for debug session
	if toolNames["grep"]+toolNames["read"] >= 8 {
		tags = append(tags, "debug_session")
	}

	if len(tags) == 0 {
		tags = append(tags, "general")
	}

	return tags
}

// msgEntryPartial is a lightweight struct for classification.
type msgEntryPartial struct {
	Role  string
	Texts []string
}

// extractTexts extracts all text content from an AgentMessage.
func extractTexts(msg agentctx.AgentMessage) []string {
	var texts []string
	for _, bl := range msg.Content {
		if tc, ok := bl.(agentctx.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return texts
}

// extractFollowUp gets the next user message and assistant response after the slice point.
func extractFollowUp(remaining []msgEntry) (string, string) {
	var userText, assistantText string

	for _, m := range remaining {
		msg := m.Message
		if msg.Role == "user" && userText == "" {
			userText = msg.ExtractText()
			if len(userText) > 200 {
				userText = userText[:200]
			}
		} else if msg.Role == "assistant" && userText != "" && assistantText == "" {
			assistantText = msg.ExtractText()
			if len(assistantText) > 500 {
				assistantText = assistantText[:500]
			}
			break
		}
		if userText != "" && assistantText != "" {
			break
		}
	}

	return userText, assistantText
}

// selectDiverse picks a diverse subset of snapshots.
func selectDiverse(snaps []evolvemini.Snapshot, target int) []evolvemini.Snapshot {
	if len(snaps) <= target {
		return snaps
	}

	// Group by primary tag
	byTag := make(map[string][]evolvemini.Snapshot)
	for _, s := range snaps {
		tag := "general"
		if len(s.Tags) > 0 {
			tag = s.Tags[0]
		}
		byTag[tag] = append(byTag[tag], s)
	}

	// Allocate slots proportionally, ensuring at least 1 per tag
	var result []evolvemini.Snapshot
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for _, group := range byTag {
		// Shuffle within group
		r.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
		// Take up to proportional share
		share := max(1, target*len(group)/len(snaps))
		if share > len(group) {
			share = len(group)
		}
		result = append(result, group[:share]...)
	}

	// Trim or pad to exactly target
	if len(result) > target {
		result = result[:target]
	}

	return result
}

// runSnapshotList prints a summary table of all snapshots in a suite directory.
func runSnapshotList(suiteDir string) error {
	suite, err := evolvemini.LoadSuite(suiteDir)
	if err != nil {
		return fmt.Errorf("load suite: %w", err)
	}

	if len(suite.Snapshots) == 0 {
		fmt.Println("No snapshots found.")
		return nil
	}

	fmt.Printf("%-20s | %-6s | %-10s | %-40s\n", "ID", "Msgs", "Tags", "Description")
	fmt.Println(strings.Repeat("-", 90))

	for _, s := range suite.Snapshots {
		msgCount := len(s.RecentMessages)
		tags := strings.Join(s.Tags, ",")
		desc := s.Description
		if len(desc) > 40 {
			desc = desc[:40] + "..."
		}
		fmt.Printf("%-20s | %-6d | %-10s | %-40s\n", s.ID, msgCount, tags, desc)
	}

	fmt.Printf("\nTotal: %d snapshots\n", len(suite.Snapshots))
	return nil
}
