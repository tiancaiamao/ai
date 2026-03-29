package orchestrate

import (
	"github.com/spf13/cobra"
)

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main().
func Execute() {
	rootCmd := &cobra.Command{
		Use:   "orchestrate",
		Short: "Team orchestration CLI for multi-agent workflows",
	}

	// Add subcommands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(approveCmd)
	rootCmd.AddCommand(templatesCmd)

	if err := rootCmd.Execute(); err != nil {
		panic(err)
	}
}
