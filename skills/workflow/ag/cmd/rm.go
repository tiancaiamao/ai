package cmd

import (
	"fmt"
	"os"

	"github.com/genius/ag/internal/storage"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm <agent-id>",
	Short: "Remove a completed/failed agent's state",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		agentDir := storage.AgentDir(id)
		if !storage.Exists(agentDir) {
			fmt.Fprintf(os.Stderr, "Agent not found: %s\n", id)
			os.Exit(1)
		}

		status := storage.ReadStatus(agentDir)
		if status == "running" && !rmForce {
			fmt.Fprintf(os.Stderr, "Agent %s is still running, kill it first\n", id)
			os.Exit(1)
		}

		if err := os.RemoveAll(agentDir); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove: %v\n", err)
			os.Exit(1)
		}
		if rmForce {
			fmt.Printf("Agent %s removed (forced)\n", id)
		} else {
			fmt.Printf("Agent %s removed\n", id)
		}
	},
}

// rmForce skips the running check
var rmForce bool

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Force remove even if running")
}
