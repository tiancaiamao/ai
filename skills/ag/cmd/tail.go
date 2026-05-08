package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

		"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/conv"
	"github.com/genius/ag/internal/run"
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

	if useAIAdapterForCommand(id) {
		return tailFromAI(id, tailSince)
	}

	return tailFromBridge(id, tailSince)
}

// tailFromAI reads incremental output from ai events.
func tailFromAI(id string, since int64) error {
	runID, err := aiAdapter.getRunIDForAgent(id)
	if err != nil {
		return fmt.Errorf("get run ID for agent %s: %w", id, err)
	}

		eventsPath, err := run.EventsPath(runID)
	if err != nil {
		return fmt.Errorf("get events path: %w", err)
	}

	f, err := os.Open(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("---cursor:0")
			return nil
		}
		return fmt.Errorf("open events.jsonl: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read events.jsonl: %w", err)
	}

	content, newCursor, err := parseEventsTail(data, since)
	if err != nil {
		return fmt.Errorf("parse events tail: %w", err)
	}

	if content != "" {
		fmt.Print(content)
		if !strings.HasSuffix(content, "\n") {
			fmt.Println()
		}
	}

	fi, _ := f.Stat()
	if newCursor == 0 {
		newCursor = fi.Size()
	}
	fmt.Printf("---cursor:%d\n", newCursor)
	return nil
}

// tailFromBridge reads incremental output from legacy stream.log.
func tailFromBridge(id string, since int64) error {
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

	if since > 0 {
		if _, err := f.Seek(since, io.SeekStart); err != nil {
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

	fi, _ := f.Stat()
	newCursor := fi.Size()
	fmt.Printf("---cursor:%d\n", newCursor)
	return nil
}

// parseEventsTail parses assistant text from events.jsonl data.
func parseEventsTail(data []byte, since int64) (content string, newCursor int64, err error) {
	if since < 0 {
		since = 0
	}

	if int(since) >= len(data) {
		return "", int64(len(data)), nil
	}

	chunk := data[since:]
	if since > 0 {
		if idx := strings.IndexByte(string(chunk), '\n'); idx >= 0 {
			chunk = chunk[idx+1:]
		} else {
			return "", int64(len(data)), nil
		}
	}

		messages := conv.BuildAssistantTexts(chunk)
	return strings.Join(messages, "\n\n"), int64(len(data)), nil
}
