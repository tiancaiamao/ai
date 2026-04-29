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
	cmdType := "steer"
	if len(message) > 0 && message[0] == '/' {
		cmdType = "prompt"
	}
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

// isTerminal returns true if the file is a terminal (character device).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}