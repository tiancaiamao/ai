package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// newTestSessionDir builds a persisted session directory with two windows
// (pre- and post-compaction) and returns its path plus the first entry ID.
func newTestSessionDir(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session-under-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := session.NewSession(dir)

	firstID, err := sess.AppendMessage(agentctx.NewUserMessage("hello world from history"))
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := sess.AppendMessage(agentctx.NewUserMessage("Hello WORLD again")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := sess.AppendCompaction("first generation summary", []agentctx.AgentMessage{
		agentctx.NewUserMessage("kept snapshot message"),
	}); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	if _, err := sess.AppendMessage(agentctx.NewUserMessage("after compaction")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	return dir, firstID
}

// runCapture runs runHistory and captures both output streams.
func runCapture(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := runHistory(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

// decodeJSONLines parses every stdout line as a JSON object.
func decodeJSONLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	objects := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("stdout line is not valid JSON (%v): %q", err, line)
		}
		objects = append(objects, obj)
	}
	return objects
}

func TestRunHistoryNoArgsShowsUsageError(t *testing.T) {
	stdout, stderr, code := runCapture()
	if code == 0 {
		t.Fatal("expected non-zero exit code for missing action")
	}
	if stdout != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "missing history action") || !strings.Contains(stderr, "Usage:") {
		t.Errorf("expected helpful usage error on stderr, got %q", stderr)
	}
}

func TestRunHistoryUnknownAction(t *testing.T) {
	_, stderr, code := runCapture("bogus")
	if code == 0 {
		t.Fatal("expected non-zero exit code for unknown action")
	}
	if !strings.Contains(stderr, `unknown history action "bogus"`) {
		t.Errorf("expected unknown-action error, got %q", stderr)
	}
}

func TestRunHistoryReadRequiresEntry(t *testing.T) {
	_, stderr, code := runCapture("read")
	if code == 0 {
		t.Fatal("expected non-zero exit code when --entry is missing")
	}
	if !strings.Contains(stderr, "--entry is required") {
		t.Errorf("expected --entry error, got %q", stderr)
	}
}

func TestRunHistorySearchQueryValidation(t *testing.T) {
	_, stderr, code := runCapture("search")
	if code == 0 || !strings.Contains(stderr, "query is required") {
		t.Errorf("expected missing-query error, got code %d stderr %q", code, stderr)
	}

	_, stderr, code = runCapture("search", strings.Repeat("x", 1001))
	if code == 0 || !strings.Contains(stderr, "exceeds 1000 characters") {
		t.Errorf("expected over-long-query error, got code %d stderr %q", code, stderr)
	}
}

func TestRunHistoryWindowsJSON(t *testing.T) {
	dir, _ := newTestSessionDir(t)
	stdout, stderr, code := runCapture("windows", "--session", dir, "--json")
	if code != 0 {
		t.Fatalf("windows --json failed: %s", stderr)
	}
	windows := decodeJSONLines(t, stdout)
	if len(windows) != 2 {
		t.Fatalf("got %d windows, want 2: %v", len(windows), windows)
	}
	// Default order is newest first: the compaction window comes first.
	if _, ok := windows[0]["window_id"]; !ok {
		t.Errorf("window_id field missing: %v", windows[0])
	}
	if _, ok := windows[0]["item_count"]; !ok {
		t.Errorf("item_count field missing: %v", windows[0])
	}
	if windows[0]["summary_preview"] != "first generation summary" {
		t.Errorf("summary_preview = %v", windows[0]["summary_preview"])
	}
}

func TestRunHistoryListTruncatesItemContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sess")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := session.NewSession(dir)
	if _, err := sess.AppendMessage(agentctx.NewUserMessage(strings.Repeat("x", 900))); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	stdout, stderr, code := runCapture("list", "--session", dir, "--max-chars", "100")
	if code != 0 {
		t.Fatalf("list failed: %s", stderr)
	}
	if !strings.Contains(stdout, "…[truncated, 900 chars total]") {
		t.Errorf("expected truncation marker in output, got %q", stdout)
	}
}

func TestRunHistoryListClampedTotalShowsLowerBound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sess")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := session.NewSession(dir)
	for i := 0; i < session.HistoryMaxLimit+5; i++ {
		if _, err := sess.AppendMessage(agentctx.NewUserMessage(fmt.Sprintf("message %d", i))); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	// Default limit (20) with a full probe page: the summary must be a lower
	// bound, not the shown count masquerading as a total.
	stdout, stderr, code := runCapture("list", "--session", dir)
	if code != 0 {
		t.Fatalf("list failed: %s", stderr)
	}
	if !strings.Contains(stdout, "showing 20 of 100+ item(s)") {
		t.Errorf("expected lower-bound summary, got %q", stdout)
	}
	if strings.Contains(stdout, "showing 20 of 20 item(s)") {
		t.Error("full page must not be reported as an exact total")
	}

	// --limit above HistoryMaxLimit is clamped, so the page itself is full.
	stdout, _, code = runCapture("list", "--session", dir, "--limit", "200")
	if code != 0 {
		t.Fatalf("list --limit 200 failed")
	}
	if !strings.Contains(stdout, "showing 100 of 100+ item(s)") {
		t.Errorf("expected clamped lower-bound summary, got %q", stdout)
	}
}

func TestRunHistoryReadPaging(t *testing.T) {
	dir, firstID := newTestSessionDir(t)
	stdout, stderr, code := runCapture("read", "--session", dir, "--entry", firstID)
	if code != 0 {
		t.Fatalf("read failed: %s", stderr)
	}
	if !strings.Contains(stdout, "total_chars: 24") || !strings.Contains(stdout, "hello world from history") {
		t.Errorf("unexpected read output: %q", stdout)
	}

	// Offset beyond the end is valid: empty content, exit 0.
	stdout, stderr, code = runCapture("read", "--session", dir, "--entry", firstID, "--offset-chars", "100000")
	if code != 0 {
		t.Fatalf("read beyond end should exit 0: %s", stderr)
	}
	if !strings.Contains(stdout, "total_chars: 24") {
		t.Errorf("read beyond end should still report total_chars: %q", stdout)
	}

	// Unknown entry errors with a clear message.
	_, stderr, code = runCapture("read", "--session", dir, "--entry", "no-such-entry")
	if code == 0 || !strings.Contains(stderr, "no-such-entry") {
		t.Errorf("expected not-found error, got code %d stderr %q", code, stderr)
	}

	// JSON mode emits one machine-readable object.
	stdout, _, code = runCapture("read", "--session", dir, "--entry", firstID, "--json")
	if code != 0 {
		t.Fatalf("read --json failed")
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &item); err != nil {
		t.Fatalf("read --json output is not a JSON object: %v", err)
	}
	if item["entry_id"] != firstID {
		t.Errorf("entry_id = %v, want %v", item["entry_id"], firstID)
	}
}

func TestRunHistorySearchCaseSensitivity(t *testing.T) {
	dir, _ := newTestSessionDir(t)

	// Default search is case-insensitive: "HELLO WORLD" matches both the
	// lowercase and mixed-case messages.
	stdout, stderr, code := runCapture("search", "--session", dir, "--json", "HELLO WORLD")
	if code != 0 {
		t.Fatalf("search failed: %s", stderr)
	}
	results := decodeJSONLines(t, stdout)
	if len(results) != 2 {
		t.Fatalf("got %d case-insensitive matches, want 2: %v", len(results), results)
	}
	for _, r := range results {
		if total, ok := r["total_count"].(float64); !ok || int(total) != len(results) {
			t.Errorf("total_count = %v, want %d", r["total_count"], len(results))
		}
	}

	// --case-sensitive flips the behavior.
	stdout, _, code = runCapture("search", "--session", dir, "--case-sensitive", "--json", "HELLO WORLD")
	if code != 0 {
		t.Fatalf("case-sensitive search failed")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("expected no case-sensitive matches, got %q", stdout)
	}
}

func TestRunHistorySearchFlagsAfterQuery(t *testing.T) {
	dir, _ := newTestSessionDir(t)
	// Design §4.1 shows flags after the positional query; both orders work.
	stdout, stderr, code := runCapture("search", "HELLO WORLD", "--session", dir, "--limit", "1", "--json")
	if code != 0 {
		t.Fatalf("search with trailing flags failed: %s", stderr)
	}
	results := decodeJSONLines(t, stdout)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (--limit applied)", len(results))
	}
	if results[0]["total_count"].(float64) != 2 {
		t.Errorf("total_count = %v, want 2", results[0]["total_count"])
	}
}

func TestSplitPositional(t *testing.T) {
	flags, positional := splitPositional(
		[]string{"-id", "abc", "--json", "some query", "--limit", "5", "words"}, searchValueFlags)
	wantFlags := "-id abc --json --limit 5"
	if got := strings.Join(flags, " "); got != wantFlags {
		t.Errorf("flags = %q, want %q", got, wantFlags)
	}
	if got := strings.Join(positional, " "); got != "some query words" {
		t.Errorf("positional = %q", got)
	}

	flags, positional = splitPositional([]string{"--role=user", "query", "--", "--not-a-flag"}, searchValueFlags)
	if got := strings.Join(flags, " "); got != "--role=user" {
		t.Errorf("flags = %q", got)
	}
	if got := strings.Join(positional, " "); got != "query --not-a-flag" {
		t.Errorf("positional = %q", got)
	}
}

func TestRunHistoryOutputCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sess")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := session.NewSession(dir)
	for i := 0; i < 40; i++ {
		if _, err := sess.AppendMessage(agentctx.NewUserMessage(strings.Repeat("y", 3000))); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	stdout, stderr, code := runCapture("list", "--session", dir, "--limit", "100", "--max-chars", "2000", "--oldest-first")
	if code != 0 {
		t.Fatalf("list failed: %s", stderr)
	}
	if !strings.Contains(stdout, outputTruncatedMarker) {
		t.Error("expected output truncation marker")
	}
	if utf8.RuneCountInString(stdout) > maxOutputChars+utf8.RuneCountInString(outputTruncatedMarker)+2 {
		t.Errorf("output %d chars exceeds cap", utf8.RuneCountInString(stdout))
	}

	// JSON mode also stops at the cap and appends a machine-readable notice.
	stdout, _, code = runCapture("list", "--session", dir, "--limit", "100", "--max-chars", "2000", "--oldest-first", "--json")
	if code != 0 {
		t.Fatalf("list --json failed")
	}
	if !strings.Contains(stdout, `"history_truncated":true`) {
		t.Error("expected JSON truncation notice")
	}
	decodeJSONLines(t, stdout) // every line, including the notice, must parse
}

// withTempHome points HOME at a fresh temp dir so resolution never touches
// the developer's real ~/.ai.
func withTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestSessionDirForRun(t *testing.T) {
	withTempHome(t)
	cwd := t.TempDir() // not a git repo, so it is its own session root
	sessionUUID := "0123456789abcdef0123456789abcdef"

	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		t.Fatalf("GetDefaultSessionsDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionsDir, sessionUUID), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	meta := &tui.RunMeta{ID: "abc123", CWD: cwd, Session: sessionUUID}
	dir, err := sessionDirForRun(meta)
	if err != nil {
		t.Fatalf("sessionDirForRun: %v", err)
	}
	if dir != filepath.Join(sessionsDir, sessionUUID) {
		t.Errorf("sessionDirForRun = %q, want %q", dir, filepath.Join(sessionsDir, sessionUUID))
	}
}

func TestSessionDirForRunMissingSession(t *testing.T) {
	withTempHome(t)
	meta := &tui.RunMeta{ID: "abc123", CWD: t.TempDir(), Session: "deadbeef"}
	_, err := sessionDirForRun(meta)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func saveRun(t *testing.T, baseDir, id, cwd, sessionID string) {
	t.Helper()
	meta := &tui.RunMeta{ID: id, PID: os.Getpid(), CWD: cwd, Status: tui.StatusDone, Session: sessionID}
	if err := tui.SaveRunMeta(meta, tui.RunMetaPath(baseDir, id)); err != nil {
		t.Fatalf("SaveRunMeta: %v", err)
	}
}

func TestResolveSessionDirAmbiguousPrefixListsCandidates(t *testing.T) {
	withTempHome(t)
	baseDir := t.TempDir()
	saveRun(t, baseDir, "abc123", "/tmp/a", "")
	saveRun(t, baseDir, "abd456", "/tmp/b", "")

	_, err := resolveSessionDir(baseDir, "ab", "")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	for _, candidate := range []string{"abc123", "abd456"} {
		if !strings.Contains(err.Error(), candidate) {
			t.Errorf("error %q should list candidate %s", err, candidate)
		}
	}
}

func TestResolveSessionDirMissingSessionPromptsEscapeHatch(t *testing.T) {
	withTempHome(t)
	baseDir := t.TempDir()
	saveRun(t, baseDir, "abc123", "/tmp/a", "")

	_, err := resolveSessionDir(baseDir, "abc123", "")
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Errorf("expected error suggesting --session, got %v", err)
	}
}

func TestResolveSessionDirEscapeHatch(t *testing.T) {
	dir, _ := newTestSessionDir(t)
	got, err := resolveSessionDir("", "", dir)
	if err != nil {
		t.Fatalf("resolveSessionDir: %v", err)
	}
	if got != dir {
		t.Errorf("resolveSessionDir = %q, want %q", got, dir)
	}
}
