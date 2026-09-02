package history

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tiancaiamao/ai/pkg/session"
)

// maxQueryRunes is the search query length upper bound (design §4.1).
const maxQueryRunes = 1000

// homeBaseDir returns the ai state directory (~/.ai).
func homeBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, ".ai"), nil
}

// runWindows implements `ai history windows`.
func runWindows(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("windows")
	limit := fs.Int("limit", session.HistoryDefaultLimit, "max windows to return (max 100)")
	oldestFirst := fs.Bool("oldest-first", false, "oldest generation first")
	jsonOutput := fs.Bool("json", false, "emit JSONL")
	var idFlag, sessionFlag string
	addGlobalFlags(fs, &idFlag, &sessionFlag)
	if !parseActionFlags(fs, args, stderr) {
		return 1
	}

	baseDir, err := homeBaseDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	sess, err := loadTarget(baseDir, idFlag, sessionFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	windows, err := sess.ListWindows(session.HistoryListOptions{Limit: *limit, OldestFirst: *oldestFirst})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	out := &boundedWriter{json: *jsonOutput}
	for _, win := range windows {
		out.emitRecord(formatWindow(win), win)
	}
	if !*jsonOutput {
		out.emitRecord(formatSummary("window", len(windows), len(windows)), "")
	}
	out.flush(stdout)
	return 0
}

// runList implements `ai history list`.
func runList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list")
	windowID := fs.String("window", "", "list a specific window instead of the current path")
	role := fs.String("role", "", "filter by role: user|assistant|tool|system|developer")
	noTool := fs.Bool("no-tool", false, "exclude tool results")
	entryID := fs.String("entry", "", "start from this entry and include its ancestor chain")
	limit := fs.Int("limit", session.HistoryDefaultLimit, "max items to return (max 100)")
	maxChars := fs.Int("max-chars", session.HistoryDefaultItemChars, "truncate item content at N chars (max 2000)")
	oldestFirst := fs.Bool("oldest-first", false, "oldest item first")
	jsonOutput := fs.Bool("json", false, "emit JSONL")
	var idFlag, sessionFlag string
	addGlobalFlags(fs, &idFlag, &sessionFlag)
	if !parseActionFlags(fs, args, stderr) {
		return 1
	}

	baseDir, err := homeBaseDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	sess, err := loadTarget(baseDir, idFlag, sessionFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	opts := session.HistoryItemsOptions{
		WindowID: *windowID, Role: *role, ExcludeTool: *noTool, EntryID: *entryID,
		Limit: *limit, MaxChars: *maxChars, OldestFirst: *oldestFirst,
	}
	items, err := sess.ListItems(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	// The query layer clamps results at HistoryMaxLimit without reporting a
	// total; probe a full page to report how much is left unfetched.
	total, totalKnown := totalItemCount(sess, opts)

	out := &boundedWriter{json: *jsonOutput}
	for _, item := range items {
		out.emitRecord(formatItem(item), item)
	}
	if !*jsonOutput {
		if totalKnown {
			out.emitRecord(formatSummary("item", len(items), total), "")
		} else {
			out.emitRecord(formatSummary("item", len(items), len(items)), "")
		}
	}
	out.flush(stdout)
	return 0
}

// totalItemCount estimates the number of items matching opts, capped at
// session.HistoryMaxLimit: when the probe page is full the caller displays
// that as a lower bound ("100+"). The second parameter reports whether the
// total is exact.
func totalItemCount(sess *session.Session, opts session.HistoryItemsOptions) (int, bool) {
	probe, err := sess.ListItems(session.HistoryItemsOptions{
		WindowID: opts.WindowID, Role: opts.Role, ExcludeTool: opts.ExcludeTool, EntryID: opts.EntryID,
		Limit: session.HistoryMaxLimit, MaxChars: 1, OldestFirst: true,
	})
	if err != nil {
		return 0, false
	}
	if len(probe) < session.HistoryMaxLimit {
		return len(probe), true
	}
	return len(probe), false
}

// runRead implements `ai history read`.
func runRead(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("read")
	entryID := fs.String("entry", "", "entry to read (required)")
	offsetChars := fs.Int("offset-chars", 0, "character offset to start from")
	limitChars := fs.Int("limit-chars", session.HistoryDefaultReadChars, "max characters to return (max 50000)")
	jsonOutput := fs.Bool("json", false, "emit a single JSON object")
	var idFlag, sessionFlag string
	addGlobalFlags(fs, &idFlag, &sessionFlag)
	if !parseActionFlags(fs, args, stderr) {
		return 1
	}

	if *entryID == "" {
		fmt.Fprintln(stderr, "error: --entry is required")
		return 1
	}
	if *offsetChars < 0 {
		fmt.Fprintf(stderr, "error: --offset-chars must not be negative\n")
		return 1
	}

	baseDir, err := homeBaseDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	sess, err := loadTarget(baseDir, idFlag, sessionFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	item, err := sess.ReadItem(*entryID, *offsetChars, *limitChars)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		data, err := json.Marshal(item)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}

	fmt.Fprintf(stdout, "entry_id: %s\nrole: %s\ntimestamp: %s\ntotal_chars: %d\n",
		item.EntryID, item.Role, item.Timestamp, item.TotalChars)
	if item.ToolName != "" {
		fmt.Fprintf(stdout, "tool_name: %s\n", item.ToolName)
	}
	fmt.Fprintf(stdout, "\n%s", item.Content)
	if !strings.HasSuffix(item.Content, "\n") && item.Content != "" {
		fmt.Fprintln(stdout)
	}
	return 0
}

// searchValueFlags lists search flags that consume a separate value token,
// used to reorder args so flags may appear after the positional query
// (e.g. `ai history search "auth bug" --json`).
var searchValueFlags = map[string]bool{
	"window": true, "role": true, "limit": true, "id": true, "session": true,
}

// splitPositional separates positional arguments from flag tokens. Tokens
// after a bare "--" are always positional.
func splitPositional(args []string, valueFlags map[string]bool) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(arg) > 1 && strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if strings.IndexByte(name, '=') >= 0 {
				continue // value included in the token
			}
			if valueFlags[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return flags, positionals
}

// runSearch implements `ai history search`.
func runSearch(args []string, stdout, stderr io.Writer) int {
	flagArgs, positional := splitPositional(args, searchValueFlags)
	fs := newFlagSet("search")
	windowID := fs.String("window", "", "restrict to one window")
	role := fs.String("role", "", "filter by role")
	limit := fs.Int("limit", session.HistoryDefaultLimit, "max matches to return (max 100)")
	caseSensitive := fs.Bool("case-sensitive", false, "match case-sensitively (default: insensitive)")
	jsonOutput := fs.Bool("json", false, "emit JSONL")
	var idFlag, sessionFlag string
	addGlobalFlags(fs, &idFlag, &sessionFlag)
	if !parseActionFlags(fs, flagArgs, stderr) {
		return 1
	}

	// Join positional arguments so an unquoted multi-word query still works.
	query := strings.Join(positional, " ")
	if query == "" {
		fmt.Fprintln(stderr, "error: search query is required")
		return 1
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		fmt.Fprintf(stderr, "error: search query exceeds %d characters\n", maxQueryRunes)
		return 1
	}

	baseDir, err := homeBaseDir()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	sess, err := loadTarget(baseDir, idFlag, sessionFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	response, err := sess.Search(query, session.HistorySearchOptions{
		WindowID: *windowID, Role: *role, Limit: *limit, CaseSensitive: *caseSensitive,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	out := &boundedWriter{json: *jsonOutput}
	for _, result := range response.Results {
		out.emitRecord(formatSearchResult(result), result)
	}
	if !*jsonOutput {
		out.emitRecord(formatSummary("match", len(response.Results), response.TotalCount), "")
	}
	out.flush(stdout)
	return 0
}
