package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "workflow-ctl",
		Short: "Workflow state manager for conversation-driven development",
	}

	startCmd := &cobra.Command{
		Use:   "start <template> <description>",
		Short: "Start a new workflow",
		Args:  cobra.ExactArgs(2),
		Run:   runStart,
	}

	advanceCmd := &cobra.Command{
		Use:   "advance [--output file] [--force]",
		Short: "Advance to the next phase",
		Run:   runAdvance,
	}
	advanceCmd.Flags().String("output", "", "Output artifact file path")
	advanceCmd.Flags().Bool("force", false, "Skip gate and output validation")

	backCmd := &cobra.Command{
		Use:   "back [steps]",
		Short: "Roll back to a previous phase",
		Args:  cobra.MaximumNArgs(1),
		Run:   runBack,
	}

	approveCmd := &cobra.Command{
		Use:   "approve",
		Short: "Approve the current gate",
		Run:   runApprove,
	}

	rejectCmd := &cobra.Command{
		Use:   "reject [feedback]",
		Short: "Reject the current gate (keep phase active)",
		Run:   runReject,
	}

	skipCmd := &cobra.Command{
		Use:   "skip [reason]",
		Short: "Skip the current phase and advance",
		Run:   runSkip,
	}

	noteCmd := &cobra.Command{
		Use:   "note <text>",
		Short: "Add a progress note to the current phase",
		Args:  cobra.MinimumNArgs(1),
		Run:   runNote,
	}

	failCmd := &cobra.Command{
		Use:   "fail [reason]",
		Short: "Mark the current phase as failed",
		Run:   runFail,
	}

	retryCmd := &cobra.Command{
		Use:   "retry",
		Short: "Retry the current failed phase",
		Run:   runRetry,
	}

	statusCmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Show current workflow state",
		Run:   runStatus,
	}
	statusCmd.Flags().Bool("json", false, "Output as JSON")

	pauseCmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause the workflow",
		Run:   runPause,
	}

	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused workflow",
		Run:   runResume,
	}

	templatesCmd := &cobra.Command{
		Use:   "templates [name]",
		Short: "List available templates or show details",
		Run:   runTemplates,
	}

	planLintCmd := &cobra.Command{
		Use:   "plan-lint <plan.yml>",
		Short: "Validate a PLAN.yml file",
		Args:  cobra.ExactArgs(1),
		Run:   runPlanLint,
	}
	planLintCmd.Flags().Bool("json", false, "Output as JSON")

	planRenderCmd := &cobra.Command{
		Use:   "plan-render <plan.yml> [output.md]",
		Short: "Render PLAN.yml to markdown",
		Args:  cobra.MinimumNArgs(1),
		Run:   runPlanRender,
	}

	rootCmd.AddCommand(
		startCmd,
		advanceCmd,
		backCmd,
		approveCmd,
		rejectCmd,
		skipCmd,
		noteCmd,
		failCmd,
		retryCmd,
		statusCmd,
		pauseCmd,
		resumeCmd,
		templatesCmd,
		planLintCmd,
		planRenderCmd,
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}