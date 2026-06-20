package send

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	meta, err := resolveRunID(baseDir, *idFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sockPath := tui.SocketPath(baseDir, meta.ID)

	if *waitFlag {
		sendAndWait(sockPath, message, meta.ID, *summaryFlag, *timeoutFlag)
		return
	}

	// Fire-and-forget: send message and exit immediately.
	resp, err := sendMessage(sockPath, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error sending message: %v\n", err)
		os.Exit(1)
	}

	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Println("message sent to run", meta.ID)
}

// sendAndWait sends a message and blocks until the agent finishes processing it,
// streaming the response in real-time. This eliminates the race between send+watch.
func sendAndWait(sockPath, message, runID string, summary bool, timeout time.Duration) {
	// Step 1: Subscribe to the event stream BEFORE sending.
	// Use a fromSeq that exceeds any realistic sequence number to skip replay
	// and only receive events produced after this point.
	const noReplaySeq = uint64(1) << 60
	client := tui.NewSocketClient(sockPath)
	streamConn, _, err := client.Stream(noReplaySeq)
	if err != nil {
		// Stream unavailable (e.g. no broadcaster) — fall back to fire-and-forget.
		fmt.Fprintf(os.Stderr, "warning: cannot subscribe to stream: %v\n", err)
		resp, sendErr := sendMessage(sockPath, message)
		if sendErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", sendErr)
			os.Exit(1)
		}
		if !resp.OK {
			fmt.Fprintf(os.Stderr, "error: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Println("message sent to run", runID)
		return
	}
	defer streamConn.Close()

	if timeout > 0 {
		streamConn.SetDeadline(time.Now().Add(timeout))
	}

	// Step 2: Send the message on a separate connection.
	resp, err := sendMessage(sockPath, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Error)
		os.Exit(1)
	}

	// Step 3: Read events from the stream until agent_end.
	scanner := bufio.NewScanner(streamConn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if summary {
		waitStreamSummary(scanner)
	} else {
		waitStreamPretty(scanner)
	}
}

// waitStreamPretty prints formatted agent events in real-time, exiting when
// the agent finishes (agent_end event). Output mirrors watch --follow --pretty.
func waitStreamPretty(scanner *bufio.Scanner) {
	lastKind := tui.EventKind("")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		evt := tui.ParseEvent(line)
		if evt == nil {
			continue
		}

		// Add line break on kind transitions for readability.
		if evt.Kind != lastKind && lastKind != "" && lastKind != tui.KindTool {
			fmt.Println()
		}

		switch evt.Kind {
		case tui.KindText:
			fmt.Print(evt.Text)
		case tui.KindThinking:
			fmt.Print(evt.Text)
		case tui.KindTool:
			fmt.Printf("  %s\n", evt.Text)
		case tui.KindMeta:
			fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
		case tui.KindResponse:
			fmt.Print(evt.Text)
		}

		if evt.Kind != tui.KindMeta {
			lastKind = evt.Kind
		}

		// On agent_end: the task is complete — exit.
		if strings.Contains(line, `"agent_end"`) {
			fmt.Println()
			return
		}
	}
	// Stream closed without agent_end (e.g. agent process exited).
	fmt.Fprintln(os.Stderr, "--- agent stream ended ---")
}

// waitStreamSummary accumulates only the final assistant text and prints it
// on agent_end. Tool output, thinking, and intermediate text are suppressed.
func waitStreamSummary(scanner *bufio.Scanner) {
	var currentText strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.Contains(line, `"agent_end"`) {
			text := strings.TrimSpace(currentText.String())
			if text != "" {
				fmt.Println(text)
			}
			return
		}

		evt := tui.ParseEvent(line)
		if evt != nil && evt.Kind == tui.KindText {
			currentText.WriteString(evt.Text)
		}
	}
	// Stream closed without agent_end — print whatever we have.
	text := strings.TrimSpace(currentText.String())
	if text != "" {
		fmt.Println(text)
	}
}

// resolveRunID resolves the target run given an optional ID flag.
// If id is empty, it auto-selects by cwd. If id is a partial prefix,
// it uses FindByPrefix.
func resolveRunID(baseDir, id string) (*tui.RunMeta, error) {
	if id != "" {
		// Try exact match first: look for run.json directly.
		exactPath := tui.RunMetaPath(baseDir, id)
		if meta, err := tui.LoadRunMeta(exactPath); err == nil && tui.IsRunning(meta) {
			return meta, nil
		}

		// Try prefix match.
		matches, err := tui.FindByPrefix(baseDir, id)
		if err != nil {
			return nil, fmt.Errorf("prefix match for %q: %w", id, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", id)
		}
		// FindByPrefix returns at most 1 match on success (errors on multiple).
		m := matches[0]
		if !tui.IsRunning(&m) {
			return nil, fmt.Errorf("run %s is not running (status: %s)", m.ID, m.Status)
		}
		return &m, nil
	}

	// Auto-select by cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}

	matches, err := tui.FindRunningByCwd(baseDir, cwd)
	if err != nil {
		return nil, fmt.Errorf("find running by cwd: %w", err)
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no running instances found in %s", cwd)
	case 1:
		return &matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return nil, fmt.Errorf("multiple running instances in %s (IDs: %v), use --id to disambiguate", cwd, ids)
	}
}

// sendMessage connects to the Unix domain socket at sockPath, sends a steer
// command with the given message, and returns the response.
func sendMessage(sockPath, message string) (*tui.Response, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dial socket %s: %w", sockPath, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	// Slash commands go through prompt handler for proper parsing.
	// Regular messages use steer to work during streaming.
	cmdType := "prompt"
	cmd := tui.Command{
		Type:    cmdType,
		Message: message,
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write command: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp tui.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}

// isTerminal returns true if the file is a terminal (character device).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
