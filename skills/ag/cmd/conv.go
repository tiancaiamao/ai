package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/genius/ag/internal/conv"
	"github.com/spf13/cobra"
)

var convCmd = &cobra.Command{
	Use:   "conv",
	Short: "Convert ai --mode rpc JSON events to human-readable text",
	Long: `Reads newline-delimited JSON events from stdin and writes
human-readable output to stdout. Designed for piping:

  ai --mode rpc | ag conv
  cat stream.log | ag conv --only tools
  ai --mode rpc | tee raw.log | ag conv`,
	RunE: runConv,
}

var (
	convOnly string // "text", "tools", or "" (all)
)

func init() {
	convCmd.Flags().StringVar(&convOnly, "only", "", "Filter: 'text' for assistant text only, 'tools' for tool calls only")
}

func runConv(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)
	// Handle large events
	const maxTokenSize = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 4096), maxTokenSize)

	var (
		textOnly = convOnly == "text"
		toolOnly = convOnly == "tools"
	)

	for scanner.Scan() {
		line := scanner.Text()
		evt := conv.ParseEvent(line)
		if evt == nil {
			continue
		}

		// Apply filter
		if textOnly && evt.Kind != conv.KindText {
			continue
		}
		if toolOnly && evt.Kind != conv.KindTool {
			continue
		}

		fmt.Fprintln(os.Stdout, evt.Text)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("reading stdin: %w", err)
	}
	return nil
}