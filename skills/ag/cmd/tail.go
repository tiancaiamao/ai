package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
	"github.com/spf13/cobra"
)

var (
	tailFollow bool
	tailLines  int
	tailSince  int64 // byte offset cursor
)

var agentTailCmd = &cobra.Command{
	Use:   "tail <id>",
	Short: "Tail agent output stream",
	Long: `View agent output in real-time or by cursor.

  # Human: follow live output (like tail -f)
  ag agent tail worker-1 -f

  # Human: last N lines
  ag agent tail worker-1 --lines 100

  # LLM: incremental reads with cursor
  ag agent tail worker-1 --since 4096
  # Returns: new content + new cursor offset at the end as "---cursor:12345"`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentTail,
}

func init() {
	agentTailCmd.Flags().BoolVarP(&tailFollow, "follow", "f", false, "Follow output (like tail -f)")
	agentTailCmd.Flags().IntVarP(&tailLines, "lines", "n", 50, "Number of last lines to show (0 = all)")
	agentTailCmd.Flags().Int64Var(&tailSince, "since", -1, "Byte offset cursor: show content after this position")
}

func runAgentTail(cmd *cobra.Command, args []string) error {
	id := args[0]

	if err := agent.EnsureExists(id); err != nil {
		return err
	}

	agentDir := agent.AgentDir(id)
	streamPath := filepath.Join(agentDir, "stream.log")

	// --since mode: LLM incremental read
	if tailSince >= 0 {
		return tailSinceMode(streamPath, id)
	}

	// -f mode: human follow
	if tailFollow {
		return tailFollowMode(streamPath, id)
	}

	// Default: last N lines
	return tailLastN(streamPath)
}

func tailSinceMode(streamPath, id string) error {
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
	if _, err := f.Seek(tailSince, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
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

func tailFollowMode(streamPath, id string) error {
	// First, print last N lines
	if err := tailLastN(streamPath); err != nil {
		return err
	}

	f, err := os.Open(streamPath)
	if err != nil {
		return fmt.Errorf("open stream.log: %w", err)
	}
	defer f.Close()

	// Seek to end
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek end: %w", err)
	}

	reader := bufio.NewReader(f)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Read any new data
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if err == io.EOF {
						break
					}
					return fmt.Errorf("read: %w", err)
				}
				fmt.Print(line)
			}

			// Check if agent is done
			act, err := agent.ReadActivity(id)
			if err == nil && agent.IsTerminal(act.Status) {
				// Agent finished — drain remaining and exit
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						break
					}
					fmt.Print(line)
				}
				return nil
			}

			// Check if stream.log still exists
			if !storage.Exists(streamPath) {
				return nil
			}
		}
	}
}

func tailLastN(streamPath string) error {
	f, err := os.Open(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no output yet)")
			return nil
		}
		return fmt.Errorf("open stream.log: %w", err)
	}
	defer f.Close()

	// Read lines into ring buffer
	if tailLines <= 0 {
		// Read all
		data, err := io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		fmt.Print(string(data))
		return nil
	}

	scanner := bufio.NewScanner(f)
	var ring []string
	for scanner.Scan() {
		ring = append(ring, scanner.Text())
		if len(ring) > tailLines {
			ring = ring[1:]
		}
	}

	for _, line := range ring {
		fmt.Println(line)
	}
	return scanner.Err()
}