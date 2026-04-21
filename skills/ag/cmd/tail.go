package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/genius/ag/internal/agent"
	"github.com/spf13/cobra"
)

var (
	tailSince int64 // byte offset cursor for incremental reads
)

var agentTailCmd = &cobra.Command{
	Use:   "tail <id>",
	Short: "Read agent output with cursor-based incremental access",
	Long: `Read agent output stream for LLM incremental consumption.

For human use, prefer system tools directly:
  tail -f ~/.ag/agents/<id>/stream.log    # follow live output
  tail -n 100 ~/.ag/agents/<id>/stream.log # last N lines

For LLM incremental reads with cursor:
  ag agent tail worker-1 --since 4096
  # Returns: new content + new cursor offset as "---cursor:12345"

  ag agent tail worker-1 --since 0
  # Returns: all content + final cursor`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentTail,
}

func init() {
	agentTailCmd.Flags().Int64Var(&tailSince, "since", 0, "Byte offset cursor: show content after this position (default: 0 = from start)")
}

func runAgentTail(cmd *cobra.Command, args []string) error {
	id := args[0]

	if err := agent.EnsureExists(id); err != nil {
		return err
	}

	agentDir := agent.AgentDir(id)
	streamPath := filepath.Join(agentDir, "stream.log")

	f, err := os.Open(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("---cursor:0")
			return nil
		}
		return fmt.Errorf("open stream.log: %w", err)
	}
	defer f.Close()

	// Seek to cursor position
	if tailSince > 0 {
		if _, err := f.Seek(tailSince, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	content := string(data)
	if content != "" {
		fmt.Print(content)
		if !strings.HasSuffix(content, "\n") {
			fmt.Println()
		}
	}

	// Get current file size as new cursor
	fi, _ := f.Stat()
	newCursor := fi.Size()
	fmt.Printf("---cursor:%d\n", newCursor)
	return nil
}