// Package tieredmemory integrates tiered-memory system into claw.
// Provides automatic memory retrieval and storage.
package tieredmemory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store manages tiered memory operations.
type Store struct {
	memoryDir  string
	warmFile   string
	hotFile    string
	treeFile   string
}

// WarmMemoryEntry represents a warm memory entry.
type WarmMemoryEntry struct {
	ID        string                 `json:"id"`
	Text      string                 `json:"text"`
	Category  string                 `json:"category"`
	Importance float64                `json:"importance"`
	CreatedAt float64                `json:"created_at"`
	AccessCount int                  `json:"access_count"`
	Score     float64                `json:"score"`
	Metadata  map[string][]string    `json:"metadata"`
	Tier      string                 `json:"tier"`
}

// HotMemoryState represents the hot memory state.
type HotMemoryState struct {
	AgentID string `json:"agent_id"`
	Identity struct {
		Name        string `json:"name"`
		Emoji       string `json:"emoji"`
		Description string `json:"description"`
	} `json:"identity"`
	Owner struct {
		Name        string   `json:"name"`
		Preferences []string `json:"preferences"`
		Timezone    string   `json:"timezone"`
	} `json:"owner"`
	ActiveContext struct {
		CurrentProject string   `json:"current_project"`
		RecentTopics   []string `json:"recent_topics"`
		PendingTasks   []string `json:"pending_tasks"`
	} `json:"active_context"`
	Lessons []map[string]any `json:"lessons"`
	Events  []map[string]any `json:"events"`
	Tasks   []map[string]any `json:"tasks"`
}

// NewStore creates a new tiered memory store.
func NewStore(basePath string) *Store {
	memoryDir := filepath.Join(basePath, "memory")
	return &Store{
		memoryDir: memoryDir,
		warmFile:  filepath.Join(memoryDir, "warm-memory.json"),
		hotFile:   filepath.Join(memoryDir, "hot-memory-state.json"),
		treeFile:  filepath.Join(memoryDir, "memory-tree.json"),
	}
}

// ReadWarmMemories reads warm memory entries.
func (s *Store) ReadWarmMemories() ([]WarmMemoryEntry, error) {
	data, err := os.ReadFile(s.warmFile)
	if err != nil {
		return nil, err
	}

	var entries []WarmMemoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

// ReadHotState reads hot memory state.
func (s *Store) ReadHotState() (*HotMemoryState, error) {
	data, err := os.ReadFile(s.hotFile)
	if err != nil {
		return nil, err
	}

	var state HotMemoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// Retrieve searches for relevant memories based on query.
// Returns top N relevant entries.
func (s *Store) Retrieve(query string, limit int) ([]WarmMemoryEntry, error) {
	entries, err := s.ReadWarmMemories()
	if err != nil {
		return nil, err
	}

	// Simple keyword matching (can be improved with BM25)
	type scoredEntry struct {
		entry WarmMemoryEntry
		score float64
	}

	var scored []scoredEntry
	queryLower := strings.ToLower(query)

	for _, entry := range entries {
		score := s.calculateScore(entry, queryLower)
		if score > 0 {
			scored = append(scored, scoredEntry{entry: entry, score: score})
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top N
	if limit > len(scored) {
		limit = len(scored)
	}

	result := make([]WarmMemoryEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = scored[i].entry
	}

	return result, nil
}

// calculateScore calculates relevance score for keyword matching.
func (s *Store) calculateScore(entry WarmMemoryEntry, queryLower string) float64 {
	textLower := strings.ToLower(entry.Text)
	categoryLower := strings.ToLower(entry.Category)

	// Exact match in category
	if strings.Contains(categoryLower, queryLower) || strings.Contains(queryLower, categoryLower) {
		return 1.0
	}

	// Text matching
	if strings.Contains(textLower, queryLower) {
		return 0.8
	}

	// Partial word matching
	words := strings.Fields(queryLower)
	for _, word := range words {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(textLower, word) {
			return 0.5
		}
	}

	return 0
}

// GetMemoryContext retrieves relevant memory for inclusion in system prompt.
// It combines hot state and relevant warm memories.
func (s *Store) GetMemoryContext(query string) (string, error) {
	var parts []string

	// Add hot state (identity, owner profile)
	hotState, err := s.ReadHotState()
	if err == nil {
		hotSummary := s.formatHotState(hotState)
		if hotSummary != "" {
			parts = append(parts, fmt.Sprintf("# Memory\n\n%s", hotSummary))
		}
	}

	// Retrieve relevant warm memories
	memories, err := s.Retrieve(query, 5)
	if err == nil && len(memories) > 0 {
		memSummary := s.formatMemories(memories)
		if memSummary != "" {
			if len(parts) > 0 {
				parts = append(parts, memSummary)
			} else {
				parts = append(parts, fmt.Sprintf("# Relevant Memories\n\n%s", memSummary))
			}
		}
	}

	if len(parts) == 0 {
		return "", nil
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// formatHotState formats hot state for system prompt.
func (s *Store) formatHotState(state *HotMemoryState) string {
	var lines []string

	if state.Identity.Name != "" {
		lines = append(lines, fmt.Sprintf("**Identity**: %s %s", state.Identity.Emoji, state.Identity.Name))
		if state.Identity.Description != "" {
			lines = append(lines, fmt.Sprintf("%s", state.Identity.Description))
		}
	}

	if state.Owner.Name != "" {
		lines = append(lines, fmt.Sprintf("**Owner**: %s (timezone: %s)", state.Owner.Name, state.Owner.Timezone))
		if len(state.Owner.Preferences) > 0 {
			lines = append(lines, fmt.Sprintf("Preferences: %s", strings.Join(state.Owner.Preferences, ", ")))
		}
	}

	if state.ActiveContext.CurrentProject != "" {
		lines = append(lines, fmt.Sprintf("**Current Project**: %s", state.ActiveContext.CurrentProject))
	}

	if len(state.ActiveContext.RecentTopics) > 0 {
		lines = append(lines, fmt.Sprintf("**Recent Topics**: %s", strings.Join(state.ActiveContext.RecentTopics, ", ")))
	}

	if len(state.ActiveContext.PendingTasks) > 0 {
		lines = append(lines, fmt.Sprintf("**Pending Tasks**:"))
		for _, task := range state.ActiveContext.PendingTasks {
			lines = append(lines, fmt.Sprintf("- %s", task))
		}
	}

	if len(state.Lessons) > 0 {
		lines = append(lines, fmt.Sprintf("**Lessons**:"))
		for _, lesson := range state.Lessons {
			if text, ok := lesson["text"].(string); ok {
				lines = append(lines, fmt.Sprintf("- %s", text))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// formatMemories formats retrieved memories for system prompt.
func (s *Store) formatMemories(memories []WarmMemoryEntry) string {
	if len(memories) == 0 {
		return ""
	}

	var lines []string
	for _, mem := range memories {
		lines = append(lines, fmt.Sprintf("- **%s**: %s", mem.Category, mem.Text))
	}

	return strings.Join(lines, "\n")
}
