// Package history implements the `ai history` subcommand: a read-only,
// bounded interface over persisted session history (compaction generations,
// message items, full-text search).
//
// Output contract (docs/design-history-cli.md §4.1):
//   - stdout carries pure data; diagnostics and errors go to stderr
//   - --json emits JSONL (one object per line) for pipeline consumption
//   - every invocation is bounded: result limits, per-item content caps,
//     and a global 40000-char output ceiling with explicit truncation markers
package history

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// actionHandlers maps a history action name to its runner. Each runner parses
// its own flags from args (which excludes the action name itself).
func actionHandlers() map[string]func(args []string, stdout, stderr io.Writer) int {
	return map[string]func(args []string, stdout, stderr io.Writer) int{
		"windows": runWindows,
		"list":    runList,
		"read":    runRead,
		"search":  runSearch,
	}
}

// HistorySubcommand is the entry point registered in cmd/ai/main.go.
// main.go has already shifted os.Args so os.Args[0] is "history".
func HistorySubcommand() {
	os.Exit(runHistory(os.Args[1:], os.Stdout, os.Stderr))
}

// runHistory dispatches a history action and returns the process exit code.
// It is separated from HistorySubcommand so tests can exercise the full flag
// parsing and output path without spawning a process.
func runHistory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "error: missing history action\n\n%s", usageText())
		return 1
	}

	action, rest := args[0], args[1:]
	switch action {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageText())
		return 0
	}

	handler, ok := actionHandlers()[action]
	if !ok {
		fmt.Fprintf(stderr, "error: unknown history action %q\n\n%s", action, usageText())
		return 1
	}
	return handler(rest, stdout, stderr)
}

// newFlagSet builds a flag set that reports parse failures as errors instead
// of exiting, so runHistory can return a proper exit code.
func newFlagSet(action string) *flag.FlagSet {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseActionFlags parses action-specific flags. Parse failures are reported
// on stderr with the usage text and mapped to exit code 1.
func parseActionFlags(fs *flag.FlagSet, args []string, stderr io.Writer) bool {
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n\n%s", err, usageText())
		return false
	}
	return true
}

// addGlobalFlags registers the flags shared by every action.
func addGlobalFlags(fs *flag.FlagSet, idFlag, sessionFlag *string) {
	fs.StringVar(idFlag, "id", "", "run ID or unique prefix (required unless --session is given)")
	fs.StringVar(sessionFlag, "session", "", "session directory path (escape hatch when the run does not record a session)")
}

// usageText returns the help text for the history subcommand.
func usageText() string {
	return strings.TrimSpace(`
ai history - read-only access to persisted session history

Usage:
  ai history <action> [flags]

Actions:
  windows    List compaction generations (windows) of the session
  list       List message items in a window or along the current path
  read       Read one entry in full, in character-based pages
  search     Literal substring search over messages and compaction snapshots

Global flags:
  --id <run-id|prefix>   Run ID or unique prefix; required unless --session
                         is given (no cwd auto-select: an agent's cwd can
                         change during its lifetime; done/failed runs are
                         matched too; ambiguous prefixes error with a
                         bounded candidate list)
  --session <path>       Session directory path, bypassing run resolution
  --json                 Machine mode: JSONL on stdout, no truncation markers

Flags for 'windows':
  --limit <n>            Max windows to return (default 20, max 100)
  --oldest-first         Oldest generation first (default: newest first)

Flags for 'list':
  --window <id>          List a specific window instead of the current path
  --role <role>          Filter by role: user|assistant|tool|system|developer
  --no-tool              Exclude tool results
  --entry <id>           Start from this entry and include its ancestor chain
  --limit <n>            Max items to return (default 20, max 100)
  --max-chars <n>        Truncate item content (default 400, max 2000), with
                         a "...[truncated, N chars total]" marker
  --oldest-first         Oldest item first (default: newest first)

Flags for 'read':
  --entry <id>           Entry to read (required)
  --offset-chars <n>     Character offset to start from (default 0; offsets
                         beyond the end return empty content, exit 0)
  --max-chars <n>        Max characters to return (default 20000, max 50000)

Flags for 'search':
  <query>                Literal substring to search (required, 1..1000 chars)
  --window <id>          Restrict to one window
  --role <role>          Filter by role
  --no-tool              Exclude tool results (avoids self-matches on
                         earlier search output)
  --limit <n>            Max matches to return (default 20, max 100)
  --case-sensitive       Match case-sensitively (default: insensitive)

All output is bounded: list/search return at most 40000 characters per
invocation and end with "...[output truncated at 40000 chars, refine your
query]" when the cap is hit.

Examples:
  ai history windows --id a1b2c3       List generations of a run's session
  ai history list --id a1b2c3 --limit 50
                                       List recent items of a run
  ai history read --id a1b2c3 --entry <entry-id>
                                       Read one entry in full
  ai history read --id a1b2c3 --entry e --offset-chars 20000 --max-chars 20000
  ai history search "auth bug" --id a1b2c3 --json
                                       Search, machine-readable output
  ai history search "err" --id a1b2c3 --json | jq -r '.entry_id'
                                       Collect entry IDs for batch reads
`)
}
