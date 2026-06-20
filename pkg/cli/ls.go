package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

func LsSubcommand() {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	allFlag := fs.Bool("all", false, "show all runs, not just running ones")
	jsonFlag := fs.Bool("json", false, "output as JSON array")
	_ = fs.Parse(os.Args[1:])

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	runsDir := filepath.Join(home, ".ai", "runs")

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No runs directory yet — output nothing.
			if *jsonFlag {
				fmt.Println("[]")
			}
			return
		}
		fmt.Fprintf(os.Stderr, "failed to read runs directory: %v\n", err)
		os.Exit(1)
	}

	var runs []tui.RunMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(runsDir, e.Name(), "run.json")
		meta, err := tui.LoadRunMeta(metaPath)
		if err != nil {
			continue
		}

		// Reconcile stale runs: if run.json says "running" but PID is
		// dead or recycled, update to "failed" so callers see the real state.
		if meta.Status == tui.StatusRunning && !tui.IsRunning(meta) {
			meta.Status = tui.StatusFailed
			meta.FinishedAt = time.Now().Unix()
			_ = tui.SaveRunMeta(meta, metaPath)
		}

		// For non --all mode, check if the run is actually alive.
		if !*allFlag {
			if !tui.IsRunning(meta) {
				continue
			}
		}

		runs = append(runs, *meta)
	}

	// Sort by StartedAt descending (newest first).
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt > runs[j].StartedAt
	})

	if *jsonFlag {
		emitJSON(runs)
	} else {
		emitTable(runs)
	}
}

// lsRunEntry is the JSON output structure with added fields.
type lsRunEntry struct {
	tui.RunMeta
	Age    string            `json:"age"`
	Status string            `json:"status"` // overridden: includes "idle" for completed-prompt agents
	End    *tui.AgentEndInfo `json:"end,omitempty"`
}

func emitJSON(runs []tui.RunMeta) {
	entries := make([]lsRunEntry, len(runs))
	for i, r := range runs {
		entry := lsRunEntry{
			RunMeta: r,
			Age:     formatAge(r.StartedAt),
			Status:  r.Status,
		}

		// For running agents, check if they've completed at least one prompt.
		if r.Status == tui.StatusRunning && tui.IsRunning(&r) {
			eventsPath := tui.EventsPath("", r.ID)
			if endInfo := tui.FindLastAgentEndFast(eventsPath); endInfo != nil {
				entry.End = endInfo
				if endInfo.Success {
					entry.Status = "idle"
				} else if endInfo.Error != "" {
					entry.Status = "error"
				}
			}
		}

		entries[i] = entry
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func emitTable(runs []tui.RunMeta) {
	if len(runs) == 0 {
		return
	}

	// Header
	fmt.Printf("%-10s  %-12s  %-30s  %s\n", "ID", "STATUS", "NAME", "AGE")

	for _, r := range runs {
		id := r.ID
		if len(id) > 6 {
			id = id[:6]
		}

		displayStatus := r.Status
		// Re-check actual liveness for display accuracy.
		if r.Status == tui.StatusRunning && !tui.IsRunning(&r) {
			displayStatus = "dead"
		} else if r.Status == tui.StatusRunning {
			// Check if agent has completed a prompt (idle) vs still processing.
			eventsPath := tui.EventsPath("", r.ID)
			if endInfo := tui.FindLastAgentEndFast(eventsPath); endInfo != nil && endInfo.Success {
				displayStatus = "idle"
			}
		}
		coloredStatus := colorizeStatus(displayStatus)

		name := r.Name
		if name == "" {
			name = truncateStr(r.CWD, 30)
		}

		age := formatAge(r.StartedAt)

		fmt.Printf("%-10s  %-12s  %-30s  %s\n", id, coloredStatus, name, age)
	}
}

// colorizeStatus wraps the status string with ANSI color codes.
func colorizeStatus(status string) string {
	switch status {
	case tui.StatusRunning:
		return "\x1b[32m" + status + "\x1b[0m" // green
	case "idle":
		return "\x1b[36m" + status + "\x1b[0m" // cyan
	case tui.StatusDone:
		return "\x1b[90m" + status + "\x1b[0m" // gray
	case tui.StatusFailed:
		return "\x1b[31m" + status + "\x1b[0m" // red
	case tui.StatusKilled:
		return "\x1b[33m" + status + "\x1b[0m" // yellow
	case "dead":
		return "\x1b[31m" + status + "\x1b[0m" // red
	default:
		return status
	}
}

// truncateStr truncates s to maxLen characters, appending "…" if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	// Truncate from the beginning (keep suffix), showing "…" prefix
	return "…" + s[len(s)-maxLen+1:]
}

// formatAge converts a unix timestamp to a human-readable age string.
func formatAge(startedAt int64) string {
	dur := time.Since(time.Unix(startedAt, 0))
	seconds := int64(dur.Seconds())

	if seconds < 0 {
		seconds = 0
	}

	switch {
	case seconds < 5:
		return "just now"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}
