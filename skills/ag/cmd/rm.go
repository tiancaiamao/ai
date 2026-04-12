package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/genius/ag/internal/storage"
	"github.com/spf13/cobra"
)

var rmForce bool

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
			fmt.Fprintf(os.Stderr, "Agent %s is still running, kill it first (or use -f)\n", id)
			os.Exit(1)
		}

		// Clean up tmux session if it exists
		tmuxFile := agentDir + "/tmux-session"
		if data, err := os.ReadFile(tmuxFile); err == nil {
			sessionName := strings.TrimSpace(string(data))
			if strings.HasPrefix(sessionName, "mock-") {
				// mock agents don't have tmux sessions
			} else {
				exec.Command("tmux", "kill-session", "-t", sessionName).Run()
			}
		}

		if err := os.RemoveAll(agentDir); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent %s removed\n", id)
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Force remove even if running")
}