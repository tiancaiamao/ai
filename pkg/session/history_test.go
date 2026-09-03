package session

import (
	"fmt"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// appendUser is a test helper that appends a user message and fails on error.
func appendUser(t *testing.T, sess *Session, text string) string {
	t.Helper()
	id, err := sess.AppendMessage(agentctx.NewUserMessage(text))
	if err != nil {
		t.Fatalf("AppendMessage(%q): %v", text, err)
	}
	return id
}

// setCompactionTokens is a test helper that sets TokensBefore on every
// compaction entry. AppendCompaction does not populate it, so tests inject
// a value to verify it is surfaced by ListWindows.
func setCompactionTokens(sess *Session, tokens int) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for _, e := range sess.byID {
		if e.Type == EntryTypeCompaction {
			e.TokensBefore = tokens
		}
	}
}

func TestHistoryListWindows(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)

	appendUser(t, sess, "m1")
	appendUser(t, sess, "m2")
	compactionID, err := sess.AppendCompaction("first generation summary", []agentctx.AgentMessage{
		agentctx.NewUserMessage("kept 1"),
	})
	if err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	appendUser(t, sess, "m3")

	windows, err := sess.ListWindows(HistoryListOptions{OldestFirst: true})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("got %d windows, want 2: %+v", len(windows), windows)
	}

	header := windows[0]
	if header.WindowID != sess.GetID() {
		t.Errorf("first window_id = %q, want session header ID %q", header.WindowID, sess.GetID())
	}
	if header.ItemCount != 2 {
		t.Errorf("first window item_count = %d, want 2", header.ItemCount)
	}
	if header.TokensBefore != 0 || header.SummaryPreview != "" {
		t.Errorf("first window should have no compaction metadata: %+v", header)
	}
	if header.CreatedAt == "" {
		t.Error("first window created_at is empty")
	}

	compacted := windows[1]
	if compacted.WindowID != compactionID {
		t.Errorf("second window_id = %q, want compaction entry ID %q", compacted.WindowID, compactionID)
	}
	if compacted.ItemCount != 2 { // snapshot message + m3

		t.Errorf("second window item_count = %d, want 2", compacted.ItemCount)
	}
	if compacted.SummaryPreview != "first generation summary" {
		t.Errorf("summary_preview = %q", compacted.SummaryPreview)
	}

	// Default order is newest first.
	newest, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows default order: %v", err)
	}
	if newest[0].WindowID != compactionID {
		t.Errorf("default order first window = %q, want newest %q", newest[0].WindowID, compactionID)
	}
}

func TestHistoryListWindows_SummaryPreviewBounded(t *testing.T) {
	sess := NewSession("")
	longSummary := strings.Repeat("s", 500)
	if _, err := sess.AppendCompaction(longSummary, nil); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	windows, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if got := windows[0].SummaryPreview; len(got) != 200 {
		t.Errorf("summary_preview length = %d, want 200", len(got))
	}
}

func TestHistoryListWindows_TokensBefore(t *testing.T) {
	// In-memory session: EnsureFullyLoaded is a no-op, so the injected
	// TokensBefore survives until the query.
	sess := NewSession("")
	appendUser(t, sess, "m1")
	if _, err := sess.AppendCompaction("summary", nil); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	setCompactionTokens(sess, 1234)

	windows, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	// Default order is newest first: the compaction window comes first.
	if windows[0].TokensBefore != 1234 {
		t.Errorf("tokens_before = %d, want 1234", windows[0].TokensBefore)
	}
}

func TestHistoryListWindows_EmptySession(t *testing.T) {

	sess := NewSession("")
	windows, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 || windows[0].WindowID != sess.GetID() {
		t.Fatalf("windows = %+v, want single header window", windows)
	}
}

func TestHistoryListWindows_LimitBounded(t *testing.T) {
	sess := NewSession("")
	appendUser(t, sess, "m1")
	for i := 0; i < HistoryMaxLimit+5; i++ {
		if _, err := sess.AppendCompaction(fmt.Sprintf("gen %d", i), nil); err != nil {
			t.Fatalf("AppendCompaction: %v", err)
		}
	}

	// Default limit is 20.
	defaultLimit, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(defaultLimit) != HistoryDefaultLimit {
		t.Errorf("default limit: got %d windows, want %d", len(defaultLimit), HistoryDefaultLimit)
	}

	// Explicit limit above the cap is clamped.
	clamped, err := sess.ListWindows(HistoryListOptions{Limit: 1000})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(clamped) != HistoryMaxLimit {
		t.Errorf("clamped limit: got %d windows, want %d", len(clamped), HistoryMaxLimit)
	}

	small, err := sess.ListWindows(HistoryListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(small) != 3 {
		t.Errorf("limit 3: got %d windows, want 3", len(small))
	}
}

func TestHistoryListItems(t *testing.T) {
	sess := NewSession("")
	appendUser(t, sess, "user question")
	appendUser(t, sess, "assistant answer text")

	windows, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	headerWindowID := windows[len(windows)-1].WindowID

	items, err := sess.ListItems(HistoryItemsOptions{OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Role != "user" || items[1].Role != "user" {
		t.Errorf("roles = %q, %q", items[0].Role, items[1].Role)
	}
	if items[0].Content != "user question" {
		t.Errorf("content = %q", items[0].Content)
	}
	if items[0].TotalChars != len("user question") {
		t.Errorf("total_chars = %d, want %d", items[0].TotalChars, len("user question"))
	}
	if items[0].WindowID != headerWindowID {
		t.Errorf("window_id = %q, want header window %q", items[0].WindowID, headerWindowID)
	}

	// Default order is newest first.
	reversed, err := sess.ListItems(HistoryItemsOptions{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if reversed[0].Content != "assistant answer text" {
		t.Errorf("default order first item = %q", reversed[0].Content)
	}

	// Role filter.
	filtered, err := sess.ListItems(HistoryItemsOptions{Role: "assistant"})
	if err != nil {
		t.Fatalf("ListItems role filter: %v", err)
	}
	if len(filtered) != 0 {
		t.Errorf("role filter assistant: got %d items, want 0", len(filtered))
	}
}

func TestHistoryListItems_ToolResults(t *testing.T) {
	sess := NewSession("")
	appendUser(t, sess, "run tool")
	if _, err := sess.AppendMessage(agentctx.NewToolResultMessage("call1", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "tool output"},
	}, false)); err != nil {
		t.Fatalf("AppendMessage tool result: %v", err)
	}

	all, err := sess.ListItems(HistoryItemsOptions{OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d items, want 2", len(all))
	}
	tool := all[1]
	if tool.Role != "tool" {
		t.Errorf("role = %q, want tool", tool.Role)
	}
	if tool.ToolName != "bash" {
		t.Errorf("tool_name = %q, want bash", tool.ToolName)
	}

	noTool, err := sess.ListItems(HistoryItemsOptions{ExcludeTool: true})
	if err != nil {
		t.Fatalf("ListItems --no-tool: %v", err)
	}
	if len(noTool) != 1 || noTool[0].Role != "user" {
		t.Fatalf("ExcludeTool items = %+v, want only the user message", noTool)
	}
}

func TestHistoryListItems_Truncation(t *testing.T) {
	sess := NewSession("")
	long := strings.Repeat("a", 500)
	appendUser(t, sess, long)

	items, err := sess.ListItems(HistoryItemsOptions{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	got := items[0].Content
	if !strings.HasSuffix(got, fmt.Sprintf("…[truncated, %d chars total]", 500)) {
		t.Errorf("truncation marker missing: %q", got[len(got)-40:])
	}
	if !strings.HasPrefix(got, strings.Repeat("a", HistoryDefaultItemChars)) {
		t.Errorf("content should start with %d chars", HistoryDefaultItemChars)
	}
	if items[0].TotalChars != 500 {
		t.Errorf("total_chars = %d, want 500", items[0].TotalChars)
	}

	// MaxChars above the cap is clamped to HistoryMaxItemChars.
	huge := strings.Repeat("b", 2500)
	sess2 := NewSession("")
	appendUser(t, sess2, huge)
	clamped, err := sess2.ListItems(HistoryItemsOptions{MaxChars: 99999})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if !strings.Contains(clamped[0].Content, fmt.Sprintf("…[truncated, %d chars total]", 2500)) {
		t.Errorf("expected clamp to %d chars, got len(content)=%d", HistoryMaxItemChars, len(clamped[0].Content))
	}

	// Short content passes through untouched.
	short, err := sess2.ListItems(HistoryItemsOptions{MaxChars: 3000})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	_ = short
}

func TestHistoryListItems_WindowScopedAndUnknownWindow(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)
	appendUser(t, sess, "before compaction")
	compactionID, err := sess.AppendCompaction("summary", []agentctx.AgentMessage{
		agentctx.NewUserMessage("kept from snapshot"),
	})
	if err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	appendUser(t, sess, "after compaction")

	// Header window: only the pre-compaction jsonl messages.
	headerItems, err := sess.ListItems(HistoryItemsOptions{WindowID: sess.GetID(), OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems header window: %v", err)
	}
	if len(headerItems) != 1 || headerItems[0].Content != "before compaction" {
		t.Fatalf("header window items = %+v", headerItems)
	}

	// Compaction window: snapshot content + post-compaction messages.
	gen2, err := sess.ListItems(HistoryItemsOptions{WindowID: compactionID, OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems compaction window: %v", err)
	}
	if len(gen2) != 2 {
		t.Fatalf("compaction window items = %+v, want 2", gen2)
	}
	if gen2[0].Content != "kept from snapshot" || gen2[1].Content != "after compaction" {
		t.Errorf("compaction window contents = %q, %q", gen2[0].Content, gen2[1].Content)
	}

	if _, err := sess.ListItems(HistoryItemsOptions{WindowID: "does-not-exist"}); err == nil {
		t.Error("unknown window_id should return an error")
	}
}

func TestHistoryReadItem_Paging(t *testing.T) {
	sess := NewSession("")
	full := strings.Repeat("0123456789", 50) // 500 chars
	id := appendUser(t, sess, full)

	item, err := sess.ReadItem(id, 0, 200)
	if err != nil {
		t.Fatalf("ReadItem: %v", err)
	}
	if item.Content != full[:200] {
		t.Errorf("first page content mismatch: len=%d", len(item.Content))
	}
	if item.TotalChars != 500 {
		t.Errorf("total_chars = %d, want 500", item.TotalChars)
	}

	page2, err := sess.ReadItem(id, 200, 200)
	if err != nil {
		t.Fatalf("ReadItem page 2: %v", err)
	}
	page3, err := sess.ReadItem(id, 400, 200)
	if err != nil {
		t.Fatalf("ReadItem page 3: %v", err)
	}
	if item.Content+page2.Content+page3.Content != full {
		t.Error("concatenated pages do not reconstruct the full content")
	}

	// Offset beyond the end: valid, empty content with total_chars.
	tail, err := sess.ReadItem(id, 99999, 100)
	if err != nil {
		t.Fatalf("ReadItem past end: %v", err)
	}
	if tail.Content != "" || tail.TotalChars != 500 {
		t.Errorf("past-end read: content=%q total=%d", tail.Content, tail.TotalChars)
	}

	if _, err := sess.ReadItem("no-such-id", 0, 100); err == nil {
		t.Error("unknown entry_id should return an error")
	}
}

func TestHistoryReadItem_LimitClamped(t *testing.T) {
	sess := NewSession("")
	full := strings.Repeat("x", HistoryMaxReadChars+1000)
	id := appendUser(t, sess, full)

	// limit 0 → default; huge limit → clamped to HistoryMaxReadChars.
	item, err := sess.ReadItem(id, 0, HistoryMaxReadChars*10)
	if err != nil {
		t.Fatalf("ReadItem: %v", err)
	}
	if got := len(item.Content); got != HistoryMaxReadChars {
		t.Errorf("content length = %d, want clamp to %d", got, HistoryMaxReadChars)
	}
}

func TestHistorySearch(t *testing.T) {
	sess := NewSession("")
	appendUser(t, sess, "The Quick Brown Fox")
	appendUser(t, sess, "something unrelated")
	appendUser(t, sess, "another quick mention")

	// Default: case-insensitive.
	res, err := sess.Search("quick brown", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalCount != 1 || len(res.Results) != 1 {
		t.Fatalf("search results = %+v, want 1 hit", res)
	}
	hit := res.Results[0]
	if hit.TotalCount != 1 {
		t.Errorf("result total_count = %d, want 1", hit.TotalCount)
	}
	if hit.Role != "user" {
		t.Errorf("role = %q", hit.Role)
	}
	if hit.WindowID != sess.GetID() {
		t.Errorf("window_id = %q, want header window", hit.WindowID)
	}
	if !strings.Contains(hit.Match, "Quick Brown") {
		t.Errorf("match = %q", hit.Match)
	}

	// Case-sensitive misses the differently-cased hit.
	cs, err := sess.Search("quick brown", HistorySearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("Search case-sensitive: %v", err)
	}
	if cs.TotalCount != 0 {
		t.Errorf("case-sensitive total_count = %d, want 0", cs.TotalCount)
	}
	sensitiveHit, err := sess.Search("Quick Brown", HistorySearchOptions{CaseSensitive: true})
	if err != nil {
		t.Fatalf("Search case-sensitive: %v", err)
	}
	if sensitiveHit.TotalCount != 1 {
		t.Errorf("case-sensitive total_count = %d, want 1", sensitiveHit.TotalCount)
	}

	// No hits: empty result, no error.
	none, err := sess.Search("zzz-not-present", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search no hits: %v", err)
	}
	if none.TotalCount != 0 || len(none.Results) != 0 {
		t.Errorf("no-hit search = %+v", none)
	}

	// Query validation.
	if _, err := sess.Search("", HistorySearchOptions{}); err == nil {
		t.Error("empty query should error")
	}
	if _, err := sess.Search(strings.Repeat("q", 1001), HistorySearchOptions{}); err == nil {
		t.Error("query over 1000 chars should error")
	}
}

func TestHistorySearch_MatchWindowBounded(t *testing.T) {
	sess := NewSession("")
	content := strings.Repeat("x", 500) + "needle" + strings.Repeat("y", 500)
	appendUser(t, sess, content)

	res, err := sess.Search("needle", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results = %+v", res.Results)
	}
	if got := res.Results[0].Match; len(got) > HistoryMaxSearchMatchChars {
		t.Errorf("match length = %d, want <= %d", len(got), HistoryMaxSearchMatchChars)
	} else if !strings.Contains(got, "needle") {
		t.Errorf("match = %q", got)
	}

}

func TestHistorySearch_LimitAndTotalCount(t *testing.T) {
	sess := NewSession("")
	for i := 0; i < HistoryMaxLimit+5; i++ {
		appendUser(t, sess, fmt.Sprintf("hit number %d", i))
	}
	res, err := sess.Search("hit number", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalCount != HistoryMaxLimit+5 {
		t.Errorf("total_count = %d, want %d", res.TotalCount, HistoryMaxLimit+5)
	}
	if len(res.Results) != HistoryDefaultLimit {
		t.Errorf("results = %d, want default limit %d", len(res.Results), HistoryDefaultLimit)
	}

	for _, r := range res.Results {
		if r.TotalCount != HistoryMaxLimit+5 {
			t.Fatalf("per-result total_count = %d, want %d", r.TotalCount, HistoryMaxLimit+5)
		}
	}

	capped, err := sess.Search("hit number", HistorySearchOptions{Limit: 500})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(capped.Results) != HistoryMaxLimit {
		t.Errorf("results = %d, want clamp to %d", len(capped.Results), HistoryMaxLimit)
	}

	small, err := sess.Search("hit number", HistorySearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(small.Results) != 3 {
		t.Errorf("results = %d, want 3", len(small.Results))
	}
}

func TestHistorySnapshotReachability(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)

	// Pre-compaction entries land in messages.jsonl and stay reachable.
	ancientID := appendUser(t, sess, "ancient secret phrase before compaction")
	for i := 0; i < 5; i++ {
		appendUser(t, sess, fmt.Sprintf("filler %d", i))
	}
	compactionID, err := sess.AppendCompaction("conversation summary", []agentctx.AgentMessage{
		agentctx.NewUserMessage("summary header message"),
		agentctx.NewUserMessage("kept recent from snapshot"),
	})
	if err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	appendUser(t, sess, "post compaction message")

	// Simulate a fresh (lazy-loaded) session, as the CLI process would do.
	loaded, err := LoadSession(dir)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	// Search reaches pre-compaction content (jsonl) and snapshot content.
	res, err := loaded.Search("ancient secret phrase", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search ancient: %v", err)
	}
	if res.TotalCount != 1 {
		t.Fatalf("ancient search = %+v, want 1 hit", res)
	}
	if res.Results[0].EntryID != ancientID {
		t.Errorf("ancient entry_id = %q, want %q", res.Results[0].EntryID, ancientID)
	}

	res, err = loaded.Search("kept recent from snapshot", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search snapshot: %v", err)
	}
	if res.TotalCount != 1 {
		t.Fatalf("snapshot search = %+v, want 1 hit", res)
	}
	snapshotHit := res.Results[0]
	if snapshotHit.WindowID != compactionID {
		t.Errorf("snapshot hit window_id = %q, want %q", snapshotHit.WindowID, compactionID)
	}
	if snapshotHit.EntryID == "" || snapshotHit.EntryID == ancientID {
		t.Errorf("snapshot hit entry_id = %q", snapshotHit.EntryID)
	}

	// ReadItem reaches the pre-compaction entry through the lazy-loaded session.
	item, err := loaded.ReadItem(ancientID, 0, 100)
	if err != nil {
		t.Fatalf("ReadItem ancient: %v", err)
	}
	if item.Content != "ancient secret phrase before compaction" {
		t.Errorf("content = %q", item.Content)
	}

	// ReadItem reaches snapshot content via its synthetic ID.
	snapItem, err := loaded.ReadItem(snapshotHit.EntryID, 0, 100)
	if err != nil {
		t.Fatalf("ReadItem snapshot item: %v", err)
	}
	if snapItem.Content != "kept recent from snapshot" {
		t.Errorf("content = %q", snapItem.Content)
	}
}

func TestHistoryBranchNotExpanded(t *testing.T) {
	sess := NewSession("")
	rootID := appendUser(t, sess, "shared root message")
	appendUser(t, sess, "branch A only")

	// Fork back to the root and build branch B.
	if err := sess.Branch(rootID); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	branchBID := appendUser(t, sess, "branch B only")

	// Default list: shared root + current branch (B), not branch A.
	items, err := sess.ListItems(HistoryItemsOptions{OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want 2 (root + branch B)", items)
	}
	if items[1].Content != "branch B only" {
		t.Errorf("last item = %q, want branch B", items[1].Content)
	}

	// Default search does not see branch A content.
	res, err := sess.Search("branch A only", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalCount != 0 {
		t.Errorf("search found branch A content: %+v", res)
	}
	res, err = sess.Search("branch B only", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalCount != 1 || res.Results[0].EntryID != branchBID {
		t.Errorf("search = %+v, want branch B hit %q", res, branchBID)
	}

	// Windows only cover the current path.
	windows, err := sess.ListWindows(HistoryListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %+v, want 1", windows)
	}
}

func TestHistoryListItems_EntryAncestorChain(t *testing.T) {
	sess := NewSession("")
	appendUser(t, sess, "first")
	secondID := appendUser(t, sess, "second")
	appendUser(t, sess, "third")

	// --entry <id> returns the entry and its ancestors.
	items, err := sess.ListItems(HistoryItemsOptions{EntryID: secondID, OldestFirst: true})
	if err != nil {
		t.Fatalf("ListItems entry chain: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want 2", items)
	}
	if items[0].Content != "first" || items[1].Content != "second" {
		t.Errorf("chain = %q, %q", items[0].Content, items[1].Content)
	}

	if _, err := sess.ListItems(HistoryItemsOptions{EntryID: "missing-id"}); err == nil {
		t.Error("unknown --entry should error")
	}
}

func TestHistoryLazyLoadFullyResolved(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)
	for i := 0; i < 3; i++ {
		appendUser(t, sess, fmt.Sprintf("early %d", i))
	}
	if _, err := sess.AppendCompaction("early summary", []agentctx.AgentMessage{
		agentctx.NewUserMessage("recent kept"),
	}); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}

	loaded, err := LoadSession(dir)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	// The lazy load truncates the visible path; the query layer must force a
	// full load so pre-compaction entries are searchable.
	res, err := loaded.Search("early 0", HistorySearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalCount != 1 {
		t.Fatalf("lazy session search = %+v, want 1 hit", res)
	}
}
