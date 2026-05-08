package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/genius/ag/internal/conv"
	"github.com/spf13/cobra"
)

var convCmd = &cobra.Command{
	Use:   "conv",
	Short: "Convert JSON events to human-readable text",
	Long: `Reads newline-delimited JSON events from stdin or watches a file
and writes human-readable output to stdout. Designed for piping:

  cat events.jsonl | ag conv
  cat events.jsonl | ag conv --only tools
  ag conv --watch ~/.ai/runs/abc123/events.jsonl
  ag conv --watch ~/.ai/runs/abc123/events.jsonl --on agent_end`,
	RunE: runConv,
}

var (
	convOnly  string // "text", "tools", or "" (all)
	convWatch string // file path to watch
	convOn    string // stop and exit when this event kind is detected (e.g. "agent_end")
)

func init() {
	convCmd.Flags().StringVar(&convOnly, "only", "", "Filter: 'text' for assistant text only, 'tools' for tool calls only")
	convCmd.Flags().StringVar(&convWatch, "watch", "", "Watch an events file in real-time instead of reading stdin")
	convCmd.Flags().StringVar(&convOn, "on", "", "Stop and exit when a specific event is detected (agent_end, agent_start, tool, etc.)")
}

func runConv(cmd *cobra.Command, args []string) error {
	if convWatch != "" {
		return runConvWatch()
	}
	return runConvStdin()
}

func runConvStdin() error {
	printHook := func(evt *conv.FormattedEvent) bool {
		if !matchesFilter(evt) {
			return true
		}
		fmt.Fprintln(os.Stdout, evt.Text)
		return true
	}

	conv.StreamEvents(os.Stdin, printHook)
	return nil
}

func runConvWatch() error {
	stopCh := make(chan struct{})

	// Build the stop-on hook if --on is specified
	var stopHook conv.HookFunc
	if convOn != "" {
		stopHook = func(evt *conv.FormattedEvent) bool {
			if matchesOnEvent(evt, convOn) {
				fmt.Fprintln(os.Stdout, evt.Text)
				close(stopCh)
				return false
			}
			return true
		}
	}

	printHook := func(evt *conv.FormattedEvent) bool {
		if !matchesFilter(evt) {
			return true
		}
		fmt.Fprintln(os.Stdout, evt.Text)
		return true
	}

	hooks := []conv.HookFunc{printHook}
	if stopHook != nil {
		hooks = append(hooks, stopHook)
	}

	err := conv.WatchFile(convWatch, 500*time.Millisecond, stopCh, hooks...)
	if err != nil && err != io.EOF {
		return fmt.Errorf("watching file: %w", err)
	}
	return nil
}

func matchesFilter(evt *conv.FormattedEvent) bool {
	textOnly := convOnly == "text"
	toolOnly := convOnly == "tools"

	if textOnly && evt.Kind != conv.KindText {
		return false
	}
	if toolOnly && evt.Kind != conv.KindTool {
		return false
	}
	return true
}

func matchesOnEvent(evt *conv.FormattedEvent, onEvent string) bool {
	switch onEvent {
	case "agent_end", "agent_done":
		return conv.IsAgentDone(evt)
	case "agent_start":
		return evt.Kind == conv.KindMeta && evt.Text == "--- agent started ---"
	case "agent_fail", "agent_failed":
		return evt.Kind == conv.KindMeta && conv.IsAgentDone(evt) && !conv.IsAgentSuccess(evt)
	case "tool":
		return evt.Kind == conv.KindTool
	case "meta":
		return evt.Kind == conv.KindMeta
	default:
		return false
	}
}