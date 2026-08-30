package send

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	rpc "github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

func SendSubcommand() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	waitFlag := fs.Bool("wait", false, "wait for agent to finish processing and stream the response")
	summaryFlag := fs.Bool("summary", false, "with --wait: only show final assistant text (suppress tool output)")
	timeoutFlag := fs.Duration("timeout", 0, "with --wait: max wait time (0 = unlimited)")
	fs.Parse(os.Args[1:])

	// Determine the message to send.
	// If both stdin (pipe) and arguments are provided, combine them:
	// stdin content is prepended to the argument message.
	var parts []string

	if !isTerminal(os.Stdin) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		if len(data) > 0 {
			parts = append(parts, string(data))
		}
	}

	args := fs.Args()
	if len(args) > 0 {
		parts = append(parts, args[0])
		for _, a := range args[1:] {
			parts[len(parts)-1] += " " + a
		}
	}

	message := strings.Join(parts, "\n")

	if message == "" {
		fmt.Fprintf(os.Stderr, "error: no message provided. pass a message as argument or via stdin\n")
		os.Exit(1)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Join(home, ".ai")

	meta, err := helpers.ResolveRunID(baseDir, *idFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client, sid, err := rpc.DialACP(tui.SocketPath(baseDir, meta.ID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot connect to run %s: %v\n", meta.ID, err)
		os.Exit(1)
	}
	defer client.Close()

	if *waitFlag {
		sendAndWait(client, sid, message, *summaryFlag, *timeoutFlag)
		return
	}

	// Fire-and-forget: send the message and exit immediately.
	if err := client.PromptAsync(sid, message); err != nil {
		fmt.Fprintf(os.Stderr, "error sending message: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("message sent to run", meta.ID)
}

// sendAndWait sends a message and blocks until the agent finishes processing
// it (_turn_end), streaming the response in real-time.
func sendAndWait(client *rpc.ACPClient, sid, message string, summary bool, timeout time.Duration) {
	updates := client.Updates()
	var deadline <-chan time.Time
	if timeout > 0 {
		deadline = time.After(timeout)
	}

	if err := client.PromptAsync(sid, message); err != nil {
		fmt.Fprintf(os.Stderr, "error sending message: %v\n", err)
		os.Exit(1)
	}

	var currentText strings.Builder
	lastKind := tui.EventKind("")
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				// Stream closed without _turn_end (e.g. agent process exited).
				finishSend(summary, currentText.String())
				fmt.Fprintln(os.Stderr, "--- agent stream ended ---")
				return
			}
			if u.SessionUpdate == "_turn_end" {
				finishSend(summary, currentText.String())
				return
			}

			evt := tui.ParseACPUpdate(u)
			if evt == nil {
				continue
			}
			if evt.Kind == tui.KindText && evt.Role == "assistant" {
				currentText.WriteString(evt.Text)
			}
			if !summary {
				printSendEvent(evt, &lastKind)
			}
		case <-deadline:
			fmt.Fprintln(os.Stderr, "--- timeout ---")
			return
		}
	}
}

// finishSend prints the final assistant text for --summary mode.
func finishSend(summary bool, text string) {
	if summary {
		text = strings.TrimSpace(text)
		if text != "" {
			fmt.Println(text)
		}
		return
	}
	fmt.Println()
}

// printSendEvent prints one formatted agent event. Output mirrors
// watch --follow --pretty.
func printSendEvent(evt *tui.FormattedEvent, lastKind *tui.EventKind) {
	// Add line break on kind transitions for readability.
	if evt.Kind != *lastKind && *lastKind != "" && *lastKind != tui.KindTool {
		fmt.Println()
	}

	switch evt.Kind {
	case tui.KindText, tui.KindThinking:
		fmt.Print(evt.Text)
	case tui.KindTool:
		fmt.Printf("  %s\n", evt.Text)
	case tui.KindMeta:
		fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
	}

	if evt.Kind != tui.KindMeta {
		*lastKind = evt.Kind
	}
}

// isTerminal returns true if the file is a terminal (character device).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
