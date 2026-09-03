package session

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

const (
	// HistoryDefaultLimit is the default number of results returned by a list or search.
	HistoryDefaultLimit = 20
	// HistoryMaxLimit is the maximum number of results returned by a list or search.
	HistoryMaxLimit = 100
	// HistoryDefaultItemChars is the default content limit for list items.
	HistoryDefaultItemChars = 400
	// HistoryMaxItemChars is the maximum content limit for list items.
	HistoryMaxItemChars = 2000
	// HistoryDefaultReadChars is the default content limit for a read operation.
	HistoryDefaultReadChars = 20000
	// HistoryMaxReadChars is the maximum content limit for a read operation.
	HistoryMaxReadChars = 50000
	// HistoryMaxSearchMatchChars is the maximum size of a search context.
	HistoryMaxSearchMatchChars = 400
)

// HistoryWindow describes one session generation. The header ID identifies the
// first generation; subsequent generations are identified by their compaction ID.
type HistoryWindow struct {
	WindowID       string `json:"window_id"`
	CreatedAt      string `json:"created_at"`
	TokensBefore   int    `json:"tokens_before"`
	ItemCount      int    `json:"item_count"`
	SummaryPreview string `json:"summary_preview"`
}

// HistoryItem is a message entry exposed by the history query layer.
type HistoryItem struct {
	EntryID    string `json:"entry_id"`
	Role       string `json:"role"`
	Timestamp  string `json:"timestamp"`
	Content    string `json:"content"`
	TotalChars int    `json:"total_chars"`
	ToolName   string `json:"tool_name,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
}

// HistoryListOptions controls ListWindows ordering and bounds.
type HistoryListOptions struct {
	Limit       int
	OldestFirst bool
}

// HistoryItemsOptions controls ListItems filtering, ordering, and bounds.
type HistoryItemsOptions struct {
	WindowID    string
	Role        string
	ExcludeTool bool
	EntryID     string
	Limit       int
	MaxChars    int
	OldestFirst bool
}

// HistorySearchOptions controls Search matching, filtering, and bounds.
type HistorySearchOptions struct {
	WindowID      string
	Role          string
	ExcludeTool   bool
	Limit         int
	CaseSensitive bool
}

// HistorySearchResult is a matching message or snapshot item. TotalCount is
// repeated on every result so a JSONL consumer can use a result independently.
type HistorySearchResult struct {
	EntryID    string `json:"entry_id"`
	Role       string `json:"role"`
	WindowID   string `json:"window_id"`
	Timestamp  string `json:"timestamp"`
	Match      string `json:"match"`
	TotalCount int    `json:"total_count"`
}

// HistorySearchResponse contains bounded search results and the unbounded match count.
type HistorySearchResponse struct {
	Results    []HistorySearchResult `json:"results"`
	TotalCount int                   `json:"total_count"`
}

type historyWindowData struct {
	window HistoryWindow
	items  []HistoryItem
}

// ListWindows returns compaction generations visible from the current leaf.
func (s *Session) ListWindows(opts HistoryListOptions) ([]HistoryWindow, error) {
	if err := s.prepareHistory(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	windows, _ := s.historyWindowsLocked()
	if !opts.OldestFirst {
		reverseWindows(windows)
	}
	return windows[:boundedEnd(len(windows), opts.Limit)], nil
}

// ListItems returns message entries from a generation or the current path.
func (s *Session) ListItems(opts HistoryItemsOptions) ([]HistoryItem, error) {
	if err := s.prepareHistory(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.historyItemsLocked(opts)
	if err != nil {
		return nil, err
	}
	if !opts.OldestFirst {
		reverseItems(items)
	}
	items = items[:boundedEnd(len(items), opts.Limit)]
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = HistoryDefaultItemChars
	}
	if maxChars > HistoryMaxItemChars {
		maxChars = HistoryMaxItemChars
	}
	for i := range items {
		items[i].Content = truncateHistoryContent(items[i].Content, maxChars)
	}
	return items, nil
}

// ReadItem returns one message in character-based pages. An offset beyond the
// end is valid and returns empty content with the original total length.
func (s *Session) ReadItem(entryID string, offset, limit int) (HistoryItem, error) {
	if err := s.prepareHistory(); err != nil {
		return HistoryItem{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID == "" {
		return HistoryItem{}, fmt.Errorf("entry ID is required")
	}
	item, ok := s.findHistoryItemLocked(entryID)
	if !ok {
		return HistoryItem{}, fmt.Errorf("history entry %q not found", entryID)
	}
	if offset < 0 {
		return HistoryItem{}, fmt.Errorf("offset must not be negative")
	}
	if limit <= 0 {
		limit = HistoryDefaultReadChars
	}
	if limit > HistoryMaxReadChars {
		limit = HistoryMaxReadChars
	}
	item.Content = sliceHistoryContent(item.Content, offset, limit)
	return item, nil
}

// Search performs a literal substring search over current-branch message
// entries and the snapshots referenced by current-branch compactions.
func (s *Session) Search(query string, opts HistorySearchOptions) (HistorySearchResponse, error) {
	if query == "" {
		return HistorySearchResponse{}, fmt.Errorf("search query must not be empty")
	}
	if utf8.RuneCountInString(query) > 1000 {
		return HistorySearchResponse{}, fmt.Errorf("search query exceeds 1000 characters")
	}
	if err := s.prepareHistory(); err != nil {
		return HistorySearchResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.searchItemsLocked(opts)
	if err != nil {
		return HistorySearchResponse{}, err
	}
	response := HistorySearchResponse{Results: make([]HistorySearchResult, 0), TotalCount: 0}
	for _, item := range items {
		if !opts.CaseSensitive {
			if !strings.Contains(strings.ToLower(item.Content), strings.ToLower(query)) {
				continue
			}
		} else if !strings.Contains(item.Content, query) {
			continue
		}
		response.TotalCount++
		if len(response.Results) < boundedLimit(opts.Limit) {
			response.Results = append(response.Results, HistorySearchResult{
				EntryID: item.EntryID, Role: item.Role, WindowID: item.WindowID,
				Timestamp: item.Timestamp, Match: historyMatch(item.Content, query, opts.CaseSensitive),
			})
		}
	}
	for i := range response.Results {
		response.Results[i].TotalCount = response.TotalCount
	}
	return response, nil
}

func (s *Session) prepareHistory() error {
	// EnsureFullyLoaded deliberately uses the session's lock and the canonical
	// full loader. Queries acquire the lock again while traversing the snapshot.
	return s.EnsureFullyLoaded()
}

func (s *Session) historyWindowsLocked() ([]HistoryWindow, []historyWindowData) {
	path := pathToLeaf(s.entries, s.leafID, s.byID)
	compactionIndexes := make([]int, 0)
	for i, entry := range path {
		if entry.Type == EntryTypeCompaction {
			compactionIndexes = append(compactionIndexes, i)
		}
	}

	windows := make([]HistoryWindow, 0, len(compactionIndexes)+1)
	data := make([]historyWindowData, 0, len(compactionIndexes)+1)
	appendWindow := func(window HistoryWindow, items []HistoryItem) {
		windows = append(windows, window)
		data = append(data, historyWindowData{window: window, items: items})
	}

	firstCompaction := len(path)
	if len(compactionIndexes) > 0 {
		firstCompaction = compactionIndexes[0]
	}
	headerItems := s.mainMessageItems(path[:firstCompaction], s.header.ID)
	appendWindow(HistoryWindow{
		WindowID: s.header.ID, CreatedAt: s.header.Timestamp, ItemCount: len(headerItems),
	}, headerItems)

	for i, compactionIndex := range compactionIndexes {
		compaction := path[compactionIndex]
		nextIndex := len(path)
		if i+1 < len(compactionIndexes) {
			nextIndex = compactionIndexes[i+1]
		}
		// The snapshot holds the compacted messages at the start of this
		// generation; entries appended after the compaction entry follow.
		items, err := s.snapshotItemsLocked(compaction, compaction.ID)
		if err != nil {
			items = nil
		}
		items = append(items, s.mainMessageItems(path[compactionIndex+1:nextIndex], compaction.ID)...)
		appendWindow(HistoryWindow{
			WindowID:       compaction.ID,
			CreatedAt:      compaction.Timestamp,
			TokensBefore:   compaction.TokensBefore,
			ItemCount:      len(items),
			SummaryPreview: truncateRunes(compaction.Summary, 200),
		}, items)
	}
	return windows, data
}

func (s *Session) historyItemsLocked(opts HistoryItemsOptions) ([]HistoryItem, error) {
	path := pathToLeaf(s.entries, s.leafID, s.byID)
	if opts.EntryID != "" {
		items, ok := s.itemsToEntryLocked(path, opts.EntryID)
		if !ok {
			return nil, fmt.Errorf("history entry %q not found", opts.EntryID)
		}
		return filterHistoryItems(items, opts), nil
	}
	if opts.WindowID == "" {
		return filterHistoryItems(s.currentPathItems(path), opts), nil
	}

	_, data := s.historyWindowsLocked()
	for _, generation := range data {
		if generation.window.WindowID == opts.WindowID {
			return filterHistoryItems(generation.items, opts), nil
		}
	}
	return nil, fmt.Errorf("history window %q not found", opts.WindowID)
}

func (s *Session) itemsToEntryLocked(path []*SessionEntry, id string) ([]HistoryItem, bool) {
	all := s.currentPathItems(path)
	for i, item := range all {
		if item.EntryID == id {
			return all[:i+1], true
		}
	}
	item, ok := s.findHistoryItemLocked(id)
	if !ok {
		return nil, false
	}
	return []HistoryItem{item}, true
}

func (s *Session) currentPathItems(path []*SessionEntry) []HistoryItem {
	windowID := s.header.ID
	items := make([]HistoryItem, 0)
	for _, entry := range path {
		if entry.Type == EntryTypeCompaction {
			windowID = entry.ID
			continue
		}
		if entry.Type == EntryTypeMessage && entry.Message != nil {
			items = append(items, historyItemFromEntry(entry, windowID))
		}
	}
	return items
}

func (s *Session) searchItemsLocked(opts HistorySearchOptions) ([]HistoryItem, error) {
	path := pathToLeaf(s.entries, s.leafID, s.byID)
	filter := HistoryItemsOptions{Role: opts.Role, ExcludeTool: opts.ExcludeTool}

	// A specific window searches only that generation; the default scope is
	// all current-branch messages plus every snapshot reachable from the
	// current branch (pre-compaction content).
	if opts.WindowID != "" {
		_, data := s.historyWindowsLocked()
		for _, generation := range data {
			if generation.window.WindowID == opts.WindowID {
				return filterHistoryItems(generation.items, filter), nil
			}
		}
		return nil, fmt.Errorf("history window %q not found", opts.WindowID)
	}

	items := s.currentPathItems(path)
	for _, entry := range path {
		if entry.Type != EntryTypeCompaction {
			continue
		}
		if snapshotItems, err := s.snapshotItemsLocked(entry, entry.ID); err == nil {
			items = append(items, snapshotItems...)
		}
	}
	return filterHistoryItems(items, filter), nil
}

func (s *Session) findHistoryItemLocked(id string) (HistoryItem, bool) {
	path := pathToLeaf(s.entries, s.leafID, s.byID)
	for _, item := range s.currentPathItems(path) {
		if item.EntryID == id {
			return item, true
		}
	}
	for _, entry := range path {
		if entry.Type != EntryTypeCompaction {
			continue
		}
		items, err := s.snapshotItemsLocked(entry, entry.ID)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.EntryID == id {
				return item, true
			}
		}
	}
	return HistoryItem{}, false
}

func (s *Session) mainMessageItems(path []*SessionEntry, windowID string) []HistoryItem {
	items := make([]HistoryItem, 0)
	for _, entry := range path {
		if entry.Type == EntryTypeMessage && entry.Message != nil {
			items = append(items, historyItemFromEntry(entry, windowID))
		}
	}
	return items
}

func (s *Session) snapshotItemsLocked(compaction *SessionEntry, windowID string) ([]HistoryItem, error) {
	messages, err := resolveSnapshot(compaction, s.sessionDir)
	if err != nil {
		// Legacy compactions have no snapshot file; missing/corrupt snapshots
		// are skipped so other results are unaffected.
		if compaction.SnapshotRef != "" {
			slog.Warn("[session] Failed to read history snapshot",
				"path", compaction.SnapshotRef, "error", err)
		}
		return nil, err
	}

	items := make([]HistoryItem, 0, len(messages))
	for i, message := range messages {
		id := message.EntryID
		if id == "" {
			id = fmt.Sprintf("%s:%d", compaction.ID, i)
		}
		items = append(items, historyItemFromMessage(id, &message, timestampFromMillis(message.Timestamp), windowID))
	}
	return items, nil
}

func historyItemFromEntry(entry *SessionEntry, windowID string) HistoryItem {
	return historyItemFromMessage(entry.ID, entry.Message, entry.Timestamp, windowID)
}

func historyItemFromMessage(id string, message *agentctx.AgentMessage, timestamp, windowID string) HistoryItem {
	role := message.Role
	if role == "toolResult" {
		role = "tool"
	}
	content := message.ExtractText()
	return HistoryItem{EntryID: id, Role: role, Timestamp: timestamp, Content: content,
		TotalChars: utf8.RuneCountInString(content), ToolName: message.ToolName, WindowID: windowID}
}

func filterHistoryItems(items []HistoryItem, opts HistoryItemsOptions) []HistoryItem {
	filtered := make([]HistoryItem, 0, len(items))
	for _, item := range items {
		if opts.Role != "" && item.Role != opts.Role {
			continue
		}
		if opts.ExcludeTool && item.Role == "tool" {
			continue
		}
		if opts.WindowID != "" && item.WindowID != "" && item.WindowID != opts.WindowID {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func boundedLimit(limit int) int {
	if limit <= 0 {
		return HistoryDefaultLimit
	}
	if limit > HistoryMaxLimit {
		return HistoryMaxLimit
	}
	return limit
}

func boundedEnd(length, limit int) int {
	if length == 0 {

		return 0
	}
	return min(length, boundedLimit(limit))
}

func reverseWindows(items []HistoryWindow) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func reverseItems(items []HistoryItem) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func truncateHistoryContent(content string, limit int) string {
	if utf8.RuneCountInString(content) <= limit {
		return content
	}
	return truncateRunes(content, limit) + fmt.Sprintf("…[truncated, %d chars total]", utf8.RuneCountInString(content))
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func sliceHistoryContent(content string, offset, limit int) string {
	runes := []rune(content)
	if offset >= len(runes) {
		return ""
	}
	end := min(len(runes), offset+limit)
	return string(runes[offset:end])
}

func historyMatch(content, query string, caseSensitive bool) string {
	runes := []rune(content)
	queryRunes := []rune(query)
	if !caseSensitive {
		lowerContent := []rune(strings.ToLower(content))
		lowerQuery := []rune(strings.ToLower(query))
		for i := 0; i+len(lowerQuery) <= len(lowerContent); i++ {
			if string(lowerContent[i:i+len(lowerQuery)]) == string(lowerQuery) {
				return matchWindow(runes, i, len(lowerQuery))
			}
		}
	}
	for i := 0; i+len(queryRunes) <= len(runes); i++ {
		if string(runes[i:i+len(queryRunes)]) == query {
			return matchWindow(runes, i, len(queryRunes))
		}
	}
	return truncateRunes(content, HistoryMaxSearchMatchChars)
}

func matchWindow(content []rune, start, queryLen int) string {
	contextChars := (HistoryMaxSearchMatchChars - 1) / 2
	from := max(0, start-contextChars)
	to := min(len(content), start+queryLen+contextChars)
	prefix, suffix := "", ""
	if from > 0 {
		prefix = "…"
	}
	if to < len(content) {
		suffix = "…"
	}
	body := content[from:to]
	// Keep the ellipses within the overall match budget.
	budget := HistoryMaxSearchMatchChars - len(prefix) - len(suffix)
	if budget < 0 {
		budget = 0
	}
	if len(body) > budget {
		body = body[:budget]
	}
	return prefix + string(body) + suffix
}

func timestampFromMillis(timestamp int64) string {
	if timestamp == 0 {
		return ""
	}
	return time.UnixMilli(timestamp).UTC().Format(time.RFC3339Nano)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
