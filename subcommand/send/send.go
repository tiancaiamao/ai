package send

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	protocol "github.com/tiancaiamao/ai/pkg/protocol"
	"github.com/tiancaiamao/ai/pkg/transport"

	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

func SendSubcommand() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	waitFlag := fs.Bool("wait", false, "wait for agent to finish processing and stream the response")
	summaryFlag := fs.Bool("summary", false, "with --wait: only show final assistant text (suppress tool output)")
	timeoutFlag := fs.Duration("timeout", 4*time.Minute, "with --wait: max wait time (default 4m, 0 = unlimited)")
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

	client, sid, err := connectACP(tui.SocketPath(baseDir, meta.ID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot connect to run %s: %v\n", meta.ID, err)
		os.Exit(1)
	}
	defer client.Close()

	if *waitFlag {
		os.Exit(sendAndWait(client, sid, message, *summaryFlag, *timeoutFlag, meta.ID))
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
//
// Returns a process exit code: 0 = turn completed; 2 = timed out (the agent's
// turn is still in flight — the response is incomplete); 1 = error (stream
// closed without _turn_end, or request error). Callers MUST propagate it,
// otherwise a timed-out wait looks like success to the agent.
func sendAndWait(client *protocol.ACPClient, sid, message string, summary bool, timeout time.Duration, runID string) int {
	updates := client.Updates()
	var deadline <-chan time.Time
	if timeout > 0 {
		deadline = time.After(timeout)
	}

	if err := client.PromptAsync(sid, message); err != nil {
		fmt.Fprintf(os.Stderr, "error sending message: %v\n", err)
		return 1
	}

	var currentText strings.Builder
	lastKind := tui.EventKind("")
	var lastTextRole string
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				// Stream closed without _turn_end (e.g. agent process exited).
				finishSend(summary, currentText.String())
				fmt.Fprintln(os.Stderr, "--- agent stream ended without turn completion (agent exited or connection lost) ---")
				return 1
			}
			if u.SessionUpdate == protocol.ACPUpdateRequestError {
				if e, ok := u.Meta.(protocol.ACPUpdateError); ok {
					fmt.Fprintf(os.Stderr, "error: %s failed: %s\n", e.Method, e.Message)
				}
				return 1
			}
			if u.SessionUpdate == "_turn_end" {
				finishSend(summary, currentText.String())
				return 0
			}

			evt := tui.ParseACPUpdate(u)
			if evt == nil {
				continue
			}
			if evt.Kind == tui.KindText && evt.Role == "assistant" {
				currentText.WriteString(evt.Text)
			}
			if !summary {
				printSendEvent(evt, &lastKind, &lastTextRole)
			}
		case <-deadline:
			// Timeout is NOT a completed turn: report it as an explicit failure
			// (exit 2, same convention as `ai watch --follow --timeout`).
			fmt.Fprintf(os.Stderr, "--- timeout after %s: agent's turn is STILL IN FLIGHT, the response above is INCOMPLETE ---\n", timeout)
			fmt.Fprintf(os.Stderr, "--- agent may still be working; retry to resume waiting: ai watch --id %s --follow --pretty --timeout %s ---\n", runID, timeout)
			return 2
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
func printSendEvent(evt *tui.FormattedEvent, lastKind *tui.EventKind, lastTextRole *string) {
	// Add line break on kind transitions for readability.
	if evt.Kind != *lastKind && *lastKind != "" && *lastKind != tui.KindTool {
		fmt.Println()
	}

	switch evt.Kind {
	case tui.KindText:
		// Prefix user text (echo of the sent prompt) so consumers can
		// distinguish it from the assistant's reply.
		if evt.Role == "user" && *lastTextRole != "user" {
			if *lastTextRole != "" {
				fmt.Println()
			}
			fmt.Print("user: ")
		}
		fmt.Print(evt.Text)
		*lastTextRole = evt.Role
	case tui.KindThinking:
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

func connectACP(path string) (*protocol.ACPClient, string, error) {
	conn, err := transport.DialUnix(path)
	if err != nil {
		return nil, "", err
	}
	return protocol.ConnectACP(conn)
}
