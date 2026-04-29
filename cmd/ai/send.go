package main

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

	"github.com/tiancaiamao/ai/pkg/run"
)

func sendSubcommand() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
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

	sockPath := run.SocketPath(baseDir, meta.ID)
	resp, err := sendMessage(sockPath, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error sending message: %v\n", err)
		os.Exit(1)
	}

	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Error)
		os.Exit(1)
	}

	// For slash commands, wait for and display the response from events.jsonl.
	if strings.HasPrefix(message, "/") {
		result, err := waitForCommandEvent(baseDir, meta.ID, 5*time.Second)
		if err != nil {
			// Non-fatal: command was sent, just couldn't read result.
			fmt.Fprintf(os.Stderr, "command sent (could not read result: %v)\n", err)
			return
		}
		formatCommandResult(result)
		return
	}

	fmt.Println("message sent to run", meta.ID)
}

// resolveRunID resolves the target run given an optional ID flag.
// If id is empty, it auto-selects by cwd. If id is a partial prefix,
// it uses FindByPrefix.
func resolveRunID(baseDir, id string) (*run.RunMeta, error) {
	if id != "" {
		// Try exact match first: look for run.json directly.
		exactPath := run.RunMetaPath(baseDir, id)
		if meta, err := run.LoadRunMeta(exactPath); err == nil && run.IsRunning(meta) {
			return meta, nil
		}

		// Try prefix match.
		matches, err := run.FindByPrefix(baseDir, id)
		if err != nil {
			return nil, fmt.Errorf("prefix match for %q: %w", id, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", id)
		}
		// FindByPrefix returns at most 1 match on success (errors on multiple).
		m := matches[0]
		if !run.IsRunning(&m) {
			return nil, fmt.Errorf("run %s is not running (status: %s)", m.ID, m.Status)
		}
		return &m, nil
	}

	// Auto-select by cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}

	matches, err := run.FindRunningByCwd(baseDir, cwd)
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
func sendMessage(sockPath, message string) (*run.Response, error) {
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
	cmd := run.Command{
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

	var resp run.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}

// waitForCommandEvent reads events.jsonl from the end, waiting for a new
// response event to appear after the current file position.
func waitForCommandEvent(baseDir, runID string, timeout time.Duration) (map[string]any, error) {
	eventsPath := run.EventsPath(baseDir, runID)

	// Get current file size as starting offset.
	info, err := os.Stat(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("stat events file: %w", err)
	}
	startOffset := info.Size()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Read any new content appended since last check.
		f, err := os.Open(eventsPath)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		f.Seek(startOffset, io.SeekStart)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			var evt map[string]any
			if json.Unmarshal(line, &evt) != nil {
				continue
			}
			if evt["type"] == "response" {
				f.Close()
				return evt, nil
			}
		}
		f.Close()
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout waiting for command response")
}

// formatCommandResult renders a slash command response event for the terminal.
func formatCommandResult(evt map[string]any) {
	success, _ := evt["success"].(bool)

	if !success {
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "command failed"
		}
		fmt.Fprintf(os.Stderr, "❌ %s\n", errMsg)
		return
	}

	data, _ := evt["data"].(map[string]any)
	if data == nil {
		fmt.Println("✓ done")
		return
	}

	// /help — commands list
	if commands, ok := data["commands"].([]any); ok {
		fmt.Println("Available commands:")
		for _, c := range commands {
			if cm, ok := c.(map[string]any); ok {
				name, _ := cm["name"].(string)
				desc, _ := cm["description"].(string)
				if desc != "" {
					fmt.Printf("  /%-22s %s\n", name, desc)
				} else {
					fmt.Printf("  /%s\n", name)
				}
			}
		}
		return
	}

	// /session or /resume (list_sessions) — sessions list
	if sessions, ok := data["sessions"].([]any); ok {
		for _, s := range sessions {
			if sm, ok := s.(map[string]any); ok {
				id, _ := sm["id"].(string)
				name, _ := sm["name"].(string)
				title, _ := sm["title"].(string)
				fmt.Printf("  %-12s %-20s %s\n", id[:6], name, title)
			}
		}
		return
	}

	// /model — model info
	if model, ok := data["model"].(map[string]any); ok {
		id, _ := model["id"].(string)
		name, _ := model["name"].(string)
		fmt.Printf("Model: %s (%s)\n", name, id)
		return
	}

	// /context — composite result
	if _, hasState := data["state"]; hasState {
		if state, ok := data["state"].(map[string]any); ok {
			status, _ := state["status"].(string)
			modelName, _ := state["modelName"].(string)
			fmt.Printf("Status: %s  Model: %s\n", status, modelName)
		}
		if models, ok := data["models"].(map[string]any); ok {
			if mlist, ok := models["models"].([]any); ok {
				fmt.Printf("\nAvailable models (%d):\n", len(mlist))
				for _, m := range mlist {
					if mm, ok := m.(map[string]any); ok {
						id, _ := mm["id"].(string)
						name, _ := mm["name"].(string)
						fmt.Printf("  %-30s %s\n", id, name)
					}
				}
			}
		}
		return
	}

	// /thinking — level
	if level, ok := data["level"].(string); ok {
		fmt.Printf("Thinking level: %s\n", level)
		return
	}

	// /compact — message
	if msg, ok := data["message"].(string); ok {
		fmt.Println(msg)
		return
	}

	// Fallback: pretty-print JSON
	pretty, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println(string(pretty))
		return
	}
	fmt.Println(string(pretty))
}

// isTerminal returns true if the file is a terminal (character device).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
